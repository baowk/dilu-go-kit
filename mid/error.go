package mid

import (
	"fmt"
	"strings"

	"github.com/baowk/dilu-go-kit/log"
	"github.com/baowk/dilu-go-kit/resp"
	"github.com/gin-gonic/gin"
)

// AppError is a typed error that can be caught by the ErrorHandler middleware.
type AppError struct {
	Code int
	Msg  string
}

func (e *AppError) Error() string {
	return fmt.Sprintf("AppError(%d): %s", e.Code, e.Msg)
}

// NewAppError creates an AppError for panic-based error handling.
func NewAppError(code int, msg string) *AppError {
	return &AppError{Code: code, Msg: msg}
}

// PanicApp panics with an AppError, to be caught by ErrorHandler.
func PanicApp(code int, msg string) {
	panic(NewAppError(code, msg))
}

// ErrorHandler returns a middleware that catches panics and converts them
// to proper JSON responses. It handles:
//   - *AppError → resp.Fail with the error's code and message
//   - "CustomError#code#msg" strings (legacy dilu format)
//   - Other panics → 500 internal error
func ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				switch v := r.(type) {
				case *AppError:
					resp.Fail(c, v.Code, v.Msg)
				case string:
					// Legacy format: "CustomError#code#msg"
					if strings.HasPrefix(v, "CustomError#") {
						parts := strings.SplitN(v, "#", 3)
						if len(parts) == 3 {
							code := 50000
							fmt.Sscanf(parts[1], "%d", &code)
							resp.Fail(c, code, parts[2])
						} else {
							resp.Fail(c, 50000, v)
						}
					} else {
						log.ErrorContext(c.Request.Context(), "panic", "error", v)
						resp.Fail(c, 50000, "服务内部错误")
					}
				default:
					log.ErrorContext(c.Request.Context(), "panic", "error", fmt.Sprintf("%v", r))
					resp.Fail(c, 50000, "服务内部错误")
				}
				c.Abort()
			}
		}()
		c.Next()
	}
}
