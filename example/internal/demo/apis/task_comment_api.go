package apis

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/baowk/dilu-go-kit/example/internal/demo/service"
	"github.com/baowk/dilu-go-kit/example/internal/demo/service/dto"
	"github.com/baowk/dilu-go-kit/log"
	"github.com/baowk/dilu-go-kit/mid"
	"github.com/baowk/dilu-go-kit/resp"
	base "github.com/baowk/dilu-go-kit/store"
	"github.com/gin-gonic/gin"
)

type TaskCommentAPI struct {
	svc *service.TaskCommentService
}

func NewTaskCommentAPI() *TaskCommentAPI {
	return &TaskCommentAPI{svc: service.NewTaskCommentService()}
}

func (a *TaskCommentAPI) List(c *gin.Context) {
	taskID, _ := strconv.ParseInt(c.Param("task_id"), 10, 64)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))

	list, total, err := a.svc.List(c, taskID, base.ListOpts{Page: page, Size: size})
	if err != nil {
		log.ErrorContext(c.Request.Context(), "list task comments failed", "task_id", taskID, "error", err)
		resp.FailStatus(c, http.StatusInternalServerError, resp.CodeDBError, "数据库错误")
		return
	}
	resp.Page(c, list, total, page, size)
}

func (a *TaskCommentAPI) Create(c *gin.Context) {
	taskID, _ := strconv.ParseInt(c.Param("task_id"), 10, 64)
	uid := mid.GetUID(c)
	if uid == 0 {
		resp.FailStatus(c, http.StatusUnauthorized, resp.CodeUnauthorized, "未登录")
		return
	}

	var req dto.CreateTaskCommentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, err)
		return
	}

	comment, err := a.svc.Create(c, taskID, uid, req)
	if err != nil {
		log.ErrorContext(c.Request.Context(), "create task comment failed", "task_id", taskID, "error", err)
		resp.FailStatus(c, http.StatusInternalServerError, resp.CodeDBError, "数据库错误")
		return
	}
	resp.Ok(c, comment)
}

func (a *TaskCommentAPI) Delete(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("comment_id"), 10, 64)
	if err := a.svc.Delete(c, id); err != nil {
		if errors.Is(err, service.ErrTaskCommentNotFound) {
			resp.FailStatus(c, http.StatusNotFound, resp.CodeNotFound, "评论不存在")
			return
		}
		log.ErrorContext(c.Request.Context(), "delete task comment failed", "id", id, "error", err)
		resp.FailStatus(c, http.StatusInternalServerError, resp.CodeDBError, "数据库错误")
		return
	}
	resp.Ok(c)
}
