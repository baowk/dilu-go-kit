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

type TaskAPI struct {
	svc *service.TaskService
}

func NewTaskAPI() *TaskAPI {
	return &TaskAPI{svc: service.NewTaskService()}
}

func (a *TaskAPI) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	wsID, _ := strconv.ParseInt(c.Query("workspace_id"), 10, 64)

	list, total, err := a.svc.List(c, wsID, base.ListOpts{Page: page, Size: size})
	if err != nil {
		log.ErrorContext(c.Request.Context(), "list tasks failed", "error", err)
		resp.FailStatus(c, http.StatusInternalServerError, resp.CodeDBError, "数据库错误")
		return
	}
	resp.Page(c, list, total, page, size)
}

func (a *TaskAPI) Create(c *gin.Context) {
	var req dto.CreateTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, err)
		return
	}

	task, err := a.svc.Create(c, req)
	if err != nil {
		log.ErrorContext(c.Request.Context(), "create task failed", "error", err)
		resp.FailStatus(c, http.StatusInternalServerError, resp.CodeDBError, "数据库错误")
		return
	}
	resp.Ok(c, task)
}

func (a *TaskAPI) Update(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req dto.UpdateTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, err)
		return
	}

	if err := a.svc.Update(c, id, req); err != nil {
		if errors.Is(err, service.ErrTaskNotFound) {
			resp.FailStatus(c, http.StatusNotFound, resp.CodeNotFound, "任务不存在")
			return
		}
		log.ErrorContext(c.Request.Context(), "update task failed", "id", id, "error", err)
		resp.FailStatus(c, http.StatusInternalServerError, resp.CodeDBError, "数据库错误")
		return
	}
	resp.Ok(c)
}

func (a *TaskAPI) Delete(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	uid := mid.GetUID(c)
	if uid == 0 {
		resp.FailStatus(c, http.StatusUnauthorized, resp.CodeUnauthorized, "未登录")
		return
	}
	if err := a.svc.Delete(c, id); err != nil {
		if errors.Is(err, service.ErrTaskNotFound) {
			resp.FailStatus(c, http.StatusNotFound, resp.CodeNotFound, "任务不存在")
			return
		}
		log.ErrorContext(c.Request.Context(), "delete task failed", "id", id, "error", err)
		resp.FailStatus(c, http.StatusInternalServerError, resp.CodeDBError, "数据库错误")
		return
	}
	resp.Ok(c)
}
