package ddpool

import (
	"context"
	"sync"

	"github.com/relvacode/gpool"
	"github.com/pkg/errors"
)

var (
	ErrNodeNotKnown      = errors.New("bridge: node by requested ID is not known")
	ErrNodeAlreadyExists = errors.New("bridge: attempted to add a node that already exists")
)

// NewNodeBridge creates a new node bridge with the given nodes.
// The nodes are started when the bridge is created.
// A NodeBridge can be used to connect to a gpool pool instance.
func NewNodeBridge(nodes ...*Node) *NodeBridge {
	ctx, cancel := context.WithCancel(context.Background())
	br := &NodeBridge{
		wg:        &sync.WaitGroup{},
		mtx:       new(sync.Mutex),
		Nodes:     nodes,
		requestCh: make(chan *gpool.Transaction),
		returnCh:  make(chan *gpool.JobStatus),
		ctx:       ctx,
		c:         cancel,
	}
	for _, n := range nodes {
		n.br = br
		n.start()
	}
	return br
}

// NodeBridge connects one or more Nodes to a pool ready to execute jobs.
type NodeBridge struct {
	Nodes []*Node
	mtx   *sync.Mutex
	wg    *sync.WaitGroup

	ctx context.Context
	c   context.CancelFunc

	requestCh chan *gpool.Transaction
	returnCh  chan *gpool.JobStatus
}

// Request is used to interface with the pool.
// Should never be used directly.
func (br *NodeBridge) Request() <-chan *gpool.Transaction {
	return br.requestCh
}

// Return is used to interface with the pool.
// Should never be used directly.
func (br *NodeBridge) Return() <-chan *gpool.JobStatus {
	return br.returnCh
}

func (br *NodeBridge) Add(n *Node) error {
	br.mtx.Lock()
	defer br.mtx.Unlock()

	// Check for existing node with same ID
	for _, en := range br.Nodes {
		if en.ID == n.ID {
			return ErrNodeAlreadyExists
		}
	}

	// Assign and start the node
	n.br = br
	n.start()
	br.Nodes = append(br.Nodes, n)

	return nil
}

// Node gets a specific node by ID
func (br *NodeBridge) Node(id string) (*Node, error) {
	br.mtx.Lock()
	defer br.mtx.Unlock()

	for _, n := range br.Nodes {
		if n.ID == id {
			return n, nil
		}
	}
	return nil, ErrNodeNotKnown
}

// Remove attempts to remove a node from the bridge.
// Recommended practice is to first hold the node, ensure nothing is running
// and then attempt to remove the node.
// If any actively executing job exists on the node then an error is raised.
func (br *NodeBridge) Remove(id string) error {
	br.mtx.Lock()
	defer br.mtx.Unlock()

	var at int
	var node *Node
	for i, n := range br.Nodes {
		if n.ID == id {
			at = i
			node = n
			break
		}
	}

	if node == nil {
		return ErrNodeNotKnown
	}

	// Send the request to the node
	// If the node replies with nil then it has exited.
	reply := make(chan error)
	node.dieCh <- reply
	err := <-reply
	if err != nil {
		return err
	}

	// Delete nodes from list of our tracked nodes
	br.Nodes = append(br.Nodes[:at], br.Nodes[at+1:]...)
	return nil
}

// MaxCapacity returns the maximum capacity that any node in the bridge can handle.
func (br *NodeBridge) MaxCapacity() uint64 {
	br.mtx.Lock()
	defer br.mtx.Unlock()

	var maxCap uint64
	for _, n := range br.Nodes {
		_, capacity, _ := n.Status()
		if capacity > maxCap {
			maxCap = capacity
		}
	}
	return maxCap
}

func (br *NodeBridge) Exit() <-chan struct{} {
	br.mtx.Lock()
	defer br.mtx.Unlock()

	d := make(chan struct{})
	go func() {
		br.c()
		br.wg.Wait()
		close(d)
	}()
	return d
}
