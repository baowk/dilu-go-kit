package store

import (
	"context"

	"github.com/mofang-ai/mofang-go-kit/example/internal/modules/demo/model"
	base "github.com/mofang-ai/mofang-go-kit/store"
	"gorm.io/gorm"
)

type pgTaskStore struct{ db *gorm.DB }

func (s *pgTaskStore) GetByID(ctx context.Context, id int64) (*model.Task, error) {
	var t model.Task
	err := s.db.WithContext(ctx).Where("id = ?", id).First(&t).Error
	return &t, err
}

func (s *pgTaskStore) List(ctx context.Context, wsID int64, opts base.ListOpts) ([]*model.Task, int64, error) {
	q := s.db.WithContext(ctx).Model(&model.Task{})
	if wsID != 0 {
		q = q.Where("workspace_id = ?", wsID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []*model.Task
	err := q.Order("id DESC").Offset(opts.Offset()).Limit(opts.PageSize()).Find(&list).Error
	return list, total, err
}

func (s *pgTaskStore) Create(ctx context.Context, t *model.Task) error {
	return s.db.WithContext(ctx).Create(t).Error
}

func (s *pgTaskStore) Update(ctx context.Context, id int64, updates map[string]any) (int64, error) {
	r := s.db.WithContext(ctx).Model(&model.Task{}).Where("id = ?", id).Updates(updates)
	return r.RowsAffected, r.Error
}

func (s *pgTaskStore) Delete(ctx context.Context, id int64) (int64, error) {
	r := s.db.WithContext(ctx).Where("id = ?", id).Delete(&model.Task{})
	return r.RowsAffected, r.Error
}
