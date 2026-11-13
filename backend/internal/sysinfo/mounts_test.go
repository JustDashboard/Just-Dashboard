package sysinfo

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/common"
	"github.com/shirou/gopsutil/v4/disk"
	"golang.org/x/sys/unix"
)

// hostProc is a context under which gopsutil reads testdata/host-proc as
// /proc: the mount table of a server laid out like the one the fault was
// measured on, with a second disk, a bind mount, a mounted image, a snap and
// Docker's own mounts beside it.
func hostProc() context.Context {
	return context.WithValue(context.Background(), common.EnvKey,
		common.EnvMap{common.HostProcEnvKey: "testdata/host-proc"})
}

const gib = 1 << 30

func usageOf(total, used uint64) *disk.UsageStat {
	return &disk.UsageStat{
		Total: total, Used: used, Free: total - used,
		UsedPercent: float64(used) / float64(total) * 100,
		InodesTotal: 1000, InodesUsed: 10,
	}
}

// twoViews is a server and the dashboard's container on it. The same path is
// a different place in each: the container's /boot is a directory of its
// image, on the container's root filesystem, and it has no /boot/efi.
type twoViews struct {
	inHost    atomic.Int32
	host      map[string]*disk.UsageStat
	container map[string]*disk.UsageStat
}

func (v *twoViews) run(fn func()) error {
	v.inHost.Add(1)
	defer v.inHost.Add(-1)
	fn()
	return nil
}

func (v *twoViews) measure(path string) (*disk.UsageStat, error) {
	view := v.container
	if v.inHost.Load() > 0 {
		view = v.host
	}
	if u, ok := view[path]; ok {
		return u, nil
	}
	return nil, syscall.ENOENT
}

func collectorWith(probe *mountProbe) *Collector {
	c := NewCollector()
	c.probe = probe
	return c
}

func TestMountsAreMeasuredInTheHostsMountNamespace(t *testing.T) {
	root := usageOf(386*gib, 113*gib)
	views := &twoViews{
		host: map[string]*disk.UsageStat{
			"/":                                      root,
			"/boot":                                  usageOf(923156480, 122281984),
			"/boot/efi":                              usageOf(109395456, 6399488),
			"/mnt/data":                              usageOf(100*gib, 90*gib),
			"/mnt/config":                            usageOf(4096, 4096),
			"/var/snap/firefox/common/host-hunspell": root,
		},
		// What a statfs from inside the container answers: every directory
		// of the image is on the container's root filesystem.
		container: map[string]*disk.UsageStat{"/": root, "/boot": root},
	}
	c := collectorWith(newMountProbe(views.run, views.measure, time.Second))

	got := c.mounts(hostProc(), 0)

	want := []struct {
		point, device, fs string
		total             uint64
	}{
		{"/", "/dev/sda1", "ext4", 386 * gib},
		{"/boot", "/dev/sda16", "ext4", 923156480},
		{"/boot/efi", "/dev/sda15", "vfat", 109395456},
		{"/mnt/data", "/dev/sdc1", "ext4", 100 * gib},
	}
	if len(got) != len(want) {
		t.Fatalf("mounts = %+v, want %d of them", got, len(want))
	}
	for i, w := range want {
		m := got[i]
		if m.Mountpoint != w.point || m.Device != w.device || m.FSType != w.fs || m.Total != w.total {
			t.Errorf("mount %d = %s on %s (%s), %d bytes; want %s on %s (%s), %d bytes",
				i, m.Mountpoint, m.Device, m.FSType, m.Total, w.point, w.device, w.fs, w.total)
		}
	}
	if got[3].UsedPercent != 90 || got[3].InodesTotal != 1000 || got[3].InodesUsed != 10 {
		t.Errorf("/mnt/data = %+v, want 90%% used and its inodes", got[3])
	}
}

