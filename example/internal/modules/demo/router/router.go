package router

import (
	"github.com/gin-gonic/gin"
	"github.com/mofang-ai/mofang-go-kit/example/internal/modules/demo/apis"
	"github.com/mofang-ai/mofang-go-kit/mid"
)

func Init(r *gin.Engine, jwtSecret string) {
	api := &apis.TaskAPI{}

	v1 := r.Group("/v1/demo")
	auth := v1.Group("").Use(mid.JWT(mid.JWTConfig{Secret: jwtSecret}))
	{
		auth.GET("/tasks", api.List)
		auth.POST("/tasks", api.Create)
		auth.PUT("/tasks/:id", api.Update)
		auth.DELETE("/tasks/:id", api.Delete)
	}
}
