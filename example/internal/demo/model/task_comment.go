package model

import "time"

// TaskComment 示例任务评论表
type TaskComment struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	TaskID    int64     `gorm:"column:task_id;index" json:"task_id"`
	UserID    int64     `gorm:"column:user_id;index" json:"user_id"`
	Content   string    `gorm:"column:content;size:1000" json:"content"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (TaskComment) TableName() string { return "task_comment" }
