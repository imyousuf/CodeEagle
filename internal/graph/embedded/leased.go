package embedded

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/imyousuf/CodeEagle/internal/graph"
)

// ErrBusy is returned when a writer holds the database for longer than a
// reader will wait.
var ErrBusy = errors.New("the index is being updated by a sync; try again in a minute")

// IsLocked reports whether err is Badger refusing a directory lock another
// process holds.
func IsLocked(err error) bool {
	return err != nil && strings.Contains(err.Error(), "Cannot acquire directory lock")
}

// How long a leased read waits for a writer to finish, and how often it
// tries.
const (
	LockWait  = 3 * time.Second
	lockRetry = 250 * time.Millisecond
)

// OpenWaiting calls open until it stops failing on the directory lock or
// LockWait has passed, and reports ErrBusy if the lock never came free.
func OpenWaiting(open func() (*BranchStore, error)) (*BranchStore, error) {
	s, err := open()
	for waited := time.Duration(0); IsLocked(err) && waited < LockWait; waited += lockRetry {
		time.Sleep(lockRetry)
		s, err = open()
	}
	if IsLocked(err) {
		return nil, ErrBusy
	}
	return s, err
}

// Leased is a read-only graph.Store that holds the database only while it is
// being used.
//
// A read-only Badger handle takes a shared lock that keeps every writer out
// for as long as it is open, so a server that kept one for an agent's whole
// session would stop the worker from syncing until the session ended. Leased
// opens the database on first use, shares that handle among the calls that
// overlap it, and closes it once nothing has used it for idle. A tool's burst
// of queries therefore costs one open, and the lock is free between tool
// calls.
type Leased struct {
	open func() (*BranchStore, error)
	idle time.Duration

	mu       sync.Mutex
	store    *BranchStore
	inFlight int
	timer    *time.Timer
	closed   bool
}

// NewLeased returns a store that opens the database with open when it is
// needed and closes it after idle without use.
func NewLeased(open func() (*BranchStore, error), idle time.Duration) *Leased {
	return &Leased{open: open, idle: idle}
}

func (l *Leased) acquire() (*BranchStore, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, errors.New("store is closed")
	}
	if l.timer != nil {
		l.timer.Stop()
		l.timer = nil
	}
	if l.store == nil {
		s, err := OpenWaiting(l.open)
		if err != nil {
			return nil, err
		}
		l.store = s
	}
	l.inFlight++
	return l.store, nil
}

func (l *Leased) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.inFlight--
	if l.inFlight > 0 || l.store == nil {
		return
	}
	l.timer = time.AfterFunc(l.idle, l.closeIfIdle)
}

func (l *Leased) closeIfIdle() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inFlight == 0 && l.store != nil {
		l.store.Close()
		l.store = nil
	}
}

// Held reports whether the database is open now.
func (l *Leased) Held() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.store != nil
}

// Close closes the database if it is open, and refuses later use.
func (l *Leased) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	if l.timer != nil {
		l.timer.Stop()
	}
	if l.store != nil {
		err := l.store.Close()
		l.store = nil
		return err
	}
	return nil
}

func with[T any](l *Leased, fn func(*BranchStore) (T, error)) (T, error) {
	s, err := l.acquire()
	if err != nil {
		var zero T
		return zero, err
	}
	defer l.release()
	return fn(s)
}

func do(l *Leased, fn func(*BranchStore) error) error {
	_, err := with(l, func(s *BranchStore) (struct{}, error) { return struct{}{}, fn(s) })
	return err
}

func (l *Leased) AddNode(ctx context.Context, n *graph.Node) error {
	return do(l, func(s *BranchStore) error { return s.AddNode(ctx, n) })
}

func (l *Leased) UpdateNode(ctx context.Context, n *graph.Node) error {
	return do(l, func(s *BranchStore) error { return s.UpdateNode(ctx, n) })
}

func (l *Leased) DeleteNode(ctx context.Context, id string) error {
	return do(l, func(s *BranchStore) error { return s.DeleteNode(ctx, id) })
}

func (l *Leased) GetNode(ctx context.Context, id string) (*graph.Node, error) {
	return with(l, func(s *BranchStore) (*graph.Node, error) { return s.GetNode(ctx, id) })
}

func (l *Leased) QueryNodes(ctx context.Context, f graph.NodeFilter) ([]*graph.Node, error) {
	return with(l, func(s *BranchStore) ([]*graph.Node, error) { return s.QueryNodes(ctx, f) })
}

func (l *Leased) AddEdge(ctx context.Context, e *graph.Edge) error {
	return do(l, func(s *BranchStore) error { return s.AddEdge(ctx, e) })
}

func (l *Leased) DeleteEdge(ctx context.Context, id string) error {
	return do(l, func(s *BranchStore) error { return s.DeleteEdge(ctx, id) })
}

func (l *Leased) GetEdges(ctx context.Context, nodeID string, t graph.EdgeType) ([]*graph.Edge, error) {
	return with(l, func(s *BranchStore) ([]*graph.Edge, error) { return s.GetEdges(ctx, nodeID, t) })
}

func (l *Leased) GetNeighbors(ctx context.Context, nodeID string, t graph.EdgeType, d graph.Direction) ([]*graph.Node, error) {
	return with(l, func(s *BranchStore) ([]*graph.Node, error) { return s.GetNeighbors(ctx, nodeID, t, d) })
}

func (l *Leased) DeleteByFile(ctx context.Context, path string) error {
	return do(l, func(s *BranchStore) error { return s.DeleteByFile(ctx, path) })
}

func (l *Leased) Stats(ctx context.Context) (*graph.GraphStats, error) {
	return with(l, func(s *BranchStore) (*graph.GraphStats, error) { return s.Stats(ctx) })
}
