package dockerx

import (
	"context"
	"sync"

	"github.com/docker/docker/api/types/container"
)

type snapshotKey struct{}

type snapshotRead[T any] struct {
	once  sync.Once
	done  chan struct{}
	value T
	err   error
}

func (r *snapshotRead[T]) get(ctx, owner context.Context, read func(context.Context) (T, error)) (T, error) {
	if err := ctx.Err(); err != nil {
		var zero T
		return zero, err
	}
	r.once.Do(func() {
		r.done = make(chan struct{})
		go func() { r.value, r.err = read(owner); close(r.done) }()
	})
	select {
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	case <-r.done:
		return r.value, r.err
	}
}

type readSnapshot struct {
	client      *Client
	ctx         context.Context
	lists       [2]snapshotRead[[]Container]
	mu          sync.Mutex
	inspections map[string]*snapshotRead[container.InspectResponse]
}

// WithReadSnapshot scopes duplicate discovery reads to one request. It is for
// read-only inventory handlers, never mutation preconditions. Each new request
// starts fresh, including changes made directly through Docker.
func (c *Client) WithReadSnapshot(ctx context.Context) context.Context {
	if c == nil {
		return ctx
	}
	snapshot := &readSnapshot{client: c, inspections: make(map[string]*snapshotRead[container.InspectResponse])}
	ctx = context.WithValue(ctx, snapshotKey{}, snapshot)
	snapshot.ctx = ctx
	return ctx
}

func (c *Client) snapshot(ctx context.Context) *readSnapshot {
	snapshot, _ := ctx.Value(snapshotKey{}).(*readSnapshot)
	if snapshot != nil && snapshot.client == c {
		return snapshot
	}
	return nil
}

func (c *Client) inspectContainer(ctx context.Context, id string) (container.InspectResponse, error) {
	read := func(ctx context.Context) (container.InspectResponse, error) {
		cli, err := c.api()
		if err != nil {
			return container.InspectResponse{}, err
		}
		return cli.ContainerInspect(ctx, id)
	}
	snapshot := c.snapshot(ctx)
	if snapshot == nil {
		return read(ctx)
	}
	snapshot.mu.Lock()
	result := snapshot.inspections[id]
	if result == nil {
		result = &snapshotRead[container.InspectResponse]{}
		snapshot.inspections[id] = result
	}
	snapshot.mu.Unlock()
	return result.get(ctx, snapshot.ctx, read)
}
