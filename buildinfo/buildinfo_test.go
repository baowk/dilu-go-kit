package buildinfo_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/baowk/dilu-go-kit/buildinfo"
)

func TestDevelopmentMetadata(t *testing.T) {
	want := buildinfo.Info{Repository: "unknown", Tag: "dev", Commit: "unknown", BuiltAt: "unknown", Component: "unknown"}
	if got := buildinfo.Current(); got != want {
		t.Fatalf("Current() = %+v, want %+v", got, want)
	}
}

func TestWriteVersion(t *testing.T) {
	for _, args := range [][]string{nil, {"serve"}, {"--version=false"}, {"serve", "--version"}, {"--version", "extra"}} {
		var out bytes.Buffer
		if handled, err := buildinfo.WriteVersion(args, &out); handled || err != nil || out.Len() != 0 {
			t.Fatalf("args %v: handled=%v err=%v output=%q", args, handled, err, out.String())
		}
	}
	var out bytes.Buffer
	handled, err := buildinfo.WriteVersion([]string{"--version"}, &out)
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	var info buildinfo.Info
	if err := json.Unmarshal(out.Bytes(), &info); err != nil || info != buildinfo.Current() {
		t.Fatalf("invalid output %q: %v", out.String(), err)
	}
	writeErr := errors.New("write failed")
	if handled, err := buildinfo.WriteVersion([]string{"--version"}, failingWriter{writeErr}); !handled || !errors.Is(err, writeErr) {
		t.Fatalf("write failure: handled=%v err=%v", handled, err)
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestHandler(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			out := httptest.NewRecorder()
			buildinfo.Handler().ServeHTTP(out, httptest.NewRequest(method, "/version", nil))
			if out.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("build information must not be cached")
			}
			if method == http.MethodPost {
				if out.Code != http.StatusMethodNotAllowed || out.Header().Get("Allow") != "GET, HEAD" {
					t.Fatalf("unsupported method: %v", out)
				}
				return
			}
			if out.Code != http.StatusOK || out.Header().Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatalf("invalid response: %v", out)
			}
			if method == http.MethodHead {
				if out.Body.Len() != 0 {
					t.Fatal("HEAD response has a body")
				}
				return
			}
			var info buildinfo.Info
			if err := json.Unmarshal(out.Body.Bytes(), &info); err != nil || info != buildinfo.Current() {
				t.Fatalf("invalid metadata: %+v %v", info, err)
			}
		})
	}
}

func TestLinkedBinary(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte(`package main
import "github.com/baowk/dilu-go-kit/buildinfo"
func main() {
    handled, err := buildinfo.PrintVersion()
    if err != nil { panic(err) }
    if handled { return }
    panic("normal startup reached")
}
`), 0600); err != nil {
		t.Fatal(err)
	}
	want := buildinfo.Info{Repository: "example-service", Tag: "v1.2.3", Commit: "0123456789abcdef", BuiltAt: "2026-10-09T01:02:03Z", Component: "api"}
	prefix := "github.com/baowk/dilu-go-kit/buildinfo."
	flags := "-X " + prefix + "Repository=" + want.Repository + " -X " + prefix + "Tag=" + want.Tag +
		" -X " + prefix + "Commit=" + want.Commit + " -X " + prefix + "BuiltAt=" + want.BuiltAt + " -X " + prefix + "Component=" + want.Component
	binary := filepath.Join(dir, "app")
	if out, err := exec.Command("go", "build", "-ldflags", flags, "-o", binary, source).CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	out, err := exec.Command(binary, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("--version triggered startup: %v\n%s", err, out)
	}
	var got buildinfo.Info
	if err := json.Unmarshal(out, &got); err != nil || got != want {
		t.Fatalf("linked metadata = %+v, want %+v: %v", got, want, err)
	}
	if err := exec.Command(binary).Run(); err == nil {
		t.Fatal("expected normal startup without --version")
	}
}
