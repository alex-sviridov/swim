package connector

type ProvisionRequest interface {
	Validate() error
}

type Connector interface {
	ListInstances() ([]Instance, error)
	ProvisionInstance(payload string) (Instance, error)
	// NewProvisionRequest creates a new ProvisionRequest that can be unmarshaled from JSON
	NewProvisionRequest() ProvisionRequest
}

type Instance interface {
	GetName() string
	String() string
}
