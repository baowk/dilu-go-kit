// Command dilu scaffolds services and resources that follow the dilu-go-kit
// conventions. It intentionally generates small, boring files so they are
// easy for both humans and AI coding tools to inspect and modify.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

var identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "dilu:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	switch args[0] {
	case "new":
		if len(args) < 2 || args[1] != "service" {
			return errors.New("usage: dilu new service NAME [--dir DIR] [--module MODULE]")
		}
		return newService(args[2:])
	case "add":
		if len(args) < 2 || args[1] != "resource" {
			return errors.New("usage: dilu add resource NAME [--dir DIR] [--module MODULE]")
		}
		return addResource(args[2:])
	case "generate":
		return generate(args[1:])
	case "help", "-h", "--help":
		return usageError()
	default:
		return fmt.Errorf("unknown command %q; use `dilu help`", args[0])
	}
}

func usageError() error {
	return errors.New("usage: dilu new service NAME | dilu add resource NAME | dilu generate")
}

func newService(args []string) error {
	if len(args) == 0 {
		return errors.New("service name is required")
	}
	name := args[0]
	if err := validateName(name); err != nil {
		return err
	}
	dir := "."
	module := "example.com/" + name
	dir, module, err := parseProjectFlags(args[1:], dir, module)
	if err != nil {
		return err
	}
	root := filepath.Clean(dir)
	files := serviceFiles(name, module)
	return writeFiles(root, files)
}

func addResource(args []string) error {
	if len(args) == 0 {
		return errors.New("resource name is required")
	}
	resource := args[0]
	if err := validateName(resource); err != nil {
		return err
	}
	dir, module, err := parseProjectFlags(args[1:], ".", "")
	if err != nil {
		return err
	}
	if module == "" {
		module, err = detectSingleModule(filepath.Join(dir, "internal"))
		if err != nil {
			return err
		}
	}
	if err := validateName(module); err != nil {
		return fmt.Errorf("internal module: %w", err)
	}
	return writeFiles(filepath.Clean(dir), resourceFiles(module, resource))
}

func generate(args []string) error {
	dir, _, err := parseProjectFlags(args, ".", "")
	if err != nil {
		return err
	}
	root := filepath.Clean(dir)
	bufPath := "buf"
	if _, err := exec.LookPath(bufPath); err != nil {
		localBuf := filepath.Join(root, "bin", "buf")
		if _, statErr := os.Stat(localBuf); statErr == nil {
			bufPath = localBuf
		}
	}
	cmd := exec.Command(bufPath, "generate")
	cmd.Dir = root
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("buf generate: %w (install Buf or run `make buf-install`)", err)
	}
	return nil
}

func parseProjectFlags(args []string, dir, module string) (string, string, error) {
	fs := flag.NewFlagSet("dilu", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&dir, "dir", dir, "project directory")
	fs.StringVar(&module, "module", module, "Go module path or internal module name")
	if err := fs.Parse(args); err != nil {
		return "", "", err
	}
	if fs.NArg() != 0 {
		return "", "", fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	return dir, module, nil
}

func validateName(name string) error {
	if !identifierPattern.MatchString(name) {
		return fmt.Errorf("name %q must contain lowercase letters, digits, or hyphens and start with a letter", name)
	}
	return nil
}

func pascalName(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '-' || r == '_' })
	var b strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(part)
		runes[0] = unicode.ToUpper(runes[0])
		b.WriteString(string(runes))
	}
	return b.String()
}

func writeFiles(root string, files map[string]string) error {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		full := filepath.Join(root, filepath.FromSlash(path))
		if _, err := os.Stat(full); err == nil {
			return fmt.Errorf("refusing to overwrite %s", full)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	for _, path := range paths {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(files[path]), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", full, err)
		}
	}
	fmt.Printf("generated %d files in %s\n", len(paths), root)
	return nil
}

func detectSingleModule(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("read %s: pass --module explicitly: %w", root, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() && entry.Name() != "common" {
			names = append(names, entry.Name())
		}
	}
	if len(names) != 1 {
		return "", fmt.Errorf("could not infer internal module (found %v); pass --module MODULE", names)
	}
	return names[0], nil
}

