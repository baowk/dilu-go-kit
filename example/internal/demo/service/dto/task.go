package dto

// CreateTaskReq is the request body for creating a task.
type CreateTaskReq struct {
	Title string `json:"title" binding:"required,max=200"`
}

// UpdateTaskReq is the request body for updating a task.
type UpdateTaskReq struct {
	Title  string `json:"title" binding:"omitempty,max=200"`
	Status *int16 `json:"status" binding:"omitempty,oneof=1 2"`
}
