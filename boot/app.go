package boot

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"reflect"
	"sync"
	"syscall"
	"time"

	"github.com/baowk/dilu-go-kit/log"
	"github.com/baowk/dilu-go-kit/metrics"
	"github.com/baowk/dilu-go-kit/mid"
	"github.com/baowk/dilu-go-kit/registry"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"gorm.io/gorm"
)

// ConfigChangeFunc is called when remote config changes.
// It receives the updated Config; return an error to reject the update.
type ConfigChangeFunc func(cfg *Config) error

// ConfigAppliedFunc runs after a validated runtime config has been published.
type ConfigAppliedFunc func(cfg *Config)

// App is the central service instance holding all shared resources.
type App struct {
	Config   *Config
	Gin      *gin.Engine
	DBs      map[string]*gorm.DB
	Redis    *redis.Client
	GRPC     *grpc.Server
	Registry registry.Registry

	cfgMu           sync.RWMutex // protects Config during hot-reload
	localConfig     *Config      // local file/env config used as hot-reload merge base
	instanceID      string
	onStart         []func()
	onClose         []func()
	onConfigChange  []ConfigChangeFunc
	onConfigApplied []ConfigAppliedFunc
	watchCancel     context.CancelFunc
	watchWG         sync.WaitGroup
	registeredName  string
	httpServer      *http.Server
	closeOnce       sync.Once
	callbacksOnce   sync.Once
	closeErr        error
}

// GetConfig returns the current config (thread-safe for use during runtime).
// During setup (before Run), accessing a.Config directly is fine.
func (a *App) GetConfig() *Config {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return cloneConfig(a.Config)
}

// swapConfig replaces the config under lock.
func (a *App) swapConfig(cfg *Config) {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	a.Config = cfg
}

// SetupFunc is called during Run to register routes, stores, gRPC services, etc.
type SetupFunc func(app *App) error

// New creates an App from a config file path.
func New(cfgPath string) (*App, error) {
	cfg, err := LoadBaseConfig(cfgPath)
	if err != nil {
		return nil, err
	}
	localCfg := cloneConfig(cfg)

	// Merge remote config from registry backend if configKey is set
	if cfg.Registry.ConfigKey != "" && (len(cfg.Registry.Endpoints) > 0 || cfg.Registry.Address != "") {
		if err := MergeRemoteConfig(cfg.Registry, cfg.Server.Name, cfg); err != nil {
			return nil, fmt.Errorf("remote config: %w", err)
		}
		applyEnvOverrides(cfg)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	log.Init(cfg.Server.Mode, cfg.Server.Name, cfg.Log.Output, &cfg.Log.File)
	metrics.Init(cfg.Server.Name)

	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	if err := r.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		return nil, fmt.Errorf("gin trusted proxies: %w", err)
	}

	app := &App{
		Config:      cfg,
		localConfig: localCfg,
		Gin:         r,
		DBs:         make(map[string]*gorm.DB),
	}

	if len(cfg.Database) > 0 {
		dbs, err := OpenAllDBs(cfg.Database, cfg.Server.Mode)
		if err != nil {
			return nil, err
		}
		app.DBs = dbs
	}

	if cfg.Redis.Addr != "" {
		rdb, err := OpenRedis(cfg.Redis)
		if err != nil {
			closeDBs(app.DBs)
			return nil, err
		}
		app.Redis = rdb
	}

	if cfg.GRPC.Enable {
		// gRPC server with trace propagation and metrics interceptors.
		app.GRPC = grpc.NewServer(
			grpc.MaxRecvMsgSize(defaultInt(cfg.GRPC.MaxRecvMsgSize, 4<<20)),
			grpc.MaxSendMsgSize(defaultInt(cfg.GRPC.MaxSendMsgSize, 4<<20)),
			grpc.ChainUnaryInterceptor(
				mid.GRPCUnaryServerInterceptor(),
				metrics.GRPCUnaryServerInterceptor(),
			),
			grpc.ChainStreamInterceptor(
				mid.GRPCStreamServerInterceptor(),
				metrics.GRPCStreamServerInterceptor(),
			),
		)
	}

	// Registry (optional)
	if cfg.Registry.Enable && (len(cfg.Registry.Endpoints) > 0 || cfg.Registry.Address != "") {
		reg, err := registry.New(registry.Config{
			Type:                    cfg.Registry.Type,
			Endpoints:               cfg.Registry.Endpoints,
			Address:                 cfg.Registry.Address,
			Token:                   cfg.Registry.Token,
			Prefix:                  cfg.Registry.Prefix,
			TTL:                     cfg.Registry.TTL,
			DialTimeout:             cfg.Registry.DialTimeout,
			CheckType:               cfg.Registry.consulCheckType(),
			CheckPath:               cfg.Registry.CheckPath,
			DeregisterCriticalAfter: cfg.Registry.DeregisterCriticalAfter,
		})
		if err != nil {
			closeDBs(app.DBs)
			if app.Redis != nil {
				_ = app.Redis.Close()
			}
			return nil, fmt.Errorf("registry init: %w", err)
		}
		app.Registry = reg
	}

	return app, nil
}

