package procs

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestParsePM2ListMapsStableFields(t *testing.T) {
	now := time.Now().UnixMilli()
	doc := `[{"pid":123,"name":"api","pm_id":0,"namespace":"default",` +
		`"monit":{"memory":67108864,"cpu":12.5},` +
		`"pm2_env":{"status":"online","pm_uptime":` + strconv.FormatInt(now-60000, 10) + `,` +
		`"restart_time":3,"unstable_restarts":1,"exec_mode":"fork",` +
		`"instances":1,"pm_exec_path":"/srv/api/index.js","pm_cwd":"/srv/api",` +
		`"node_version":"24.0.0","pm_out_log_path":"/home/deploy/.pm2/logs/api-out.log",` +
		`"pm_err_log_path":"/home/deploy/.pm2/logs/api-err.log",` +
		`"username":"deploy","watch":false}}]`
	got, err := parsePM2List([]byte(doc), now, "deploy")
	if err != nil {
		t.Fatalf("parse = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("processes = %d, want 1", len(got))
	}
	p := got[0]
	if p.Name != "api" || p.PID != 123 || p.User != "deploy" {
		t.Fatalf("identity = %+v", p)
	}
	if p.Memory != 67108864 || p.CPU != 12.5 {
		t.Fatalf("usage = %+v", p)
	}
	if p.OutLogPath == "" || p.ErrLogPath == "" {
		t.Fatalf("log paths missing: %+v", p)
	}
	if p.UptimeMS < 59000 || p.UptimeMS > 61000 {
		t.Fatalf("uptime = %d, want ~60000", p.UptimeMS)
	}
}

func TestParsePM2ListFallsBackToDaemonOwner(t *testing.T) {
	now := time.Now().UnixMilli()
	doc := `[{"pid":7,"name":"worker","pm_id":1,"monit":{"memory":1,"cpu":0},` +
		`"pm2_env":{"status":"stopped","pm_uptime":0,"restart_time":0}}]`
	got, err := parsePM2List([]byte(doc), now, "ubuntu")
	if err != nil {
		t.Fatalf("parse = %v", err)
	}
	if len(got) != 1 || got[0].User != "ubuntu" {
		t.Fatalf("user fallback = %+v", got)
	}
	if got[0].UptimeMS != 0 {
		t.Fatalf("stopped process uptime = %d, want 0", got[0].UptimeMS)
	}
}

func TestParsePM2ListRejectsGarbage(t *testing.T) {
	if _, err := parsePM2List([]byte("not json"), 0, ""); err == nil {
		t.Fatal("expected an error for non-JSON output")
	}
}

func TestDiscoverPM2HomesFindsNvmDaemon(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, ".nvm", "versions", "node", "v24.12.0", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "pm2"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".pm2"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := discoverPM2HomesIn([]string{home, t.TempDir()})
	if len(got) != 1 {
		t.Fatalf("homes = %+v, want one", got)
	}
	if got[0].home != home || got[0].bin != filepath.Join(binDir, "pm2") {
		t.Fatalf("home = %+v", got[0])
	}
}

func TestDiscoverPM2HomesSkipsEmptyHomes(t *testing.T) {
	if got := discoverPM2HomesIn([]string{t.TempDir()}); len(got) != 0 {
		t.Fatalf("homes = %+v, want none", got)
	}
}

func TestPM2EnvSelectsDaemon(t *testing.T) {
	home := pm2Home{home: "/home/deploy", bin: "/home/deploy/.nvm/versions/node/v24/bin/pm2"}
	env := pm2Env(home)
	values := map[string]string{}
	for _, kv := range env {
		for _, key := range []string{"HOME=", "PM2_HOME=", "PATH="} {
			if len(kv) > len(key) && kv[:len(key)] == key {
				values[key] = kv[len(key):]
			}
		}
	}
	if values["HOME="] != "/home/deploy" {
		t.Fatalf("HOME = %q", values["HOME="])
	}
	if values["PM2_HOME="] != "/home/deploy/.pm2" {
		t.Fatalf("PM2_HOME = %q", values["PM2_HOME="])
	}
	path := values["PATH="]
	want := "/home/deploy/.nvm/versions/node/v24/bin:"
	if len(path) < len(want) || path[:len(want)] != want {
		t.Fatalf("PATH = %q, want prefix %q", path, want)
	}
}
