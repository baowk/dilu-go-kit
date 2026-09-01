package boot

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadTLSConfigRejectsPartialClientCertificate(t *testing.T) {
	_, err := loadTLSConfig(TLSConfig{Enable: true, CertFile: "client.crt"})
	if err == nil {
		t.Fatal("expected partial client certificate configuration to fail")
	}
}

func TestLoadGRPCServerTLSConfigRequiresClientCA(t *testing.T) {
	dir, certFile, keyFile := writeTestCertificate(t)
	_, err := loadGRPCServerTLSConfig(TLSConfig{
		Enable:   true,
		CertFile: certFile,
		KeyFile:  keyFile,
		// RequireClientCert intentionally has no CAFile.
		RequireClientCert: true,
	})
	if err == nil {
		t.Fatalf("expected missing client CA error (certs in %s)", dir)
	}
}

func TestLoadGRPCServerTLSConfigEnablesMutualTLS(t *testing.T) {
	_, certFile, keyFile := writeTestCertificate(t)
	cfg, err := loadGRPCServerTLSConfig(TLSConfig{
		Enable:            true,
		CertFile:          certFile,
		KeyFile:           keyFile,
		CAFile:            certFile,
		RequireClientCert: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("minimum TLS version = %d", cfg.MinVersion)
	}
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("client auth = %v", cfg.ClientAuth)
	}
	if cfg.ClientCAs == nil {
		t.Fatal("client CA pool is nil")
	}
}

func writeTestCertificate(t *testing.T) (dir, certFile, keyFile string) {
	t.Helper()
	dir = t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "dilu-test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IsCA:         true,
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certFile = filepath.Join(dir, "server.crt")
	keyFile = filepath.Join(dir, "server.key")
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(certFile, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, certFile, keyFile
}
