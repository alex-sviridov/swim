package hcloud

import (
	"log/slog"
	"net"
	"os"
	"testing"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

func TestServer_GetID(t *testing.T) {
	server := &Server{
		id:   123,
		name: "test-server",
		ipv6: "2001:db8::1",
	}

	if got := server.GetID(); got != "123" {
		t.Errorf("GetID() = %v, want %v", got, "123")
	}
}

func TestServer_GetName(t *testing.T) {
	server := &Server{
		id:   123,
		name: "test-server",
		ipv6: "2001:db8::1",
	}

	if got := server.GetName(); got != "test-server" {
		t.Errorf("GetName() = %v, want %v", got, "test-server")
	}
}

func TestServer_GetIPv6Address(t *testing.T) {
	server := &Server{
		id:   123,
		name: "test-server",
		ipv6: "2001:db8::1",
	}

	if got := server.GetIPv6Address(); got != "2001:db8::1" {
		t.Errorf("GetIPv6Address() = %v, want %v", got, "2001:db8::1")
	}
}

func TestServer_String(t *testing.T) {
	server := &Server{
		id:   123,
		name: "test-server",
		ipv6: "2001:db8::1",
	}

	want := "test-server [2001:db8::1]"
	if got := server.String(); got != want {
		t.Errorf("String() = %v, want %v", got, want)
	}
}

func TestNewServer(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	ipv6Net := &net.IPNet{
		IP: net.ParseIP("2001:db8::1"),
	}
	hcloudServer := &hcloud.Server{
		ID:   123,
		Name: "test-name",
		PublicNet: hcloud.ServerPublicNet{
			IPv6: hcloud.ServerPublicNetIPv6{
				IP: ipv6Net.IP,
			},
		},
	}

	server := newServer(hcloudServer, nil, log)

	if server.GetID() != "123" {
		t.Errorf("newServer() ID = %v, want %v", server.GetID(), "123")
	}
	if server.GetName() != "test-name" {
		t.Errorf("newServer() Name = %v, want %v", server.GetName(), "test-name")
	}
	if server.GetIPv6Address() != "2001:db8::1" {
		t.Errorf("newServer() IPv6 = %v, want %v", server.GetIPv6Address(), "2001:db8::1")
	}
}

func TestNewServer_NoPublicIP(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	hcloudServer := &hcloud.Server{
		ID:   123,
		Name: "test-name",
		PublicNet: hcloud.ServerPublicNet{
			IPv6: hcloud.ServerPublicNetIPv6{
				IP: nil,
			},
		},
	}

	server := newServer(hcloudServer, nil, log)

	if server.GetIPv6Address() != "" {
		t.Errorf("newServer() with no IPs should have empty IPv6, got %v", server.GetIPv6Address())
	}
}

func TestParseServerID(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		want    int64
		wantErr bool
	}{
		{
			name:    "valid numeric ID",
			id:      "123",
			want:    123,
			wantErr: false,
		},
		{
			name:    "valid large ID",
			id:      "999999",
			want:    999999,
			wantErr: false,
		},
		{
			name:    "invalid non-numeric ID",
			id:      "abc",
			want:    0,
			wantErr: true,
		},
		{
			name:    "invalid empty ID",
			id:      "",
			want:    0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseServerID(tt.id)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseServerID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("parseServerID() = %v, want %v", got, tt.want)
			}
		})
	}
}
