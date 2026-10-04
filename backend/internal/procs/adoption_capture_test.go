package procs

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

func TestHostCapturePreservesPrivateEnvironmentAndArgumentBoundaries(t *testing.T) {
	root := t.TempDir()
	command := exec.Command("sleep", "30")
	command.Dir = root
	command.Env = []string{"PATH=/usr/bin:/bin", "PRODUCTION_TOKEN=private value with spaces", "EMPTY="}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	p, err := process.NewProcess(int32(command.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	created, err := p.CreateTime()
	if err != nil {
		t.Fatal(err)
	}
	capture, err := CaptureExistingProcess(context.Background(), p.Pid, created)
	if err != nil {
		t.Fatal(err)
	}
	if capture.SourceDirectory != root || len(capture.Command) != 2 || capture.Command[1] != "30" || capture.Environment["PRODUCTION_TOKEN"] != "private value with spaces" || capture.Environment["EMPTY"] != "" {
		t.Fatalf("capture lost configuration")
	}
	if len(capture.Blockers) == 0 {
		t.Fatal("unmanaged process wrongly claims rollback authority")
	}
	encoded, _ := json.Marshal(capture)
	if strings.Contains(string(encoded), "private value") || strings.Contains(string(encoded), `"command"`) || strings.Contains(string(encoded), `"environment"`) || strings.Contains(string(encoded), `"originalConfig"`) {
		t.Fatal("private runtime configuration leaked")
	}
	if _, err := CaptureExistingProcess(context.Background(), p.Pid, created+1); err != ErrHostWorkloadChanged {
		t.Fatalf("reused identity accepted: %v", err)
	}
}

func TestHostCaptureRejectsUnrepresentableOrAmbiguousEnvironment(t *testing.T) {
	for _, raw := range []string{"MALFORMED\x00", "INVALID-NAME=value\x00", "DUP=a\x00DUP=b\x00", "A=incomplete"} {
		if _, err := captureEnvironment([]byte(raw)); err == nil {
			t.Fatalf("accepted unsupported environment")
		}
	}
	values, err := captureNULValues([]byte("node\x00argument with spaces\x00\x00"))
	if err != nil || len(values) != 3 || values[1] != "argument with spaces" || values[2] != "" {
		t.Fatal("argument boundaries changed")
	}
}

func TestPM2AdoptionCaptureFencesNamespacesAndPreservesSecretsPrivately(t *testing.T) {
	account := &user.User{Username: "alice", Uid: "1000", Gid: "1001"}
	data := []byte(`[{"pm_id":7,"name":"api","pm2_env":{"namespace":"production","status":"online","created_at":1234,"pm_exec_path":"/srv/api/server.js","pm_cwd":"/srv/api","exec_mode":"fork_mode","exec_interpreter":"node","args":["--token","private argument"],"node_args":["--max-old-space-size=512"],"env":{"PORT":"3000","TOKEN":"private environment"}}},{"pm_id":8,"name":"api","pm2_env":{"namespace":"staging","status":"online","pm_exec_path":"/srv/staging/server.js","pm_cwd":"/srv/staging","exec_mode":"fork_mode","env":{"TOKEN":"staging"}}}]`)
	capture, err := parsePM2Capture(data, account, "production", "api")
	if err != nil {
		t.Fatal(err)
	}
	if len(capture.Processes) != 1 || capture.Processes[0].ID != 7 || capture.UID != 1000 || capture.GID != 1001 || capture.Environment["TOKEN"] != "private environment" || len(capture.Command) != 5 || capture.Command[3] != "--token" {
		t.Fatalf("capture lost exact PM2 configuration: %v", capture.Blockers)
	}
	if len(capture.Blockers) != 0 {
		t.Fatalf("valid fork configuration blocked: %v", capture.Blockers)
	}
	encoded, _ := json.Marshal(capture)
	if strings.Contains(string(encoded), "private environment") || strings.Contains(string(encoded), "private argument") || strings.Contains(string(encoded), "staging") {
		t.Fatal("private PM2 configuration leaked")
	}
	changed := strings.Replace(string(data), `"status":"online"`, `"status":"stopped"`, 1)
	again, err := parsePM2Capture([]byte(changed), account, "production", "api")
	if err != nil || again.ConfigurationDigest != capture.ConfigurationDigest {
		t.Fatal("status changes invalidate configuration identity")
	}
	changed = strings.Replace(string(data), "private environment", "different environment", 1)
	again, err = parsePM2Capture([]byte(changed), account, "production", "api")
	if err != nil || again.ConfigurationDigest == capture.ConfigurationDigest {
		t.Fatal("changed original secret not fenced")
	}
	changed = strings.Replace(string(data), `"created_at":1234`, `"created_at":5678`, 1)
	again, err = parsePM2Capture([]byte(changed), account, "production", "api")
	if err != nil || again.ConfigurationDigest == capture.ConfigurationDigest {
		t.Fatal("reassigned PM2 ID not fenced")
	}
}

func TestPM2AdoptionCaptureBlocksClusterWatchAndCommandStrings(t *testing.T) {
	data := []byte(`[{"pm_id":1,"name":"app","pm2_env":{"namespace":"default","pm_exec_path":"/srv/app.js","pm_cwd":"/srv","exec_mode":"cluster_mode","watch":true,"args":"--port 3000","env":{}}}]`)
	capture, err := parsePM2Capture(data, &user.User{Username: "alice", Uid: "1000", Gid: "1000"}, "default", "app")
	if err != nil || len(capture.Blockers) < 3 {
		t.Fatalf("unsupported PM2 behavior silently lost: %v %v", capture, err)
	}
}

func TestSystemdAdoptionReportsUnsupportedSemanticsAndStableLifecycleDigest(t *testing.T) {
	props := map[string]string{"Type": "notify", "WorkingDirectory": "/srv/app", "User": "ubuntu", "DynamicUser": "yes", "EnvironmentFiles": "/srv/app/.env", "LoadCredential": "token:/run/token", "TriggeredBy": "api.socket", "ExecStart": "{ path=/usr/bin/node ; argv[]=/usr/bin/node server.js ; ignore_errors=no ; start_time=[Sun 2026-10-04 00:00:00 UTC] ; stop_time=[n/a] ; pid=123 ; code=(null) ; status=0/0 }"}
	capture := systemdCaptureProperties(&Unit{Name: "api.service", Fragment: "/etc/systemd/system/api.service"}, props)
	if len(capture.Blockers) < 4 {
		t.Fatal("systemd migration omitted isolation or credentials")
	}
	before, _ := json.Marshal(stableSystemdProperties(props))
	props["ExecStart"] = strings.Replace(props["ExecStart"], "pid=123", "pid=0", 1)
	props["ExecStart"] = strings.Replace(props["ExecStart"], "stop_time=[n/a]", "stop_time=[Sun 2026-10-04 00:01:00 UTC]", 1)
	after, _ := json.Marshal(stableSystemdProperties(props))
	if string(before) != string(after) {
		t.Fatal("lifecycle observations invalidate native baseline")
	}
	props["ExecStart"] = strings.Replace(props["ExecStart"], "server.js", "changed.js", 1)
	after, _ = json.Marshal(stableSystemdProperties(props))
	if string(before) == string(after) {
		t.Fatal("changed original command not fenced")
	}
}

func TestPM2ControlClientVerifiesConfigurationBeforeExactIDMutation(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is unavailable")
	}
	root := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		name = filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("bin/pm2", `throw Error("must not invoke the CLI")`)
	write("node_modules/pm2-axon/index.js", `exports.socket=()=>({on(){},connect(){},close(){}})`)
	write("node_modules/pm2-axon-rpc/index.js", `exports.Client=class { call(method,args,callback){ if(method==="getMonitorData")return callback(null,[{pm_id:7,name:"api",pm2_env:{namespace:"production",status:"online",pm_exec_path:"/srv/api.js",env:{TOKEN:"private"}}}]); if(method!=="stopProcessId"||args!==7)throw Error("wrong action"); require("fs").writeFileSync(process.env.PM2_HOME+"/acted","7"); callback(null,{}) } }`)
	request := `{"action":"stop","name":"api","namespace":"production","startupEvidence":{"dump.pm2":[],"dump.pm2.bak":[]},"configuration":[{"pm_id":7,"namespace":"production","pm_exec_path":"/srv/api.js","env":{"TOKEN":"private"}}]}`
	run := func(body string) error {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, node, "-e", existingPM2ControlClient, filepath.Join(root, "bin/pm2"))
		command.Env = append(os.Environ(), "PM2_HOME="+root)
		command.Stdin = strings.NewReader(body)
		return command.Run()
	}
	if err := run(strings.Replace(request, "private", "changed", 1)); err == nil {
		t.Fatal("changed original configuration accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "acted")); !os.IsNotExist(err) {
		t.Fatal("mutated before comparing configuration")
	}
	if err := run(request); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(root, "acted")); err != nil || string(raw) != "7" {
		t.Fatal("wrong original process mutated")
	}
}

