package netx

import (
	"testing"
)

func TestHostSupportUsesHostToolsAndProbesServiceManager(t *testing.T) {
	rec := record(t)
	rec.on("systemctl is-system-running", "offline")
	old := hostTool
	defer func() { hostTool = old }()
	hostTool = func(name string) bool { return name == "systemctl" || name == "ip" }
	view := testService(t).HostSupport(t.Context())
	if view.Persistence != "systemd manager unreachable" || len(view.Tools) < 15 || len(view.Notes) == 0 {
		t.Fatalf("view = %+v", view)
	}
	for _, tool := range view.Tools {
		if tool.Available != (tool.Tool == "systemctl" || tool.Tool == "ip") {
			t.Fatalf("wrong availability: %+v", tool)
		}
	}
	if !rec.ran("systemctl is-system-running") {
		t.Fatal("binary existence mistaken for running service manager")
	}
}

func TestHostSupportWithoutSystemdHasNoPersistencePromise(t *testing.T) {
	record(t)
	old := hostTool
	defer func() { hostTool = old }()
	hostTool = func(string) bool { return false }
	view := testService(t).HostSupport(t.Context())
	if view.Persistence != "unavailable" {
		t.Fatalf("%+v", view)
	}
}
