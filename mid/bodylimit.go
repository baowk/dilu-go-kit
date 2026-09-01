package mid

import (
	"net/http"

	"github.com/baowk/dilu-go-kit/resp"
	"github.com/gin-gonic/gin"
)

const DefaultMaxBodyBytes int64 = 10 << 20

// RequestBodyLimit bounds request bodies before application handlers decode
// them. A Content-Length that is already too large is rejected immediately;
// MaxBytesReader also protects chunked requests while they are read.
func RequestBodyLimit(maxBytes int64) gin.HandlerFunc {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBodyBytes
	}
	return func(c *gin.Context) {
		if c.Request.ContentLength > maxBytes {
			resp.FailStatus(c, http.StatusRequestEntityTooLarge, 41301, "请求体过大")
			c.Abort()
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		c.Next()
	}
}
