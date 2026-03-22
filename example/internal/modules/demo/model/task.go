package model

import "time"

// Task 示例任务表
type Task struct {
	ID          int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	WorkspaceID int64     `gorm:"column:workspace_id" json:"workspace_id"`
	Title       string    `gorm:"column:title;size:200" json:"title"`
	Status      int16     `gorm:"column:status;default:1" json:"status"` // 1=pending 2=done
	CreatedAt   time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (Task) TableName() string { return "task" }
