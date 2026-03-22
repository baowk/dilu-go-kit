package boot

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mofang-ai/mofang-go-kit/registry"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"gorm.io/gorm"
)

// App is the central service instance holding all shared resources.
type App struct {
	Config   *Config
	Gin      *gin.Engine
	DBs      map[string]*gorm.DB
	Redis    *redis.Client
	GRPC     *grpc.Server        // nil if gRPC is disabled
	Registry registry.Registry   // nil if registry is disabled

	instanceID string
	onStart    []func()
	onClose    []func()
}

// SetupFunc is called during Run to register routes, stores, gRPC services, etc.
type SetupFunc func(app *App) error

// New creates an App from a config file path. It initializes the logger,
// Gin engine, database connections, and Redis (if configured).
func New(cfgPath string) (*App, error) {
	cfg, err := LoadBaseConfig(cfgPath)
	if err != nil {
		return nil, err
	}

	InitLogger(cfg.Server.Mode)

	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()

	app := &App{
		Config: cfg,
		Gin:    r,
		DBs:    make(map[string]*gorm.DB),
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
		app.GRPC = grpc.NewServer()
	}

	// Registry (optional)
	if cfg.Registry.Enable && len(cfg.Registry.Endpoints) > 0 {
		reg, err := registry.New(registry.Config{
			Endpoints: cfg.Registry.Endpoints,
			Prefix:    cfg.Registry.Prefix,
			TTL:       cfg.Registry.TTL,
		})
		if err != nil {
			Log().Warn().Err(err).Msg("registry init failed, running without service discovery")
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

// Run starts the HTTP server (and optional gRPC server), calls setup,
// then blocks until SIGINT/SIGTERM. Shutdown is graceful.
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
			Log().Info().Str("addr", a.Config.GRPC.Addr).Msg("gRPC server started")
			if err := a.GRPC.Serve(lis); err != nil {
				Log().Error().Err(err).Msg("gRPC server error")
			}
		}()
	}

	// Register service
	if a.Registry != nil {
		a.instanceID = registry.GenerateInstanceID(a.Config.Server.Name)
		svc := registry.Service{
			Name:       a.Config.Server.Name,
			InstanceID: a.instanceID,
			Addr:       a.Config.Server.Addr,
		}
		if a.Config.GRPC.Enable {
			svc.GRPCAddr = a.Config.GRPC.Addr
		}
		if err := a.Registry.Register(context.Background(), svc); err != nil {
			Log().Error().Err(err).Msg("service registration failed")
		}
	}

	// Start HTTP
	srv := &http.Server{Addr: a.Config.Server.Addr, Handler: a.Gin}
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		Log().Info().
			Str("name", a.Config.Server.Name).
			Str("addr", a.Config.Server.Addr).
			Msg("HTTP server started")

		for _, fn := range a.onStart {
			fn()
		}

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			Log().Fatal().Err(err).Msg("HTTP server error")
		}
	}()

	<-quit
	Log().Info().Msg("shutting down...")

	// OnClose callbacks
	for _, fn := range a.onClose {
		fn()
	}

	// Stop gRPC
	if a.GRPC != nil {
		a.GRPC.GracefulStop()
	}

	// Stop HTTP
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		Log().Error().Err(err).Msg("HTTP shutdown error")
	}

	// Close DB
	for name, db := range a.DBs {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
			Log().Debug().Str("db", name).Msg("db closed")
		}
	}

	// Deregister service
	if a.Registry != nil {
		dctx, dcancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = a.Registry.Deregister(dctx, a.Config.Server.Name, a.instanceID)
		_ = a.Registry.Close()
		dcancel()
	}

	// Close Redis
	if a.Redis != nil {
		_ = a.Redis.Close()
	}

	Log().Info().Msg("server exited")
	return nil
}
