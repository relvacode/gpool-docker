// Package ddpool is a utility for extending the gpool package with distributed docker back-ends.
// Each configured node in a NodeBridge attaches to an instance of a docker client with a set amount of concurrent workers on that node.
// When a job is executed on a node, the node is given to the job via a context value.
// During execution, a health check go routine checks the status of the Docker engine to ensure it is up.
// If a node goes down no further jobs will be scheduled to the node but existing jobs will continue to execute.
package ddpool

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/pkg/errors"
	"github.com/relvacode/gpool"
)

var (
	ErrNodeExecuting = errors.New("node: cannot remove node that has active jobs")
)

// TotalSizer is an interface which implement TotalSize.
// The job being executed on the node should implement this method for storage allocation.
type TotalSizer interface {
	// TotalSize returns the total size of the job in bytes.
	TotalSize() int64
}

// ErrHeld indicates that the node is unhealthy because it is current held
type ErrHeld struct {
	Reason string
}

func (err *ErrHeld) Error() string {
	return fmt.Sprintf("node held: %s", err.Reason)
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
		case <-w.n.dieWorkerCh:
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
func NewNode(Docker *DockerInstance, Strategy gpool.ScheduleStrategy, Workers uint, Cap uint64) *Node {
	if Strategy == nil {
		Strategy = gpool.DefaultStrategy
	}
	return &Node{
		DockerInstance: Docker,
		Workers:        Workers,
		strategy:       Strategy,
		mtx:            &sync.RWMutex{},
		workerCh:       make(chan chan *gpool.JobStatus),
		healthCh:       make(chan HealthStatus),
		holdCh:         make(chan *string),
		healthCond:     sync.NewCond(&sync.Mutex{}),
		dieCh:          make(chan chan error),
		dieWorkerCh:    make(chan struct{}),
		cap:            Cap,
	}
}

// A Node wraps a DockerNode so that it can be used in a Docker pool
type Node struct {
	*DockerInstance
	Workers uint

	strategy gpool.ScheduleStrategy
	mtx      *sync.RWMutex
	br       *NodeBridge
	workerCh chan chan *gpool.JobStatus

	healthCh   chan HealthStatus
	healthCond *sync.Cond

	holdCh      chan *string
	dieCh       chan chan error // receive channel, send result on reply channel
	dieWorkerCh chan struct{}   // signal to workers to exit
	isDead      bool

	exc   uint
	alloc uint64
	cap   uint64
}

// Status returns the current allocation and execution status of the node.
// Calling Status is safe for concurrent access.
func (n *Node) Status() (allocated uint64, capacity uint64, executing uint) {
	n.mtx.RLock()
	defer n.mtx.RUnlock()
	allocated, capacity, executing = n.alloc, n.cap, n.exc
	return
}

func (n *Node) done(j *gpool.JobStatus) {
	n.mtx.Lock()
	if d, ok := j.Job().(TotalSizer); ok {
		n.alloc -= uint64(d.TotalSize())
	}
	n.exc--
	n.mtx.Unlock()
	n.br.returnCh <- j
}

// Evaluate evaluates a list of JobStatuses in a pool queue and returns the first job that can fit in the node.
func (n *Node) Evaluate(q []*gpool.JobStatus) (int, bool) {
	// Do not evaluate an unhealthy node
	health := <-n.healthCh
	if !health.Healthy {
		return 0, false
	}
	n.mtx.RLock()
	defer n.mtx.RUnlock()

	var available []*gpool.JobStatus

	// Find jobs that will fit to this node
	for _, j := range q {
		if d, ok := j.Job().(TotalSizer); ok {
			if uint64(d.TotalSize()) < (n.cap - n.alloc) {
				available = append(available, j)
			}
		} else {
			available = append(available, j)
		}
	}

	if len(available) == 0 {
		return 0, false
	}

	// Ask the underlying strategy to pick an available job
	index, ok := n.strategy(available)
	if !ok {
		return 0, false
	}

	// If found, get the actual index of the job
	id := available[index].ID
	for idx, j := range q {
		if j.ID == id {
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

func (n *Node) Hold(reason string) {
	msg := &reason
	n.holdCh <- msg
}

func (n *Node) Release() {
	n.holdCh <- nil
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
		logrus.Infof("Node %q reconnected", n.ID)
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
		logrus.Errorf("Node %q down! %s", n.ID, err)
		health.Healthy = false
		health.Error = err
	}
	return err
}

// monitor the health of the node and prevent jobs from being scheduled if node is down.
func (n *Node) monitor() {
	logrus.Infof("Staring health monitor for node %q on %q", n.ID, n.Hostname)
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
			// Do not check health if node is in the held state
			if health.Held {
				continue
			}
			n.check(ctx, health)
		case msg := <-n.holdCh:
			if msg == nil && health.Held {
				// Release the node if held
				health.Held = false
				n.check(ctx, health)
				continue
			}
			if msg != nil {
				health.Held = true
				health.Healthy = false
				health.Error = &ErrHeld{
					Reason: *msg,
				}
				continue
			}
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

func (n *Node) tryToDie(reply chan error) bool {
	n.mtx.Lock()
	defer n.mtx.Unlock()
	if n.isDead {
		return false
	}

	logrus.Warnf("Attempting to remove node %s (%d executing)", n.ID, n.exc)
	if n.exc != 0 {
		reply <- ErrNodeExecuting
		return false
	}
	close(n.dieWorkerCh)
	n.isDead = true
	reply <- nil
	return true
}

// checkHealthBeforeSchedule checks the health of the node
// before scheduling excecution of the job.
// If a message is made to die or the bridge conext is cancelled the true is returned
func (n *Node) checkHealthBeforeSchedule() bool {
	health := <-n.healthCh
	if health.Healthy {
		return false
	}
	logrus.Infof("Node %s blocked waiting for healthy broadcast", n.ID)

	// Lock the health condition and wait for it to exit
	n.healthCond.L.Lock()
	ok := make(chan struct{})
	go func() {
		n.healthCond.Wait()
		n.healthCond.L.Unlock()
		close(ok)
	}()
	select {
	case <-ok:
		logrus.Infof("Node %s healthy broadcast received", n.ID)
	case <-n.br.ctx.Done():
		return true
	case die := <-n.dieCh:
		if n.tryToDie(die) {
			return true
		}
	}
	return false
}

// orchestrate receives requests for work from a worker and forwards it to the bridge.
// It calls the bridge evalutation strategy on all jobs that will fit within the current capacity of the node.
func (n *Node) orchestrate() {
	t := &gpool.Transaction{
		Evaluate: n.Evaluate,
		Return:   make(chan *gpool.JobStatus),
	}

	// Print die status on exit
	defer logrus.Errorf("Node %q is dead", n.ID)

orch:
	for {
		// Check initial health for each orchestration loop
		// Health is checked again during an evaluate call to prevent health changes after a request
		// to the bridge is made.
		// Returning if true ensures that if a kill signal is attempted then it is able to complete
		// without waiting for a healthly status first.
		if exit := n.checkHealthBeforeSchedule(); exit {
			return
		}
		var req chan *gpool.JobStatus

		select {
		case <-n.br.ctx.Done():
			// signal to immediately die from bridge
			// cancelling the context causes the node to exit immediately.
			// the workers will continue running unil their cancelletion takes affect.
			// The bridge will not exit until the waitgroup counting number of workers is finished.
			return
		case req = <-n.workerCh: // work request from worker
		case die := <-n.dieCh: // node kill signal
			if !n.tryToDie(die) {
				continue orch
			}
			return
		}

		select {
		case <-n.br.ctx.Done(): // signal to die immediately
			req <- nil
			return
		case die := <-n.dieCh: // node kill signal
			req <- nil
			if !n.tryToDie(die) {
				continue orch
			}
			return
		case n.br.requestCh <- t: // send node request to bridge

			j := <-t.Return // got given this job

			if j == nil {
				// If no job could be scheduled then inform the worker and continue with next loop
				req <- nil
				continue
			}

			// Update currently allocated capacity and increment execution count
			n.mtx.Lock()
			if d, ok := j.Job().(TotalSizer); ok {
				n.alloc += uint64(d.TotalSize())
			}
			n.exc++
			n.mtx.Unlock()

			// Return job back to worker
			req <- j
		}

	}
}

// start begins orchestrating and working on jobs
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
