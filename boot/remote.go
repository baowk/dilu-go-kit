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
	consul "github.com/hashicorp/consul/api"
	"github.com/spf13/viper"
	clientv3 "go.etcd.io/etcd/client/v3"
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

// registryType returns "etcd" (default) or "consul".
func (r *RegistryConfig) registryType() string {
	if value := strings.ToLower(strings.TrimSpace(r.Type)); value != "" {
		return value
	}
	return "etcd"
}

// ── public API ──

// LoadRemoteConfig reads a config value from etcd or consul KV and
// unmarshals it into cfg.
func LoadRemoteConfig(reg RegistryConfig, serviceName string, cfg any) error {
	key := reg.resolveConfigKey(serviceName)
	data, err := fetchRemoteByKey(reg, key)
	if err != nil {
		return err
	}
	return unmarshalBytes(data, reg.configFormat(), cfg)
}

// WatchRemoteConfig watches the service config key for changes and calls
// onChange with new raw bytes. Blocks until ctx is cancelled.
func WatchRemoteConfig(ctx context.Context, reg RegistryConfig, serviceName string, onChange func([]byte)) error {
	key := reg.resolveConfigKey(serviceName)
	return watchRemoteConfigKey(ctx, reg, key, onChange)
}

// WatchRemoteConfigTree watches the service-level key and optional node-level
// key. It calls onChange when either key changes, so callers can rebuild the
// full local → service → node merge.
func WatchRemoteConfigTree(ctx context.Context, reg RegistryConfig, serviceName string, onChange func()) error {
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
	switch reg.registryType() {
	case "etcd":
		return watchEtcd(ctx, reg, key, onChange)
	case "consul":
		return watchConsul(ctx, reg, key, onChange)
	default:
		return fmt.Errorf("remote config: unsupported type %q", reg.Type)
	}
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

func mergeRemoteConfig(reg RegistryConfig, serviceName string, base *Config, allowMissingService bool) error {
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
	switch reg.registryType() {
	case "etcd":
		return fetchEtcd(reg, key)
	case "consul":
		return fetchConsul(reg, key)
	default:
		return nil, fmt.Errorf("remote config: unsupported type %q", reg.Type)
	}
}

// ── etcd ──

func remoteEtcdClient(reg RegistryConfig) (*clientv3.Client, error) {
	if len(reg.Endpoints) == 0 {
		return nil, fmt.Errorf("remote config: no etcd endpoints")
	}
	tlsConfig, err := reg.TLS.ClientTLSConfig()
	if err != nil {
		return nil, fmt.Errorf("remote config: etcd TLS: %w", err)
	}
	return clientv3.New(clientv3.Config{
		Endpoints:   reg.Endpoints,
		DialTimeout: 5 * time.Second,
		TLS:         tlsConfig,
		Username:    reg.Username,
		Password:    reg.Password,
	})
}

func fetchEtcd(reg RegistryConfig, key string) ([]byte, error) {
	cli, err := remoteEtcdClient(reg)
	if err != nil {
		return nil, err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := cli.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("remote config: etcd get %q: %w", key, err)
	}
	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("%w: etcd key %q", ErrRemoteConfigNotFound, key)
	}
	value := resp.Kvs[0].Value
	if err := validateRemoteConfigPayload(value); err != nil {
		return nil, err
	}
	return value, nil
}