func serviceFiles(name, module string) map[string]string {
	goPackage := strings.ReplaceAll(name, "-", "_")
	serviceType := pascalName(name)
	return map[string]string{
		"go.mod": fmt.Sprintf("module %s\n\ngo 1.27.0\n\nrequire github.com/baowk/dilu-go-kit v0.13.0\n", module),
		"api/" + goPackage + "/v1/" + goPackage + ".proto": fmt.Sprintf(`syntax = "proto3";

package %s.v1;

option go_package = "%s/gen/%s/v1;%sv1";

import "google/api/annotations.proto";

service %sService {
  rpc Get(GetRequest) returns (GetResponse) {
    option (google.api.http) = { get: "/v1/%s" };
  }
}

message GetRequest {}
message GetResponse { string message = 1; }
`, goPackage, module, name, goPackage, serviceType, goPackage),
		"cmd/" + name + "/main.go": fmt.Sprintf(`package main

import (
    "log"
    "github.com/baowk/dilu-go-kit/boot"
)

func main() {
    app, err := boot.New("configs/config.yaml")
    if err != nil { log.Fatal(err) }
    if err := app.Run(func(*boot.App) error { return nil }); err != nil { log.Fatal(err) }
}
`),
		"configs/config.yaml": fmt.Sprintf(`server:
  name: %s
  addr: ":8080"
  mode: debug

security:
  requireTLS: false

database: {}
redis:
  addr: ""
grpc:
  enable: false
registry:
  enable: false
`, name),
		"internal/" + name + "/biz/README.md":     "# Business logic\n\nKeep use cases and domain rules here.\n",
		"internal/" + name + "/data/README.md":    "# Data access\n\nKeep database and external clients here.\n",
		"internal/" + name + "/service/README.md": "# Transport service\n\nMap generated API requests to biz use cases here.\n",
		"internal/" + name + "/server/README.md":  "# Servers\n\nRegister HTTP/gRPC transports here.\n",
		"buf.yaml": `version: v2

modules:
  - path: api
    name: buf.build/local/service
deps:
  - buf.build/googleapis/googleapis

lint:
  use: [STANDARD]
breaking:
  use: [FILE]
`,
		"buf.lock": `# Generated by buf. DO NOT EDIT.
version: v2
deps:
  - name: buf.build/googleapis/googleapis
    commit: c17df5b2beca46928cc87d5656bd5343
    digest: b5:648a01e0170d4512dea7d564016165decd1ed6e34bef79fe54753e51ad7e27545709ad9157d7551270147d551155c595a2fb0bf5bb33b1c83040ddbce915c604
`,
		"buf.gen.yaml": `version: v2

plugins:
  - remote: buf.build/protocolbuffers/go
    out: gen
    opt: [paths=source_relative]
  - remote: buf.build/grpc/go
    out: gen
    opt: [paths=source_relative]
  - remote: buf.build/grpc-ecosystem/gateway
    out: gen
    opt: [paths=source_relative]
  - remote: buf.build/grpc-ecosystem/openapiv2
    out: gen/openapi
    opt:
      - allow_merge=true
      - merge_file_name=openapi
`,
		"Makefile": `SHELL := /bin/sh

.PHONY: proto-lint proto-generate

proto-lint:
	@buf lint

proto-generate:
	@buf generate
`,
	}
}

func resourceFiles(module, resource string) map[string]string {
	base := filepath.ToSlash(filepath.Join("internal", module))
	goName := pascalName(resource)
	return map[string]string{
		base + "/model/" + resource + ".go":       fmt.Sprintf("package model\n\n// %s is the persistence model for the %s table.\ntype %s struct {\n\tID int64 `gorm:\"column:id;primaryKey;autoIncrement\" json:\"id\"`\n}\n\nfunc (%s) TableName() string { return \"%s\" }\n", goName, resource, goName, goName, resource),
		base + "/store/" + resource + "_pg.go":    fmt.Sprintf("package store\n\n// %sStore contains persistence operations for %s.\ntype %sStore interface {}\n", goName, resource, goName),
		base + "/service/" + resource + ".go":     fmt.Sprintf("package service\n\n// %sService contains the %s use cases.\ntype %sService struct{}\n", goName, resource, goName),
		base + "/service/dto/" + resource + ".go": fmt.Sprintf("package dto\n\n// %sRequest is the %s request DTO.\ntype %sRequest struct{}\n", goName, resource, goName),
		base + "/apis/" + resource + "_api.go":    fmt.Sprintf("package apis\n\n// %sAPI exposes the %s HTTP endpoints.\ntype %sAPI struct{}\n", goName, resource, goName),
	}
}
