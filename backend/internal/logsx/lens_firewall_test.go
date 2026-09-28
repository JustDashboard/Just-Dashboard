package logsx

import (
	"testing"
	"time"
)

const fwMAC = "MAC=52:54:00:12:34:56:52:54:00:65:43:21:08:00"

func TestLensFirewallReadsUFW(t *testing.T) {
	sysRead(t, "firewall",
		sysWant{
			text:  "2026-09-27T00:21:14.156279+00:00 web-1 kernel: [UFW BLOCK] IN=ens3 OUT= " + fwMAC + " SRC=203.0.113.11 DST=198.51.100.87 LEN=40 TOS=0x00 PREC=0x00 TTL=238 ID=50812 PROTO=TCP SPT=54703 DPT=4448 WINDOW=1024 RES=0x00 SYN URGP=0 ",
			event: "block", level: "info", at: "2026-09-27T00:21:14.156279Z",
			attrs: map[string]string{"client": "203.0.113.11", "dst": "198.51.100.87", "len": "40", "proto": "TCP",
				"spt": "54703", "dpt": "4448", "iface": "ens3", "flags": "SYN"},
		},
		sysWant{
			// UDP repeats LEN for its own header; the packet's is the first.
			text:  "2026-09-27T00:26:48.760917+00:00 web-1 kernel: [UFW BLOCK] IN=ens3 OUT= " + fwMAC + " SRC=203.0.113.27 DST=198.51.100.87 LEN=51 TOS=0x00 PREC=0x00 TTL=38 ID=63488 DF PROTO=UDP SPT=60800 DPT=7 LEN=31 ",
			event: "block", level: "info", at: "2026-09-27T00:26:48.760917Z",
			attrs: map[string]string{"client": "203.0.113.27", "dst": "198.51.100.87", "len": "51", "proto": "UDP",
				"spt": "60800", "dpt": "7", "iface": "ens3"},
		},
		sysWant{
			text:  "2026-09-27T00:42:59.207264+00:00 web-1 kernel: [UFW BLOCK] IN=ens3 OUT= " + fwMAC + " SRC=10.217.193.139 DST=198.51.100.87 LEN=40 TOS=0x00 PREC=0x00 TTL=50 ID=45050 PROTO=ICMP TYPE=13 CODE=0 ",
			event: "block", level: "info", at: "2026-09-27T00:42:59.207264Z",
			attrs: map[string]string{"client": "10.217.193.139", "dst": "198.51.100.87", "len": "40", "proto": "ICMP", "iface": "ens3"},
		},
		sysWant{
			text:  "2026-09-27T00:28:42.473367+00:00 web-1 kernel: [UFW BLOCK] IN=ens3 OUT= MAC=52:54:00:12:34:56:52:54:00:65:43:21:86:dd SRC=2001:0db8:4000:0000:0000:0000:0000:005b DST=2001:0db8:2005:0100:0000:0000:0000:0013 LEN=64 TC=0 HOPLIMIT=236 FLOWLBL=1027865 PROTO=TCP SPT=37372 DPT=25 WINDOW=14600 RES=0x00 SYN URGP=0 ",
			event: "block", level: "info", at: "2026-09-27T00:28:42.473367Z",
			attrs: map[string]string{"client": "2001:0db8:4000:0000:0000:0000:0000:005b", "dst": "2001:0db8:2005:0100:0000:0000:0000:0013",
				"len": "64", "proto": "TCP", "spt": "37372", "dpt": "25", "iface": "ens3", "flags": "SYN"},
		},
		sysWant{
			text:  "2026-09-13T04:58:10.460608+00:00 web-1 kernel: [UFW BLOCK] IN=ens3 OUT= " + fwMAC + " SRC=203.0.113.177 DST=198.51.100.87 LEN=40 TOS=0x00 PREC=0x00 TTL=104 ID=1 DF PROTO=TCP SPT=59400 DPT=3389 WINDOW=0 RES=0x00 ACK RST URGP=0 ",
			event: "block", level: "info", at: "2026-09-13T04:58:10.460608Z",
			attrs: map[string]string{"client": "203.0.113.177", "dst": "198.51.100.87", "len": "40", "proto": "TCP",
				"spt": "59400", "dpt": "3389", "iface": "ens3", "flags": "ACK RST"},
		},
		sysWant{
			// Outbound: no inbound interface, so the outbound one.
			text:  "2026-09-05T22:56:42.191252+00:00 web-1 kernel: [UFW ALLOW] IN= OUT=ens3 SRC=198.51.100.87 DST=192.0.2.99 LEN=80 TOS=0x00 PREC=0x00 TTL=64 ID=40679 PROTO=UDP SPT=47177 DPT=53 LEN=60 ",
			event: "allow", level: "info", at: "2026-09-05T22:56:42.191252Z",
			attrs: map[string]string{"client": "198.51.100.87", "dst": "192.0.2.99", "len": "80", "proto": "UDP",
				"spt": "47177", "dpt": "53", "iface": "ens3"},
		},
		sysWant{
			text:  "2026-09-05T22:56:39.156319+00:00 web-1 kernel: [UFW AUDIT] IN= OUT=ens3 SRC=198.51.100.87 DST=192.0.2.212 LEN=2675 TOS=0x00 PREC=0x00 TTL=64 ID=54954 DF PROTO=TCP SPT=33348 DPT=443 WINDOW=4005 RES=0x00 ACK PSH URGP=0 ",
			event: "audit", level: "info", at: "2026-09-05T22:56:39.156319Z",
			attrs: map[string]string{"client": "198.51.100.87", "dst": "192.0.2.212", "len": "2675", "proto": "TCP",
				"spt": "33348", "dpt": "443", "iface": "ens3", "flags": "ACK PSH"},
		},
		sysWant{
			// ufw's LIMIT BLOCK prefix on a real packet body.
			text:  "2026-09-27T00:30:02.114512+00:00 web-1 kernel: [UFW LIMIT BLOCK] IN=ens3 OUT= " + fwMAC + " SRC=203.0.113.9 DST=198.51.100.87 LEN=60 TOS=0x00 PREC=0x00 TTL=52 ID=31337 DF PROTO=TCP SPT=40022 DPT=22 WINDOW=64240 RES=0x00 SYN URGP=0 ",
			event: "limit", level: "warn", at: "2026-09-27T00:30:02.114512Z",
			attrs: map[string]string{"client": "203.0.113.9", "dst": "198.51.100.87", "len": "60", "proto": "TCP",
				"spt": "40022", "dpt": "22", "iface": "ens3", "flags": "SYN"},
		},
		sysWant{
			text:  "2026-09-13T05:13:26.234415+00:00 web-1 kernel: message repeated 2 times: [ [UFW BLOCK] IN=ens3 OUT= " + fwMAC + " SRC=203.0.113.76 DST=198.51.100.87 LEN=40 TOS=0x00 PREC=0x00 TTL=40 ID=54337 PROTO=TCP SPT=28282 DPT=23 WINDOW=41099 RES=0x00 SYN URGP=0 ]",
			event: "block", level: "info", at: "2026-09-13T05:13:26.234415Z",
			attrs: map[string]string{"client": "203.0.113.76", "dst": "198.51.100.87", "len": "40", "proto": "TCP",
				"spt": "28282", "dpt": "23", "iface": "ens3", "flags": "SYN"},
		},
		sysWant{
			text: "2026-09-20T01:06:07.297757+00:00 web-1 kernel: docker0: port 6(veth63d8774) entered blocking state",
			at:   "2026-09-20T01:06:07.297757Z",
		},
	)
}

