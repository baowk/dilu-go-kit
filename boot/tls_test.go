package boot

import "testing"

func TestLoadTLSConfigRejectsPartialClientCertificate(t *testing.T) {
	_, err := loadTLSConfig(TLSConfig{Enable: true, CertFile: "client.crt"})
	if err == nil {
		t.Fatal("expected partial client certificate configuration to fail")
	}
}
