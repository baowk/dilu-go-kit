package boot

import (
	"strings"

	"github.com/baowk/dilu-go-kit/log"
)

// warnPlaintextTransports emits an explicit production warning when an
// enabled infrastructure transport is running without TLS. Plaintext remains
// supported for local development and trusted in-cluster traffic; production
// deployments should set security.requireTLS=true after configuring the
// corresponding certificates.
func warnPlaintextTransports(cfg *Config) {
	if cfg == nil || cfg.Security.RequireTLS {
		return
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.Server.Mode))
	if mode != "release" && mode != "production" {
		return
	}
	warn := func(component string) {
		log.Warn("security warning: plaintext transport enabled", "component", component, "recommendation", "set security.requireTLS=true and configure TLS")
	}
	if cfg.Redis.Addr != "" && !cfg.Redis.TLS.Enable {
		warn("redis")
	}
	registryTransport := len(cfg.Registry.Endpoints) > 0 || strings.TrimSpace(cfg.Registry.Address) != ""
	if registryTransport && (cfg.Registry.Enable || strings.TrimSpace(cfg.Registry.ConfigKey) != "") && !cfg.Registry.TLS.Enable {
		warn("registry")
	}
	if cfg.GRPC.Enable && !cfg.GRPC.TLS.Enable {
		warn("grpc")
	}
	if strings.TrimSpace(cfg.Notify.WsURL) != "" && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(cfg.Notify.WsURL)), "https://") {
		warn("notify")
	}
	if cfg.Diagnostics.Pprof.Enabled && strings.TrimSpace(cfg.Diagnostics.Pprof.Addr) != "" && !cfg.Diagnostics.Pprof.TLS.Enable {
		warn("pprof")
	}
}
