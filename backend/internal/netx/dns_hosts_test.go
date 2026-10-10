package netx

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A hosts file the way a cloud image leaves it, deliberately awkward: CRLF
// endings on one line, tabs, trailing comments, a name with odd spacing, and
// no newline after the last line.
const awkwardHosts = "127.0.0.1 localhost\r\n" +
	"# the provider wrote this\n" +
	"::1\tlocalhost   ip6-localhost  # loopback\n" +
	"10.0.0.5\tbuild.internal\n" +
	"\n" +
	"192.0.2.9 last.example"

func writeHosts(t *testing.T, s *Service, content string) {
	t.Helper()
	if err := os.WriteFile(s.paths.Hosts, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readHosts(t *testing.T, s *Service) string {
	t.Helper()
	b, err := os.ReadFile(s.paths.Hosts)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSetHostRecordsKeepsEveryOtherByte(t *testing.T) {
	s := testService(t)
	writeHosts(t, s, awkwardHosts)

	first := []HostRecord{{Address: "192.0.2.10", Names: []string{"app.internal", "Git.Internal"}}, {Address: "2001:db8::10", Names: []string{"app6.internal"}}}
	if _, err := s.SetHostRecords(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	got := readHosts(t, s)
	// The original has no final newline and uses CRLF once, so the block
	// follows on a new CRLF line and every original byte is where it was.
	if !strings.HasPrefix(got, awkwardHosts+"\r\n") {
		t.Fatalf("the original bytes changed:\n%q", got)
	}
	block := got[len(awkwardHosts)+2:]
	if block != "# BEGIN Just Dashboard\r\n192.0.2.10 app.internal git.internal\r\n2001:db8::10 app6.internal\r\n# END Just Dashboard\r\n" {
		t.Fatalf("block = %q", block)
	}

	// A second write replaces the block and carries what comes after it.
	tail := "198.51.100.1 after.example"
	writeHosts(t, s, got+tail)
	second := []HostRecord{{Address: "192.0.2.77", Names: []string{"other.internal"}}}
	if _, err := s.SetHostRecords(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	got2 := readHosts(t, s)
	want := awkwardHosts + "\r\n# BEGIN Just Dashboard\r\n192.0.2.77 other.internal\r\n# END Just Dashboard\r\n" + tail
	if got2 != want {
		t.Fatalf("after the second write:\n%q\nwant\n%q", got2, want)
	}

	// Emptying the list removes the block and nothing else.
	if _, err := s.SetHostRecords(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if got3 := readHosts(t, s); got3 != awkwardHosts+"\r\n"+tail {
		t.Fatalf("after removing the block:\n%q", got3)
	}
}

func TestSetHostRecordsRoundTripIsExactOnATidyFile(t *testing.T) {
	s := testService(t)
	original := "127.0.0.1 localhost\n::1 localhost ip6-localhost\n# keep me\n"
	writeHosts(t, s, original)
	recs := []HostRecord{{Address: "10.1.1.1", Names: []string{"a.internal"}}}
	if _, err := s.SetHostRecords(context.Background(), recs); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetHostRecords(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if got := readHosts(t, s); got != original {
		t.Fatalf("set then remove changed the file:\n%q", got)
	}
}

// A block that ends the file without a newline keeps ending it that way.
func TestSetHostRecordsKeepsAMissingFinalNewline(t *testing.T) {
	s := testService(t)
	writeHosts(t, s, "127.0.0.1 localhost\n# BEGIN Just Dashboard\n10.0.0.1 old.internal\n# END Just Dashboard")
	if _, err := s.SetHostRecords(context.Background(), []HostRecord{{Address: "10.0.0.2", Names: []string{"new.internal"}}}); err != nil {
		t.Fatal(err)
	}
	want := "127.0.0.1 localhost\n# BEGIN Just Dashboard\n10.0.0.2 new.internal\n# END Just Dashboard"
	if got := readHosts(t, s); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestSetHostRecordsRefusesACorruptFile(t *testing.T) {
	for name, content := range map[string]string{
		"two blocks":     "# BEGIN Just Dashboard\n10.0.0.1 a.internal\n# END Just Dashboard\n127.0.0.1 localhost\n# BEGIN Just Dashboard\n10.0.0.2 b.internal\n# END Just Dashboard\n",
		"two beginnings": "# BEGIN Just Dashboard\n# BEGIN Just Dashboard\n10.0.0.1 a.internal\n# END Just Dashboard\n",
		"no end":         "127.0.0.1 localhost\n# BEGIN Just Dashboard\n10.0.0.1 a.internal\n",
		"no beginning":   "127.0.0.1 localhost\n10.0.0.1 a.internal\n# END Just Dashboard\n",
		"backwards":      "# END Just Dashboard\n10.0.0.1 a.internal\n# BEGIN Just Dashboard\n",
	} {
		t.Run(name, func(t *testing.T) {
			s := testService(t)
			writeHosts(t, s, content)
			_, err := s.SetHostRecords(context.Background(), []HostRecord{{Address: "10.0.0.9", Names: []string{"x.internal"}}})
			if err == nil || !strings.Contains(err.Error(), "Just Dashboard") {
				t.Fatalf("error = %v", err)
			}
			if got := readHosts(t, s); got != content {
				t.Fatalf("a refused edit changed the file:\n%q", got)
			}
			view, err := s.HostRecords(context.Background())
			if err != nil || view.Problem == "" {
				t.Fatalf("view = %+v, %v: reading should still work and say why editing will not", view, err)
			}
		})
	}
}

func TestHostRecordsSplitsManagedFromTheRest(t *testing.T) {
	s := testService(t)
	writeHosts(t, s, "127.0.0.1 localhost # here\n# a comment\n# BEGIN Just Dashboard\n10.0.0.1 a.internal b.internal\n# END Just Dashboard\n::1 ip6-localhost\n")
	v, err := s.HostRecords(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Managed) != 1 || v.Managed[0].Address != "10.0.0.1" || len(v.Managed[0].Names) != 2 {
		t.Fatalf("managed = %+v", v.Managed)
	}
	if len(v.Other) != 2 || v.Other[0].Line != 1 || v.Other[0].Names[0] != "localhost" || v.Other[1].Line != 6 {
		t.Fatalf("other = %+v", v.Other)
	}
	if v.Problem != "" {
		t.Fatalf("problem = %q", v.Problem)
	}
}

func TestSetHostRecordsKeepsTheFileMode(t *testing.T) {
	s := testService(t)
	writeHosts(t, s, "127.0.0.1 localhost\n")
	if err := os.Chmod(s.paths.Hosts, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetHostRecords(context.Background(), []HostRecord{{Address: "10.0.0.1", Names: []string{"a.internal"}}}); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(s.paths.Hosts); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", st.Mode().Perm())
	}
}

func TestSetHostRecordsCreatesAMissingFile(t *testing.T) {
	s := testService(t)
	if _, err := s.SetHostRecords(context.Background(), []HostRecord{{Address: "10.0.0.1", Names: []string{"a.internal"}}}); err != nil {
		t.Fatal(err)
	}
	if got := readHosts(t, s); got != "# BEGIN Just Dashboard\n10.0.0.1 a.internal\n# END Just Dashboard\n" {
		t.Fatalf("got %q", got)
	}
	empty := testService(t)
	if _, err := empty.SetHostRecords(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(empty.paths.Hosts); err == nil {
		t.Fatal("emptying records created a file")
	}
}

// Writing through a link keeps the link, which is how some distributions point
// /etc/hosts at a file elsewhere.
func TestSetHostRecordsWritesThroughASymlink(t *testing.T) {
	s := testService(t)
	real := filepath.Join(t.TempDir(), "real-hosts")
	if err := os.WriteFile(real, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, s.paths.Hosts); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetHostRecords(context.Background(), []HostRecord{{Address: "10.0.0.1", Names: []string{"a.internal"}}}); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(s.paths.Hosts); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v", err)
	}
	if b, _ := os.ReadFile(real); !bytes.Contains(b, []byte("10.0.0.1 a.internal")) {
		t.Fatalf("target = %q", b)
	}
}

func TestCleanHostRecords(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", 63)
	tests := []struct {
		name    string
		rec     HostRecord
		wantErr string
	}{
		{"ok", HostRecord{"192.0.2.1", []string{"app.internal", "app"}}, ""},
		{"v6", HostRecord{"2001:db8::1", []string{"app.internal"}}, ""},
		{"longest label", HostRecord{"192.0.2.1", []string{long + ".example"}}, ""},
		{"bad address", HostRecord{"192.0.2.999", []string{"app"}}, "not an IP address"},
		{"name for an address", HostRecord{"app.internal", []string{"app"}}, "not an IP address"},
		{"zone", HostRecord{"fe80::1%eth0", []string{"app"}}, "zone"},
		{"no names", HostRecord{"192.0.2.1", nil}, "at least one name"},
		{"space in name", HostRecord{"192.0.2.1", []string{"app internal"}}, "letters, digits"},
		{"comment in name", HostRecord{"192.0.2.1", []string{"app#internal"}}, "letters, digits"},
		{"newline in name", HostRecord{"192.0.2.1", []string{"app\n10.0.0.1 evil"}}, "letters, digits"},
		{"tab in name", HostRecord{"192.0.2.1", []string{"app\tevil"}}, "letters, digits"},
		{"underscore", HostRecord{"192.0.2.1", []string{"my_host"}}, "letters, digits"},
		{"leading hyphen", HostRecord{"192.0.2.1", []string{"-app"}}, "hyphen"},
		{"trailing hyphen label", HostRecord{"192.0.2.1", []string{"app-.internal"}}, "hyphen"},
		{"label too long", HostRecord{"192.0.2.1", []string{long + "a.example"}}, "63"},
		{"name too long", HostRecord{"192.0.2.1", []string{strings.Repeat(long+".", 4) + "com"}}, "253"},
		{"empty label", HostRecord{"192.0.2.1", []string{"a..b"}}, "empty label"},
		{"trailing dot", HostRecord{"192.0.2.1", []string{"app.internal."}}, "trailing dot"},
		{"non-ascii", HostRecord{"192.0.2.1", []string{"bücher.example"}}, "punycode"},
		{"localhost elsewhere", HostRecord{"192.0.2.1", []string{"localhost"}}, "loopback"},
		{"localhost on loopback", HostRecord{"127.0.0.2", []string{"localhost"}}, ""},
		{"too many names", HostRecord{"192.0.2.1", strings.Fields(strings.Repeat("a ", 17))}, "at most 16"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := cleanHostRecords([]HostRecord{tc.rec})
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
	many := make([]HostRecord, 501)
	for i := range many {
		many[i] = HostRecord{"192.0.2.1", []string{"a"}}
	}
	if _, err := cleanHostRecords(many); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("501 records: %v", err)
	}
	if _, err := cleanHostRecords(many[:500]); err != nil {
		t.Fatalf("500 records: %v", err)
	}
	got, _ := cleanHostRecords([]HostRecord{{"2001:0db8:0:0:0:0:0:1", []string{"A.Internal"}}})
	if got[0].Address != "2001:db8::1" || got[0].Names[0] != "a.internal" {
		t.Fatalf("normalised = %+v", got)
	}
}
