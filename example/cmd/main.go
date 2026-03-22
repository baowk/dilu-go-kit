package main

import (
	"log"

	"github.com/mofang-ai/mofang-go-kit/boot"
	"github.com/mofang-ai/mofang-go-kit/example/internal/modules/demo/router"
	"github.com/mofang-ai/mofang-go-kit/example/internal/modules/demo/store"
	"github.com/mofang-ai/mofang-go-kit/mid"
)

func main() {
	app, err := boot.New("resources/config.dev.yaml")
	if err != nil {
		log.Fatal(err)
	}

	if err := app.Run(func(a *boot.App) error {
		// Init store
		store.Init(a.DB("main"))

		// Middleware
		a.Gin.Use(mid.Recovery(), mid.CORS())

		// Routes
		router.Init(a.Gin, "your-jwt-secret")

		return nil
	}); err != nil {
		log.Fatal(err)
	}
}