// DB returns a named database connection.
func (a *App) DB(name string) *gorm.DB { return a.DBs[name] }

// OnStart registers a callback invoked after the HTTP server starts listening.
func (a *App) OnStart(fn func()) { a.onStart = append(a.onStart, fn) }

// OnClose registers a callback invoked during graceful shutdown.
func (a *App) OnClose(fn func()) { a.onClose = append(a.onClose, fn) }

// OnConfigChange registers a callback invoked when remote config changes.
// The callback receives the new Config. Return an error to reject the update
// (Config will not be replaced). Multiple callbacks run in order.
func (a *App) OnConfigChange(fn ConfigChangeFunc) {
	a.onConfigChange = append(a.onConfigChange, fn)
}

// OnConfigApplied registers a side-effect callback that runs after all
// validators accept a runtime config update.
func (a *App) OnConfigApplied(fn ConfigAppliedFunc) {
	a.onConfigApplied = append(a.onConfigApplied, fn)
}

// Run starts listeners synchronously, registers the service only after ports
// are bound, and blocks until a signal or server failure.
func (a *App) Run(setup SetupFunc) error {
	if setup == nil {
		_ = a.Close()
		return fmt.Errorf("setup is nil")
	}
	if err := setup(a); err != nil {
		_ = a.Close()
		return fmt.Errorf("setup: %w", err)
	}
	ensureOperationalRoutes(a.Gin)

	cfg := a.GetConfig()
	httpLis, err := net.Listen("tcp", cfg.Server.Addr)
	if err != nil {
		_ = a.Close()
		return fmt.Errorf("http listen %s: %w", cfg.Server.Addr, err)
	}

	var grpcLis net.Listener
	if a.GRPC != nil {
		grpcLis, err = net.Listen("tcp", cfg.GRPC.Addr)
		if err != nil {
			_ = httpLis.Close()
			_ = a.Close()
			return fmt.Errorf("grpc listen %s: %w", cfg.GRPC.Addr, err)
		}
	}

	a.httpServer = newHTTPServer(cfg.Server, a.Gin)
	serveErr := make(chan error, 2)
	go func() {
		if err := a.httpServer.Serve(httpLis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("http serve: %w", err)
		}
	}()
	if grpcLis != nil {
		go func() {
			if err := a.GRPC.Serve(grpcLis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
				serveErr <- fmt.Errorf("grpc serve: %w", err)
			}
		}()
	}

	if a.Registry != nil {
		a.instanceID = registry.GenerateInstanceID(cfg.Server.Name)
		a.registeredName = cfg.Server.Name
		svc := registry.Service{
			Name:       cfg.Server.Name,
			InstanceID: a.instanceID,
			Addr:       resolveAdvertiseAddr(cfg.Server.Addr, cfg.Server.AdvertiseAddr),
		}
		if cfg.GRPC.Enable {
			svc.GRPCAddr = resolveAdvertiseAddr(cfg.GRPC.Addr, cfg.GRPC.AdvertiseAddr)
		}
		registerCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = a.Registry.Register(registerCtx, svc)
		cancel()
		if err != nil {
			_ = a.shutdown(cfg)
			return fmt.Errorf("service registration: %w", err)
		}
	}

	if cfg.Registry.ConfigKey != "" && (len(cfg.Registry.Endpoints) > 0 || cfg.Registry.Address != "") {
		watchCtx, watchCancel := context.WithCancel(context.Background())
		a.watchCancel = watchCancel
		a.watchWG.Add(1)
		go func() {
			defer a.watchWG.Done()
			a.watchRemoteConfig(watchCtx)
		}()
	}

	log.Info("HTTP server started", "name", cfg.Server.Name, "addr", cfg.Server.Addr)
	if grpcLis != nil {
		log.Info("gRPC server started", "addr", cfg.GRPC.Addr)
	}
	for _, fn := range a.onStart {
		if err := callLifecycle(fn); err != nil {
			_ = a.shutdown(cfg)
			return fmt.Errorf("on-start callback: %w", err)
		}
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)

	var runErr error
	select {
	case <-quit:
		log.Info("shutting down...")
	case runErr = <-serveErr:
		log.Error("server stopped unexpectedly", "error", runErr)
	}
	if err := a.shutdown(cfg); err != nil && runErr == nil {
		runErr = err
	}
	log.Info("server exited")
	return runErr
}