func watchEtcd(ctx context.Context, reg RegistryConfig, key string, onChange func([]byte)) error {
	cli, err := remoteEtcdClient(reg)
	if err != nil {
		return err
	}
	defer cli.Close()

	var nextRevision int64
	for ctx.Err() == nil {
		if nextRevision == 0 {
			current, err := cli.Get(ctx, key)
			if err != nil {
				log.Warn("remote config: etcd snapshot failed", "key", key, "error", err)
				if err := waitContext(ctx, time.Second); err != nil {
					return err
				}
				continue
			}
			nextRevision = current.Header.Revision + 1
			if len(current.Kvs) > 0 {
				if err := validateRemoteConfigPayload(current.Kvs[0].Value); err != nil {
					return err
				}
				onChange(current.Kvs[0].Value)
			} else {
				onChange(nil)
			}
		}
		opts := []clientv3.OpOption{}
		if nextRevision > 0 {
			opts = append(opts, clientv3.WithRev(nextRevision))
		}
		ch := cli.Watch(ctx, key, opts...)
		for resp := range ch {
			if err := resp.Err(); err != nil {
				log.Warn("remote config: etcd watch interrupted", "key", key, "error", err)
				nextRevision = 0
				break
			}
			if resp.Header.Revision > 0 {
				nextRevision = resp.Header.Revision + 1
			}
			for _, ev := range resp.Events {
				if ev.Type == clientv3.EventTypeDelete {
					onChange(nil)
				} else if ev.Kv != nil {
					if err := validateRemoteConfigPayload(ev.Kv.Value); err != nil {
						return err
					}
					onChange(ev.Kv.Value)
				}
			}
		}
		if err := waitContext(ctx, time.Second); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// ── consul ──

func remoteConsulClient(reg RegistryConfig) (*consul.Client, error) {
	addr := reg.Address
	if addr == "" && len(reg.Endpoints) > 0 {
		addr = reg.Endpoints[0]
	}
	if addr == "" {
		return nil, fmt.Errorf("remote config: no consul address")
	}
	cfg := consul.DefaultConfig()
	cfg.Address = addr
	if reg.Token != "" {
		cfg.Token = reg.Token
	}
	if reg.TLS.Enable {
		tlsAddress := addr
		if reg.TLS.ServerName != "" {
			tlsAddress = reg.TLS.ServerName
		}
		cfg.Scheme = "https"
		cfg.TLSConfig = consul.TLSConfig{
			Address:  tlsAddress,
			CAFile:   reg.TLS.CAFile,
			CertFile: reg.TLS.CertFile,
			KeyFile:  reg.TLS.KeyFile,
		}
	}
	return consul.NewClient(cfg)
}

func fetchConsul(reg RegistryConfig, key string) ([]byte, error) {
	cli, err := remoteConsulClient(reg)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pair, _, err := cli.KV().Get(key, (&consul.QueryOptions{}).WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("remote config: consul get %q: %w", key, err)
	}
	if pair == nil {
		return nil, fmt.Errorf("%w: consul key %q", ErrRemoteConfigNotFound, key)
	}
	if err := validateRemoteConfigPayload(pair.Value); err != nil {
		return nil, err
	}
	return pair.Value, nil
}

func watchConsul(ctx context.Context, reg RegistryConfig, key string, onChange func([]byte)) error {
	cli, err := remoteConsulClient(reg)
	if err != nil {
		return err
	}

	var lastIndex uint64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		query := (&consul.QueryOptions{
			WaitIndex: lastIndex,
			WaitTime:  55 * time.Second,
		}).WithContext(ctx)
		pair, meta, err := cli.KV().Get(key, query)
		if err != nil {
			log.Warn("remote config: consul watch error, retrying", "error", err)
			if err := waitContext(ctx, 2*time.Second); err != nil {
				return err
			}
			continue
		}
		if meta != nil && meta.LastIndex != lastIndex {
			lastIndex = meta.LastIndex
			if pair != nil {
				onChange(pair.Value)
			} else {
				onChange(nil)
			}
		}
	}
}

func waitContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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

func isSensitiveConfigPath(path []string) bool {
	if len(path) >= 3 && path[0] == "database" && path[len(path)-1] == "dsn" {
		return true
	}
	if len(path) != 2 {
		return false
	}
	return (path[0] == "redis" && path[1] == "password") ||
		(path[0] == "jwt" && path[1] == "secret") ||
		(path[0] == "registry" && path[1] == "token") ||
		(path[0] == "notify" && path[1] == "token")
}
