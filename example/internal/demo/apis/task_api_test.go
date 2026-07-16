package apis

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequireWorkspaceRejectsMissingClaim(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", func(c *gin.Context) {
		if _, ok := requireWorkspace(c); ok {
			t.Fatal("expected workspace to be required")
		}
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestRequireWorkspaceUsesAuthenticatedContextOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("workspace_id", int64(42))
		c.Next()
	})
	r.GET("/x", func(c *gin.Context) {
		workspaceID, ok := requireWorkspace(c)
		if !ok || workspaceID != 42 {
			t.Fatalf("workspace_id = %d ok=%v", workspaceID, ok)
		}
		c.Status(http.StatusNoContent)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x?workspace_id=999", nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d", w.Code)
	}
}
