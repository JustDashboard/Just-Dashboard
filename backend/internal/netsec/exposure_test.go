package netsec

import (
	"encoding/json"
	"net"
	"testing"
)

func cidrs(t *testing.T, list ...string) []*net.IPNet {
	t.Helper()
	out := make([]*net.IPNet, 0, len(list))
	for _, s := range list {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			t.Fatalf("bad test CIDR %q: %v", s, err)
		}
		out = append(out, n)
	}
	return out
}

func TestGrades(t *testing.T) {
	cases := []struct {
		name  string
		allow []string
		want  string
	}{
		{"loopback only", []string{"127.0.0.1/32", "::1/128"}, "tunnel"},
		{"tailnet", []string{"100.64.0.0/10"}, "tailscale"},
		{"one tailnet host", []string{"100.101.102.103/32"}, "tailscale"},
		{"wireguard subnet", []string{"10.8.0.0/24"}, "private"},
		{"rfc1918 192", []string{"192.168.1.0/24"}, "private"},
		{"a public host", []string{"203.0.113.7/32"}, "public"},
		{"everything v4", []string{"0.0.0.0/0"}, "open"},
		{"everything v6", []string{"::/0"}, "open"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DescribeExposure(cidrs(t, c.allow...))
			if got.Grade != c.want {
				t.Fatalf("grade = %q, want %q (summary: %s)", got.Grade, c.want, got.Summary)
			}
		})
	}
}

func TestTheWeakestEntryDecides(t *testing.T) {
	// A tidy private list with one public range in it is a public dashboard.
	got := DescribeExposure(cidrs(t, "127.0.0.1/32", "100.64.0.0/10", "203.0.113.7/32"))
	if got.Grade != "public" {
		t.Fatalf("grade = %q, want public — one public entry must decide", got.Grade)
	}
	if got.Recommendation == "" {
		t.Fatal("a public dashboard should carry a recommendation")
	}
}

func TestOpenBeatsEverything(t *testing.T) {
	got := DescribeExposure(cidrs(t, "100.64.0.0/10", "0.0.0.0/0"))
	if got.Grade != "open" {
		t.Fatalf("grade = %q, want open", got.Grade)
	}
}

func TestPrivateGradesCarryNoAlarm(t *testing.T) {
	for _, list := range [][]string{{"100.64.0.0/10"}, {"10.8.0.0/24"}} {
		got := DescribeExposure(cidrs(t, list...))
		if got.Recommendation != "" && got.Grade == "tailscale" {
			t.Fatalf("a tailnet dashboard should not nag: %q", got.Recommendation)
		}
		if got.Summary == "" {
			t.Fatal("every grade needs a summary")
		}
	}
}

func TestAllowlistIsReportedVerbatim(t *testing.T) {
	got := DescribeExposure(cidrs(t, "10.8.0.0/24", "127.0.0.1/32"))
	if len(got.Allowlist) != 2 {
		t.Fatalf("allowlist = %v, want two entries", got.Allowlist)
	}
}

// A host with no tunnel device must still send an array: the dashboard reads
// .length on it, and JSON null threw on the Overview page.
func TestNoTunnelDevicesIsAnEmptyList(t *testing.T) {
	names, _ := privateInterfacesFrom([]net.Interface{{Name: "eth0", Flags: net.FlagUp}})
	raw, err := json.Marshal(names)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "[]" {
		t.Fatalf("interfaces serialised as %s, want []", raw)
	}
}

func TestEmptyAllowlistIsTreatedAsLoopback(t *testing.T) {
	got := DescribeExposure(nil)
	if got.Grade != "tunnel" {
		t.Fatalf("grade = %q, want tunnel for an empty list", got.Grade)
	}
}

// The proxy binds loopback in every configuration so that an SSH tunnel is
// always available, which means every allowlist install.sh writes now carries
// 127.0.0.1/32 and ::1/128 alongside whatever else was chosen. Loopback must
// stay the one entry that cannot make a grade worse — otherwise the Security
// page would raise an alarm on a perfectly ordinary Tailscale install.
func TestLoopbackNeverWorsensAGrade(t *testing.T) {
	loopback := []string{"127.0.0.1/32", "::1/128"}
	for _, c := range []struct {
		name string
		with []string
		want string
	}{
		{"tailscale install", []string{"100.64.0.0/10"}, "tailscale"},
		{"wireguard install", []string{"10.8.0.0/24"}, "private"},
		{"public install", []string{"203.0.113.7/32"}, "public"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := DescribeExposure(cidrs(t, append(c.with, loopback...)...))
			if got.Grade != c.want {
				t.Fatalf("grade = %q, want %q — loopback must not change it", got.Grade, c.want)
			}
		})
	}
}
