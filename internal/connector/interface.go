package connector

type Connector interface {
	ListServers() ([]Server, error)
	GetServerByID(id string) (Server, error)
	CreateServer(payload string) (Server, error)
}

type Server interface {
	GetID() string
	GetName() string
	GetIPv6Address() string
	GetState() (string, error)
	Delete() error
	String() string
}
