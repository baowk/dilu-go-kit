package migratex

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSourceURL(t *testing.T) {
	got, err := SourceURL("migrations")
	if err != nil {
		t.Fatalf("SourceURL error: %v", err)
	}
	if !strings.HasPrefix(got, "file://") {
		t.Fatalf("SourceURL = %q", got)
	}
	if !strings.Contains(got, "/migrations") {
		t.Fatalf("SourceURL missing dir: %q", got)
	}
}

func TestSourceURLKeepsFileURL(t *testing.T) {
	got, err := SourceURL("file:///tmp/migrations")
	if err != nil {
		t.Fatalf("SourceURL error: %v", err)
	}
	if got != "file:///tmp/migrations" {
		t.Fatalf("SourceURL = %q", got)
	}
}

func TestCreateMigrationFiles(t *testing.T) {
	dir := t.TempDir()
	up, down, err := Create(dir, "Add Order Status!")
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}
	if filepath.Dir(up) != dir || filepath.Dir(down) != dir {
		t.Fatalf("unexpected dirs: %q %q", up, down)
	}
	if !strings.HasSuffix(up, "_add_order_status.up.sql") {
		t.Fatalf("unexpected up path: %q", up)
	}
	if !strings.HasSuffix(down, "_add_order_status.down.sql") {
		t.Fatalf("unexpected down path: %q", down)
	}
}

func TestDownRequiresPositiveSteps(t *testing.T) {
	err := New("postgres://example", "file:///tmp/migrations").Down(0)
	if err == nil || !strings.Contains(err.Error(), "steps must be positive") {
		t.Fatalf("expected positive steps error, got %v", err)
	}
}

func TestSlugName(t *testing.T) {
	if got := slugName("  Add--Order Status! "); got != "add_order_status" {
		t.Fatalf("slugName = %q", got)
	}
}
