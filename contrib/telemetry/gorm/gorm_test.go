package gorm

import (
	"testing"

	"gorm.io/gorm"
)

func TestGORMPluginImplementsPlugin(t *testing.T) {
	var _ gorm.Plugin = (*GORMPlugin)(nil)
}
