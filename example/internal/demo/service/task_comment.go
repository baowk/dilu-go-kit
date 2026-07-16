package service

import (
	"context"
	"errors"

	"github.com/baowk/dilu-go-kit/example/internal/demo/model"
	"github.com/baowk/dilu-go-kit/example/internal/demo/service/dto"
	"github.com/baowk/dilu-go-kit/example/internal/demo/store"
	base "github.com/baowk/dilu-go-kit/store"
	"gorm.io/gorm"
)

var ErrTaskCommentNotFound = errors.New("task comment not found")

// TaskCommentService contains task comment business logic.
type TaskCommentService struct{}

func NewTaskCommentService() *TaskCommentService {
	return &TaskCommentService{}
}

func (s *TaskCommentService) List(ctx context.Context, workspaceID, taskID int64, opts base.ListOpts) ([]*model.TaskComment, int64, error) {
	if _, err := store.S().Task.GetByID(ctx, workspaceID, taskID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, 0, ErrTaskNotFound
		}
		return nil, 0, err
	}
	return store.S().TaskComment.ListByTaskID(ctx, workspaceID, taskID, opts)
}

func (s *TaskCommentService) Create(ctx context.Context, workspaceID, taskID, userID int64, req dto.CreateTaskCommentReq) (*model.TaskComment, error) {
	if _, err := store.S().Task.GetByID(ctx, workspaceID, taskID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTaskNotFound
		}
		return nil, err
	}
	comment := &model.TaskComment{
		WorkspaceID: workspaceID,
		TaskID:      taskID,
		UserID:      userID,
		Content:     req.Content,
	}
	if err := store.S().TaskComment.Create(ctx, comment); err != nil {
		return nil, err
	}
	return comment, nil
}

func (s *TaskCommentService) Delete(ctx context.Context, workspaceID, taskID, id, userID int64) error {
	rows, err := store.S().TaskComment.Delete(ctx, workspaceID, taskID, id, userID)
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrTaskCommentNotFound
	}
	return nil
}
