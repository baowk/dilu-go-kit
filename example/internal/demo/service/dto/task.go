package dto

// CreateTaskReq is the request body for creating a task.
type CreateTaskReq struct {
	WorkspaceID int64  `json:"workspace_id" binding:"required"`
	Title       string `json:"title" binding:"required,max=200"`
}

// UpdateTaskReq is the request body for updating a task.
type UpdateTaskReq struct {
	Title  string `json:"title"`
	Status *int16 `json:"status"`
}
