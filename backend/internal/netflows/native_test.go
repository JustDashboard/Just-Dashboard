package netflows

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Wayy01/Just-Dashboard/backend/internal/dockerx"
)

type identityDocker struct {
	fd              *os.File
	calls           int
	fail            bool
	requireDeadline bool
}

func (d *identityDocker) ListRunning(context.Context) ([]dockerx.Container, error) { return nil, nil }
func (d *identityDocker) NetworkSource(context.Context, string) (*dockerx.NetworkSource, error) {
	return nil, errors.New("unused")
}
func (d *identityDocker) NetworkSourceCommand(ctx context.Context, _ *dockerx.NetworkSource, _ string, _ ...string) (*exec.Cmd, func(), error) {
	d.calls++
	if d.requireDeadline {
		if _, ok := ctx.Deadline(); !ok {
			return nil, nil, errors.New("unbounded context")
		}
	}
	if d.fail {
		return nil, nil, errors.New("container restarted")
	}
	return &exec.Cmd{ExtraFiles: []*os.File{d.fd}}, func() {}, nil
}
func procStat(ticks string) string {
	return "111 (name with spaces) S " + strings.Repeat("0 ", 18) + ticks + " 0"
}
func TestDescriptorAttributionRevalidatesPIDFDContainerAndBoundsInspections(t *testing.T) {
	fd, err := os.CreateTemp(t.TempDir(), "ns")
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	id := strings.Repeat("a", 64)
	docker := &identityDocker{fd: fd, requireDeadline: true}
	native := Native{Docker: docker}
	native.procReader = func(_ int, leaf string) (string, error) {
		if leaf == "stat" {
			return procStat("99"), nil
		}
		return "0::/system.slice/docker-" + id + ".scope", nil
	}
	native.linkReader = func(string) (string, error) { return "socket:[777]", nil }
	known := map[string]verifiedSource{id: {&dockerx.NetworkSource{ID: id, Name: "fixture", StartedAt: "instance-A"}, namespaceIdentity(fd)}}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	source := parseSS(strings.Repeat(`tcp ESTAB 0 0 127.0.0.1:123 192.0.2.1:443 users:(("worker",pid=111,fd=3)) ino:777 sk:abcd bytes_sent:100`+"\n", 64), 1024)
	native.attribute(ctx, &source, known, map[string]bool{})
	if docker.calls != 1 {
		t.Fatalf("per-descriptor inspection cost=%d", docker.calls)
	}
	for _, sk := range source.Values {
		if sk.Owner.Status != "verified_container" || sk.Owner.ContainerID != id || sk.Owner.StartTicks != "99" || sk.Owner.ContainerStartedAt != "instance-A" {
			t.Fatalf("owner=%+v", sk.Owner)
		}
	}
	tests := []struct {
		name   string
		mutate func(*Native, *identityDocker)
	}{
		{"reused descriptor", func(n *Native, _ *identityDocker) {
			n.linkReader = func(string) (string, error) { return "socket:[999]", nil }
		}},
		{"PID replacement", func(n *Native, _ *identityDocker) {
			reads := 0
			n.procReader = func(_ int, leaf string) (string, error) {
				if leaf == "stat" {
					reads++
					return procStat(fmt.Sprint(reads)), nil
				}
				return "0::/docker/" + id, nil
			}
		}},
		{"container restart", func(_ *Native, d *identityDocker) { d.fail = true }},
		{"wrong cgroup", func(n *Native, _ *identityDocker) {
			n.procReader = func(_ int, leaf string) (string, error) {
				if leaf == "stat" {
					return procStat("99"), nil
				}
				return "0::/docker/" + strings.Repeat("b", 64), nil
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			n := native
			d := *docker
			d.fail = false
			n.Docker = &d
			test.mutate(&n, &d)
			src := parseSS(`tcp ESTAB 0 0 127.0.0.1:123 192.0.2.1:443 users:(("worker",pid=111,fd=3)) ino:777 sk:abcd`, 1)
			n.attribute(ctx, &src, known, map[string]bool{})
			if src.Values[0].Owner.ContainerID != "" || src.Values[0].Owner.Status == "verified_container" {
				t.Fatalf("misattributed=%+v", src.Values[0].Owner)
			}
		})
	}
	shared := parseSS(`tcp ESTAB 0 0 127.0.0.1:123 192.0.2.1:443 users:(("worker",pid=111,fd=3),("other",pid=112,fd=4)) ino:777 sk:abcd`, 1)
	native.attribute(ctx, &shared, known, map[string]bool{})
	if shared.Values[0].Owner.Status != "unknown" {
		t.Fatal("shared descriptor invented a unique owner")
	}
}
func TestCommandOutputCapAndCancellationAreObserved(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	out, capped, err := readCommand(ctx, exec.CommandContext(ctx, "head", "-c", fmt.Sprint(MaxOutputBytes+512), "/dev/zero"))
	if err != nil || !capped || len(out) > MaxOutputBytes {
		t.Fatalf("cap=%v bytes=%d err=%v", capped, len(out), err)
	}
	ctx2, cancel2 := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel2()
	at := time.Now()
	_, _, err = readCommand(ctx2, exec.CommandContext(ctx2, "sleep", "10"))
	if err == nil || time.Since(at) > time.Second {
		t.Fatalf("cancel=%v elapsed=%s", err, time.Since(at))
	}
}
func BenchmarkNativeSnapshotParseAndDelta1024(b *testing.B) {
	var raw strings.Builder
	for i := 0; i < 1024; i++ {
		fmt.Fprintf(&raw, "tcp ESTAB 0 0 127.0.0.1:%d 192.0.2.1:443 ino:%d sk:%x bytes_sent:1048576 bytes_received:262144 retrans:0/2\n", 10000+i, 1000+i, 1000+i)
	}
	text := raw.String()
	at := time.Now().UTC().Truncate(time.Hour).Add(time.Minute)
	b.ReportAllocs()
	b.SetBytes(int64(len(text)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		src := parseSS(text, 1024)
		src.ID, src.Namespace, src.ObservedAt = "host", "1:2", at
		c := Cycle{BootID: "bench", Sources: []Source{src}}
		d := differencer{}
		d.observe(&c, 30*time.Second)
		c.Sources[0].ObservedAt = at.Add(30 * time.Second)
		d.observe(&c, 30*time.Second)
	}
}
