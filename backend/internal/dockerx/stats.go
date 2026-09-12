package dockerx

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
)

// ContainerStats mirrors what `docker stats` shows, already reduced to the
// percentages and rates a dashboard needs. Doing the arithmetic here keeps the
// frontend from having to understand cgroup counter semantics.
type ContainerStats struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	TS         time.Time `json:"ts"`
	CPUPercent float64   `json:"cpuPercent"`
	MemUsage   uint64    `json:"memUsage"`
	// MemLimit is what the kernel will enforce, which for a container with no
	// limit of its own is the whole machine.
	MemLimit uint64 `json:"memLimit"`
	// MemLimited says whether that figure is a decision somebody made.
	//
	// Without it the two cases are indistinguishable on the wire, and the
	// table showed "97 MB / 62.7 GB" for a container nobody had limited —
	// a denominator that is not a budget, beside a percentage of it that means
	// nothing. When this is false the UI says "no limit" and offers the share
	// of the host instead, which is the fact that actually exists.
	MemLimited bool `json:"memLimited"`
	// MemPercent is of MemLimit, so it is only a meaningful number when
	// MemLimited is true. MemHostPercent is of the whole machine and is always
	// meaningful.
	MemPercent     float64 `json:"memPercent"`
	MemHostPercent float64 `json:"memHostPercent,omitempty"`

	// CPUPercent counts one core as 100%, which is what `docker stats` prints
	// and is not what most people assume. HostCPUs is carried beside it so the
	// UI can say "of 8 cores" rather than leaving the reader to guess whether
	// 400% is possible. CPULimit is the container's own quota in cores, zero
	// when it has none.
	HostCPUs   int     `json:"hostCpus,omitempty"`
	CPULimit   float64 `json:"cpuLimit,omitempty"`
	NetRx      uint64  `json:"netRx"`
	NetTx      uint64  `json:"netTx"`
	BlockRead  uint64  `json:"blockRead"`
	BlockWrite uint64  `json:"blockWrite"`
	PIDs       uint64  `json:"pids"`
	OnlineCPUs uint32  `json:"onlineCpus"`

	// SizeRw is the container's writable layer, folded into the sample so the
	// history can answer "how fast is this growing".
	//
	// "Writable layer: 38.7 GB" is a figure nobody can act on: either it has
	// been that for six months and is the size of the thing, or it was 26 GB
	// yesterday and the disk has two days left. Only a series can tell those
	// apart, and this is the cheapest place to record one — the disk-usage
	// walk is already cached and already refreshed in the background, so
	// reading it here costs nothing and asking the daemon for sizes per
	// container would cost a layer walk per sample.
	SizeRw int64 `json:"sizeRw,omitempty"`

	// Cumulative CPU counters, carried so a caller sampling repeatedly can
	// work out utilisation itself. They are nanosecond totals since the
	// container started and since the host booted respectively — the same
	// pair `docker stats` divides — and are only meaningful as a difference
	// between two samples.
	CPUTotal  uint64 `json:"cpuTotal"`
	SystemCPU uint64 `json:"systemCpu"`
}

