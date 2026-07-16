package main

import (
	"log"

	"github.com/baowk/dilu-go-kit/boot"
	"github.com/baowk/dilu-go-kit/example/internal/demo/router"
	"github.com/baowk/dilu-go-kit/example/internal/demo/store"
	"github.com/baowk/dilu-go-kit/mid"
)

func main() {
	app, err := boot.New("resources/config.dev.yaml")
	if err != nil {
		log.Fatal(err)
	}

	if err := app.Run(func(a *boot.App) error {
		cfg := a.GetConfig()
		// Init store
		store.Init(a.DB("main"))

		// Middleware
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

		// Routes
		router.Init(a.Gin, mid.JWTConfig{
			Secret: cfg.JWT.Secret, Issuer: cfg.JWT.Issuer,
			Subject: cfg.JWT.Subject, Audience: cfg.JWT.Audience,
		})

		return nil
	}); err != nil {
		log.Fatal(err)
	}
}
