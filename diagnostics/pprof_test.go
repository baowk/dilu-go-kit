package diagnostics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRegisterPprof(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterPprof(r, "")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestHandlerWithAccessRequiresTokenAndCIDR(t *testing.T) {
	h := HandlerWithAccess("", "secret", []string{"127.0.0.1/32"})
	for _, tc := range []struct {
		name, remote, auth string
		want               int
	}{
		{"missing", "127.0.0.1:1234", "", http.StatusForbidden},
		{"wrong source", "10.0.0.1:1234", "Bearer secret", http.StatusForbidden},
		{"ok", "127.0.0.1:1234", "Bearer secret", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
			req.RemoteAddr = tc.remote
			req.Header.Set("Authorization", tc.auth)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
		})
	}
}
