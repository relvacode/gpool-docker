// Package nodes is a utility for extending the gpool package with distributed docker back-ends.
// Each configured node in a NodeBridge attaches to an instance of a docker client with a set amount of concurrent workers on that node.
// When a job is executed on a node, the node is given to the job via a context value.
// During execution, a health check go routine checks the status of the Docker engine to ensure it is up.
// If a node goes down no further jobs will be scheduled to the node but existing jobs will continue to execute.
package nodes

import (
	"context"
	"github.com/Sirupsen/logrus"
	"github.com/pkg/errors"
	"github.com/relvacode/gpool"
	"sync"
	"time"
)

// TotalSizer is an interface which implement TotalSize.
// The job being executed on the node should implement this method for storage allocation.
type TotalSizer interface {
	// TotalSize returns the total size of the job in bytes.
	TotalSize() int64
}

// worker is an instance of a worker on a node.
type worker struct {
	n *Node
}

func (w *worker) work() {
	defer w.n.br.wg.Done()
	c := make(chan *gpool.JobStatus)
	for {
		select {
		case <-w.n.br.ctx.Done():
			return
		case w.n.workerCh <- c:
			j := <-c
			if j == nil {
				continue
			}
			ctx := j.Context()
			ctx = context.WithValue(ctx, DockerInstanceKey, w.n.DockerInstance)
			j.Error = j.Job().Run(ctx)
			w.n.done(j)
		}
	}
}

// NewNode extends a DockerNode to implement a Node with a set amount of workers and storage capacity.
func NewNode(Docker *DockerInstance, Workers uint, Cap uint64) *Node {
	return &Node{
		DockerInstance: Docker,
		Workers:        Workers,
		mtx:            &sync.RWMutex{},
		m:              Cap,
		workerCh:       make(chan chan *gpool.JobStatus),
		healthCh:       make(chan HealthStatus),
		healthCond:     sync.NewCond(&sync.Mutex{}),
	}
}

// A Node wraps a DockerNode so that it can be used in a
type Node struct {
	*DockerInstance
	Workers uint

	mtx      *sync.RWMutex
	br       *NodeBridge
	workerCh chan chan *gpool.JobStatus

	healthCh   chan HealthStatus
	healthCond *sync.Cond

	exc uint
	m   uint64
	c   uint64
}

// Status returns the current allocation and execution status of the node.
// Calling Status is safe for concurrent access.
func (n *Node) Status() (allocated uint64, capacity uint64, executing uint) {
	n.mtx.RLock()
	defer n.mtx.RUnlock()
	allocated, capacity, executing = n.c, n.m, n.exc
	return
}

func (n *Node) done(j *gpool.JobStatus) {
	n.mtx.Lock()
	if d, ok := j.Job().(TotalSizer); ok {
		n.c -= uint64(d.TotalSize())
	}
	n.exc--
	n.mtx.Unlock()
	n.br.returnCh <- j
}

// Evaluate evaluates a list of JobStatuses in a pool queue and returns the first job that can fit in the node.
func (n *Node) Evaluate(q []*gpool.JobStatus) (int, bool) {
	n.mtx.RLock()
	defer n.mtx.RUnlock()
	for idx, j := range q {
		if d, ok := j.Job().(TotalSizer); ok {
			if uint64(d.TotalSize()) < (n.m - n.c) {
				return idx, true
			}
		} else {
			return idx, true
		}
	}
	return 0, false
}

// Health returns the most recent HealthStatus of the node.
// Calling Health is safe for concurrent access.
func (n *Node) Health() HealthStatus {
	return <-n.healthCh
}

func (n *Node) check(ctx context.Context, health *HealthStatus) error {
	now := time.Now()
	health.Heartbeat = &now

	err := n.Client.Ping()

	health.ResponseTime = time.Since(now)

	if err == context.DeadlineExceeded {
		err = errors.New("timed-out waiting for reply")
	}

	// If error is clear and previously had an error
	if err == nil && health.Error != nil {
		health.Healthy = true
		health.Error = nil
		n.healthCond.L.Lock()
		n.healthCond.Broadcast()
		n.healthCond.L.Unlock()
	}
	if err == nil {
		return nil
	}
	if health.Healthy {
		logrus.Warnf("Node %s down! %s", n.ID, err)
		health.Healthy = false
		health.Error = err
	}
	return err
}

// monitor the health of the node and prevent jobs from being scheduled if node is down.
func (n *Node) monitor() {
	logrus.Infof("Staring health monitor for node %s on %s", n.ID, n.Hostname)
	t := time.NewTicker(time.Second * 10)
	defer t.Stop()

	health := &HealthStatus{
		Healthy: true,
	}

	ctx := context.Background()
	n.check(ctx, health)

	for {
		select {
		case <-t.C:
			n.check(ctx, health)
		case n.healthCh <- *health:
		case <-n.br.ctx.Done():
			// If done allow the orchestrator to continue and exit.
			n.healthCond.L.Lock()
			n.healthCond.Broadcast()
			n.healthCond.L.Unlock()
			return
		}
	}
}

func (n *Node) orchestrate() {
	t := &gpool.Transaction{
		Evaluate: n.Evaluate,
		Return:   make(chan *gpool.JobStatus),
	}
	for {
		health := <-n.healthCh
		if !health.Healthy {
			logrus.Infof("Node %s blocked waiting for healthy broadcast", n.ID)
			n.healthCond.L.Lock()
			n.healthCond.Wait()
			n.healthCond.L.Unlock()
			logrus.Infof("Node %s healthy broadcast received", n.ID)
		}

		var req chan *gpool.JobStatus

		select {
		case <-n.br.ctx.Done():
			return
		case req = <-n.workerCh:
		}

		select {
		case <-n.br.ctx.Done():
			req <- nil
			return
		case n.br.requestCh <- t:
			j := <-t.Return
			// If no job could be scheduled then inform the worker
			if j == nil {
				req <- nil
				continue
			}
			n.mtx.Lock()
			if d, ok := j.Job().(TotalSizer); ok {
				n.c += uint64(d.TotalSize())
			}
			n.exc++
			n.mtx.Unlock()
			req <- j
		}

	}
}

func (n *Node) start() {
	logrus.Infof("Node %s starting %d worker(s)", n.ID, n.Workers)
	for i := uint(0); i < n.Workers; i++ {
		w := &worker{n: n}
		w.n.br.wg.Add(1)
		go w.work()
	}
	go n.monitor()
	go n.orchestrate()
}