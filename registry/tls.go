package registry

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

func (c TLSConfig) ClientTLSConfig() (*tls.Config, error) {
	if !c.Enable {
		return nil, nil
	}
	result := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: c.ServerName}
	if c.CAFile != "" {
		data, err := os.ReadFile(c.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, fmt.Errorf("parse CA file %q", c.CAFile)
		}
		result.RootCAs = pool
	}
	if c.CertFile != "" || c.KeyFile != "" {
		if c.CertFile == "" || c.KeyFile == "" {
			return nil, fmt.Errorf("certFile and keyFile must be configured together")
		}
		cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		result.Certificates = []tls.Certificate{cert}
	}
	return result, nil
}
