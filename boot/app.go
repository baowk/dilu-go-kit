package boot

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/baowk/dilu-go-kit/log"
	"github.com/baowk/dilu-go-kit/registry"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"gorm.io/gorm"
)

// ConfigChangeFunc is called when remote config changes.
// It receives the updated Config; return an error to reject the update.
type ConfigChangeFunc func(cfg *Config) error

// App is the central service instance holding all shared resources.
type App struct {
	Config   *Config
	Gin      *gin.Engine
	DBs      map[string]*gorm.DB
	Redis    *redis.Client
	GRPC     *grpc.Server
	Registry registry.Registry

	cfgMu          sync.RWMutex // protects Config during hot-reload
	localConfig    *Config      // local file/env config used as hot-reload merge base
	instanceID     string
	onStart        []func()
	onClose        []func()
	onConfigChange []ConfigChangeFunc
	watchCancel    context.CancelFunc
}

// GetConfig returns the current config (thread-safe for use during runtime).
// During setup (before Run), accessing a.Config directly is fine.
func (a *App) GetConfig() *Config {
	a.cfgMu.RLock()
	defer a.cfgMu.RUnlock()
	return a.Config
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
	localCfg, err := cloneConfig(cfg)
	if err != nil {
		return nil, err
	}

	// Merge remote config from registry backend if configKey is set
	if cfg.Registry.ConfigKey != "" && (len(cfg.Registry.Endpoints) > 0 || cfg.Registry.Address != "") {
		if err := MergeRemoteConfig(cfg.Registry, cfg.Server.Name, cfg); err != nil {
			return nil, fmt.Errorf("remote config: %w", err)
		}
		applyEnvOverrides(cfg)
	}

	log.Init(cfg.Server.Mode, cfg.Server.Name, cfg.Log.Output, &cfg.Log.File)

	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	if err := r.SetTrustedProxies(nil); err != nil {
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
			return nil, err
		}
		app.Redis = rdb
	}

	if cfg.GRPC.Enable {
		// gRPC server with traceId interceptors
		app.GRPC = grpc.NewServer(
			grpc.UnaryInterceptor(grpcUnaryServerTrace()),
			grpc.StreamInterceptor(grpcStreamServerTrace()),
		)
	}

	// Registry (optional)
	if cfg.Registry.Enable && (len(cfg.Registry.Endpoints) > 0 || cfg.Registry.Address != "") {
		reg, err := registry.New(registry.Config{
			Type:      cfg.Registry.Type,
			Endpoints: cfg.Registry.Endpoints,
			Address:   cfg.Registry.Address,
			Token:     cfg.Registry.Token,
			Prefix:    cfg.Registry.Prefix,
			TTL:       cfg.Registry.TTL,
		})
		if err != nil {
			log.Warn("registry init failed, running without service discovery", "error", err)
		} else {
			app.Registry = reg
		}
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

// Run starts the HTTP server (and optional gRPC server), calls setup,
// then blocks until SIGINT/SIGTERM.
func (a *App) Run(setup SetupFunc) error {
	if err := setup(a); err != nil {
		return fmt.Errorf("setup: %w", err)
	}

	// Start gRPC if enabled
	if a.GRPC != nil && a.Config.GRPC.Addr != "" {
		lis, err := net.Listen("tcp", a.Config.GRPC.Addr)
		if err != nil {
			return fmt.Errorf("grpc listen %s: %w", a.Config.GRPC.Addr, err)
		}
		go func() {
			log.Info("gRPC server started", "addr", a.Config.GRPC.Addr)
			if err := a.GRPC.Serve(lis); err != nil {
				log.Error("gRPC server error", "error", err)
			}
		}()
	}

	// Register service
	if a.Registry != nil {
		a.instanceID = registry.GenerateInstanceID(a.Config.Server.Name)
		svc := registry.Service{
			Name:       a.Config.Server.Name,
			InstanceID: a.instanceID,
			Addr:       resolveAdvertiseAddr(a.Config.Server.Addr, a.Config.Server.AdvertiseAddr),
		}
		if a.Config.GRPC.Enable {
			svc.GRPCAddr = resolveAdvertiseAddr(a.Config.GRPC.Addr, a.Config.GRPC.AdvertiseAddr)
		}
		if err := a.Registry.Register(context.Background(), svc); err != nil {
			log.Error("service registration failed", "error", err)
		}
	}

	// Start remote config watcher if enabled
	if a.Config.Registry.ConfigKey != "" && (len(a.Config.Registry.Endpoints) > 0 || a.Config.Registry.Address != "") {
		watchCtx, watchCancel := context.WithCancel(context.Background())
		a.watchCancel = watchCancel
		go a.watchRemoteConfig(watchCtx)
	}

	// Start HTTP
	srv := &http.Server{Addr: a.Config.Server.Addr, Handler: a.Gin}
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		log.Info("HTTP server started", "name", a.Config.Server.Name, "addr", a.Config.Server.Addr)

		for _, fn := range a.onStart {
			fn()
		}

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("HTTP server error", "error", err)
		}
	}()

	<-quit
	log.Info("shutting down...")

	// Stop config watcher
	if a.watchCancel != nil {
		a.watchCancel()
	}

	// Deregister FIRST so other services stop sending traffic
	if a.Registry != nil {
		dctx, dcancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = a.Registry.Deregister(dctx, a.Config.Server.Name, a.instanceID)
		dcancel()
	}

	for _, fn := range a.onClose {
		fn()
	}

	// Now drain in-flight requests
	if a.GRPC != nil {
		a.GRPC.GracefulStop()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Error("HTTP shutdown error", "error", err)
	}

	for name, db := range a.DBs {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
			log.Debug("db closed", "db", name)
		}
	}

	if a.Registry != nil {
		_ = a.Registry.Close()
	}

	if a.Redis != nil {
		_ = a.Redis.Close()
	}

	log.Info("server exited")
	return nil
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
		newCfg, err := cloneConfig(base)
		if err != nil {
			log.Error("remote config: failed to clone local base", "error", err)
			return
		}
		if err := MergeRemoteConfigOptional(reg, serviceName, newCfg); err != nil {
			log.Error("remote config: failed to merge update", "error", err)
			return
		}
		applyEnvOverrides(newCfg)

		// Run all OnConfigChange callbacks; any error rejects the update
		for _, fn := range callbacks {
			if err := fn(newCfg); err != nil {
				log.Warn("remote config: update rejected by callback", "error", err)
				return
			}
		}

		a.swapConfig(newCfg)
		log.Info("remote config: config updated")
	})
	if err != nil && ctx.Err() == nil {
		log.Error("remote config: watch stopped unexpectedly", "error", err)
	}
}

func resolveAdvertiseAddr(listenAddr, advertiseAddr string) string {
	if advertiseAddr != "" {
		return advertiseAddr
	}
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil || port == "" {
		return listenAddr
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		return net.JoinHostPort(localAdvertiseIP(), port)
	}
	return listenAddr
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
