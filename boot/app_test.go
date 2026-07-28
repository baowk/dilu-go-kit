package boot

import (
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func TestResolveAdvertiseAddrUsesExplicitValue(t *testing.T) {
	t.Setenv("MF_ADVERTISE_IP", "10.0.1.9")
	got := resolveAdvertiseAddr(":8080", "10.0.1.5:8080")
	if got != "10.0.1.5:8080" {
		t.Fatalf("resolveAdvertiseAddr = %q", got)
	}
}

func TestNewHTTPServerUsesSafeDefaults(t *testing.T) {
	srv := newHTTPServer(ServerConfig{Addr: ":0"}, gin.New())
	if srv.ReadHeaderTimeout != 5*time.Second || srv.IdleTimeout != 60*time.Second {
		t.Fatalf("unexpected server timeouts: %+v", srv)
	}
	if srv.MaxHeaderBytes != 1<<20 {
		t.Fatalf("max header bytes = %d", srv.MaxHeaderBytes)
	}
}

func TestRunReturnsWhenHTTPPortCannotBind(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	app := &App{
		Config: &Config{Server: ServerConfig{Name: "test", Addr: listener.Addr().String()}},
		Gin:    gin.New(), DBs: map[string]*gorm.DB{},
	}
	err = app.Run(func(*App) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "http listen") {
		t.Fatalf("Run error = %v", err)
	}
}

func TestValidateRuntimeConfigRejectsStartupFields(t *testing.T) {
	current := &Config{Server: ServerConfig{Name: "test", Addr: ":8080"}}
	next := cloneConfig(current)
	next.Server.Addr = ":9090"
	if err := validateRuntimeConfigChange(current, next); err == nil {
		t.Fatal("expected startup field change to be rejected")
	}
	next.Server.Addr = current.Server.Addr
	next.JWT.Secret = "rotated"
	if err := validateRuntimeConfigChange(current, next); err != nil {
		t.Fatalf("dynamic JWT change rejected: %v", err)
	}
}

func TestResolveAdvertiseAddrExpandsUnspecifiedHost(t *testing.T) {
	got := resolveAdvertiseAddr(":8080", "")
	host, port, err := net.SplitHostPort(got)
	if err != nil {
		t.Fatalf("advertise addr should be host:port, got %q: %v", got, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		t.Fatalf("advertise host should be reachable, got %q", host)
	}
	if port != "8080" {
		t.Fatalf("advertise port = %q", port)
	}
}

func TestResolveAdvertiseAddrKeepsSpecificHost(t *testing.T) {
	got := resolveAdvertiseAddr("127.0.0.1:8080", "")
	if got != "127.0.0.1:8080" {
		t.Fatalf("resolveAdvertiseAddr = %q", got)
	}
}

func TestResolveAdvertiseAddrUsesMFAdvertiseIPBeforePodIP(t *testing.T) {
	t.Setenv("MF_ADVERTISE_IP", "10.0.1.5")
	t.Setenv("POD_IP", "10.0.1.6")
	t.Setenv("SERVICE_IP", "10.0.1.7")

	got := resolveAdvertiseAddr(":8080", "")
	if got != "10.0.1.5:8080" {
		t.Fatalf("resolveAdvertiseAddr = %q", got)
	}
}

func TestResolveAdvertiseAddrUsesPodIPFallback(t *testing.T) {
	t.Setenv("POD_IP", "10.0.1.6")
	t.Setenv("SERVICE_IP", "10.0.1.7")

	got := resolveAdvertiseAddr("127.0.0.1:8080", "")
	if got != "10.0.1.6:8080" {
		t.Fatalf("resolveAdvertiseAddr = %q", got)
	}
}

func TestEnsureOperationalRoutesAddsHealthAndReady(t *testing.T) {
	r := gin.New()
	ensureOperationalRoutes(r)

	if !hasRoute(r, http.MethodGet, "/health") {
		t.Fatal("missing /health route")
	}
	if !hasRoute(r, http.MethodGet, "/ready") {
		t.Fatal("missing /ready route")
	}
}

func TestEnsureOperationalRoutesKeepsExistingReady(t *testing.T) {
	r := gin.New()
	r.GET("/ready", func(c *gin.Context) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not-ready"})
	})
	ensureOperationalRoutes(r)

	count := 0
	for _, route := range r.Routes() {
		if route.Method == http.MethodGet && route.Path == "/ready" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("/ready route count = %d", count)
	}
}