func newHTTPServer(cfg ServerConfig, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: time.Duration(defaultInt(cfg.ReadHeaderTimeout, 5)) * time.Second,
		ReadTimeout:       time.Duration(defaultInt(cfg.ReadTimeout, 15)) * time.Second,
		WriteTimeout:      time.Duration(defaultInt(cfg.WriteTimeout, 30)) * time.Second,
		IdleTimeout:       time.Duration(defaultInt(cfg.IdleTimeout, 60)) * time.Second,
		MaxHeaderBytes:    defaultInt(cfg.MaxHeaderBytes, 1<<20),
	}
}

func defaultInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func ensureOperationalRoutes(r *gin.Engine) {
	if r == nil {
		return
	}
	if !hasRoute(r, http.MethodGet, "/health") {
		r.GET("/health", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		})
	}
	if !hasRoute(r, http.MethodGet, "/ready") {
		r.GET("/ready", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		})
	}
}

func hasRoute(r *gin.Engine, method, path string) bool {
	for _, route := range r.Routes() {
		if route.Method == method && route.Path == path {
			return true
		}
	}
	return false
}

func (a *App) shutdown(cfg *Config) error {
	if a.watchCancel != nil {
		a.watchCancel()
	}

	if a.Registry != nil && a.instanceID != "" && a.registeredName != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if err := a.Registry.Deregister(ctx, a.registeredName, a.instanceID); err != nil {
			log.Warn("service deregistration failed", "error", err)
		}
		cancel()
	}

	timeout := time.Duration(defaultInt(cfg.Server.ShutdownTimeout, 10)) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	errCh := make(chan error, 2)
	go func() {
		if a.httpServer == nil {
			errCh <- nil
			return
		}
		errCh <- a.httpServer.Shutdown(ctx)
	}()
	go func() {
		errCh <- a.stopGRPC(time.Duration(defaultInt(cfg.GRPC.ShutdownTimeout, 10)) * time.Second)
	}()

	var shutdownErr error
	for range 2 {
		if err := <-errCh; err != nil && !errors.Is(err, context.Canceled) {
			shutdownErr = errors.Join(shutdownErr, err)
		}
	}

	watchDone := make(chan struct{})
	go func() {
		a.watchWG.Wait()
		close(watchDone)
	}()
	select {
	case <-watchDone:
	case <-time.After(3 * time.Second):
		shutdownErr = errors.Join(shutdownErr, fmt.Errorf("remote config watcher did not stop in time"))
	}

	a.callbacksOnce.Do(func() {
		for _, fn := range a.onClose {
			if err := callLifecycle(fn); err != nil {
				shutdownErr = errors.Join(shutdownErr, fmt.Errorf("on-close callback: %w", err))
			}
		}
	})
	return errors.Join(shutdownErr, a.Close())
}

func callLifecycle(fn func()) (err error) {
	if fn == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	fn()
	return nil
}

func (a *App) stopGRPC(timeout time.Duration) error {
	if a.GRPC == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		a.GRPC.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		a.GRPC.Stop()
		return fmt.Errorf("grpc graceful shutdown timed out after %s", timeout)
	}
}

// Close releases resources opened by New. It is safe to call more than once.
func (a *App) Close() error {
	if a == nil {
		return nil
	}
	a.closeOnce.Do(func() {
		if a.watchCancel != nil {
			a.watchCancel()
		}
		var errs error
		for name, db := range a.DBs {
			if sqlDB, err := db.DB(); err == nil {
				if err := sqlDB.Close(); err != nil {
					errs = errors.Join(errs, fmt.Errorf("close db %s: %w", name, err))
				}
			}
		}
		if a.Registry != nil {
			errs = errors.Join(errs, a.Registry.Close())
		}
		if a.Redis != nil {
			errs = errors.Join(errs, a.Redis.Close())
		}
		a.closeErr = errs
	})
	return a.closeErr
}

