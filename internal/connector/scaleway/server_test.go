package scaleway

import (
	"testing"

	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
	"github.com/scaleway/scaleway-sdk-go/scw"
)

func TestServer_GetID(t *testing.T) {
	server := &Server{
		id:   "test-id-123",
		name: "test-server",
		ipv6: "2001:db8::1",
	}

	if got := server.GetID(); got != "test-id-123" {
		t.Errorf("GetID() = %v, want %v", got, "test-id-123")
	}
}

func TestServer_GetName(t *testing.T) {
	server := &Server{
		id:   "test-id-123",
		name: "test-server",
		ipv6: "2001:db8::1",
	}

	if got := server.GetName(); got != "test-server" {
		t.Errorf("GetName() = %v, want %v", got, "test-server")
	}
}

func TestServer_GetIPv6Address(t *testing.T) {
	server := &Server{
		id:   "test-id-123",
		name: "test-server",
		ipv6: "2001:db8::1",
	}

	if got := server.GetIPv6Address(); got != "2001:db8::1" {
		t.Errorf("GetIPv6Address() = %v, want %v", got, "2001:db8::1")
	}
}

func TestServer_String(t *testing.T) {
	server := &Server{
		id:   "test-id-123",
		name: "test-server",
		ipv6: "2001:db8::1",
	}

	want := "test-server [2001:db8::1]"
	if got := server.String(); got != want {
		t.Errorf("String() = %v, want %v", got, want)
	}
}

func TestNewServer(t *testing.T) {
	ipAddr := scw.IPAddress("2001:db8::1")
	scwServer := &instance.Server{
		ID:   "test-id",
		Name: "test-name",
		PublicIPs: []*instance.ServerIP{
			{
				Address: &ipAddr,
			},
		},
	}

	server := newServer(scwServer, nil)

	if server.GetID() != "test-id" {
		t.Errorf("newServer() ID = %v, want %v", server.GetID(), "test-id")
	}
	if server.GetName() != "test-name" {
		t.Errorf("newServer() Name = %v, want %v", server.GetName(), "test-name")
	}
	if server.GetIPv6Address() != "2001:db8::1" {
		t.Errorf("newServer() IPv6 = %v, want %v", server.GetIPv6Address(), "2001:db8::1")
	}
}

func TestNewServer_NoPublicIP(t *testing.T) {
	scwServer := &instance.Server{
		ID:        "test-id",
		Name:      "test-name",
		PublicIPs: []*instance.ServerIP{},
	}

	server := newServer(scwServer, nil)

	if server.GetIPv6Address() != "" {
		t.Errorf("newServer() with no IPs should have empty IPv6, got %v", server.GetIPv6Address())
	}
}
