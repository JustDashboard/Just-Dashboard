package proxysvc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// servingNginx starts liveNginx's private instance with one site on a free
// loopback port, for traffic to count, and returns that site's address. It
// runs in the foreground until the test ends.
func servingNginx(t *testing.T, root string) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	site := fmt.Sprintf("server { listen 127.0.0.1:%d; location / { return 200 \"site\"; } }\n", port)
	if err := os.WriteFile(filepath.Join(root, "sites-enabled", "site"), []byte(site), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command := exec.Command(filepath.Join(root, "bin", "nginx"), "-g", "daemon off;")
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Signal(syscall.SIGTERM)
		_ = command.Wait()
		if t.Failed() {
			log, _ := os.ReadFile(filepath.Join(root, "logs", "error.log"))
			t.Logf("nginx output:\n%s\nerror log:\n%s", output.String(), log)
		}
	})
	address := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := liveGet(address + "/"); err == nil {
			return address
		}
		if time.Now().After(deadline) {
			t.Fatal("the private nginx did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func liveGet(url string) (int, error) {
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	response, err := client.Get(url)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode, nil
}

// The whole switch against real nginx: the file it writes passes nginx's own
// test, nginx loads it from conf.d, the reloaded server answers with a page
// the parser reads, the counters move with real requests while the sampler's
// own stay out of them, and switching off takes the server away again.
func TestLiveStatusServer(t *testing.T) {
	root := liveNginx(t)
	site := servingNginx(t, root)
	service := New(root, "")
	sampler := NewStatusSampler(service)

	change, err := sampler.Enable(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !change.Changed || !change.Reloaded || !change.Validation.Valid {
		t.Fatalf("change %+v", change)
	}
	endpoint := statusEndpoint(change.Port)
	first := sampler.Report(0, 0)
	if !first.Enabled || first.Endpoint != endpoint || first.Current == nil || first.Error != "" {
		t.Fatalf("switched on, the report says %+v", first)
	}
	if first.Current.Active != 0 || first.Current.Writing != 0 {
		t.Fatalf("an idle nginx reads %+v: the sampler's own connection was counted", *first.Current)
	}

	for range 20 {
		if status, err := liveGet(site + "/"); err != nil || status != http.StatusOK {
			t.Fatalf("site answered %d, %v", status, err)
		}
	}
	// A second apart, so the rate is over a gap the clock can see.
	time.Sleep(time.Second)
	if err := sampler.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	report := sampler.Report(0, 0)
	if report.HourRequests != 20 || report.Current == nil || report.Current.Requests == nil || *report.Current.Requests <= 0 {
		t.Fatalf("20 requests read as %d in the hour, current %+v", report.HourRequests, report.Current)
	}
	if report.Totals == nil || report.Totals.Accepts != report.Totals.Handled {
		t.Fatalf("totals %+v", report.Totals)
	}

	// Nothing but the status page.
	if status, err := liveGet(fmt.Sprintf("http://127.0.0.1:%d/", change.Port)); err != nil || status != http.StatusNotFound {
		t.Fatalf("the status server's other paths answered %d, %v", status, err)
	}

	off, err := sampler.Disable(context.Background())
	if err != nil || !off.Changed || !off.Reloaded {
		t.Fatalf("switching off gave %+v, %v", off, err)
	}
	if _, err := os.Stat(filepath.Join(root, "conf.d", statusFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the status file is still there: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := liveGet(endpoint); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the status server still answers after switching off")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if report := sampler.Report(0, 0); report.Enabled || len(report.Samples) != 0 {
		t.Fatalf("switched off, the report says %+v", report)
	}
	if status, err := liveGet(site + "/"); err != nil || status != http.StatusOK {
		t.Fatalf("the site stopped answering: %d, %v", status, err)
	}
}

// An nginx.conf that does not include conf.d passes its test with the file
// in place, because nginx never reads it. nginx's own dump is what tells.
func TestLiveStatusServerRefusesAConfDNginxDoesNotRead(t *testing.T) {
	root := liveNginx(t)
	config := filepath.Join(root, "nginx.conf")
	raw, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(strings.Replace(string(raw), "include conf.d/*.conf;", "", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	service := New(root, "")
	_, err = service.EnableStatusServer(context.Background(), func(context.Context, string) error {
		t.Error("verified a status server nginx does not load")
		return nil
	})
	if !errors.Is(err, ErrStatusNotIncluded) {
		t.Fatalf("error %v", err)
	}
	if !strings.Contains(err.Error(), "conf.d/*.conf") {
		t.Fatalf("error %q does not say what nginx.conf lacks", err)
	}
	if _, err := os.Stat(filepath.Join(root, "conf.d", statusFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the status file was left behind: %v", err)
	}
}
