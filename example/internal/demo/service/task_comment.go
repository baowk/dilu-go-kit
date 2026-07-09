package service

import (
	"context"
	"errors"

	"github.com/baowk/dilu-go-kit/example/internal/demo/model"
	"github.com/baowk/dilu-go-kit/example/internal/demo/service/dto"
	"github.com/baowk/dilu-go-kit/example/internal/demo/store"
	base "github.com/baowk/dilu-go-kit/store"
)

var ErrTaskCommentNotFound = errors.New("task comment not found")

// TaskCommentService contains task comment business logic.
type TaskCommentService struct{}

func NewTaskCommentService() *TaskCommentService {
	return &TaskCommentService{}
}

func (s *TaskCommentService) List(ctx context.Context, taskID int64, opts base.ListOpts) ([]*model.TaskComment, int64, error) {
	return store.S().TaskComment.ListByTaskID(ctx, taskID, opts)
}

func (s *TaskCommentService) Create(ctx context.Context, taskID, userID int64, req dto.CreateTaskCommentReq) (*model.TaskComment, error) {
	comment := &model.TaskComment{
		TaskID:  taskID,
		UserID:  userID,
		Content: req.Content,
	}
	if err := store.S().TaskComment.Create(ctx, comment); err != nil {
		return nil, err
	}
	return comment, nil
}

func (s *TaskCommentService) Delete(ctx context.Context, id int64) error {
	rows, err := store.S().TaskComment.Delete(ctx, id)
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrTaskCommentNotFound
	}
	return nil
}