// StatsStream follows one container's stats until the context ends.
func (c *Client) StatsStream(ctx context.Context, id string, out chan<- ContainerStats) error {
	cli, err := c.api()
	if err != nil {
		return err
	}
	resp, err := cli.ContainerStats(ctx, id, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// The container's own limits, read once rather than per frame: they cannot
	// change while it runs, and the alternative is an inspect a second.
	limit := c.resourceLimitsOf(ctx, id)

	dec := json.NewDecoder(resp.Body)
	for {
		var raw container.StatsResponse
		if err := dec.Decode(&raw); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		st := convertStats(id, raw)
		frame := []ContainerStats{st}
		c.applyHostCapacity(ctx, frame)
		st = frame[0]
		st.MemLimited = limit.memory > 0
		if !st.MemLimited {
			st.MemPercent = 0
		} else {
			st.MemLimit = uint64(limit.memory)
			st.MemPercent = round2(float64(st.MemUsage) / float64(limit.memory) * 100)
		}
		st.CPULimit = limit.cpus
		select {
		case <-ctx.Done():
			return nil
		case out <- st:
		}
	}
}

// containerLimits is what a container was told it may use, as opposed to what
// the kernel reports it is allowed to use — which for an unlimited container
// is the whole machine.
type containerLimits struct {
	memory int64
	cpus   float64
}

func (c *Client) resourceLimitsOf(ctx context.Context, id string) containerLimits {
	cli, err := c.api()
	if err != nil {
		return containerLimits{}
	}
	insp, err := cli.ContainerInspect(ctx, id)
	if err != nil || insp.HostConfig == nil {
		return containerLimits{}
	}
	out := containerLimits{memory: insp.HostConfig.Memory}
	switch {
	case insp.HostConfig.NanoCPUs > 0:
		out.cpus = float64(insp.HostConfig.NanoCPUs) / 1e9
	case insp.HostConfig.CPUQuota > 0 && insp.HostConfig.CPUPeriod > 0:
		out.cpus = float64(insp.HostConfig.CPUQuota) / float64(insp.HostConfig.CPUPeriod)
	}
	return out
}

// StatsSampler turns Docker's cheap one-shot stats into current utilisation.
//
// One-shot is the endpoint worth calling for a whole table of containers: it
// answers immediately, where the non-streaming call primes for about a second
// per container. The cost is that it reports no previous sample, so the
// percentage has to be derived from the difference between two of its own
// calls — which is what this holds the counters for.
//
// Unlike sysinfo's collector, sharing one of these between callers is safe.
// CPU percent is a ratio of two deltas measured over the same window, so it
// does not depend on how long that window was; a second caller sampling in
// between makes the next reading noisier, never wrong. Everything else on
// ContainerStats is a cumulative counter, not a rate. A sampler per caller is
// still the better default where the caller has one to spare — a series stored
// for a week deserves its own even intervals — but nothing breaks if it does
// not.
type StatsSampler struct {
	client *Client

	mu   sync.Mutex
	prev map[string]cpuCounters
}

type cpuCounters struct {
	total  uint64
	system uint64
}

func (c *Client) NewStatsSampler() *StatsSampler {
	return &StatsSampler{client: c, prev: map[string]cpuCounters{}}
}

// applyHostCapacity separates "limited to the whole machine" from "not
// limited", which the Engine reports identically.
//
// A container with no cgroup memory limit has `MemoryStats.Limit` set to the
// host's total RAM. Read literally that is a 62.7 GB budget nobody set, and a
// percentage of it that says a container using 400 MB is at 0.6% of its
// limit — a sentence with no meaning. The only way to tell the two apart is to
// know what the machine has, so that is looked up once and compared.
func (c *Client) applyHostCapacity(ctx context.Context, out []ContainerStats) {
	memTotal, cpus := c.HostCapacity(ctx)
	for i := range out {
		if cpus > 0 {
			out[i].HostCPUs = cpus
		}
		if memTotal <= 0 {
			continue
		}
		if out[i].MemUsage > 0 {
			out[i].MemHostPercent = round2(float64(out[i].MemUsage) / float64(memTotal) * 100)
		}
		// A megabyte of tolerance: some kernels report the machine's memory
		// a few pages under what the daemon does, and a container that reads
		// as limited to 62.6 of 62.7 GB is not limited.
		if out[i].MemLimit == 0 || int64(out[i].MemLimit)+(1<<20) >= memTotal {
			out[i].MemLimited = false
			out[i].MemPercent = 0
		}
	}
}

// Sample reads every named container once.
//
// The first call for a container reports no CPU percentage, because there is
// nothing to difference against yet; the caller either primes the sampler or
// accepts one blank frame.
func (s *StatsSampler) Sample(ctx context.Context, ids []string) ([]ContainerStats, error) {
	cli, err := s.client.api()
	if err != nil {
		return nil, err
	}
	out := make([]ContainerStats, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		resp, err := cli.ContainerStatsOneShot(ctx, id)
		if err != nil {
			continue
		}
		var raw container.StatsResponse
		decErr := json.NewDecoder(resp.Body).Decode(&raw)
		resp.Body.Close()
		if decErr != nil {
			continue
		}
		st := convertStats(id, raw)
		seen[id] = struct{}{}
		s.fillCPU(id, &st, cpuCount(raw))
		out = append(out, st)
	}
	s.forget(seen)
	s.client.applyHostCapacity(ctx, out)
	s.client.applyWritableSizes(ctx, out)
	return out, nil
}

// applyWritableSizes folds the cached disk-usage walk into a stats batch.
//
// Reads the cache and never forces a refresh: a sampler running every fifteen
// seconds must not trigger a walk of every layer on the host. A batch taken
// before the first walk completes simply carries no size, which the history
// records as absent rather than as zero.
func (c *Client) applyWritableSizes(ctx context.Context, out []ContainerStats) {
	if len(out) == 0 {
		return
	}
	du := c.diskUsage(ctx)
	if du == nil {
		return
	}
	sizes := make(map[string]int64, len(du.Containers))
	for _, ct := range du.Containers {
		sizes[ct.ID] = ct.SizeRw
	}
	for i := range out {
		if size, ok := sizes[out[i].ID]; ok {
			out[i].SizeRw = size
		}
	}
}

// SampleAll reads every running container, which is what a recorder wants: it
// should follow whatever is up now rather than a list captured at startup.
func (s *StatsSampler) SampleAll(ctx context.Context) ([]ContainerStats, error) {
	list, err := s.client.ListContainers(ctx, false)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(list))
	for _, c := range list {
		if c.State == "running" {
			ids = append(ids, c.ID)
		}
	}
	if len(ids) == 0 {
		s.forget(nil)
		return nil, nil
	}
	return s.Sample(ctx, ids)
}

