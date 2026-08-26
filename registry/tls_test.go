package registry

import "testing"

func TestClientTLSConfigRejectsPartialClientCertificate(t *testing.T) {
	_, err := (TLSConfig{Enable: true, KeyFile: "client.key"}).ClientTLSConfig()
	if err == nil {
		t.Fatal("expected partial client certificate configuration to fail")
	}
}
