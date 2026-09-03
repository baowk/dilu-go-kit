// Package etcd is the optional etcd registry adapter.
package etcd

import (
	"time"

	core "github.com/baowk/dilu-go-kit/registry"
)

type Config = core.Config
type TLSConfig = core.TLSConfig
type Registry = core.Registry
type Service = core.Service
type Event = core.Event
type EventType = core.EventType
type KVStore = core.KVStore

var ErrKeyNotFound = core.ErrKeyNotFound

const (
	EventPut    = core.EventPut
	EventDelete = core.EventDelete
)

var (
	validateService        = core.ValidateService
	validateServiceName    = core.ValidateServiceName
	serviceKey             = core.ServiceKey
	servicePrefixKey       = core.ServicePrefixKey
	marshalService         = core.MarshalService
	unmarshalService       = core.UnmarshalService
	serviceInstanceFromKey = core.ServiceInstanceFromKey
	sendEvent              = core.SendEvent
	waitForContext         = core.WaitForContext
	localIP                = core.LocalIP
	GenerateInstanceID     = core.GenerateInstanceID
)

func configPrefix(c Config) string             { return c.EffectivePrefix() }
func configTTL(c Config) int64                 { return c.EffectiveTTL() }
func configDialTimeout(c Config) time.Duration { return c.EffectiveDialTimeout() }

// Register makes the etcd backend available to registry.New.
func Register() error { return core.RegisterBackend("etcd", New) }

func New(cfg Config) (Registry, error) { return NewEtcd(cfg) }
