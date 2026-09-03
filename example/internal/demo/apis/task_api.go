package apis

import (
	"errors"
	"net/http"
	"strconv"

	jwtmid "github.com/baowk/dilu-go-kit/contrib/mid/jwt"
	"github.com/baowk/dilu-go-kit/example/internal/demo/service"
	"github.com/baowk/dilu-go-kit/example/internal/demo/service/dto"
	"github.com/baowk/dilu-go-kit/log"
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
	workspaceID, ok := requireWorkspace(c)
	if !ok {
		return
	}
	page, size, ok := parsePagination(c)
	if !ok {
		return
	}

	list, total, err := a.svc.List(c, workspaceID, base.ListOpts{Page: page, Size: size})
	if err != nil {
		log.ErrorContext(c.Request.Context(), "list tasks failed", "error", err)
		resp.FailStatus(c, http.StatusInternalServerError, resp.CodeDBError, "数据库错误")
		return
	}
	resp.Page(c, list, total, page, size)
}

func (a *TaskAPI) Create(c *gin.Context) {
	workspaceID, ok := requireWorkspace(c)
	if !ok {
		return
	}
	var req dto.CreateTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, err)
		return
	}

	task, err := a.svc.Create(c, workspaceID, req)
	if err != nil {
		log.ErrorContext(c.Request.Context(), "create task failed", "error", err)
		resp.FailStatus(c, http.StatusInternalServerError, resp.CodeDBError, "数据库错误")
		return
	}
	resp.Ok(c, task)
}

func (a *TaskAPI) Update(c *gin.Context) {
	workspaceID, ok := requireWorkspace(c)
	if !ok {
		return
	}
	id, ok := parsePositiveID(c, "id")
	if !ok {
		return
	}
	var req dto.UpdateTaskReq
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, err)
		return
	}

	if err := a.svc.Update(c, workspaceID, id, req); err != nil {
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
	workspaceID, ok := requireWorkspace(c)
	if !ok {
		return
	}
	id, ok := parsePositiveID(c, "id")
	if !ok {
		return
	}
	if err := a.svc.Delete(c, workspaceID, id); err != nil {
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

func requireWorkspace(c *gin.Context) (int64, bool) {
	workspaceID := jwtmid.GetWorkspaceID(c)
	if workspaceID <= 0 {
		resp.FailStatus(c, http.StatusForbidden, resp.CodeForbidden, "缺少工作区权限")
		return 0, false
	}
	return workspaceID, true
}

func parsePositiveID(c *gin.Context, name string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || id <= 0 {
		resp.FailStatus(c, http.StatusBadRequest, resp.CodeInvalidParam, "参数错误")
		return 0, false
	}
	return id, true
}

func parsePagination(c *gin.Context) (int, int, bool) {
	page, errPage := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, errSize := strconv.Atoi(c.DefaultQuery("size", "20"))
	if errPage != nil || errSize != nil || page <= 0 || size <= 0 {
		resp.FailStatus(c, http.StatusBadRequest, resp.CodeInvalidParam, "分页参数错误")
		return 0, 0, false
	}
	if size > 500 {
		size = 500
	}
	return page, size, true
}
