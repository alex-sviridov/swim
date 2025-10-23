package integration

import (
	"fmt"
	"sync"
	"time"

	"github.com/alex-sviridov/swim/internal/connector"
)

// MockConnector is a test implementation of the Connector interface
type MockConnector struct {
	mu      sync.Mutex
	servers map[string]*MockServer
	nextID  int
}

// NewMockConnector creates a new mock connector
func NewMockConnector() *MockConnector {
	return &MockConnector{
		servers: make(map[string]*MockServer),
		nextID:  1,
	}
}

// CreateServer creates a mock server
func (m *MockConnector) CreateServer(payload string) (connector.Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := fmt.Sprintf("mock-server-%d", m.nextID)
	m.nextID++

	server := &MockServer{
		id:      id,
		name:    fmt.Sprintf("Test Server %s", id),
		ipv6:    fmt.Sprintf("2001:db8::%d", m.nextID-1),
		state:   "initializing",
		created: time.Now(),
		deleted: false,
	}

	m.servers[id] = server

	// Simulate async state transition to running
	go func() {
		time.Sleep(100 * time.Millisecond)
		server.mu.Lock()
		if !server.deleted {
			server.state = "running"
		}
		server.mu.Unlock()
	}()

	return server, nil
}

// ListServers returns all servers
func (m *MockConnector) ListServers() ([]connector.Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var servers []connector.Server
	for _, server := range m.servers {
		if !server.deleted {
			servers = append(servers, server)
		}
	}

	return servers, nil
}

// GetServerByID retrieves a server by ID
func (m *MockConnector) GetServerByID(serverID string) (connector.Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	server, exists := m.servers[serverID]
	if !exists || server.deleted {
		return nil, fmt.Errorf("server not found: %s", serverID)
	}

	return server, nil
}

// GetServerCount returns the number of active servers (for testing)
func (m *MockConnector) GetServerCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for _, server := range m.servers {
		if !server.deleted {
			count++
		}
	}
	return count
}

// MockServer is a test implementation of the Server interface
type MockServer struct {
	mu      sync.Mutex
	id      string
	name    string
	ipv6    string
	state   string
	created time.Time
	deleted bool
}

// GetID returns the server ID
func (s *MockServer) GetID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// GetName returns the server name
func (s *MockServer) GetName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.name
}

// GetIPv6Address returns the IPv6 address
func (s *MockServer) GetIPv6Address() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ipv6
}

// GetState returns the current state
func (s *MockServer) GetState() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.deleted {
		return "", fmt.Errorf("server has been deleted")
	}

	return s.state, nil
}

// Delete marks the server as deleted
func (s *MockServer) Delete() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.deleted {
		return fmt.Errorf("server already deleted")
	}

	s.deleted = true
	s.state = "deleting"
	return nil
}

// String returns a string representation
func (s *MockServer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("MockServer{id=%s, name=%s, ipv6=%s, state=%s}", s.id, s.name, s.ipv6, s.state)
}
