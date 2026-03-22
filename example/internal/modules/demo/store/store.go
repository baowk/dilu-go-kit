package store

import (
	"context"

	"github.com/baowk/dilu-go-kit/example/internal/modules/demo/model"
	base "github.com/baowk/dilu-go-kit/store"
	"gorm.io/gorm"
)

// TaskStore defines the data access interface for tasks.
type TaskStore interface {
	GetByID(ctx context.Context, id int64) (*model.Task, error)
	List(ctx context.Context, wsID int64, opts base.ListOpts) ([]*model.Task, int64, error)
	Create(ctx context.Context, t *model.Task) error
	Update(ctx context.Context, id int64, updates map[string]any) (int64, error)
	Delete(ctx context.Context, id int64) (int64, error)
}

// Stores holds all store instances.
type Stores struct {
	Task TaskStore
}

var s *Stores

// Init creates all stores from a gorm.DB (call once at startup).
func Init(db *gorm.DB) {
	s = &Stores{
		Task: &pgTaskStore{db: db},
	}
}

// S returns the global Stores instance.
func S() *Stores { return s }
