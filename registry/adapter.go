package registry

// Adapter is a named optional registry backend. It is useful for dependency
// injection and keeps backend selection outside business code.
type Adapter struct {
	Name    string
	Factory Factory
}

// Install registers this adapter and returns any validation error.
func (a Adapter) Install() error { return RegisterBackend(a.Name, a.Factory) }
