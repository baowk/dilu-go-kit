package router

import (
	jwtmid "github.com/baowk/dilu-go-kit/contrib/mid/jwt"
	"github.com/baowk/dilu-go-kit/example/internal/demo/apis"
	"github.com/gin-gonic/gin"
)

func Init(r *gin.Engine, jwtConfig jwtmid.JWTConfig) {
	taskAPI := apis.NewTaskAPI()
	commentAPI := apis.NewTaskCommentAPI()

	v1 := r.Group("/v1/demo")
	auth := v1.Group("").Use(jwtmid.JWT(jwtConfig))
	{
		auth.GET("/tasks", taskAPI.List)
		auth.POST("/tasks", taskAPI.Create)
		auth.PUT("/tasks/:id", taskAPI.Update)
		auth.DELETE("/tasks/:id", taskAPI.Delete)

		auth.GET("/tasks/:task_id/comments", commentAPI.List)
		auth.POST("/tasks/:task_id/comments", commentAPI.Create)
		auth.DELETE("/tasks/:task_id/comments/:comment_id", commentAPI.Delete)
	}
}
