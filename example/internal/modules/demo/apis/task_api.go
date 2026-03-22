package apis

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/baowk/dilu-go-kit/example/internal/modules/demo/model"
	"github.com/baowk/dilu-go-kit/example/internal/modules/demo/store"
	"github.com/baowk/dilu-go-kit/mid"
	"github.com/baowk/dilu-go-kit/resp"
	base "github.com/baowk/dilu-go-kit/store"
)

type TaskAPI struct{}

func (a *TaskAPI) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	wsID, _ := strconv.ParseInt(c.Query("workspace_id"), 10, 64)

	list, total, err := store.S().Task.List(c, wsID, base.ListOpts{Page: page, Size: size})
	if err != nil {
		resp.Fail(c, 50001, err.Error())
		return
	}
	resp.Page(c, list, total, page, size)
}

func (a *TaskAPI) Create(c *gin.Context) {
	var req struct {
		WorkspaceID int64  `json:"workspace_id" binding:"required"`
		Title       string `json:"title" binding:"required,max=200"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, err)
		return
	}

	task := &model.Task{
		WorkspaceID: req.WorkspaceID,
		Title:       req.Title,
		Status:      1,
	}
	if err := store.S().Task.Create(c, task); err != nil {
		resp.Fail(c, 50001, err.Error())
		return
	}
	resp.Ok(c, task)
}

func (a *TaskAPI) Update(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		Title  string `json:"title"`
		Status *int16 `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, err)
		return
	}

	updates := make(map[string]any)
	if req.Title != "" {
		updates["title"] = req.Title
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}
	if len(updates) == 0 {
		resp.Ok(c)
		return
	}

	rows, err := store.S().Task.Update(c, id, updates)
	if err != nil {
		resp.Fail(c, 50001, err.Error())
		return
	}
	if rows == 0 {
		resp.Fail(c, 40401, "任务不存在")
		return
	}
	resp.Ok(c)
}

func (a *TaskAPI) Delete(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	uid := mid.GetUID(c)
	if uid == 0 {
		resp.Fail(c, 40101, "未登录")
		return
	}
	rows, err := store.S().Task.Delete(c, id)
	if err != nil {
		resp.Fail(c, 50001, err.Error())
		return
	}
	if rows == 0 {
		resp.Fail(c, 40401, "任务不存在")
		return
	}
	resp.Ok(c)
}
