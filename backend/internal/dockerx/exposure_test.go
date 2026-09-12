package dockerx

import "testing"

func TestDescribePortSeparatesLoopbackFromEveryInterface(t *testing.T) {
	cases := []struct {
		name  string
		ip    string
		host  int
		want  PortScope
		label string
	}{
		{"loopback v4", "127.0.0.1", 3000, ScopeLoopback, "This server only"},
		{"loopback v6", "::1", 3000, ScopeLoopback, "This server only"},
		{"every interface, explicit", "0.0.0.0", 443, ScopeAll, "Every interface"},
		{"every interface, v6", "::", 443, ScopeAll, "Every interface"},
		{"every interface, empty", "", 443, ScopeAll, "Every interface"},
		{"one address", "10.0.0.5", 5432, ScopePrivate, "One interface"},
		{"not published", "", 0, ScopeInternal, "Not published"},
	}
	for _, c := range cases {
		got := DescribePort(c.ip, c.host, 8080, "tcp")
		if got.Scope != c.want {
			t.Errorf("%s: scope = %q, want %q", c.name, got.Scope, c.want)
		}
		if got.Label != c.label {
			t.Errorf("%s: label = %q, want %q", c.name, got.Label, c.label)
		}
	}
}

func TestDescribePortDoesNotClaimInternetReachability(t *testing.T) {
	got := DescribePort("0.0.0.0", 5432, 5432, "tcp")
	if contains(got.Summary, "internet") {
		t.Errorf("a binding alone cannot know it is on the internet: %q", got.Summary)
	}
	if !contains(got.Summary, "depends on") {
		t.Errorf("the qualification is the point of the sentence: %q", got.Summary)
	}
}

func TestDescribePortMarksIPv6(t *testing.T) {
	if !DescribePort("::1", 3000, 3000, "tcp").IPv6 {
		t.Error("::1 is a v6 binding")
	}
	if DescribePort("127.0.0.1", 3000, 3000, "tcp").IPv6 {
		t.Error("127.0.0.1 is not a v6 binding")
	}
}

func TestPortURLOnlyWhereThereIsOneRightAnswer(t *testing.T) {
	if got := DescribePort("127.0.0.1", 3000, 3000, "tcp").URL(); got != "http://127.0.0.1:3000" {
		t.Errorf("loopback URL = %q", got)
	}
	if got := DescribePort("10.0.0.5", 8080, 80, "tcp").URL(); got != "http://10.0.0.5:8080" {
		t.Errorf("address URL = %q", got)
	}
	if got := DescribePort("::1", 3000, 3000, "tcp").URL(); got != "http://[::1]:3000" {
		t.Errorf("v6 URL must be bracketed, got %q", got)
	}
	if got := DescribePort("0.0.0.0", 443, 443, "tcp").URL(); got != "" {
		t.Errorf("every-interface has no single URL, got %q", got)
	}
	if got := DescribePort("", 0, 80, "tcp").URL(); got != "" {
		t.Errorf("an unpublished port has no URL, got %q", got)
	}
}

func TestDescribePortsPutsTheMostExposedFirst(t *testing.T) {
	got := DescribePorts([]Port{
		{IP: "127.0.0.1", PublicPort: 3000, PrivatePort: 3000, Type: "tcp"},
		{IP: "0.0.0.0", PublicPort: 443, PrivatePort: 443, Type: "tcp"},
		{PrivatePort: 9000, Type: "tcp"},
		{IP: "10.0.0.5", PublicPort: 5432, PrivatePort: 5432, Type: "tcp"},
	})
	want := []PortScope{ScopeAll, ScopePrivate, ScopeLoopback, ScopeInternal}
	for i, w := range want {
		if got[i].Scope != w {
			t.Fatalf("position %d = %q, want %q (%+v)", i, got[i].Scope, w, got)
		}
	}
}

func TestDescribePortsHandlesMultipleHostMappingsAndNoPorts(t *testing.T) {
	if got := DescribePorts(nil); len(got) != 0 {
		t.Errorf("no ports produces no rows, got %d", len(got))
	}
	got := DescribePorts([]Port{
		{IP: "0.0.0.0", PublicPort: 8080, PrivatePort: 80, Type: "tcp"},
		{IP: "::", PublicPort: 8080, PrivatePort: 80, Type: "tcp"},
	})
	if len(got) != 2 || !got[1].IPv6 {
		t.Errorf("both families are kept and distinguished: %+v", got)
	}
}

func TestPublicBindingsExcludesLoopbackAndUnpublished(t *testing.T) {
	got := PublicBindings([]Port{
		{IP: "127.0.0.1", PublicPort: 3000, PrivatePort: 3000, Type: "tcp"},
		{IP: "0.0.0.0", PublicPort: 443, PrivatePort: 443, Type: "tcp"},
		{PrivatePort: 9000, Type: "tcp"},
	})
	if len(got) != 1 || got[0].HostPort != 443 {
		t.Fatalf("only the public binding counts: %+v", got)
	}
}
