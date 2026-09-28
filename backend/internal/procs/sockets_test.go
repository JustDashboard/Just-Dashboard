package procs

import (
	"reflect"
	"testing"
)

// hostSockets is `systemctl list-sockets --all --no-legend --no-pager --full
// --show-types --output=json` on an Ubuntu 25.04 host with socket-activated
// sshd, cut to one entry of each shape; systemd 252 prints the same fields.
const hostSockets = `[{"listen":"0.0.0.0:22","type":"Stream","unit":"ssh.socket","activates":"ssh.service"},{"listen":"[::]:22","type":"Stream","unit":"ssh.socket","activates":"ssh.service"},{"listen":"audit 1","type":"Netlink","unit":"systemd-journald-audit.socket","activates":"systemd-journald.service"},{"listen":"route 1361","type":"Netlink","unit":"systemd-networkd.socket","activates":"systemd-networkd.service"},{"listen":"vsock::22","type":"Stream","unit":"sshd-vsock.socket","activates":null},{"listen":"/run/apport.socket","type":"Stream","unit":"apport-forward.socket","activates":null},{"listen":"/run/docker.sock","type":"Stream","unit":"docker.socket","activates":"docker.service"}]`

func TestSocketUnitsParseAsTheHostPrintsThem(t *testing.T) {
	units, err := parseSocketUnits(hostSockets + "\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []SocketUnit{
		{Listen: "0.0.0.0:22", Type: "Stream", Unit: "ssh.socket", Activates: "ssh.service"},
		{Listen: "[::]:22", Type: "Stream", Unit: "ssh.socket", Activates: "ssh.service"},
		{Listen: "audit 1", Type: "Netlink", Unit: "systemd-journald-audit.socket", Activates: "systemd-journald.service"},
		{Listen: "route 1361", Type: "Netlink", Unit: "systemd-networkd.socket", Activates: "systemd-networkd.service"},
		// Accept=yes starts a service per connection and names none.
		{Listen: "vsock::22", Type: "Stream", Unit: "sshd-vsock.socket"},
		{Listen: "/run/apport.socket", Type: "Stream", Unit: "apport-forward.socket"},
		{Listen: "/run/docker.sock", Type: "Stream", Unit: "docker.socket", Activates: "docker.service"},
	}
	if !reflect.DeepEqual(units, want) {
		t.Errorf("units =\n%+v\nwant\n%+v", units, want)
	}
}

// systemctl exits 0 and prints nothing when it will not talk to the host's
// manager; that is not an empty list of sockets.
func TestSocketUnitsRefuseSilence(t *testing.T) {
	if _, err := parseSocketUnits("  \n"); err == nil {
		t.Error("no output parsed as no socket units")
	}
	if _, err := parseSocketUnits("0.0.0.0:22 ssh.socket ssh.service"); err == nil {
		t.Error("a table parsed as JSON")
	}
}
