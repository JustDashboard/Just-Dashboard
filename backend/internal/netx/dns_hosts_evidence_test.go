package netx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreviewHostRecordsNamesOverlaps(t *testing.T) {
	s := testService(t)
	file := "127.0.0.1 localhost\n192.0.2.50 nas.lan\n# BEGIN Just Dashboard\n192.0.2.10 nas.lan nas\n# END Just Dashboard\n10.0.4.9 grafana.lan\n192.0.2.77 printer.lan\n"
	if err := os.WriteFile(s.paths.Hosts, []byte(file), 0o644); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewHostRecords(context.Background(), []HostRecord{
		{Address: "192.0.2.10", Names: []string{"NAS.lan", "nas"}},
		{Address: "192.0.2.11", Names: []string{"nas"}},
		{Address: "2001:db8::10", Names: []string{"nas"}},
		{Address: "10.0.4.20", Names: []string{"grafana.lan"}},
		{Address: "192.0.2.77", Names: []string{"printer.lan"}},
		{Address: "192.0.2.10", Names: []string{"nas.lan"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]HostRecordIssue{}
	for _, issue := range preview.Issues {
		kinds[issue.Kind+" "+issue.Name] = issue
	}
	if c, ok := kinds["conflict nas"]; !ok || c.Record != 1 || c.Other != "192.0.2.10" {
		t.Fatalf("conflict = %+v (%v)", c, preview.Issues)
	}
	if _, ok := kinds["conflict nas"]; ok && strings.Contains(kinds["conflict nas"].Detail, "2001:db8") {
		t.Fatal("an IPv6 address of the same name is not a conflict")
	}
	if d, ok := kinds["duplicate nas.lan"]; !ok || d.Record != 5 {
		t.Fatalf("duplicate = %+v", d)
	}
	if sh, ok := kinds["shadowed nas.lan"]; !ok || sh.Line != 2 || sh.Other != "192.0.2.50" || !strings.Contains(sh.Detail, "before the block") {
		t.Fatalf("shadowed = %+v", sh)
	}
	if ov, ok := kinds["overrides grafana.lan"]; !ok || ov.Line != 6 || !strings.Contains(ov.Detail, "block's address is returned first") {
		t.Fatalf("overrides = %+v", ov)
	}
	if rp, ok := kinds["repeated printer.lan"]; !ok || rp.Line != 7 {
		t.Fatalf("repeated = %+v", rp)
	}
	if len(preview.Block) != 8 || preview.Block[0] != hostsBegin || preview.Block[1] != "192.0.2.10 nas.lan nas" || len(preview.Added) != 5 || len(preview.Removed) != 0 {
		t.Fatalf("preview = %+v", preview)
	}
	// A preview writes nothing, and refuses what a save would refuse.
	if after, _ := os.ReadFile(s.paths.Hosts); string(after) != file {
		t.Fatalf("preview wrote the file: %q", after)
	}
	if _, err := s.PreviewHostRecords(context.Background(), []HostRecord{{Address: "192.0.2.1", Names: []string{"localhost"}}}); err == nil {
		t.Fatal("localhost off loopback was previewed")
	}
	if err := os.WriteFile(s.paths.Hosts, []byte(hostsBegin+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreviewHostRecords(context.Background(), nil); err == nil {
		t.Fatal("a file with untrusted markers was previewed")
	}
}

func TestHostResolutionAsksTheHostsNSS(t *testing.T) {
	record(t)
	if err := os.MkdirAll(filepath.Join(dnsOwnerRoot, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dnsOwnerRoot, "etc", "nsswitch.conf"), []byte("passwd: files\nhosts:          files mdns4_minimal [NOTFOUND=return] dns\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := testService(t)
	if err := os.WriteFile(s.paths.Hosts, []byte("192.0.2.50 nas.lan\n"+hostsBegin+"\n192.0.2.10 nas.lan nas\n2001:db8::10 nas\n10.0.4.20 grafana.lan\n10.0.4.21 gone.lan\n10.0.4.22 broken.lan\n"+hostsEnd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	asked := []string{}
	prev := dnsHostsExecutor
	dnsHostsExecutor = func(_ context.Context, name string, args ...string) (string, error) {
		asked = append(asked, name+" "+strings.Join(args, " "))
		switch strings.Join(args, " ") {
		case "ahostsv4 nas.lan":
			return "192.0.2.50      STREAM nas.lan\n192.0.2.50      DGRAM\n192.0.2.10      STREAM\n", nil
		case "ahostsv4 nas":
			return "192.0.2.10      STREAM nas\n", nil
		case "ahostsv6 nas":
			return "2001:db8::10    STREAM nas\n", nil
		case "ahostsv6 nas.lan":
			return "::ffff:192.0.2.50 STREAM nas.lan\n", nil
		case "ahostsv4 grafana.lan":
			return "10.0.4.99       STREAM grafana.lan\n", nil
		case "ahostsv4 broken.lan":
			return "getent: something failed\n", errors.New("native DNS command failed")
		}
		return "", errors.New("native DNS command failed: ")
	}
	t.Cleanup(func() { dnsHostsExecutor = prev })
	got, err := s.HostResolution(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]HostNameResolution{}
	for _, n := range got.Names {
		states[n.Name] = n
	}
	if n := states["nas"]; n.State != "matches" || n.IPv4[0] != "192.0.2.10" || n.IPv6[0] != "2001:db8::10" {
		t.Fatalf("nas = %+v", n)
	}
	if n := states["nas.lan"]; n.State != "includes" || len(n.IPv6) != 0 || n.IPv4[0] != "192.0.2.50" {
		t.Fatalf("nas.lan = %+v", n)
	}
	if states["grafana.lan"].State != "differs" || states["gone.lan"].State != "unresolved" || states["broken.lan"].State != "unknown" {
		t.Fatalf("states = %+v", states)
	}
	if got.Hosts != "files mdns4_minimal [NOTFOUND=return] dns" || len(asked) != 10 || got.Omitted != 0 {
		t.Fatalf("evidence = %+v asked %v", got, asked)
	}
	for _, line := range asked {
		if !strings.HasPrefix(line, "getent ahostsv") {
			t.Fatalf("ran %q", line)
		}
	}
}
