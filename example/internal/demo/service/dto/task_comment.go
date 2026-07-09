package dto

// CreateTaskCommentReq is the request body for creating a task comment.
type CreateTaskCommentReq struct {
	Content string `json:"content" binding:"required,max=1000"`
}