// firewalld's LogDenied prefixes and an older kernel's uptime stamp, in the
// BSD envelope a RHEL host writes.
func TestLensFirewallReadsFirewalldAndOldKernels(t *testing.T) {
	sysInZone(t)
	sysRead(t, "firewall",
		sysWant{
			text:  "Sep  7 03:12:01 web-1 kernel: filter_IN_public_REJECT: IN=eth0 OUT= " + fwMAC + " SRC=203.0.113.50 DST=192.0.2.10 LEN=60 TOS=0x00 PREC=0x00 TTL=52 ID=4125 DF PROTO=TCP SPT=51234 DPT=3306 WINDOW=29200 RES=0x00 SYN URGP=0 ",
			event: "block", level: "info", at: sysBSDWant(time.September, 7, 3, 12, 1),
			attrs: map[string]string{"client": "203.0.113.50", "dst": "192.0.2.10", "len": "60", "proto": "TCP",
				"spt": "51234", "dpt": "3306", "iface": "eth0", "flags": "SYN"},
		},
		sysWant{
			text:  "Sep  7 03:12:09 web-1 kernel: FINAL_REJECT: IN=eth0 OUT= " + fwMAC + " SRC=203.0.113.51 DST=192.0.2.10 LEN=52 TOS=0x00 PREC=0x00 TTL=116 ID=2218 DF PROTO=TCP SPT=61022 DPT=445 WINDOW=8192 RES=0x00 SYN URGP=0 ",
			event: "block", level: "info", at: sysBSDWant(time.September, 7, 3, 12, 9),
			attrs: map[string]string{"client": "203.0.113.51", "dst": "192.0.2.10", "len": "52", "proto": "TCP",
				"spt": "61022", "dpt": "445", "iface": "eth0", "flags": "SYN"},
		},
		sysWant{
			text:  "Sep  7 03:12:15 web-1 kernel: [ 1234.567890] [UFW BLOCK] IN=eth0 OUT= " + fwMAC + " SRC=203.0.113.52 DST=192.0.2.10 LEN=40 TOS=0x00 PREC=0x00 TTL=241 ID=54321 PROTO=TCP SPT=43118 DPT=23 WINDOW=65535 RES=0x00 SYN URGP=0 ",
			event: "block", level: "info", at: sysBSDWant(time.September, 7, 3, 12, 15),
			attrs: map[string]string{"client": "203.0.113.52", "dst": "192.0.2.10", "len": "40", "proto": "TCP",
				"spt": "43118", "dpt": "23", "iface": "eth0", "flags": "SYN"},
		},
	)
}

func BenchmarkLensFirewall(b *testing.B) {
	benchmarkSysLens(b, "firewall", []string{
		"2026-09-27T00:21:14.156279+00:00 web-1 kernel: [UFW BLOCK] IN=ens3 OUT= " + fwMAC + " SRC=203.0.113.11 DST=198.51.100.87 LEN=40 TOS=0x00 PREC=0x00 TTL=238 ID=50812 PROTO=TCP SPT=54703 DPT=4448 WINDOW=1024 RES=0x00 SYN URGP=0 ",
		"2026-09-27T00:26:48.760917+00:00 web-1 kernel: [UFW BLOCK] IN=ens3 OUT= " + fwMAC + " SRC=203.0.113.27 DST=198.51.100.87 LEN=51 TOS=0x00 PREC=0x00 TTL=38 ID=63488 DF PROTO=UDP SPT=60800 DPT=7 LEN=31 ",
	})
}
