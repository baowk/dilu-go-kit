package consul

import (
	"testing"
	"time"
)

func TestConsulConfigAppliesTokenAndTLS(t *testing.T) {
	cfg := consulConfig("127.0.0.1:8500", "token")
	if cfg.Address != "127.0.0.1:8500" || cfg.Token != "token" || cfg.HttpClient == nil || cfg.HttpClient.Timeout < 60*time.Second {
		t.Fatalf("unexpected consul config: %#v", cfg)
	}
	tlsCfg := consulConfig("127.0.0.1:8500", "", TLSConfig{Enable: true, ServerName: "consul.internal"})
	if tlsCfg.Scheme != "https" || tlsCfg.TLSConfig.Address != "consul.internal" {
		t.Fatalf("unexpected TLS config: scheme=%q address=%q", tlsCfg.Scheme, tlsCfg.TLSConfig.Address)
	}
}

func TestConsulServiceCheckDefaultsToTTL(t *testing.T) {
	check, usesTTL, err := consulServiceCheck(Config{}, "check-inst-1", "10.0.1.5:7801")
	if err != nil || !usesTTL || check.TTL != "30s" || check.DeregisterCriticalServiceAfter != "300s" {
		t.Fatalf("unexpected check: %+v usesTTL=%v err=%v", check, usesTTL, err)
	}
}
