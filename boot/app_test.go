package boot

import (
	"net"
	"testing"
)

func TestResolveAdvertiseAddrUsesExplicitValue(t *testing.T) {
	got := resolveAdvertiseAddr(":8080", "10.0.1.5:8080")
	if got != "10.0.1.5:8080" {
		t.Fatalf("resolveAdvertiseAddr = %q", got)
	}
}

func TestResolveAdvertiseAddrExpandsUnspecifiedHost(t *testing.T) {
	got := resolveAdvertiseAddr(":8080", "")
	host, port, err := net.SplitHostPort(got)
	if err != nil {
		t.Fatalf("advertise addr should be host:port, got %q: %v", got, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		t.Fatalf("advertise host should be reachable, got %q", host)
	}
	if port != "8080" {
		t.Fatalf("advertise port = %q", port)
	}
}

func TestResolveAdvertiseAddrKeepsSpecificHost(t *testing.T) {
	got := resolveAdvertiseAddr("127.0.0.1:8080", "")
	if got != "127.0.0.1:8080" {
		t.Fatalf("resolveAdvertiseAddr = %q", got)
	}
}
