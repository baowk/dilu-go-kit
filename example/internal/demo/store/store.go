package store

import (
	"context"

	"github.com/baowk/dilu-go-kit/example/internal/demo/model"
	base "github.com/baowk/dilu-go-kit/store"
	"gorm.io/gorm"
)

// TaskStore defines the data access interface for tasks.
type TaskStore interface {
	GetByID(ctx context.Context, workspaceID, id int64) (*model.Task, error)
	List(ctx context.Context, wsID int64, opts base.ListOpts) ([]*model.Task, int64, error)
	Create(ctx context.Context, t *model.Task) error
	Update(ctx context.Context, workspaceID, id int64, updates map[string]any) (int64, error)
	Delete(ctx context.Context, workspaceID, id int64) (int64, error)
}

// TaskCommentStore defines the data access interface for task comments.
type TaskCommentStore interface {
	GetByID(ctx context.Context, workspaceID, taskID, id int64) (*model.TaskComment, error)
	ListByTaskID(ctx context.Context, workspaceID, taskID int64, opts base.ListOpts) ([]*model.TaskComment, int64, error)
	Create(ctx context.Context, c *model.TaskComment) error
	Delete(ctx context.Context, workspaceID, taskID, id, userID int64) (int64, error)
}

// Stores holds all store instances.
type Stores struct {
	Task        TaskStore
	TaskComment TaskCommentStore
}

var s *Stores

// Init creates all stores from a gorm.DB (call once at startup).
func Init(db *gorm.DB) {
	s = &Stores{
		Task:        &pgTaskStore{db: db},
		TaskComment: &pgTaskCommentStore{db: db},
	}
}

// S returns the global Stores instance.
func S() *Stores { return s }
