package buildinfo

import (
	"encoding/json"
	"net/http"
)

// Handler serves build metadata for GET and HEAD. Applications choose the route
// and access controls explicitly; importing buildinfo registers no routes.
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		// Info contains only strings, so encoding cannot fail before writing.
		// A disconnected client can still cause a write error after headers.
		_ = json.NewEncoder(w).Encode(Current())
	})
}