func closeDBs(dbs map[string]*gorm.DB) {
	for _, db := range dbs {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
}

// watchRemoteConfig watches the remote config key and applies changes.
func (a *App) watchRemoteConfig(ctx context.Context) {
	cfg := a.GetConfig()
	reg := cfg.Registry
	serviceName := cfg.Server.Name

	// Snapshot callbacks — OnConfigChange must be called before Run
	a.cfgMu.RLock()
	callbacks := make([]ConfigChangeFunc, len(a.onConfigChange))
	copy(callbacks, a.onConfigChange)
	appliedCallbacks := make([]ConfigAppliedFunc, len(a.onConfigApplied))
	copy(appliedCallbacks, a.onConfigApplied)
	a.cfgMu.RUnlock()

	log.Info("remote config: watching for changes",
		"key", reg.resolveConfigKey(serviceName),
		"node_key", reg.resolveConfigNodeKey(serviceName),
	)

	var reloadMu sync.Mutex
	err := WatchRemoteConfigTree(ctx, reg, serviceName, func() {
		reloadMu.Lock()
		defer reloadMu.Unlock()

		base := a.localConfig
		if base == nil {
			base = a.GetConfig()
		}
		newCfg := cloneConfig(base)
		if err := MergeRemoteConfigOptional(reg, serviceName, newCfg); err != nil {
			log.Error("remote config: failed to merge update", "error", err)
			return
		}
		applyEnvOverrides(newCfg)
		current := a.GetConfig()
		if err := validateRuntimeConfigChange(current, newCfg); err != nil {
			log.Warn("remote config: update rejected", "error", err)
			return
		}

		// Run all OnConfigChange callbacks; any error rejects the update
		for _, fn := range callbacks {
			if err := callConfigValidator(fn, newCfg); err != nil {
				log.Warn("remote config: update rejected by callback", "error", err)
				return
			}
		}

		published := cloneConfig(newCfg)
		a.swapConfig(published)
		for _, fn := range appliedCallbacks {
			if err := callConfigApplied(fn, a.GetConfig()); err != nil {
				log.Error("remote config: applied callback failed", "error", err)
			}
		}
		log.Info("remote config: config updated")
	})
	if err != nil && ctx.Err() == nil {
		log.Error("remote config: watch stopped unexpectedly", "error", err)
	}
}

func callConfigValidator(fn ConfigChangeFunc, cfg *Config) (err error) {
	if fn == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	return fn(cfg)
}

func callConfigApplied(fn ConfigAppliedFunc, cfg *Config) (err error) {
	if fn == nil {
		return nil
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	fn(cfg)
	return nil
}

func validateRuntimeConfigChange(current, next *Config) error {
	if current == nil || next == nil {
		return fmt.Errorf("config cannot be nil")
	}
	staticCurrent := struct {
		Server   ServerConfig
		Log      LogConfig
		Database map[string]DatabaseConfig
		Redis    RedisConfig
		GRPC     GRPCConfig
		Registry RegistryConfig
	}{current.Server, current.Log, current.Database, current.Redis, current.GRPC, current.Registry}
	staticNext := struct {
		Server   ServerConfig
		Log      LogConfig
		Database map[string]DatabaseConfig
		Redis    RedisConfig
		GRPC     GRPCConfig
		Registry RegistryConfig
	}{next.Server, next.Log, next.Database, next.Redis, next.GRPC, next.Registry}
	if !reflect.DeepEqual(staticCurrent, staticNext) {
		return fmt.Errorf("startup-only server/log/database/redis/grpc/registry fields changed; restart is required")
	}
	return next.Validate()
}

func resolveAdvertiseAddr(listenAddr, advertiseAddr string) string {
	if advertiseAddr != "" {
		return advertiseAddr
	}
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil || port == "" {
		return listenAddr
	}
	if ip := advertiseIPFromEnv(); ip != "" {
		return net.JoinHostPort(ip, port)
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		return net.JoinHostPort(localAdvertiseIP(), port)
	}
	return listenAddr
}

func advertiseIPFromEnv() string {
	for _, name := range []string{"MF_ADVERTISE_IP", "POD_IP"} {
		if ip := os.Getenv(name); ip != "" {
			return ip
		}
	}
	return ""
}

func localAdvertiseIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		if ip := ipNet.IP.To4(); ip != nil {
			return ip.String()
		}
	}
	return "127.0.0.1"
}