func TestAMountThatDoesNotAnswerIsLeftOutAndNotAskedAgain(t *testing.T) {
	var (
		mu      sync.Mutex
		asked   = map[string]int{}
		release = make(chan struct{})
	)
	measure := func(path string) (*disk.UsageStat, error) {
		mu.Lock()
		asked[path]++
		mu.Unlock()
		if path == "/mnt/data" {
			<-release
		}
		return usageOf(10*gib, gib), nil
	}
	count := func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return asked[path]
	}
	const timeout = 150 * time.Millisecond
	probe := newMountProbe(func(fn func()) error { fn(); return nil }, measure, timeout)
	c := collectorWith(probe)

	began := time.Now()
	first := c.mounts(hostProc(), 0)
	if took := time.Since(began); took > 20*timeout {
		t.Fatalf("the pass took %s with one mount hung; it must give up after %s", took, timeout)
	}
	if len(first) != 3 || first[0].Mountpoint != "/" || first[1].Mountpoint != "/boot" || first[2].Mountpoint != "/boot/efi" {
		t.Fatalf("mounts = %+v, want the three that answered", first)
	}

	// The hung call is still out. Later passes neither wait for it nor start
	// another behind it, or every sample would cost a thread and a timeout.
	time.Sleep(timeout)
	began = time.Now()
	for range 5 {
		if got := c.mounts(hostProc(), 0); len(got) != 3 {
			t.Fatalf("mounts = %+v, want the three that answer", got)
		}
	}
	if took := time.Since(began); took > timeout {
		t.Fatalf("five passes took %s: they waited for a mount already known to be hung", took)
	}
	if n := count("/mnt/data"); n != 1 {
		t.Fatalf("the hung mount was asked %d times, want once", n)
	}
	// A second collector, as each live socket has, shares the one call.
	if got := collectorWith(probe).mounts(hostProc(), 0); len(got) != 3 || count("/mnt/data") != 1 {
		t.Fatalf("a second collector got %d mounts and the hung mount was asked %d times", len(got), count("/mnt/data"))
	}

	// The device comes back: the old call ends and the mount is measured again.
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := c.mounts(hostProc(), 0); len(got) == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the mount never came back after its device answered")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAPanicMeasuringOneMountCostsOnlyThatMount(t *testing.T) {
	measure := func(path string) (*disk.UsageStat, error) {
		if path == "/boot" {
			panic("unreadable")
		}
		return usageOf(10*gib, gib), nil
	}
	c := collectorWith(newMountProbe(func(fn func()) error { fn(); return nil }, measure, time.Second))
	got := c.mounts(hostProc(), 0)
	if len(got) != 3 {
		t.Fatalf("mounts = %+v, want every mount but /boot", got)
	}
	for _, m := range got {
		if m.Mountpoint == "/boot" {
			t.Fatalf("mounts = %+v, /boot panicked and must be absent", got)
		}
	}
}

func TestStatMountReadsARealFilesystem(t *testing.T) {
	u, err := statMount(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if u.Total == 0 {
		t.Fatalf("usage = %+v, want a filesystem with a size", u)
	}
}

// TestCollectedMountsAreTheHostsFilesystems needs what the backend container
// has, root, the host PID namespace and a mount namespace of its own, so it
// runs only there: `docker run --privileged --pid=host` with the test binary.
// It checks each reported mount against the same filesystem reached another
// way, through /proc/1/root.
func TestCollectedMountsAreTheHostsFilesystems(t *testing.T) {
	ours, err := os.Stat("/proc/self/ns/mnt")
	if err != nil {
		t.Skip(err)
	}
	theirs, err := os.Stat("/proc/1/ns/mnt")
	if err != nil || os.SameFile(ours, theirs) {
		t.Skip("no other mount namespace to enter")
	}
	snap, err := NewCollector().Collect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Mounts) == 0 {
		t.Fatal("no mounts reported")
	}
	for _, m := range snap.Mounts {
		var st unix.Statfs_t
		if err := unix.Statfs("/proc/1/root"+m.Mountpoint, &st); err != nil {
			t.Errorf("%s: %v", m.Mountpoint, err)
			continue
		}
		if want := uint64(st.Blocks) * uint64(st.Bsize); m.Total != want {
			t.Errorf("%s reported as %d bytes; the host's filesystem there is %d", m.Mountpoint, m.Total, want)
		}
		t.Logf("%-12s %-10s %s %d bytes", m.Mountpoint, m.Device, m.FSType, m.Total)
	}
}
