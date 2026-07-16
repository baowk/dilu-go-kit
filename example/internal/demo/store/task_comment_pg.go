package store

import (
	"context"

	"github.com/baowk/dilu-go-kit/example/internal/demo/model"
	base "github.com/baowk/dilu-go-kit/store"
	"gorm.io/gorm"
)

type pgTaskCommentStore struct{ db *gorm.DB }

func (s *pgTaskCommentStore) GetByID(ctx context.Context, workspaceID, taskID, id int64) (*model.TaskComment, error) {
	var c model.TaskComment
	err := s.db.WithContext(ctx).Where("workspace_id = ? AND task_id = ? AND id = ?", workspaceID, taskID, id).First(&c).Error
	return &c, err
}

func (s *pgTaskCommentStore) ListByTaskID(ctx context.Context, workspaceID, taskID int64, opts base.ListOpts) ([]*model.TaskComment, int64, error) {
	q := s.db.WithContext(ctx).Model(&model.TaskComment{}).Where("workspace_id = ? AND task_id = ?", workspaceID, taskID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []*model.TaskComment
	err := q.Order("id ASC").Offset(opts.Offset()).Limit(opts.PageSize()).Find(&list).Error
	return list, total, err
}

func (s *pgTaskCommentStore) Create(ctx context.Context, c *model.TaskComment) error {
	return s.db.WithContext(ctx).Create(c).Error
}

func (s *pgTaskCommentStore) Delete(ctx context.Context, workspaceID, taskID, id, userID int64) (int64, error) {
	r := s.db.WithContext(ctx).
		Where("workspace_id = ? AND task_id = ? AND id = ? AND user_id = ?", workspaceID, taskID, id, userID).
		Delete(&model.TaskComment{})
	return r.RowsAffected, r.Error
}