func (s *StatsSampler) fillCPU(id string, st *ContainerStats, cpus float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.prev[id]
	s.prev[id] = cpuCounters{total: st.CPUTotal, system: st.SystemCPU}
	if !ok || st.CPUPercent > 0 {
		// Either nothing to difference against, or the sample already carried
		// its own predecessor because it came from the streaming endpoint.
		return
	}
	st.CPUPercent = cpuPercent(
		float64(st.CPUTotal)-float64(prev.total),
		float64(st.SystemCPU)-float64(prev.system),
		cpus,
	)
}

// forget drops containers that were not in this pass, so a host that recreates
// containers often does not accumulate their counters for the life of the
// process. A nil set clears everything, which is what "nothing is running"
// means.
func (s *StatsSampler) forget(seen map[string]struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.prev {
		if _, ok := seen[id]; !ok {
			delete(s.prev, id)
		}
	}
}

// cpuPercent is the share of the whole host this container used over the
// window, scaled the way `docker stats` scales it: 100% means one core.
//
// A counter that went backwards means the container was recreated under the
// same id-space or the host rebooted, and reporting the resulting enormous
// delta as a spike would be worse than reporting nothing.
func cpuPercent(cpuDelta, sysDelta, cpus float64) float64 {
	if cpuDelta <= 0 || sysDelta <= 0 || cpus <= 0 {
		return 0
	}
	return round2(cpuDelta / sysDelta * cpus * 100)
}

func cpuCount(raw container.StatsResponse) float64 {
	if raw.CPUStats.OnlineCPUs > 0 {
		return float64(raw.CPUStats.OnlineCPUs)
	}
	return float64(len(raw.CPUStats.CPUUsage.PercpuUsage))
}

func convertStats(id string, raw container.StatsResponse) ContainerStats {
	s := ContainerStats{
		ID:         id,
		Name:       trimName(raw.Name),
		TS:         raw.Read.UTC(),
		MemLimit:   raw.MemoryStats.Limit,
		PIDs:       raw.PidsStats.Current,
		OnlineCPUs: raw.CPUStats.OnlineCPUs,
	}
	// Docker reports total memory including the page cache; subtracting the
	// reclaimable portion is what the CLI does and is what operators expect.
	usage := raw.MemoryStats.Usage
	if cache, ok := raw.MemoryStats.Stats["inactive_file"]; ok && cache < usage {
		usage -= cache
	} else if cache, ok := raw.MemoryStats.Stats["total_inactive_file"]; ok && cache < usage {
		usage -= cache
	}
	s.MemUsage = usage
	if s.MemLimit > 0 {
		s.MemPercent = round2(float64(usage) / float64(s.MemLimit) * 100)
	}
	s.MemLimited = s.MemLimit > 0

	s.CPUTotal = raw.CPUStats.CPUUsage.TotalUsage
	s.SystemCPU = raw.CPUStats.SystemUsage

	// Network and block totals are cumulative counters and have nothing to do
	// with the CPU delta below, so they are read before the early return that
	// the one-shot path takes. They used to sit after it, which meant every
	// caller that samples rather than streams — the container table, and the
	// recorder that keeps the history — saw a permanent zero for both.
	//
	// An absent `networks` is not the same failure: Docker omits it entirely
	// for a container sharing the host's network namespace, because there is
	// no per-container interface to measure. Nothing is missing there and
	// nothing can be reported.
	for _, n := range raw.Networks {
		s.NetRx += n.RxBytes
		s.NetTx += n.TxBytes
	}
	for _, b := range raw.BlkioStats.IoServiceBytesRecursive {
		switch b.Op {
		case "read", "Read":
			s.BlockRead += b.Value
		case "write", "Write":
			s.BlockWrite += b.Value
		}
	}

	// Only the streaming endpoint fills PreCPUStats. The one-shot endpoint
	// zeroes it, and subtracting zero turns the arithmetic below into "this
	// container's whole lifetime divided by the host's whole uptime" — an
	// average since start, reported as though it were the reading now. On
	// anything long-lived that reads as nearly idle however hard the container
	// is working, so a sample without a predecessor claims no percentage at
	// all and leaves StatsSampler to work it out from two of them.
	if raw.PreCPUStats.SystemUsage == 0 {
		return s
	}
	cpuDelta := float64(raw.CPUStats.CPUUsage.TotalUsage) - float64(raw.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(raw.CPUStats.SystemUsage) - float64(raw.PreCPUStats.SystemUsage)
	s.CPUPercent = cpuPercent(cpuDelta, sysDelta, cpuCount(raw))
	return s
}

func trimName(n string) string {
	if len(n) > 0 && n[0] == '/' {
		return n[1:]
	}
	return n
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }
