package boot

import (
	"github.com/baowk/dilu-go-kit/log"
)

// InitLogger initializes the global logger via the log package.
// Deprecated: use log.Init() directly. Kept for boot.New() internal use.
func InitLogger(mode, serviceName string) {
	log.Init(mode, serviceName)
}
