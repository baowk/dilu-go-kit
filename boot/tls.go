package boot

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

func loadTLSConfig(cfg TLSConfig) (*tls.Config, error) {
	if !cfg.Enable {
		return nil, nil
	}
	result := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.ServerName}
	if cfg.CAFile != "" {
		data, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("parse CA file %q", cfg.CAFile)
		}
		result.RootCAs = pool
	}
	if cfg.CertFile != "" || cfg.KeyFile != "" {
		if cfg.CertFile == "" || cfg.KeyFile == "" {
			return nil, fmt.Errorf("certFile and keyFile must be configured together")
		}
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		result.Certificates = []tls.Certificate{cert}
	}
	return result, nil
}
