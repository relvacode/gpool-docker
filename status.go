package ddpool

// NodeWorkerStatus is a representation of the current node workers status.
type NodeWorkerStatus struct {
	Total     uint
	Executing uint
}

// NodeStorageStatus is a representation of the current node storage status.
type NodeStorageStatus struct {
	Capacity  uint64
	Allocated uint64
}

// NodeStatus is a representation of the current node status.
type NodeStatus struct {
	ID       string
	Hostname string

	Workers NodeWorkerStatus
	Health  HealthStatus
	Storage NodeStorageStatus
}

// NewNodeStatus generates a node status from the given Node.
func NewNodeStatus(n *Node) NodeStatus {
	s := NodeStatus{}
	s.ID = n.ID
	s.Hostname = n.Hostname
	s.Workers.Total = n.Workers
	s.Storage.Allocated, s.Storage.Capacity, s.Workers.Executing = n.Status()
	s.Health = n.Health()
	return s
}
