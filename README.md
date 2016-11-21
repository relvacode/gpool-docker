Distributed Docker engine pooling

[![GoDoc](https://godoc.org/github.com/relvacode/gpool-docker?status.svg)](https://godoc.org/github.com/relvacode/gpool-docker)

For use with [gpool](https://github.com/relvacode/gpool), a concurrent execution toolkit.

Run "jobs" on one or more remote Docker engines using the [go-dockerclient](https://github.com/fsouza/go-dockerclient) library.

## Features

 * Concurrently execute any number of workers on any number of remote Docker engines
 * Built-in health checks for Docker
 * Storage allocation

## How To

First get an understanding of [gpool](https://github.com/relvacode/gpool) for local node execution.

### Creating your Job

A job is an interface that satisfies [gpool.Job](https://godoc.org/github.com/relvacode/gpool#Job). For this library we must also implement `nodes.TotalSizer` if we want storage allocation.

```go
type MyJob struct {
  Size int64
}

func (j *MyJob) Header() fmt.Stringer {
  return gpool.Header("ExampleJob")
}

func (j *MyJob) TotalSize() int64 {
  return j.Size
}

func (j *MyJob) Run(ctx context.Context) error {
  	n, ok := DockerInstanceFromContext(ctx)
    if !ok {
      return errors.New("Node not supplied to context")
    }
    // Now we can use n to operate on the Docker engine.
    n.Version()
}
```

### Creating the Pool

Then we will create a pool and attach this libraries custom bridge to the pool.
In this example we use one node with `8` workers and a maximum storage capacity of `128M`

```go
	// Create a new DockerInstance using a unix socket without TLS authentication.
	dn, err := NewDockerInstance(DefaultHostname, "", false)
	if err != nil {

	}
	// Create a Node using the instance, with 8 workers and a maximum capacity of 128M
	n := NewNode(dn, 8, 128<<20)

	// Create a bridge we can attached to a pool
	br := NewNodeBridge(n)

	// Create the pool with our node bridge
	pool := gpool.New(true, br)
```

### Submit to the Pool

Now we can submit any number of our `MyJob` examples to the pool in any way gpool supports.

```go
pool.Submit(&MyJob{Size: 64 << 20})
```

### Checking health

The health of the Docker engine is checked automatically every 10 seconds by making a ping call to the engine.
You can check what the most recent health status is by using `Node.Health()`

```go
for _, n := range br.Nodes() {
  fmt.Printf("Node %s (%s) is healthy: %t", n.ID, n.Hostname, n.Health().Healthy)
}
```

> Node AAAA:BBBB:CCCC:DDDD (unix:///var/run/docker.sock) is healthy: true
