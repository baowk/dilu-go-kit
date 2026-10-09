// Package buildinfo exposes application metadata injected at build time.
// It does not start services, read configuration, or manage releases.
package buildinfo

import (
	"encoding/json"
	"io"
	"os"
)

// These variables are set with go build -ldflags '-X <package>.<name>=<value>'.
// They describe the application binary, not the dilu-go-kit dependency version.
// Applications must not modify them at runtime.
var (
	Repository = "unknown"
	Tag        = "dev"
	Commit     = "unknown"
	BuiltAt    = "unknown"
	Component  = "unknown"
)

// Info is the application build identity. BuiltAt is a UTC RFC3339 timestamp
// when supplied by the build pipeline; uninjected fields retain their defaults.
type Info struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Commit     string `json:"commit"`
	BuiltAt    string `json:"built_at"`
	Component  string `json:"component"`
}

// Current returns a copy of the metadata embedded in this binary.
func Current() Info {
	return Info{Repository: Repository, Tag: Tag, Commit: Commit, BuiltAt: BuiltAt, Component: Component}
}

// WriteVersion writes one JSON object followed by a newline only when args is
// exactly ["--version"]. args excludes the executable name. handled remains
// true on a write error so callers never proceed with normal startup.
func WriteVersion(args []string, w io.Writer) (handled bool, err error) {
	if len(args) != 1 || args[0] != "--version" {
		return false, nil
	}
	return true, json.NewEncoder(w).Encode(Current())
}

// PrintVersion handles the process's --version argument using stdout.
// Call it before loading configuration or connecting to external services.
func PrintVersion() (handled bool, err error) {
	return WriteVersion(os.Args[1:], os.Stdout)
}
