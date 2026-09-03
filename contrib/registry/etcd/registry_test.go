package etcd

import (
	"testing"
	"time"

	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
)

func TestEtcdEventRejectsPayloadKeyMismatch(t *testing.T) {
	event, ok := etcdEvent("/svc/mf-user/", "mf-user", &clientv3.Event{
		Type: clientv3.EventTypePut,
		Kv: &mvccpb.KeyValue{
			Key:   []byte("/svc/mf-user/inst-1"),
			Value: []byte(`{"name":"mf-user","instance_id":"inst-2","addr":"10.0.1.5:7801"}`),
		},
	})
	if ok || event.Service.InstanceID != "inst-2" {
		t.Fatalf("mismatched event = %+v ok=%v", event, ok)
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := Config{}
	if cfg.EffectivePrefix() != "/mofang/services/" || cfg.EffectiveTTL() != 30 || cfg.EffectiveDialTimeout() != 5*time.Second {
		t.Fatalf("unexpected defaults: prefix=%q ttl=%d timeout=%s", cfg.EffectivePrefix(), cfg.EffectiveTTL(), cfg.EffectiveDialTimeout())
	}
}
