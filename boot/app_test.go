package boot

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type testComponent struct {
	name             string
	started, stopped bool
}

func (c *testComponent) Name() string                { return c.name }
func (c *testComponent) Start(context.Context) error { c.started = true; return nil }
func (c *testComponent) Stop(context.Context) error  { c.stopped = true; return nil }

type dependentComponent struct {
	testComponent
	deps  []string
	order *[]string
}

type readyComponent struct{ testComponent }

func (c *readyComponent) Ready(context.Context) error { return errors.New("warming up") }

func (c *dependentComponent) DependsOn() []string { return c.deps }
func (c *dependentComponent) Start(ctx context.Context) error {
	*c.order = append(*c.order, c.name)
	return c.testComponent.Start(ctx)
}

func TestAppComponentLifecycleAndReadiness(t *testing.T) {
	a := &App{}
	first, second := &testComponent{name: "first"}, &testComponent{name: "second"}
	if err := a.AddComponent(first); err != nil {
		t.Fatal(err)
	}
	if err := a.AddComponent(second); err != nil {
		t.Fatal(err)
	}
	if err := a.AddComponent(first); err == nil {
		t.Fatal("expected duplicate component error")
	}
	if err := a.AddReadinessCheck("dependency", func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if !a.readinessOK() {
		t.Fatal("readiness should pass")
	}
	if err := a.AddReadinessCheck("dependency", func(context.Context) error { return errors.New("down") }); err == nil {
		t.Fatal("expected duplicate readiness check error")
	}
	if err := a.startComponents(1); err != nil {
		t.Fatal(err)
	}
	if !first.started || !second.started {
		t.Fatal("components did not start")
	}
	if err := a.stopComponents(1); err != nil {
		t.Fatal(err)
	}
	if !first.stopped || !second.stopped {
		t.Fatal("components did not stop")
	}
}

func TestOrderComponentsHonorsDependencies(t *testing.T) {
	order := []string{}
	c := &dependentComponent{testComponent: testComponent{name: "c"}, order: &order}
	b := &dependentComponent{testComponent: testComponent{name: "b"}, deps: []string{"c"}, order: &order}
	a := &dependentComponent{testComponent: testComponent{name: "a"}, deps: []string{"b"}, order: &order}
	ordered, err := orderComponents([]Component{a, b, c})
	if err != nil {
		t.Fatal(err)
	}
	for _, component := range ordered {
		if err := component.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := strings.Join(order, ","), "c,b,a"; got != want {
		t.Fatalf("start order = %q, want %q", got, want)
	}
}

func TestOrderComponentsRejectsUnknownDependencyAndCycle(t *testing.T) {
	missing := &dependentComponent{testComponent: testComponent{name: "a"}, deps: []string{"missing"}}
	if _, err := orderComponents([]Component{missing}); err == nil || !strings.Contains(err.Error(), "unknown component") {
		t.Fatalf("missing dependency error = %v", err)
	}
	a := &dependentComponent{testComponent: testComponent{name: "a"}, deps: []string{"b"}}
	b := &dependentComponent{testComponent: testComponent{name: "b"}, deps: []string{"a"}}
	if _, err := orderComponents([]Component{a, b}); err == nil || !strings.Contains(err.Error(), "dependency cycle") {
		t.Fatalf("cycle error = %v", err)
	}
}

func TestReadyAwareComponentIsIncludedInReadiness(t *testing.T) {
	a := &App{}
	component := &readyComponent{testComponent{name: "consumer"}}
	if err := a.AddComponent(component); err != nil {
		t.Fatal(err)
	}
	if a.readinessOK() {
		t.Fatal("readiness should fail while component is not ready")
	}
}

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

func TestEnsureOperationalRoutesAddsStartup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	ensureOperationalRoutesWithReady(r, func() bool { return true })
	req := httptest.NewRequest(http.MethodGet, "/startup", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "started") {
		t.Fatalf("startup response = %d %q", rec.Code, rec.Body.String())
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
	next.Security.RequireTLS = true
	if err := validateRuntimeConfigChange(current, next); err == nil {
		t.Fatal("expected transport security policy change to require restart")
	}
	next.Security.RequireTLS = current.Security.RequireTLS
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

func TestEnsureOperationalRoutesReportsNotReadyUntilReady(t *testing.T) {
	r := gin.New()
	ready := false
	ensureOperationalRoutesWithReady(r, func() bool { return ready })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready status = %d", w.Code)
	}

	ready = true
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("ready status = %d", w.Code)
	}
}

func TestReadinessCheckPanicAndContextHangDoNotCrashOrBlock(t *testing.T) {
	a := &App{}
	if err := a.AddReadinessCheck("panic", func(context.Context) error { panic("boom") }); err != nil {
		t.Fatal(err)
	}
	if a.readinessOK() {
		t.Fatal("panic readiness check should fail")
	}
	b := &App{readinessCacheTTL: -1}
	if err := b.AddReadinessCheck("hang", func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if b.readinessOK() {
		t.Fatal("hanging readiness check should fail")
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("readiness check exceeded bounded timeout")
	}
}

func TestRunComponentStopBoundsContextIgnoringComponent(t *testing.T) {
	stopRelease := make(chan struct{})
	component := &blockingStopComponent{release: stopRelease}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := runComponentStop(ctx, component)
	if err == nil || !strings.Contains(err.Error(), "stop timeout") {
		t.Fatalf("stop error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("stop exceeded bound: %s", elapsed)
	}
	close(stopRelease)
}

type blockingStopComponent struct {
	release chan struct{}
}

func (c *blockingStopComponent) Name() string                { return "blocking-stop" }
func (c *blockingStopComponent) Start(context.Context) error { return nil }
func (c *blockingStopComponent) Stop(context.Context) error {
	<-c.release
	return nil
}
