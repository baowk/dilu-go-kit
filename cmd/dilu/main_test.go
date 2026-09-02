package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateName(t *testing.T) {
	for _, name := range []string{"user-service", "a1"} {
		if err := validateName(name); err != nil {
			t.Errorf("validateName(%q): %v", name, err)
		}
	}
	for _, name := range []string{"User", "-user", "user_service", ""} {
		if err := validateName(name); err == nil {
			t.Errorf("validateName(%q) should fail", name)
		}
	}
}

func TestPascalName(t *testing.T) {
	for input, want := range map[string]string{
		"user-service": "UserService",
		"task_comment": "TaskComment",
		"orders":       "Orders",
	} {
		if got := pascalName(input); got != want {
			t.Errorf("pascalName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNewServiceAndAddResource(t *testing.T) {
	dir := t.TempDir()
	if err := newService([]string{"orders", "--dir", dir, "--module", "example.com/orders"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"api/orders/v1/orders.proto", "buf.yaml", "cmd/orders/main.go"} {
		if _, err := os.Stat(filepath.Join(dir, path)); err != nil {
			t.Fatalf("missing generated file %s: %v", path, err)
		}
	}
	if err := addResource([]string{"task", "--dir", dir, "--module", "orders"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "internal/orders/model/task.go")); err != nil {
		t.Fatal(err)
	}
	if err := newService([]string{"orders", "--dir", dir, "--module", "example.com/orders"}); err == nil {
		t.Fatal("expected overwrite protection")
	}
}

func TestAddResourceRejectsPathTraversalModule(t *testing.T) {
	if err := addResource([]string{"task", "--dir", t.TempDir(), "--module", "../outside"}); err == nil {
		t.Fatal("expected path traversal module to be rejected")
	}
}
