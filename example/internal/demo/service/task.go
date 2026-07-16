package service

import (
	"context"
	"errors"

	"github.com/baowk/dilu-go-kit/example/internal/demo/model"
	"github.com/baowk/dilu-go-kit/example/internal/demo/service/dto"
	"github.com/baowk/dilu-go-kit/example/internal/demo/store"
	base "github.com/baowk/dilu-go-kit/store"
)

var ErrTaskNotFound = errors.New("task not found")

// TaskService contains task business logic.
type TaskService struct{}

func NewTaskService() *TaskService {
	return &TaskService{}
}

func (s *TaskService) List(ctx context.Context, wsID int64, opts base.ListOpts) ([]*model.Task, int64, error) {
	return store.S().Task.List(ctx, wsID, opts)
}

func (s *TaskService) Create(ctx context.Context, workspaceID int64, req dto.CreateTaskReq) (*model.Task, error) {
	task := &model.Task{
		WorkspaceID: workspaceID,
		Title:       req.Title,
		Status:      1,
	}
	if err := store.S().Task.Create(ctx, task); err != nil {
		return nil, err
	}
	return task, nil
}

func (s *TaskService) Update(ctx context.Context, workspaceID, id int64, req dto.UpdateTaskReq) error {
	updates := make(map[string]any)
	if req.Title != "" {
		updates["title"] = req.Title
	}
	if req.Status != nil {
		updates["status"] = *req.Status
	}
	if len(updates) == 0 {
		return nil
	}

	rows, err := store.S().Task.Update(ctx, workspaceID, id, updates)
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrTaskNotFound
	}
	return nil
}

func (s *TaskService) Delete(ctx context.Context, workspaceID, id int64) error {
	rows, err := store.S().Task.Delete(ctx, workspaceID, id)
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrTaskNotFound
	}
	return nil
}
