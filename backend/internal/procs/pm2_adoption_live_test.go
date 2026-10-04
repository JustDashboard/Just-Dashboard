package procs

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

func TestLiveExistingPM2CaptureAndManagerControls(t *testing.T) {
	if os.Getenv("JD_PM2_ADOPTION_LIVE") != "1" {
		t.Skip("set JD_PM2_ADOPTION_LIVE=1 to exercise a separately owned real PM2 daemon")
	}
	account, err := user.Current()
	if err != nil || account.Uid != strconv.Itoa(os.Getuid()) {
		t.Fatal("the fixture account could not be verified")
	}
	bin := findPM2Bin(account.HomeDir)
	if bin == "" {
		t.Fatal("the fixture account has no installed PM2 binary")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0755); err != nil {
		t.Fatal(err)
	}
	home := pm2Home{home: account.HomeDir, bin: bin, daemonDir: filepath.Join(root, ".pm2")}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	script := filepath.Join(source, "server.js")
	if err := os.WriteFile(script, []byte(`require("http").createServer((req,res)=>res.end(JSON.stringify({marker:"owned-pm2-fixture",token:process.env.PROOF_TOKEN,empty:process.env.PROOF_EMPTY,uid:process.getuid()}))).listen(Number(process.env.PORT),"127.0.0.1",()=>console.log("JD_PM2_FIXTURE_READY"))`), 0644); err != nil {
		t.Fatal(err)
	}
	environment := append(pm2Env(home), "PORT="+strconv.Itoa(port), "PROOF_TOKEN=owned-fixture-private-value", "PROOF_EMPTY=")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	run := func(ctx context.Context, args ...string) error {
		command := exec.CommandContext(ctx, bin, args...)
		command.Env = environment
		command.Dir = source
		if _, err := command.Output(); err != nil {
			return err
		}
		return nil
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := run(cleanup, "kill"); err != nil {
			t.Error("failed to stop the separately owned PM2 fixture daemon")
		}
	})
	name := "jd-adoption-owned-fixture"
	if err := run(ctx, "start", script, "--name", name, "--cwd", source); err != nil {
		t.Fatal("the separately owned PM2 fixture failed to start")
	}
	manager, err := NewPM2ForExistingDaemon(account.Username, home.daemonDir)
	if err != nil {
		t.Fatal(err)
	}
	assertServing := func() {
		t.Helper()
		until := time.Now().Add(10 * time.Second)
		for time.Now().Before(until) {
			client := &http.Client{Timeout: time.Second}
			response, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port))
			if err == nil {
				var body struct {
					Marker string `json:"marker"`
					Token  string `json:"token"`
					Empty  string `json:"empty"`
					UID    int    `json:"uid"`
				}
				err = json.NewDecoder(response.Body).Decode(&body)
				_ = response.Body.Close()
				if err == nil && body.Marker == "owned-pm2-fixture" && body.Token == "owned-fixture-private-value" && body.Empty == "" && body.UID == os.Getuid() {
					return
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("the owned fixture did not serve its expected environment and account")
	}
	assertServing()
	// Valid saved entries for another application must remain untouched and
	// must not prevent the exact owned application's lifecycle operations.
	for _, filename := range []string{"dump.pm2", "dump.pm2.bak"} {
		if err := os.WriteFile(filepath.Join(home.daemonDir, filename), []byte(`[{"name":"another-owned-app","namespace":"default"}]`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	capture, err := manager.CaptureExisting(ctx, account.Username, "default", name)
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Blockers) > 0 {
		t.Fatalf("a standard fork-mode Node app was blocked: %v", capture.Blockers)
	}
	if len(capture.Processes) != 1 || capture.UID != uint32(os.Getuid()) || capture.Environment["PROOF_TOKEN"] != "owned-fixture-private-value" {
		t.Fatal("capture did not preserve the original runtime account/environment")
	}
	rows, err := manager.ListExisting(ctx)
	if err != nil || len(rows) != 1 || rows[0].PID != int(capture.Processes[0].PID) || !strings.HasPrefix(rows[0].OutLogPath, home.daemonDir+string(filepath.Separator)) {
		t.Fatal("the owned fixture's exact log source was unavailable")
	}
	logs, err := os.ReadFile(rows[0].OutLogPath)
	if err != nil || !strings.Contains(string(logs), "JD_PM2_FIXTURE_READY") {
		t.Fatal("the owned fixture did not retain its native log output")
	}
	p, err := process.NewProcess(capture.Processes[0].PID)
	if err != nil {
		t.Fatal(err)
	}
	created, err := p.CreateTime()
	if err != nil || created != capture.Processes[0].CreateTime {
		t.Fatal("capture did not fence the authoritative PID creation time")
	}
	public, _ := json.Marshal(capture)
	if strings.Contains(string(public), "owned-fixture-private-value") {
		t.Fatal("public capture disclosed a private value")
	}
	if err := manager.ControlCaptured(ctx, capture, "default", "stop"); err != nil {
		t.Fatal(err)
	}
	stopped, err := manager.CaptureExisting(ctx, account.Username, "default", name)
	if err != nil || stopped.ConfigurationDigest != capture.ConfigurationDigest || stopped.Processes[0].State != "stopped" {
		if err != nil {
			t.Logf("stopped capture error: %v", err)
		} else {
			var before, after []map[string]json.RawMessage
			_ = json.Unmarshal(capture.OriginalConfig, &before)
			_ = json.Unmarshal(stopped.OriginalConfig, &after)
			for _, original := range before {
				for key, value := range original {
					if len(after) > 0 && string(value) != string(after[0][key]) {
						t.Logf("manager changed field: %s", key)
					}
				}
				for key := range after[0] {
					if _, ok := original[key]; !ok {
						t.Logf("manager added field: %s", key)
					}
				}
			}
		}
		t.Fatal("the original manager identity/configuration changed while stopping")
	}
	if err := manager.ControlCaptured(ctx, stopped, "default", "start"); err != nil {
		t.Fatal(err)
	}
	assertServing()
	restarted, err := manager.CaptureExisting(ctx, account.Username, "default", name)
	if err != nil || restarted.ConfigurationDigest != capture.ConfigurationDigest {
		t.Fatal("the original manager configuration was not retained across restoration")
	}
	for _, filename := range []string{"dump.pm2", "dump.pm2.bak"} {
		path := filepath.Join(home.daemonDir, filename)
		if err := os.WriteFile(path, []byte(`[{"name":"`+name+`","namespace":"default","pm_id":987,"TOKEN":"owned-saved-private-value"}]`), 0600); err != nil {
			t.Fatal(err)
		}
		saved, err := manager.CaptureExisting(ctx, account.Username, "default", name)
		if err != nil || len(saved.Blockers) == 0 || saved.ConfigurationDigest == restarted.ConfigurationDigest {
			t.Fatal("real saved startup authority was not blocked and fenced")
		}
		if err := manager.ControlCaptured(ctx, restarted, "default", "stop"); !errors.Is(err, ErrHostWorkloadChanged) {
			t.Fatal("saved startup authority added after capture did not prevent lifecycle control")
		}
		current, err := manager.ListExisting(ctx)
		if err != nil || len(current) != 1 || current[0].PID != int(restarted.Processes[0].PID) {
			t.Fatal("startup authority guard mutated the original process")
		}
		assertServing()
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	t.Log("real PM2 capture preserved HTTP response, secret/empty environment, UID, exact PID identity, logs, original manager restart configuration, and no-mutation guards for both saved startup lists")
}
