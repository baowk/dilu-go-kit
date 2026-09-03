package boot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/baowk/dilu-go-kit/log"
	"github.com/baowk/dilu-go-kit/registry"
	"github.com/spf13/viper"
)

// ErrRemoteConfigNotFound indicates that a remote config key is missing.
var ErrRemoteConfigNotFound = errors.New("remote config key not found")

const maxRemoteConfigBytes = 4 << 20

func validateRemoteConfigPayload(data []byte) error {
	if len(data) > maxRemoteConfigBytes {
		return fmt.Errorf("remote config payload exceeds %d bytes", maxRemoteConfigBytes)
	}
	return nil
}

// ── key resolution helpers (on RegistryConfig) ──

// configKeyPrefix returns the config key prefix, default "/config/".
func (r *RegistryConfig) configKeyPrefix() string {
	prefix := r.ConfigKey
	if prefix == "" {
		prefix = "/config/"
	}
	return strings.TrimRight(prefix, "/") + "/"
}

// resolveConfigKey returns the service-level KV key: configKey + serviceName.
func (r *RegistryConfig) resolveConfigKey(serviceName string) string {
	return r.configKeyPrefix() + serviceName
}

// resolveConfigNodeKey returns the node-level KV key, or "" if no node is set.
func (r *RegistryConfig) resolveConfigNodeKey(serviceName string) string {
	node := r.configNode()
	if node == "" {
		return ""
	}
	return r.resolveConfigKey(serviceName) + "/" + strings.Trim(node, "/")
}

func (r *RegistryConfig) configNode() string {
	if r.ConfigNode != "" {
		return r.ConfigNode
	}
	return os.Getenv("REMOTE_NODE")
}

func (r *RegistryConfig) configFormat() string {
	if r.ConfigFormat == "json" {
		return "json"
	}
	return "yaml"
}

func (r RegistryConfig) dialTimeout() time.Duration {
	if r.DialTimeout > 0 {
		// Keep public helpers bounded even when called without Config.Validate.
		if r.DialTimeout > 5*60 {
			return 5 * time.Minute
		}
		return time.Duration(r.DialTimeout) * time.Second
	}
	return 5 * time.Second
}

// registryType returns the configured backend name (default "etcd").
func (r *RegistryConfig) registryType() string {
	if value := strings.ToLower(strings.TrimSpace(r.Type)); value != "" {
		return value
	}
	return "etcd"
}

// ── public API ──

// LoadRemoteConfig reads a config value from the configured registry KV store
// and unmarshals it into cfg. The backend must be registered through the
// registry factory; vendor SDKs are intentionally not referenced here.
func LoadRemoteConfig(reg RegistryConfig, serviceName string, cfg any) error {
	serviceName = strings.TrimSpace(serviceName)
	if err := validateRemoteConfigInputs(reg, serviceName); err != nil {
		return err
	}
	if cfg == nil {
		return errors.New("remote config: destination is nil")
	}
	key := reg.resolveConfigKey(serviceName)
	data, err := fetchRemoteByKey(reg, key)
	if err != nil {
		return err
	}
	return unmarshalBytes(data, reg.configFormat(), cfg)
}

// LoadRemoteConfigFromStore loads remote configuration from an injected KV
// store. This is the preferred API for custom registry backends because boot
// does not need to know which vendor implements the store.
func LoadRemoteConfigFromStore(store registry.KVStore, reg RegistryConfig, serviceName string, cfg any) error {
	serviceName = strings.TrimSpace(serviceName)
	if err := validateRemoteConfigInputs(reg, serviceName); err != nil {
		return err
	}
	if store == nil {
		return errors.New("remote config: KV store is nil")
	}
	if cfg == nil {
		return errors.New("remote config: destination is nil")
	}
	data, err := store.Get(context.Background(), reg.resolveConfigKey(serviceName))
	if errors.Is(err, registry.ErrKeyNotFound) {
		return ErrRemoteConfigNotFound
	}
	if err != nil {
		return err
	}
	if err := validateRemoteConfigPayload(data); err != nil {
		return err
	}
	return unmarshalBytes(data, reg.configFormat(), cfg)
}

