package boot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/baowk/dilu-go-kit/log"
	consul "github.com/hashicorp/consul/api"
	"github.com/spf13/viper"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// ── key resolution helpers (on RegistryConfig) ──

// configKeyPrefix returns the config key prefix, default "/config/".
func (r *RegistryConfig) configKeyPrefix() string {
	if r.ConfigKey != "" {
		return r.ConfigKey
	}
	return "/config/"
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
	return r.resolveConfigKey(serviceName) + "/" + node
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
	if r.Type != "" {
		return r.Type
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
	svcKey := reg.resolveConfigKey(serviceName)

	// Load local base into viper via JSON round-trip
	merged := viper.New()
	merged.SetConfigType("json")
	buf, _ := json.Marshal(base)
	if err := merged.ReadConfig(bytes.NewReader(buf)); err != nil {
		return fmt.Errorf("remote config: encode local: %w", err)
	}

	// Layer 1: service shared config
	svcData, err := fetchRemoteByKey(reg, svcKey)
	if err != nil {
		return err
	}
	if err := mergeLayer(merged, svcData, reg.configFormat()); err != nil {
		return fmt.Errorf("remote config: merge service: %w", err)
	}
	log.Info("remote config: service config loaded", "key", svcKey)

	// Layer 2: node-specific config (optional, missing key is not an error)
	nodeKey := reg.resolveConfigNodeKey(serviceName)
	if nodeKey != "" {
		nodeData, err := fetchRemoteByKey(reg, nodeKey)
		if err == nil {
			if err := mergeLayer(merged, nodeData, reg.configFormat()); err != nil {
				return fmt.Errorf("remote config: merge node: %w", err)
			}
			log.Info("remote config: node override applied", "key", nodeKey)
		}
		// missing node key is fine — just skip
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
	return clientv3.New(clientv3.Config{
		Endpoints:   reg.Endpoints,
		DialTimeout: 5 * time.Second,
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
		return nil, fmt.Errorf("remote config: etcd key %q not found", key)
	}
	return resp.Kvs[0].Value, nil
}

func watchEtcd(ctx context.Context, reg RegistryConfig, key string, onChange func([]byte)) error {
	cli, err := remoteEtcdClient(reg)
	if err != nil {
		return err
	}
	defer cli.Close()

	ch := cli.Watch(ctx, key)
	for resp := range ch {
		for _, ev := range resp.Events {
			if ev.Kv != nil && ev.Kv.Value != nil {
				onChange(ev.Kv.Value)
			}
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
	return consul.NewClient(cfg)
}

func fetchConsul(reg RegistryConfig, key string) ([]byte, error) {
	cli, err := remoteConsulClient(reg)
	if err != nil {
		return nil, err
	}
	pair, _, err := cli.KV().Get(key, nil)
	if err != nil {
		return nil, fmt.Errorf("remote config: consul get %q: %w", key, err)
	}
	if pair == nil {
		return nil, fmt.Errorf("remote config: consul key %q not found", key)
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

		pair, meta, err := cli.KV().Get(key, &consul.QueryOptions{
			WaitIndex: lastIndex,
			WaitTime:  55 * time.Second,
		})
		if err != nil {
			log.Warn("remote config: consul watch error, retrying", "error", err)
			time.Sleep(2 * time.Second)
			continue
		}
		if pair != nil && meta.LastIndex != lastIndex {
			lastIndex = meta.LastIndex
			onChange(pair.Value)
		}
	}
}

// ── unmarshal / merge helpers ──

func unmarshalBytes(data []byte, format string, cfg any) error {
	v := viper.New()
	v.SetConfigType(format)
	if err := v.ReadConfig(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("remote config: parse %s: %w", format, err)
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
	return v.MergeConfigMap(layer.AllSettings())
}
