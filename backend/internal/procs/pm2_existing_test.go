package procs

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExistingPM2ClientReadsMonitorRPCWithoutLoadingPM2API(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is not installed")
	}
	root := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("bin/pm2", `throw new Error("PM2 CLI/API must not execute")`)
	write("node_modules/pm2/index.js", `throw new Error("PM2 API must not initialize files or start a daemon")`)
	write("node_modules/pm2-axon/index.js", `exports.socket = function(type) {
  if (type !== "req") throw new Error("wrong socket type")
  return { on() {}, close() {}, connect(path) {
    if (path !== process.env.PM2_HOME + "/rpc.sock") throw new Error("wrong daemon")
  } }
}`)
	write("node_modules/pm2-axon-rpc/index.js", `exports.Client = class {
  constructor(socket) { this.socket = socket }
  call(method, args, callback) {
    if (method !== "getMonitorData" || Object.keys(args).length !== 0) throw new Error("mutation attempted")
    callback(null, [{pm_id: 7, name: "app", pm2_env: {status: "online", PASSWORD: "never-persist"}}])
  }
}`)
	command := exec.Command(node, "-e", existingPM2Client, filepath.Join(root, "bin", "pm2"))
	command.Env = append(os.Environ(), "PM2_HOME="+root)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("read existing daemon: %v", err)
	}
	rows, err := parsePM2List(output, 0, "fixture-account")
	if err != nil || len(rows) != 1 || rows[0].DaemonID != "fixture-account" || rows[0].ID != 7 {
		t.Fatalf("rows=%#v err=%v", rows, err)
	}
	encoded, _ := json.Marshal(rows)
	if strings.Contains(string(encoded), "never-persist") {
		t.Fatal("daemon environment leaked into inventory")
	}
	for _, path := range []string{"dump.pm2", "pm2.pid", "logs"} {
		if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Fatalf("read initialized %s", path)
		}
	}
	if err := os.Mkdir(filepath.Join(root, "modules"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pm2-axon", "pm2-axon-rpc"} {
		if err := os.Rename(filepath.Join(root, "node_modules", name), filepath.Join(root, "modules", name)); err != nil {
			t.Fatal(err)
		}
	}
	command = exec.Command(node, "-e", existingPM2Client, "pm2")
	command.Env = append(os.Environ(), "PM2_HOME="+root, "PATH="+filepath.Join(root, "bin")+":"+os.Getenv("PATH"))
	if output, err := command.Output(); err != nil || !strings.Contains(string(output), `"pm_id":7`) {
		t.Fatalf("read bundled transport through host PATH: %s %v", output, err)
	}
}

func TestExistingPM2MonitorKeepsApplicationNamespaces(t *testing.T) {
	rows, err := parsePM2List([]byte(`[{"pm_id":1,"name":"api","pm2_env":{"namespace":"production","status":"online"}},{"pm_id":2,"name":"api","pm2_env":{"namespace":"preview","status":"online"}}]`), 0, "alice")
	if err != nil || len(rows) != 2 || rows[0].Namespace != "production" || rows[1].Namespace != "preview" {
		t.Fatalf("namespaces lost: %#v %v", rows, err)
	}
}
