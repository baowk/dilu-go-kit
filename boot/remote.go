package boot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/baowk/dilu-go-kit/log"
	consul "github.com/hashicorp/consul/api"
	"github.com/spf13/viper"
	clientv3 "go.etcd.io/etcd/client/v3"
)

// RemoteConfig describes a remote configuration source (etcd or consul KV).
type RemoteConfig struct {
	Enable    bool     `mapstructure:"enable"`
	Type      string   `mapstructure:"type"`      // "etcd" or "consul"
	Endpoints []string `mapstructure:"endpoints"` // etcd endpoints, e.g. ["127.0.0.1:2379"]
	Address   string   `mapstructure:"address"`   // consul address, e.g. "127.0.0.1:8500"
	Token     string   `mapstructure:"token"`     // consul ACL token (optional)
	Key       string   `mapstructure:"key"`       // KV key, e.g. "/config/mf-user"
	Format    string   `mapstructure:"format"`    // "yaml" (default) or "json"
}

func (r *RemoteConfig) format() string {
	if r.Format == "json" {
		return "json"
	}
	return "yaml"
}

// LoadRemoteConfig reads a config value from etcd or consul KV and
// unmarshals it into cfg.
func LoadRemoteConfig(src RemoteConfig, cfg any) error {
	data, err := fetchRemote(src)
	if err != nil {
		return err
	}
	return unmarshalBytes(data, src.format(), cfg)
}

// WatchRemoteConfig watches the remote key for changes and calls onChange
// with the new raw bytes whenever the value is updated.
// It blocks until ctx is cancelled.
func WatchRemoteConfig(ctx context.Context, src RemoteConfig, onChange func([]byte)) error {
	switch src.Type {
	case "etcd":
		return watchEtcd(ctx, src, onChange)
	case "consul":
		return watchConsul(ctx, src, onChange)
	default:
		return fmt.Errorf("remote config: unsupported type %q", src.Type)
	}
}

// fetchRemote reads the raw value from the remote KV store.
func fetchRemote(src RemoteConfig) ([]byte, error) {
	switch src.Type {
	case "etcd":
		return fetchEtcd(src)
	case "consul":
		return fetchConsul(src)
	default:
		return nil, fmt.Errorf("remote config: unsupported type %q", src.Type)
	}
}

// ── etcd ──

func etcdClient(src RemoteConfig) (*clientv3.Client, error) {
	if len(src.Endpoints) == 0 {
		return nil, fmt.Errorf("remote config: no etcd endpoints")
	}
	return clientv3.New(clientv3.Config{
		Endpoints:   src.Endpoints,
		DialTimeout: 5 * time.Second,
	})
}

func fetchEtcd(src RemoteConfig) ([]byte, error) {
	cli, err := etcdClient(src)
	if err != nil {
		return nil, err
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := cli.Get(ctx, src.Key)
	if err != nil {
		return nil, fmt.Errorf("remote config: etcd get %q: %w", src.Key, err)
	}
	if len(resp.Kvs) == 0 {
		return nil, fmt.Errorf("remote config: etcd key %q not found", src.Key)
	}
	return resp.Kvs[0].Value, nil
}

func watchEtcd(ctx context.Context, src RemoteConfig, onChange func([]byte)) error {
	cli, err := etcdClient(src)
	if err != nil {
		return err
	}
	defer cli.Close()

	ch := cli.Watch(ctx, src.Key)
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

func consulClient(src RemoteConfig) (*consul.Client, error) {
	addr := src.Address
	if addr == "" && len(src.Endpoints) > 0 {
		addr = src.Endpoints[0]
	}
	if addr == "" {
		return nil, fmt.Errorf("remote config: no consul address")
	}
	cfg := consul.DefaultConfig()
	cfg.Address = addr
	if src.Token != "" {
		cfg.Token = src.Token
	}
	return consul.NewClient(cfg)
}

func fetchConsul(src RemoteConfig) ([]byte, error) {
	cli, err := consulClient(src)
	if err != nil {
		return nil, err
	}
	pair, _, err := cli.KV().Get(src.Key, nil)
	if err != nil {
		return nil, fmt.Errorf("remote config: consul get %q: %w", src.Key, err)
	}
	if pair == nil {
		return nil, fmt.Errorf("remote config: consul key %q not found", src.Key)
	}
	return pair.Value, nil
}

func watchConsul(ctx context.Context, src RemoteConfig, onChange func([]byte)) error {
	cli, err := consulClient(src)
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

		pair, meta, err := cli.KV().Get(src.Key, &consul.QueryOptions{
			WaitIndex: lastIndex,
			WaitTime:  55 * time.Second, // consul long-poll
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

// ── unmarshal helper ──

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

// MergeRemoteConfig loads config from a remote source and merges it into
// an existing Config. Remote values override local ones.
func MergeRemoteConfig(src RemoteConfig, base *Config) error {
	data, err := fetchRemote(src)
	if err != nil {
		return err
	}
	// Re-encode base to JSON, decode remote on top of it
	buf, _ := json.Marshal(base)
	if err := json.Unmarshal(buf, base); err != nil {
		return err
	}
	return unmarshalBytes(data, src.format(), base)
}
