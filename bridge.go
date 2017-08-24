package ddpool

import (
	"context"
	"sync"

	"github.com/relvacode/gpool"
)

// NewNodeBridge creates a new node bridge with the given nodes.
// The nodes are started when the bridge is created.
// A NodeBridge can be used to connect to a gpool pool instance.
func NewNodeBridge(nodes ...*Node) *NodeBridge {
	ctx, cancel := context.WithCancel(context.Background())
	br := &NodeBridge{
		wg:        &sync.WaitGroup{},
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
	wg    *sync.WaitGroup

	ctx context.Context
	c   context.CancelFunc

	requestCh chan *gpool.Transaction
	returnCh  chan *gpool.JobStatus
}

func (br *NodeBridge) Request() <-chan *gpool.Transaction {
	return br.requestCh
}

func (br *NodeBridge) Return() <-chan *gpool.JobStatus {
	return br.returnCh
}

// Node gets a specific node by ID
func (br *NodeBridge) Node(id string) *Node {
	for _, n := range br.Nodes {
		if n.ID == id {
			return n
		}
	}
	return nil
}

func (br *NodeBridge) Exit() <-chan struct{} {
	d := make(chan struct{})
	go func() {
		br.c()
		br.wg.Wait()
		close(d)
	}()
	return d
}
