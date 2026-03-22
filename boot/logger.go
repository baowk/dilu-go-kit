package boot

import (
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
)

var log zerolog.Logger

// InitLogger sets up a zerolog logger.
// In debug mode it uses colored console output; otherwise JSON.
func InitLogger(mode string) {
	if mode == "debug" {
		log = zerolog.New(zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: time.RFC3339,
		}).With().Timestamp().Caller().Logger()
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		log = zerolog.New(os.Stdout).With().Timestamp().Logger()
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
}

// Log returns the global logger instance.
func Log() *zerolog.Logger { return &log }

// LogWriter returns the logger as an io.Writer (useful for Gin).
func LogWriter() io.Writer { return log }
