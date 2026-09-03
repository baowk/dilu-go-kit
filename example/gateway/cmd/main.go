// gateway demonstrates an HTTP-only deployment. Database, Redis, migration,
// and Redis-specific telemetry adapters are not imported or configured.
package main

import (
	"log"
	"net/http"

	"github.com/baowk/dilu-go-kit/boot"
	"github.com/baowk/dilu-go-kit/metrics"
	"github.com/baowk/dilu-go-kit/mid"
	"github.com/gin-gonic/gin"
)

func main() {
	app, err := boot.New("resources/config.yaml")
	if err != nil {
		log.Fatal(err)
	}
	if err := app.Run(func(a *boot.App) error {
		cfg := a.GetConfig()
		metrics.Init(cfg.Server.Name)
		mid.Default(a.Gin, mid.DefaultConfig{
			CORS:        mid.CORSCfg{Enable: true, Mode: "allow-all"},
			AccessLimit: mid.AccessLimitCfg{Enable: true, Total: 600, Duration: 5, Backend: "memory"},
		})
		a.Gin.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
		a.Gin.GET("/ready", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
		a.Gin.GET("/metrics", metrics.Handler())
		a.Gin.Any("/api/*path", func(c *gin.Context) {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "route not configured"})
		})
		return nil
	}); err != nil {
		log.Fatal(err)
	}
}