// WatchRemoteConfig watches the service config key for changes and calls
// onChange with new raw bytes. Blocks until ctx is cancelled.
func WatchRemoteConfig(ctx context.Context, reg RegistryConfig, serviceName string, onChange func([]byte)) error {
	serviceName = strings.TrimSpace(serviceName)
	if err := validateRemoteConfigInputs(reg, serviceName); err != nil {
		return err
	}
	if onChange == nil {
		return errors.New("remote config: onChange callback is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	key := reg.resolveConfigKey(serviceName)
	return watchRemoteConfigKey(ctx, reg, key, onChange)
}

// WatchRemoteConfigFromStore watches a config key using an injected KV store.
func WatchRemoteConfigFromStore(ctx context.Context, store registry.KVStore, reg RegistryConfig, serviceName string, onChange func([]byte)) error {
	serviceName = strings.TrimSpace(serviceName)
	if err := validateRemoteConfigInputs(reg, serviceName); err != nil {
		return err
	}
	if store == nil {
		return errors.New("remote config: KV store is nil")
	}
	if onChange == nil {
		return errors.New("remote config: onChange callback is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return store.WatchKey(ctx, reg.resolveConfigKey(serviceName), func(data []byte) {
		if len(data) > maxRemoteConfigBytes {
			return
		}
		onChange(data)
	})
}

// WatchRemoteConfigTree watches the service-level key and optional node-level
// key. It calls onChange when either key changes, so callers can rebuild the
// full local → service → node merge.
func WatchRemoteConfigTree(ctx context.Context, reg RegistryConfig, serviceName string, onChange func()) error {
	serviceName = strings.TrimSpace(serviceName)
	if err := validateRemoteConfigInputs(reg, serviceName); err != nil {
		return err
	}
	if onChange == nil {
		return errors.New("remote config: onChange callback is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	keys := []string{reg.resolveConfigKey(serviceName)}
	if nodeKey := reg.resolveConfigNodeKey(serviceName); nodeKey != "" {
		keys = append(keys, nodeKey)
	}

	watchCtx, cancel := context.WithCancel(ctx)
	var watchers sync.WaitGroup
	defer func() {
		cancel()
		watchers.Wait()
	}()

	errCh := make(chan error, len(keys))
	for _, key := range keys {
		key := key
		watchers.Add(1)
		go func() {
			defer watchers.Done()
			errCh <- watchRemoteConfigKey(watchCtx, reg, key, func([]byte) {
				onChange()
			})
		}()
	}

	for range keys {
		select {
		case err := <-errCh:
			if err != nil && watchCtx.Err() == nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return ctx.Err()
}

func watchRemoteConfigKey(ctx context.Context, reg RegistryConfig, key string, onChange func([]byte)) error {
	store, closeStore, err := openRemoteKV(reg)
	if err != nil {
		return err
	}
	defer closeStore()
	return store.WatchKey(ctx, key, onChange)
}

// MergeRemoteConfig loads config from registry's KV backend and deep-merges
// it into base. Only keys present in the remote config are overwritten.
//
// Merge order: local → service shared → node-specific.
func MergeRemoteConfig(reg RegistryConfig, serviceName string, base *Config) error {
	return mergeRemoteConfig(reg, serviceName, base, false)
}

func MergeRemoteConfigOptional(reg RegistryConfig, serviceName string, base *Config) error {
	return mergeRemoteConfig(reg, serviceName, base, true)
}

// MergeRemoteConfigFromStore applies service and optional node layers from an
// injected KV store. Missing service config is rejected unless allowMissing is
// true; a missing node override is always ignored.
func MergeRemoteConfigFromStore(store registry.KVStore, reg RegistryConfig, serviceName string, base *Config, allowMissing bool) error {
	serviceName = strings.TrimSpace(serviceName)
	if err := validateRemoteConfigInputs(reg, serviceName); err != nil {
		return err
	}
	if store == nil {
		return errors.New("remote config: KV store is nil")
	}
	if base == nil {
		return errors.New("remote config: base config is nil")
	}
	get := func(key string) ([]byte, error) {
		data, err := store.Get(context.Background(), key)
		if errors.Is(err, registry.ErrKeyNotFound) {
			return nil, ErrRemoteConfigNotFound
		}
		if err == nil {
			err = validateRemoteConfigPayload(data)
		}
		return data, err
	}
	serviceData, err := get(reg.resolveConfigKey(serviceName))
	if err != nil && (!allowMissing || !errors.Is(err, ErrRemoteConfigNotFound)) {
		return err
	}
	var nodeData []byte
	if key := reg.resolveConfigNodeKey(serviceName); key != "" {
		nodeData, err = get(key)
		if err != nil && !errors.Is(err, ErrRemoteConfigNotFound) {
			return err
		}
	}
	return mergeConfigLayers(base, reg.configFormat(), serviceData, nodeData)
}

func mergeRemoteConfig(reg RegistryConfig, serviceName string, base *Config, allowMissingService bool) error {
	serviceName = strings.TrimSpace(serviceName)
	if err := validateRemoteConfigInputs(reg, serviceName); err != nil {
		return err
	}
	if base == nil {
		return errors.New("remote config: base config is nil")
	}
	svcKey := reg.resolveConfigKey(serviceName)

	// Layer 1: service shared config
	svcData, err := fetchRemoteByKey(reg, svcKey)
	if err != nil {
		if !allowMissingService || !errors.Is(err, ErrRemoteConfigNotFound) {
			return err
		}
		log.Info("remote config: service config missing, using local base", "key", svcKey)
	} else {
		log.Info("remote config: service config loaded", "key", svcKey)
	}

	// Layer 2: node-specific config (optional, missing key is not an error)
	var nodeData []byte
	nodeKey := reg.resolveConfigNodeKey(serviceName)
	if nodeKey != "" {
		nodeData, err = fetchRemoteByKey(reg, nodeKey)
		if err == nil {
			log.Info("remote config: node override applied", "key", nodeKey)
		} else if !errors.Is(err, ErrRemoteConfigNotFound) {
			return err
		}
		// missing node key is fine — just skip
	}

	return mergeConfigLayers(base, reg.configFormat(), svcData, nodeData)
}

func mergeConfigLayers(base *Config, format string, serviceData, nodeData []byte) error {
	// Load local base into viper via JSON round-trip
	merged := viper.New()
	merged.SetConfigType("json")
	buf, err := json.Marshal(base)
	if err != nil {
		return fmt.Errorf("remote config: encode local: %w", err)
	}
	if err := merged.ReadConfig(bytes.NewReader(buf)); err != nil {
		return fmt.Errorf("remote config: encode local: %w", err)
	}

	if len(serviceData) > 0 {
		if err := mergeLayer(merged, serviceData, format); err != nil {
			return fmt.Errorf("remote config: merge service: %w", err)
		}
	}
	if len(nodeData) > 0 {
		if err := mergeLayer(merged, nodeData, format); err != nil {
			return fmt.Errorf("remote config: merge node: %w", err)
		}
	}
	if err := merged.Unmarshal(base); err != nil {
		return fmt.Errorf("remote config: unmarshal merged: %w", err)
	}
	return nil
}

// ── fetch by key ──

func fetchRemoteByKey(reg RegistryConfig, key string) ([]byte, error) {
	store, closeStore, err := openRemoteKV(reg)
	if err != nil {
		return nil, err
	}
	defer closeStore()
	data, err := store.Get(context.Background(), key)
	if errors.Is(err, registry.ErrKeyNotFound) {
		return nil, fmt.Errorf("%w: key %q", ErrRemoteConfigNotFound, key)
	}
	if err == nil {
		err = validateRemoteConfigPayload(data)
	}
	return data, err
}

func openRemoteKV(reg RegistryConfig) (registry.KVStore, func(), error) {
	r, err := registry.New(registry.Config{Type: reg.Type, Endpoints: reg.Endpoints, Address: reg.Address, Token: reg.Token, Username: reg.Username, Password: reg.Password, Prefix: reg.Prefix, TTL: reg.TTL, DialTimeout: reg.DialTimeout, CheckType: reg.CheckType, CheckPath: reg.CheckPath, CheckTLS: reg.CheckTLS, DeregisterCriticalAfter: reg.DeregisterCriticalAfter, TLS: registry.TLSConfig{Enable: reg.TLS.Enable, CAFile: reg.TLS.CAFile, CertFile: reg.TLS.CertFile, KeyFile: reg.TLS.KeyFile, ServerName: reg.TLS.ServerName}})
	if err != nil {
		return nil, nil, err
	}
	store, ok := r.(registry.KVStore)
	if !ok {
		_ = r.Close()
		return nil, nil, fmt.Errorf("remote config: registry backend %q does not support KV storage", reg.Type)
	}
	return store, func() { _ = r.Close() }, nil
}

// ── unmarshal / merge helpers ──

func unmarshalBytes(data []byte, format string, cfg any) error {
	v := viper.New()
	v.SetConfigType(format)
	if err := v.ReadConfig(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("remote config: parse %s: %w", format, err)
	}
	if path := sensitiveRemotePath(v.AllSettings()); path != "" {
		return fmt.Errorf("sensitive value %s is not allowed in remote config; use an environment variable", path)
	}
	if err := v.Unmarshal(cfg); err != nil {
		return fmt.Errorf("remote config: unmarshal: %w", err)
	}
	return nil
}

func mergeLayer(v *viper.Viper, data []byte, format string) error {
	layer := viper.New()
	layer.SetConfigType(format)
	if err := layer.ReadConfig(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("parse %s: %w", format, err)
	}
	if path := sensitiveRemotePath(layer.AllSettings()); path != "" {
		return fmt.Errorf("sensitive value %s is not allowed in remote config; use an environment variable", path)
	}
	return v.MergeConfigMap(layer.AllSettings())
}

func sensitiveRemotePath(settings map[string]any) string {
	var walk func(map[string]any, []string) string
	walk = func(values map[string]any, prefix []string) string {
		for key, value := range values {
			path := append(append([]string{}, prefix...), strings.ToLower(key))
			if nested, ok := value.(map[string]any); ok {
				if found := walk(nested, path); found != "" {
					return found
				}
				continue
			}
			if isSensitiveConfigPath(path) && fmt.Sprint(value) != "" {
				return strings.Join(path, ".")
			}
		}
		return ""
	}
	return walk(settings, nil)
}

func validateRemoteConfigInputs(reg RegistryConfig, serviceName string) error {
	if serviceName == "" || len(serviceName) > 128 || strings.Contains(serviceName, "/") || strings.ContainsAny(serviceName, "\r\n\x00") {
		return fmt.Errorf("remote config: invalid service name %q", serviceName)
	}
	if len(reg.ConfigKey) > 256 || strings.ContainsAny(reg.ConfigKey, "\r\n\x00") || strings.Contains(reg.ConfigKey, "..") {
		return fmt.Errorf("remote config: invalid config key")
	}
	node := reg.configNode()
	if node != "" && (len(node) > 128 || strings.ContainsAny(node, "/\r\n\x00")) {
		return fmt.Errorf("remote config: invalid config node")
	}
	return nil
}

func isSensitiveConfigPath(path []string) bool {
	if len(path) >= 3 && path[0] == "database" && path[len(path)-1] == "dsn" {
		return true
	}
	if len(path) == 3 && path[0] == "diagnostics" && path[1] == "pprof" && path[2] == "authtoken" {
		return true
	}
	if len(path) != 2 {
		return false
	}
	return (path[0] == "redis" && path[1] == "password") ||
		(path[0] == "jwt" && path[1] == "secret") ||
		(path[0] == "registry" && path[1] == "token") ||
		(path[0] == "registry" && path[1] == "password") ||
		(path[0] == "notify" && path[1] == "token")
}