func TestSystemdCapturePreservesRepresentableLifecycleAndBlocksTransientAuthority(t *testing.T) {
	properties := map[string]string{"Type": "exec", "User": "ubuntu", "Restart": "on-failure", "KillMode": "control-group", "KillSignal": "15", "TimeoutStopUSec": "1min 30s"}
	unit := &Unit{Name: "owned-api.service", Fragment: "/run/systemd/system/owned-api.service", UnitFile: "disabled"}
	capture := systemdCaptureProperties(unit, properties)
	if len(capture.Blockers) != 0 || capture.RestartPolicy != "on-failure" || capture.StopSignal != "SIGTERM" || capture.GracePeriodSeconds != 90 {
		t.Fatalf("simple systemd lifecycle not preserved: %+v", capture)
	}
	before, _ := json.Marshal(stableSystemdProperties(properties))
	properties["ProtectKernelTunables"] = "yes"
	after, _ := json.Marshal(stableSystemdProperties(properties))
	if string(before) == string(after) {
		t.Fatal("changed effective security policy was not fenced")
	}
	delete(properties, "ProtectKernelTunables")
	properties["Transient"] = "yes"
	properties["Restart"] = "on-watchdog"
	properties["TimeoutStopUSec"] = "infinity"
	properties["KillMode"] = "process"
	capture = systemdCaptureProperties(unit, properties)
	if len(capture.Blockers) < 4 {
		t.Fatal("transient restart authority and unrepresentable lifecycle semantics accepted")
	}
}

func TestNativeSourceFenceRejectsPermissionAndContentChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "entrypoint.js")
	if err := os.WriteFile(path, []byte("original source"), 0755); err != nil {
		t.Fatal(err)
	}
	captured, err := CaptureHostSourceFiles([]string{path})
	if err != nil || VerifyHostSourceFiles(captured) != nil {
		t.Fatal("original entrypoint could not be verified")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if VerifyHostSourceFiles(captured) != ErrHostWorkloadChanged {
		t.Fatal("changed entrypoint permission was accepted")
	}
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed source"), 0755); err != nil {
		t.Fatal(err)
	}
	if VerifyHostSourceFiles(captured) != ErrHostWorkloadChanged {
		t.Fatal("changed entrypoint content was accepted")
	}
}
