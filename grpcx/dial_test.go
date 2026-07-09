package grpcx

import (
	"crypto/tls"
	"testing"
	"time"
)

func TestNormalizeDialOptionDefaults(t *testing.T) {
	opt := normalizeDialOption(DialOption{KeepaliveTime: time.Second})
	if opt.Timeout != defaultOpts.Timeout {
		t.Fatalf("Timeout = %v", opt.Timeout)
	}
	if opt.KeepaliveTime != time.Second {
		t.Fatalf("KeepaliveTime = %v", opt.KeepaliveTime)
	}
	if opt.KeepaliveTimeout != defaultOpts.KeepaliveTimeout {
		t.Fatalf("KeepaliveTimeout = %v", opt.KeepaliveTimeout)
	}
}

func TestTransportCredentialsDefaultInsecure(t *testing.T) {
	creds := DialOption{}.transportCredentials()
	if got := creds.Info().SecurityProtocol; got != "insecure" {
		t.Fatalf("SecurityProtocol = %q", got)
	}
}

func TestTransportCredentialsTLS(t *testing.T) {
	creds := DialOption{TLSConfig: &tls.Config{ServerName: "example.com"}}.transportCredentials()
	if got := creds.Info().SecurityProtocol; got != "tls" {
		t.Fatalf("SecurityProtocol = %q", got)
	}
}
