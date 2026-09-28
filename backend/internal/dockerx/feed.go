package dockerx

import (
	"context"
	"time"
)

// ContainerUpdate is immutable once published. Inventory and stats retain the
// stream's existing order and cadence; slow readers retain only the latest pair.
type ContainerUpdate struct {
	Kind       string
	Containers []Container
	Stats      []ContainerStats
	Err        error
}

type containerFeed struct {
	cancel  context.CancelFunc
	readers map[chan ContainerUpdate]struct{}
	latest  []ContainerUpdate
}

// SubscribeContainers shares one sampler between the table's viewers. It stops
// when the last viewer leaves and keeps no cached inventory between visits.
func (c *Client) SubscribeContainers() (<-chan ContainerUpdate, func()) {
	if c == nil {
		ch := make(chan ContainerUpdate, 1)
		ch <- ContainerUpdate{Err: ErrUnavailable}
		close(ch)
		return ch, func() {}
	}
	c.feedMu.Lock()
	f := c.feed
	if f == nil {
		ctx, cancel := context.WithCancel(context.Background())
		f = &containerFeed{cancel: cancel, readers: make(map[chan ContainerUpdate]struct{})}
		c.feed = f
		go c.runContainerFeed(ctx, f)
	}
	ch := make(chan ContainerUpdate, 2)
	f.readers[ch] = struct{}{}
	for _, update := range f.latest {
		ch <- update
	}
	c.feedMu.Unlock()
	return ch, func() {
		c.feedMu.Lock()
		defer c.feedMu.Unlock()
		if _, ok := f.readers[ch]; !ok {
			return
		}
		delete(f.readers, ch)
		close(ch)
		if len(f.readers) == 0 {
			f.cancel()
			if c.feed == f {
				c.feed = nil
			}
		}
	}
}

func (c *Client) publishContainers(f *containerFeed, update ContainerUpdate) {
	c.feedMu.Lock()
	defer c.feedMu.Unlock()
	if c.feed != f {
		return
	}
	if update.Kind == "containers" {
		f.latest = nil
	}
	if len(f.latest) == 2 {
		f.latest = f.latest[1:]
	}
	f.latest = append(f.latest, update)
	for ch := range f.readers {
		select {
		case ch <- update:
		default:
			// A socket never holds the collector or another viewer behind it.
			select {
			case <-ch:
			default:
			}
			ch <- update
		}
	}
}

func (c *Client) runContainerFeed(ctx context.Context, f *containerFeed) {
	defer func() {
		c.feedMu.Lock()
		defer c.feedMu.Unlock()
		for ch := range f.readers {
			close(ch)
			delete(f.readers, ch)
		}
		if c.feed == f {
			c.feed = nil
		}
	}()
	sampler := c.NewStatsSampler()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		list, err := c.ListContainers(ctx, true)
		if err != nil {
			c.publishContainers(f, ContainerUpdate{Err: err})
			return
		}
		c.publishContainers(f, ContainerUpdate{Kind: "containers", Containers: list})
		ids := make([]string, 0, len(list))
		for _, item := range list {
			if item.State == "running" {
				ids = append(ids, item.ID)
			}
		}
		if stats, err := sampler.Sample(ctx, ids); err == nil {
			c.publishContainers(f, ContainerUpdate{Kind: "stats", Stats: stats})
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
