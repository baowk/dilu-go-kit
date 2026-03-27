package boot

import (
	"github.com/baowk/dilu-go-kit/log"
)

// InitLogger initializes the global logger (console only).
// Deprecated: use log.Init() directly. Kept for backward compatibility.
func InitLogger(mode, serviceName string) {
	log.Init(mode, serviceName, "", nil)
}
