package grpcx

import (
	"crypto/tls"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
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
	if opt.RetryMaxAttempts != defaultOpts.RetryMaxAttempts {
		t.Fatalf("RetryMaxAttempts = %v", opt.RetryMaxAttempts)
	}
}

func TestDialWaitsForReadyConnection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	go server.Serve(listener)
	defer server.Stop()

	conn, err := Dial(listener.Addr().String(), DialOption{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
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

func TestRetryServiceConfigCanDisableRetries(t *testing.T) {
	opt := normalizeDialOption(DialOption{RetryMaxAttempts: 1})
	got, err := opt.retryServiceConfigValidated()
	if err != nil || got != `{"methodConfig":[]}` {
		t.Fatalf("retry config = %s, err = %v", got, err)
	}
}

func TestRetryRequiresExplicitMethods(t *testing.T) {
	opt := normalizeDialOption(DialOption{RetryMaxAttempts: 3})
	if err := opt.validate(); err == nil {
		t.Fatal("expected retries without methods to be rejected")
	}

	opt.RetryMethods = []string{"/demo.TaskService/Get"}
	if err := opt.validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	config, err := opt.retryServiceConfigValidated()
	if err != nil {
		t.Fatal(err)
	}
	if config == "" {
		t.Fatal("expected retry service config")
	}
}

func TestDurationString(t *testing.T) {
	if got := durationString(100 * time.Millisecond); got != "0.100s" {
		t.Fatalf("durationString = %q", got)
	}
}
