package scaleway

import (
	"fmt"

	"github.com/alex-sviridov/swim/internal/connector"
	"github.com/scaleway/scaleway-sdk-go/api/instance/v1"
)

type Instance struct {
	id    string
	name  string
	ipv6  string
	state string
}

func newInstance(server *instance.Server) *Instance {
	var ipv6 string
	if len(server.PublicIPs) > 0 {
		ipv6 = server.PublicIPs[0].Address.String()
	}
	return &Instance{
		id:    server.ID,
		name:  server.Name,
		ipv6:  ipv6,
		state: server.StateDetail,
	}
}

func (i *Instance) GetName() string {
	return i.name
}

func (i *Instance) GetState() string {
	return i.state
}

func (i *Instance) String() string {
	return fmt.Sprintf("%v [%v]", i.name, i.ipv6)
}

var _ connector.Instance = (*Instance)(nil)
