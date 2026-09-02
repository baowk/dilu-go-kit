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
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/baowk/dilu-go-kit/diagnostics"
	"github.com/baowk/dilu-go-kit/log"
	"github.com/baowk/dilu-go-kit/metrics"
	"github.com/baowk/dilu-go-kit/mid"
	"github.com/baowk/dilu-go-kit/registry"
	"github.com/baowk/dilu-go-kit/telemetry"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"gorm.io/gorm"
)

// ConfigChangeFunc is called when remote config changes.
// It receives the updated Config; return an error to reject the update.
type ConfigChangeFunc func(cfg *Config) error

// ConfigAppliedFunc runs after a validated runtime config has been published.
type ConfigAppliedFunc func(cfg *Config)

// App is the central service instance holding all shared resources.
type App struct {
	Config    *Config
	Gin       *gin.Engine
	DBs       map[string]*gorm.DB
	Redis     *redis.Client
	GRPC      *grpc.Server
	Registry  registry.Registry
	Telemetry *telemetry.Provider

	cfgMu             sync.RWMutex // protects Config during hot-reload
	localConfig       *Config      // local file/env config used as hot-reload merge base
	instanceID        string
	onStart           []func()
	onClose           []func()
	onConfigChange    []ConfigChangeFunc
	onConfigApplied   []ConfigAppliedFunc
	watchCancel       context.CancelFunc
	watchWG           sync.WaitGroup
	registeredName    string
	httpServer        *http.Server
	closeOnce         sync.Once
	callbacksOnce     sync.Once
	closeErr          error
	ready             atomic.Bool
	components        []Component
	startedComponents []Component
	componentMu       sync.Mutex
	componentStopOnce sync.Once
	componentCancel   context.CancelFunc
	readinessMu       sync.RWMutex
	readinessChecks   map[string]func(context.Context) error
	readinessEvalMu   sync.Mutex
	readinessInFlight atomic.Int32
	readinessCacheMu  sync.RWMutex
	readinessCachedAt time.Time
	readinessCachedOK bool
	readinessCacheTTL time.Duration
	diagnosticServer  *http.Server
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

// Component is a background service managed by App. Start should return once
// the component is running; long-lived work must stop when Stop is called.
type Component interface {
	Name() string
	Start(context.Context) error
	Stop(context.Context) error
}

// AddComponent registers a background component. Components start in
// registration order and stop in reverse order during graceful shutdown.
func (a *App) AddComponent(component Component) error {
	if component == nil {
		return errors.New("boot: component is nil")
	}
	if strings.TrimSpace(component.Name()) == "" {
		return errors.New("boot: component name is required")
	}
	a.componentMu.Lock()
	defer a.componentMu.Unlock()
	for _, existing := range a.components {
		if existing.Name() == component.Name() {
			return fmt.Errorf("boot: component %q already registered", component.Name())
		}
	}
	a.components = append(a.components, component)
	return nil
}

// AddReadinessCheck registers a dependency check used by the default /ready
// endpoint. Checks run with a bounded context and must return nil when ready.
func (a *App) AddReadinessCheck(name string, check func(context.Context) error) error {
	name = strings.TrimSpace(name)
	if name == "" || check == nil {
		return errors.New("boot: readiness check name and function are required")
	}
	a.readinessMu.Lock()
	defer a.readinessMu.Unlock()
	if a.readinessChecks == nil {
		a.readinessChecks = make(map[string]func(context.Context) error)
	}
	if _, exists := a.readinessChecks[name]; exists {
		return fmt.Errorf("boot: readiness check %q already registered", name)
	}
	a.readinessChecks[name] = check
	return nil
}

// SetReadinessCacheTTL controls how long a successful/failed dependency
// result is reused by /ready. The default is two seconds; set a negative value
// to disable caching and run checks on every probe.
func (a *App) SetReadinessCacheTTL(ttl time.Duration) {
	a.readinessCacheMu.Lock()
	a.readinessCacheTTL = ttl
	a.readinessCachedAt = time.Time{}
	a.readinessCacheMu.Unlock()
}

// New creates an App from a config file path.
func New(cfgPath string) (*App, error) {
	cfg, err := LoadBaseConfig(cfgPath)
	if err != nil {
		return nil, err
	}
	localCfg := cloneConfig(cfg)
	// Validate the local transport policy before contacting a registry for
	// remote configuration. This prevents requireTLS=true from first fetching
	// configuration over a plaintext registry connection.
	if err := cfg.validateTLSRequirements(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	// Merge remote config from registry backend if configKey is set
	if cfg.Registry.ConfigKey != "" && (len(cfg.Registry.Endpoints) > 0 || cfg.Registry.Address != "") {
		if err := MergeRemoteConfig(cfg.Registry, cfg.Server.Name, cfg); err != nil {
			return nil, fmt.Errorf("remote config: %w", err)
		}
		applyEnvOverrides(cfg)
	}
	if err := cfg.validateInlineSecrets(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	var grpcServerCredentials credentials.TransportCredentials
	if cfg.GRPC.Enable && cfg.GRPC.TLS.Enable {
		tlsConfig, err := loadGRPCServerTLSConfig(cfg.GRPC.TLS)
		if err != nil {
			return nil, fmt.Errorf("grpc TLS: %w", err)
		}
		grpcServerCredentials = credentials.NewTLS(tlsConfig)
	}

	log.Init(cfg.Server.Mode, cfg.Server.Name, cfg.Log.Output, &cfg.Log.File)
	warnPlaintextTransports(cfg)
	metrics.Init(cfg.Server.Name)

	if cfg.Server.Mode == "release" || cfg.Server.Mode == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	if err := r.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		return nil, fmt.Errorf("gin trusted proxies: %w", err)
	}

	tp, err := telemetry.Init(context.Background(), cfg.Telemetry)
	if err != nil {
		return nil, fmt.Errorf("telemetry init: %w", err)
	}
	if tp.Enabled() {
		r.Use(tp.GinMiddleware())
	}
	diagnostics.RegisterFromConfig(r, cfg.Diagnostics)
	app := &App{
		Config:          cfg,
		localConfig:     localCfg,
		Gin:             r,
		DBs:             make(map[string]*gorm.DB),
		Telemetry:       tp,
		readinessChecks: make(map[string]func(context.Context) error),
	}
	// Bound request bodies globally so services cannot accidentally accept an
	// unbounded upload when a handler forgets to install its own limit.
	r.Use(mid.RequestBodyLimit(int64(defaultInt(cfg.Server.MaxBodyBytes, int(mid.DefaultMaxBodyBytes)))))

	if len(cfg.Database) > 0 {
		dbs, err := OpenAllDBs(cfg.Database, cfg.Server.Mode)
		if err != nil {
			_ = tp.Shutdown(context.Background())
			return nil, err
		}
		app.DBs = dbs
		for name, db := range app.DBs {
			name, db := name, db
			_ = app.AddReadinessCheck("db:"+name, func(ctx context.Context) error {
				sqlDB, err := db.DB()
				if err != nil {
					return err
				}
				return sqlDB.PingContext(ctx)
			})
		}
		if tp.Enabled() {
			for name, db := range app.DBs {
				db = db.Set("dilu:db_name", name)
				app.DBs[name] = db
				if err := db.Use(telemetry.NewGORMPlugin(tp)); err != nil {
					closeDBs(app.DBs)
					_ = tp.Shutdown(context.Background())
					return nil, fmt.Errorf("db telemetry: %w", err)
				}
			}
		}
	}

	if cfg.Redis.Addr != "" {
		rdb, err := OpenRedis(cfg.Redis)
		if err != nil {
			closeDBs(app.DBs)
			_ = tp.Shutdown(context.Background())
			return nil, err
		}
		app.Redis = rdb
		_ = app.AddReadinessCheck("redis", func(ctx context.Context) error { return rdb.Ping(ctx).Err() })
		if tp.Enabled() {
			rdb.AddHook(telemetry.NewRedisHook(tp))
		}
	}

	if cfg.GRPC.Enable {
		// gRPC server with trace propagation and metrics interceptors.
		grpcOptions := []grpc.ServerOption{
			grpc.MaxRecvMsgSize(defaultInt(cfg.GRPC.MaxRecvMsgSize, 4<<20)),
			grpc.MaxSendMsgSize(defaultInt(cfg.GRPC.MaxSendMsgSize, 4<<20)),
			grpc.ChainUnaryInterceptor(
				mid.GRPCUnaryServerInterceptor(),
				mid.GRPCRecoveryUnaryInterceptor(),
				metrics.GRPCUnaryServerInterceptor(),
			),
			grpc.ChainStreamInterceptor(
				mid.GRPCStreamServerInterceptor(),
				mid.GRPCRecoveryStreamInterceptor(),
				metrics.GRPCStreamServerInterceptor(),
			),
		}
		if grpcServerCredentials != nil {
			grpcOptions = append(grpcOptions, grpc.Creds(grpcServerCredentials))
		}
		if tp.Enabled() {
			grpcOptions = append(grpcOptions, tp.GRPCServerOption())
		}
		app.GRPC = grpc.NewServer(grpcOptions...)
	}

	// Registry (optional)
	if cfg.Registry.Enable && (len(cfg.Registry.Endpoints) > 0 || cfg.Registry.Address != "") {
		reg, err := registry.New(registry.Config{
			Type:                    cfg.Registry.Type,
			Endpoints:               cfg.Registry.Endpoints,
			Address:                 cfg.Registry.Address,
			Token:                   cfg.Registry.Token,
			Username:                cfg.Registry.Username,
			Password:                cfg.Registry.Password,
			Prefix:                  cfg.Registry.Prefix,
			TTL:                     cfg.Registry.TTL,
			DialTimeout:             cfg.Registry.DialTimeout,
			CheckType:               cfg.Registry.consulCheckType(),
			CheckPath:               cfg.Registry.CheckPath,
			CheckTLS:                cfg.Registry.CheckTLS,
			DeregisterCriticalAfter: cfg.Registry.DeregisterCriticalAfter,
			TLS:                     cfg.Registry.TLS,
		})
		if err != nil {
			closeDBs(app.DBs)
			if app.Redis != nil {
				_ = app.Redis.Close()
			}
			_ = tp.Shutdown(context.Background())
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
	// Install the readiness guard before setup so it also wraps routes that
	// setup registers itself.
	a.Gin.Use(func(c *gin.Context) {
		if c.Request.URL.Path == "/ready" && (!a.ready.Load() || !a.readinessOK()) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not-ready"})
			c.Abort()
			return
		}
		c.Next()
	})
	if err := setup(a); err != nil {
		_ = a.Close()
		return fmt.Errorf("setup: %w", err)
	}
	ensureOperationalRoutesWithReady(a.Gin, func() bool {
		return a.ready.Load() && a.readinessOK()
	})

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
	// HTTP, gRPC, and the optional diagnostics listener may all report a
	// terminal serve error; keep the channel buffered for every listener so a
	// secondary failure cannot strand its goroutine during shutdown.
	serveErr := make(chan error, 3)
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
	if cfg.Diagnostics.Pprof.Enabled && cfg.Diagnostics.Pprof.Addr != "" {
		diagLis, listenErr := net.Listen("tcp", cfg.Diagnostics.Pprof.Addr)
		if listenErr != nil {
			_ = a.shutdown(cfg)
			return fmt.Errorf("pprof listen %s: %w", cfg.Diagnostics.Pprof.Addr, listenErr)
		}
		diagTLS, tlsErr := cfg.Diagnostics.Pprof.TLS.ServerTLSConfig()
		if tlsErr != nil {
			_ = diagLis.Close()
			_ = a.shutdown(cfg)
			return tlsErr
		}
		a.diagnosticServer = &http.Server{Addr: cfg.Diagnostics.Pprof.Addr, Handler: diagnostics.HandlerWithAccess(cfg.Diagnostics.Pprof.Prefix, cfg.Diagnostics.Pprof.AuthToken, splitCIDRs(cfg.Diagnostics.Pprof.AllowedCIDRs)), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20, TLSConfig: diagTLS}
		go func() {
			var err error
			if diagTLS != nil {
				err = a.diagnosticServer.ServeTLS(diagLis, "", "")
			} else {
				err = a.diagnosticServer.Serve(diagLis)
			}
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				serveErr <- fmt.Errorf("pprof serve: %w", err)
			}
		}()
	}

	if a.Registry != nil {
		a.instanceID = registry.GenerateInstanceID(cfg.Server.Name)
		a.registeredName = cfg.Server.Name
		svc := registry.Service{
			Name:       cfg.Server.Name,
			Version:    cfg.Server.Version,
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
	if err := a.startComponents(cfg.Server.ShutdownTimeout); err != nil {
		_ = a.shutdown(cfg)
		return fmt.Errorf("component start: %w", err)
	}
	a.ready.Store(true)

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

func splitCIDRs(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func ensureOperationalRoutes(r *gin.Engine) {
	ensureOperationalRoutesWithReady(r)
}

func ensureOperationalRoutesWithReady(r *gin.Engine, ready ...func() bool) {
	if r == nil {
		return
	}
	isReady := func() bool { return true }
	if len(ready) > 0 && ready[0] != nil {
		isReady = ready[0]
	}
	if !hasRoute(r, http.MethodGet, "/health") {
		r.GET("/health", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		})
	}
	if !hasRoute(r, http.MethodGet, "/ready") {
		r.GET("/ready", func(c *gin.Context) {
			if !isReady() {
				c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not-ready"})
				return
			}
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
	a.ready.Store(false)
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

	componentErr := a.stopComponents(cfg.Server.ShutdownTimeout)

	timeout := time.Duration(defaultInt(cfg.Server.ShutdownTimeout, 10)) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	errCh := make(chan error, 2)
	if a.diagnosticServer != nil {
		errCh = make(chan error, 3)
	}
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
	if a.diagnosticServer != nil {
		go func() { errCh <- a.diagnosticServer.Shutdown(ctx) }()
	}

	var shutdownErr error
	count := 2
	if a.diagnosticServer != nil {
		count = 3
	}
	for range count {
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
	return errors.Join(componentErr, shutdownErr, a.Close())
}

func (a *App) startComponents(timeoutSeconds int) error {
	a.componentMu.Lock()
	components := append([]Component(nil), a.components...)
	a.componentMu.Unlock()
	if len(components) == 0 {
		return nil
	}
	startTimeout := time.Duration(defaultInt(timeoutSeconds, 10)) * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	started := make([]Component, 0, len(components))
	for _, component := range components {
		if err := runComponentStart(ctx, component, startTimeout); err != nil {
			cancel()
			stopCtx, stopCancel := context.WithTimeout(context.Background(), startTimeout)
			for i := len(started) - 1; i >= 0; i-- {
				_ = runComponentStop(stopCtx, started[i])
			}
			stopCancel()
			return fmt.Errorf("%s: %w", component.Name(), err)
		}
		started = append(started, component)
	}
	a.componentMu.Lock()
	a.startedComponents = started
	a.componentCancel = cancel
	a.componentMu.Unlock()
	return nil
}

func (a *App) stopComponents(timeoutSeconds int) error {
	var stopErr error
	a.componentStopOnce.Do(func() {
		a.componentMu.Lock()
		started := append([]Component(nil), a.startedComponents...)
		cancel := a.componentCancel
		a.componentMu.Unlock()
		if len(started) == 0 {
			return
		}
		if cancel != nil {
			cancel()
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(defaultInt(timeoutSeconds, 10))*time.Second)
		defer cancel()
		for i := len(started) - 1; i >= 0; i-- {
			if err := runComponentStop(ctx, started[i]); err != nil {
				stopErr = errors.Join(stopErr, fmt.Errorf("%s: %w", started[i].Name(), err))
			}
		}
	})
	return stopErr
}

func (a *App) readinessOK() bool {
	now := time.Now()
	a.readinessCacheMu.RLock()
	ttl := a.readinessCacheTTL
	cachedAt, cachedOK := a.readinessCachedAt, a.readinessCachedOK
	a.readinessCacheMu.RUnlock()
	if ttl == 0 {
		ttl = 2 * time.Second
	}
	if ttl > 0 && !cachedAt.IsZero() && now.Sub(cachedAt) < ttl {
		return cachedOK
	}
	a.readinessEvalMu.Lock()
	defer a.readinessEvalMu.Unlock()
	if ttl > 0 {
		a.readinessCacheMu.RLock()
		cachedAt, cachedOK = a.readinessCachedAt, a.readinessCachedOK
		a.readinessCacheMu.RUnlock()
		if !cachedAt.IsZero() && time.Since(cachedAt) < ttl {
			return cachedOK
		}
	}
	a.readinessMu.RLock()
	checks := make([]func(context.Context) error, 0, len(a.readinessChecks))
	for _, check := range a.readinessChecks {
		checks = append(checks, check)
	}
	a.readinessMu.RUnlock()
	if len(checks) == 0 {
		a.storeReadinessCache(true)
		return true
	}
	// A check that ignores context cannot be forcefully terminated. Do not
	// start another copy while a previous timed-out check is still running;
	// this keeps repeated /ready probes from leaking an unbounded number of
	// goroutines.
	if a.readinessInFlight.Load() > 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	results := make(chan error, len(checks))
	for _, check := range checks {
		a.readinessInFlight.Add(1)
		go func(check func(context.Context) error) {
			defer a.readinessInFlight.Add(-1)
			results <- runReadinessCheck(ctx, check)
		}(check)
	}
	ok := true
	remaining := len(checks)
	for remaining > 0 {
		select {
		case err := <-results:
			remaining--
			if err != nil {
				ok = false
				cancel()
			}
		case <-ctx.Done():
			// A dependency that ignores context must not block readiness forever.
			return false
		}
	}
	a.storeReadinessCache(ok)
	return ok
}

func runReadinessCheck(ctx context.Context, check func(context.Context) error) (err error) {
	if check == nil {
		return errors.New("nil readiness check")
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("readiness check panic: %v", recovered)
		}
	}()
	return check(ctx)
}

func runComponentStart(parent context.Context, component Component, timeout time.Duration) (err error) {
	if component == nil {
		return errors.New("component is nil")
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				done <- fmt.Errorf("panic: %v", recovered)
			}
		}()
		done <- component.Start(ctx)
	}()
	select {
	case err = <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("start timeout after %s: %w", timeout, ctx.Err())
	}
}

func runComponentStop(ctx context.Context, component Component) (err error) {
	if component == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	done := make(chan error, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				done <- fmt.Errorf("panic: %v", recovered)
			}
		}()
		done <- component.Stop(ctx)
	}()
	select {
	case err = <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("stop timeout: %w", ctx.Err())
	}
}

func (a *App) storeReadinessCache(ok bool) {
	a.readinessCacheMu.Lock()
	a.readinessCachedAt = time.Now()
	a.readinessCachedOK = ok
	a.readinessCacheMu.Unlock()
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
		componentErr := a.stopComponents(10)
		if a.watchCancel != nil {
			a.watchCancel()
		}
		watchDone := make(chan struct{})
		go func() { a.watchWG.Wait(); close(watchDone) }()
		select {
		case <-watchDone:
		case <-time.After(3 * time.Second):
			componentErr = errors.Join(componentErr, errors.New("remote config watcher did not stop in time"))
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
		if a.Telemetry != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			errs = errors.Join(errs, a.Telemetry.Shutdown(ctx))
			cancel()
		}
		a.closeErr = errors.Join(componentErr, errs)
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
		Server      ServerConfig
		Security    SecurityConfig
		Log         LogConfig
		Database    map[string]DatabaseConfig
		Redis       RedisConfig
		GRPC        GRPCConfig
		Registry    RegistryConfig
		Telemetry   telemetry.Config
		Diagnostics diagnostics.Config
	}{current.Server, current.Security, current.Log, current.Database, current.Redis, current.GRPC, current.Registry, current.Telemetry, current.Diagnostics}
	staticNext := struct {
		Server      ServerConfig
		Security    SecurityConfig
		Log         LogConfig
		Database    map[string]DatabaseConfig
		Redis       RedisConfig
		GRPC        GRPCConfig
		Registry    RegistryConfig
		Telemetry   telemetry.Config
		Diagnostics diagnostics.Config
	}{next.Server, next.Security, next.Log, next.Database, next.Redis, next.GRPC, next.Registry, next.Telemetry, next.Diagnostics}
	if !reflect.DeepEqual(staticCurrent, staticNext) {
		return fmt.Errorf("startup-only server/log/database/redis/grpc/registry/security fields changed; restart is required")
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
