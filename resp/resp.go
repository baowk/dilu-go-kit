// Package resp provides a unified HTTP response format for Gin-based APIs.
//
// Usage:
//
//	resp.Ok(c, user)
//	resp.Fail(c, 40101, "未登录")
//	resp.Page(c, list, total, page, size)
package resp

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// R is the standard API response envelope.
type R struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data any    `json:"data,omitempty"`
}

// PageData wraps paginated results.
type PageData struct {
	List        any   `json:"list"`
	Total       int64 `json:"total"`
	PageSize    int   `json:"pageSize"`
	CurrentPage int   `json:"currentPage"`
}

// Ok sends a 200 success response with optional data.
func Ok(c *gin.Context, data ...any) {
	r := R{Code: 200, Msg: "OK"}
	if len(data) > 0 {
		r.Data = data[0]
	}
	c.JSON(http.StatusOK, r)
}

// Fail sends a business error response (HTTP 200, error in code field).
func Fail(c *gin.Context, code int, msg string) {
	c.JSON(http.StatusOK, R{Code: code, Msg: msg})
}

// Page sends a paginated success response.
func Page(c *gin.Context, list any, total int64, page, size int) {
	if page <= 0 {
		page = 1
	}
	c.JSON(http.StatusOK, R{
		Code: 200,
		Msg:  "OK",
		Data: PageData{
			List:        list,
			Total:       total,
			PageSize:    size,
			CurrentPage: page,
		},
	})
}

// Error sends a parameter binding error (code 40001).
func Error(c *gin.Context, err error) {
	c.JSON(http.StatusOK, R{Code: 40001, Msg: err.Error()})
}
