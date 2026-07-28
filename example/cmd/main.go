package main

import (
	"log"
	"net/http"

	"github.com/baowk/dilu-go-kit/boot"
	"github.com/baowk/dilu-go-kit/example/internal/demo/router"
	"github.com/baowk/dilu-go-kit/example/internal/demo/store"
	"github.com/baowk/dilu-go-kit/metrics"
	"github.com/baowk/dilu-go-kit/mid"
	"github.com/baowk/dilu-go-kit/notify"
	"github.com/gin-gonic/gin"
)

func main() {
	app, err := boot.New("resources/config.dev.yaml")
	if err != nil {
		log.Fatal(err)
	}

	if err := app.Run(func(a *boot.App) error {
		cfg := a.GetConfig()
		metrics.Init(cfg.Server.Name)
		if err := applyNotifyConfig(cfg); err != nil {
			return err
		}
		a.OnConfigApplied(func(updated *boot.Config) {
			if err := applyNotifyConfig(updated); err != nil {
				log.Printf("apply notify config: %v", err)
			}
		})

		// Init store
		store.Init(a.DB("main"))

		// Middleware
		a.Gin.Use(metrics.GinMiddleware())
		mid.Default(a.Gin, mid.DefaultConfig{
			CORS: mid.CORSCfg{
				Enable: cfg.CORS.Enable, Mode: cfg.CORS.Mode, Whitelist: cfg.CORS.Whitelist,
			},
			AccessLimit: mid.AccessLimitCfg{
				Enable: cfg.AccessLimit.Enable, Total: cfg.AccessLimit.Total,
				Duration: cfg.AccessLimit.Duration, Backend: cfg.AccessLimit.Backend,
				Redis: a.Redis, KeyPrefix: cfg.AccessLimit.KeyPrefix,
			},
		})

		// Operational routes stay outside JWT authentication.
		a.Gin.GET("/health", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		})
		a.Gin.GET("/ready", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		})
		a.Gin.GET("/metrics", metrics.Handler())

		// Business routes are protected by JWT in router.Init.
		router.Init(a.Gin, mid.JWTConfig{
			Secret: cfg.JWT.Secret, Issuer: cfg.JWT.Issuer,
			Subject: cfg.JWT.Subject, Audience: cfg.JWT.Audience,
		})

		return nil
	}); err != nil {
		log.Fatal(err)
	}
}

func applyNotifyConfig(cfg *boot.Config) error {
	return notify.InitConfig(notify.Config{
		BaseURL: cfg.Notify.WsURL,
		Token:   cfg.Notify.Token,
	})
}
