// Package diagnostics exposes opt-in runtime diagnostics for local or
// protected admin endpoints.
package diagnostics

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/http/pprof"
	"strings"

	"github.com/gin-gonic/gin"
)

// Config controls optional diagnostics route registration.
type Config struct {
	Pprof PprofConfig `mapstructure:"pprof"`
}

type PprofConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	Prefix       string `mapstructure:"prefix"`
	Addr         string `mapstructure:"addr"`
	AuthToken    string `mapstructure:"authToken"`
	AllowedCIDRs string `mapstructure:"allowedCidrs"`
}

// RegisterFromConfig registers pprof only when explicitly enabled.
func RegisterFromConfig(r *gin.Engine, cfg Config) {
	if cfg.Pprof.Enabled && cfg.Pprof.Addr == "" {
		RegisterPprofWithAccess(r, cfg.Pprof.Prefix, cfg.Pprof.AuthToken, splitCIDRs(cfg.Pprof.AllowedCIDRs))
	}
}

// Handler returns an isolated HTTP handler for pprof. It is intended for a
// loopback or protected management listener.
func Handler(prefix string) http.Handler {
	return HandlerWithAccess(prefix, "", []string{"127.0.0.0/8", "::1/128"})
}

// HandlerWithAccess returns an isolated pprof handler protected by an
// optional bearer token and source CIDR allowlist.
func HandlerWithAccess(prefix, token string, allowedCIDRs []string) http.Handler {
	prefix = normalizePrefix(prefix)
	mux := http.NewServeMux()
	mux.HandleFunc(prefix, pprof.Index)
	mux.HandleFunc(prefix+"/", pprof.Index)
	mux.HandleFunc(prefix+"/cmdline", pprof.Cmdline)
	mux.HandleFunc(prefix+"/profile", pprof.Profile)
	mux.HandleFunc(prefix+"/symbol", pprof.Symbol)
	mux.HandleFunc(prefix+"/trace", pprof.Trace)
	for _, name := range []string{"allocs", "block", "goroutine", "heap", "mutex", "threadcreate"} {
		mux.Handle(prefix+"/"+name, pprof.Handler(name))
	}
	return accessHandler(mux, token, allowedCIDRs)
}

// RegisterPprof registers the standard net/http pprof handlers below prefix.
// It is intentionally explicit: callers must protect the routes with network
// policy or authentication before exposing them outside localhost.
func RegisterPprof(r *gin.Engine, prefix string) {
	RegisterPprofWithAccess(r, prefix, "", nil)
}

// RegisterPprofWithAccess registers pprof routes with optional access control.
func RegisterPprofWithAccess(r *gin.Engine, prefix, token string, allowedCIDRs []string) {
	if r == nil {
		return
	}
	if prefix == "" {
		prefix = "/debug/pprof"
	}
	prefix = normalizePrefix(prefix)
	group := r.Group(prefix, ginAccessMiddleware(token, allowedCIDRs))
	group.GET("", gin.WrapF(pprof.Index))
	group.GET("/", gin.WrapF(pprof.Index))
	group.GET("/cmdline", gin.WrapF(pprof.Cmdline))
	group.GET("/profile", gin.WrapF(pprof.Profile))
	group.POST("/symbol", gin.WrapF(pprof.Symbol))
	group.GET("/symbol", gin.WrapF(pprof.Symbol))
	group.GET("/trace", gin.WrapF(pprof.Trace))
	group.GET("/allocs", gin.WrapH(pprof.Handler("allocs")))
	group.GET("/block", gin.WrapH(pprof.Handler("block")))
	group.GET("/goroutine", gin.WrapH(pprof.Handler("goroutine")))
	group.GET("/heap", gin.WrapH(pprof.Handler("heap")))
	group.GET("/mutex", gin.WrapH(pprof.Handler("mutex")))
	group.GET("/threadcreate", gin.WrapH(pprof.Handler("threadcreate")))
}

func normalizePrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return "/debug/pprof"
	}
	if !strings.HasPrefix(prefix, "/") {
		prefix = "/" + prefix
	}
	prefix = strings.TrimRight(prefix, "/")
	if len(prefix) > 128 || strings.Contains(prefix, "..") || strings.ContainsAny(prefix, "\r\n") {
		return "/debug/pprof"
	}
	return prefix
}

func splitCIDRs(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func ginAccessMiddleware(token string, allowedCIDRs []string) gin.HandlerFunc {
	allowed := parseCIDRs(allowedCIDRs)
	return func(c *gin.Context) {
		if !sourceAllowed(c.Request, allowed) || !tokenAllowed(c.Request, token) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
}

func accessHandler(next http.Handler, token string, allowedCIDRs []string) http.Handler {
	allowed := parseCIDRs(allowedCIDRs)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sourceAllowed(r, allowed) || !tokenAllowed(r, token) {
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func tokenAllowed(r *http.Request, expected string) bool {
	if expected == "" {
		return true
	}
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(value) < len("Bearer ") || !strings.EqualFold(value[:len("Bearer ")], "Bearer ") {
		return false
	}
	got := strings.TrimSpace(value[len("Bearer "):])
	return len(got) == len(expected) && subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

func parseCIDRs(values []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(values))
	for _, value := range values {
		_, network, err := net.ParseCIDR(strings.TrimSpace(value))
		if err == nil {
			out = append(out, network)
		}
	}
	return out
}

func sourceAllowed(r *http.Request, allowed []*net.IPNet) bool {
	if len(allowed) == 0 {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, network := range allowed {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}
