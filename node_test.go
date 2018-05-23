package ddpool

import "github.com/relvacode/gpool"

func Example() {
	// Create a new DockerInstance using a unix socket without TLS authentication.
	dn, err := NewDockerInstance(DefaultHostname, "", false)
	if err != nil {

	}
	// Create a Node using the instance, with 8 workers and a maximum capacity of 128M
	n := NewNode(dn, gpool.FIFOStrategy, 8, 128<<20)

	// Create a bridge we can attached to a pool
	br := NewNodeBridge(n)

	// Create the pool with our node bridge
	gpool.New(true, br)
	// Use the pool as you would normally.
}
