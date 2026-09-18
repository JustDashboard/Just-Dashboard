package procs

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPM2StartArgsBuildsOneArgvElementPerField(t *testing.T) {
	args, err := pm2StartArgs(PM2StartRequest{
		Script: "/srv/api/index.js", Name: "api", Cwd: "/srv/api", Interpreter: "node",
		Instances: 4, Watch: true, MaxMemoryRestart: "300M", Args: []string{"--port", "3000"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"start", "/srv/api/index.js", "--name", "api", "--cwd", "/srv/api", "--interpreter", "node",
		"-i", "4", "--watch", "--max-memory-restart", "300M", "--", "--port", "3000",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("args = %q, want %q", args, want)
	}
	if args, _ := pm2StartArgs(PM2StartRequest{Script: "/srv/api/index.js", Instances: -1}); args[len(args)-1] != "max" {
		t.Fatalf("one-per-cpu instances = %q", args)
	}
}

func TestPM2StartArgsHandsAnEcosystemFileToPM2Untouched(t *testing.T) {
	args, err := pm2StartArgs(PM2StartRequest{
		Script: "/srv/api/ecosystem.config.js", Name: "api", Cwd: "/tmp", Instances: 8, Watch: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"start", "/srv/api/ecosystem.config.js", "--only", "api"}; !reflect.DeepEqual(args, want) {
		t.Fatalf("ecosystem args = %q, want %q", args, want)
	}
}

// Every field becomes an argv element, so the only things that could go
// wrong are a relative path resolved against the wrong directory, a name
// systemctl or pm2 would read as an option, and an interpreter that is not a
// runtime at all.
func TestPM2StartArgsRejectsWhatCannotBeAnArgument(t *testing.T) {
	for name, req := range map[string]PM2StartRequest{
		"relative script":       {Script: "index.js"},
		"option-shaped name":    {Script: "/srv/a.js", Name: "--watch"},
		"relative cwd":          {Script: "/srv/a.js", Cwd: "srv"},
		"shell as interpreter":  {Script: "/srv/a.js", Interpreter: "sh -c"},
		"nonsense memory limit": {Script: "/srv/a.js", MaxMemoryRestart: "lots"},
		"too many instances":    {Script: "/srv/a.js", Instances: 129},
		"nul in argument":       {Script: "/srv/a.js", Args: []string{"a\x00b"}},
	} {
		if _, err := pm2StartArgs(req); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestParsePM2ListReadsRunSettings(t *testing.T) {
	doc := `[{"pid":1,"name":"api","pm_id":0,"monit":{"memory":1,"cpu":0},` +
		`"pm2_env":{"status":"online","exec_interpreter":"node","version":"1.4.2",` +
		`"autorestart":false,"max_memory_restart":314572800,"created_at":1700000000000}}]`
	got, err := parsePM2List([]byte(doc), 0, "deploy")
	if err != nil || len(got) != 1 {
		t.Fatalf("parse: %v %+v", err, got)
	}
	p := got[0]
	if p.Interpreter != "node" || p.Version != "1.4.2" || p.Autorestart || p.MaxMemoryRestart != 314572800 || p.CreatedAtMS != 1700000000000 {
		t.Fatalf("settings = %+v", p)
	}
	// PM2 only writes autorestart when it was set, and the default is on.
	got, _ = parsePM2List([]byte(`[{"pm_id":1,"name":"w","pm2_env":{"status":"online"}}]`), 0, "deploy")
	if !got[0].Autorestart {
		t.Fatal("absent autorestart should read as enabled")
	}
}

func TestParseTimerListOrdersBySoonestAndReadsMicroseconds(t *testing.T) {
	doc := `[{"next":null,"left":null,"last":1789581911520658,"passed":1,"unit":"never.timer","activates":"never.service"},` +
		`{"next":1789612700017890,"left":1789612700017890,"last":1789581911520658,"passed":5806381034450,"unit":"motd-news.timer","activates":"motd-news.service"},` +
		`{"next":1789605600000000,"left":"2h","last":null,"passed":null,"unit":"sysstat-collect.timer","activates":"sysstat-collect.service"}]`
	timers, err := parseTimerList([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(timers) != 3 || timers[0].Unit != "sysstat-collect.timer" || timers[2].Unit != "never.timer" {
		t.Fatalf("order = %+v", timers)
	}
	if timers[0].Next == nil || !timers[0].Next.Equal(time.UnixMicro(1789605600000000)) || timers[0].Last != nil {
		t.Fatalf("timestamps = %+v", timers[0])
	}
	if timers[2].Next != nil || timers[2].Last == nil {
		t.Fatalf("never-firing timer = %+v", timers[2])
	}
	if got, err := parseTimerList([]byte("  ")); err != nil || len(got) != 0 {
		t.Fatalf("empty listing = %+v, %v", got, err)
	}
	if _, err := parseTimerList([]byte("nope")); err == nil || !strings.Contains(err.Error(), "list-timers") {
		t.Fatalf("garbage listing = %v", err)
	}
}

func TestPM2ControlAllRefusesVerbsThatDoNotTakeAll(t *testing.T) {
	p := NewPM2()
	for _, action := range []PM2Action{PM2Delete, PM2Flush, PM2Reset, PM2Action("kill")} {
		if _, err := p.ControlAll(t.Context(), "nobody", action); err == nil || strings.Contains(err.Error(), "no PM2 installation") {
			t.Fatalf("%s reached account resolution: %v", action, err)
		}
	}
}
