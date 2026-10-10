package netsec

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestSourceDiagnosticFixture(t *testing.T) {
	if os.Getenv("JD_SOURCE_DIAGNOSTIC_FIXTURE") != "1" {
		return
	}
	fmt.Print(os.Getenv("JD_SOURCE_DIAGNOSTIC_OUTPUT"))
	if os.Getenv("JD_SOURCE_DIAGNOSTIC_FAIL") == "1" {
		os.Exit(127)
	}
	os.Exit(0)
}

func fixtureSourceCommand(t *testing.T, output string, fail bool, record func(string, []string)) SourceCommand {
	t.Helper()
	return func(ctx context.Context, tool string, args ...string) (*exec.Cmd, func(), error) {
		if record != nil {
			record(tool, args)
		}
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestSourceDiagnosticFixture$")
		cmd.Env = append(os.Environ(), "JD_SOURCE_DIAGNOSTIC_FIXTURE=1", "JD_SOURCE_DIAGNOSTIC_OUTPUT="+output)
		if fail {
			cmd.Env = append(cmd.Env, "JD_SOURCE_DIAGNOSTIC_FAIL=1")
		}
		return cmd, func() {}, nil
	}
}

func TestSourceDNSNativeNegativeDoesNotTryPublicFallback(t *testing.T) {
	var calls int
	result, err := SourceDNS(context.Background(), fixtureSourceCommand(t, ";; ->>HEADER<<- opcode: QUERY, status: NXDOMAIN, id: 1", false, func(tool string, args []string) {
		calls++
		if tool != "dig" || !reflect.DeepEqual(args, []string{"@127.0.0.11", "+time=2", "+tries=1", "+noall", "+answer", "+comments", "private.corp.", "A"}) {
			t.Fatalf("leaked name or wrong native scope: %s %v", tool, args)
		}
	}), "private.corp", "inet", []string{"127.0.0.11", "8.8.8.8"})
	if err != nil || calls != 1 || result.OK || len(result.Records) != 0 {
		t.Fatalf("negative answer triggered fallback %#v %v calls=%d", result, err, calls)
	}
}

func TestSourceDNSMissingNativeServerDoesNotRun(t *testing.T) {
	result, err := SourceDNS(context.Background(), func(context.Context, string, ...string) (*exec.Cmd, func(), error) {
		t.Fatal("query sent without native server")
		return nil, nil, nil
	}, "private.corp", "inet", nil)
	if err != nil || result.Error == "" {
		t.Fatalf("missing native scope hidden %#v %v", result, err)
	}
}

func TestSourceDNSKeepsOnlySelectedFamilyAnswers(t *testing.T) {
	r, err := SourceDNS(context.Background(), fixtureSourceCommand(t, ";; status: NOERROR\nprivate.corp. 30 IN AAAA 2001:db8::8\nprivate.corp. 30 IN A 192.0.2.8", false, nil), "private.corp", "inet6", []string{"::1"})
	if err != nil || !reflect.DeepEqual(r.Records, []string{"2001:db8::8"}) {
		t.Fatalf("wrong family %#v %v", r, err)
	}
}

func TestSourceTCPMissingToolIsUnknownAdapter(t *testing.T) {
	r, err := SourcePortCheck(context.Background(), fixtureSourceCommand(t, "nsenter: failed to execute nc: No such file or directory", true, nil), "192.0.2.8", 443, "192.0.2.2")
	if err == nil || r == nil || r.OK {
		t.Fatalf("missing binary reported as measured failure %#v %v", r, err)
	}
}

func TestSourceTCPUsesLiteralPinnedTuple(t *testing.T) {
	_, err := SourcePortCheck(context.Background(), fixtureSourceCommand(t, "", false, func(tool string, args []string) {
		if tool != "nc" || !reflect.DeepEqual(args, []string{"-z", "-w", "6", "-6", "-s", "2001:db8::2", "2001:db8::8", "443"}) {
			t.Fatalf("wrong literal tuple %s %v", tool, args)
		}
	}), "2001:db8::8", 443, "2001:db8::2")
	if err != nil {
		t.Fatal(err)
	}
}

func TestHostTCPProbeBindsSelectedSource(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	peer := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			peer <- conn.RemoteAddr().String()
			conn.Close()
		}
	}()
	result, err := (&Service{}).PortCheckFromSource(context.Background(), "127.0.0.1", listener.Addr().(*net.TCPAddr).Port, "127.0.0.2")
	if err != nil || !result.OK || !strings.HasPrefix(<-peer, "127.0.0.2:") {
		t.Fatalf("selected source not used %#v %v", result, err)
	}
}
