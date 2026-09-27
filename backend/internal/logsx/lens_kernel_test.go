package logsx

import (
	"testing"
	"time"
)

// One OOM report from this host's kern.log, cut down to a few lines of each
// part: the invocation opens it, the trace, tables and summary fold under
// it, and the kill is its own line, carrying the unit the summary named.
// The packet lines either side are the firewall's.
func TestLensKernelFoldsAnOOMReport(t *testing.T) {
	cont := func(text, at string) sysWant {
		return sysWant{text: text, at: at, level: "error", cont: true}
	}
	sysRead(t, "kernel",
		sysWant{
			text:  "2026-09-19T17:27:55.138263+00:00 web-1 kernel: [UFW BLOCK] IN=ens3 OUT= " + fwMAC + " SRC=203.0.113.28 DST=198.51.100.87 LEN=40 TOS=0x00 PREC=0x00 TTL=236 ID=42169 PROTO=TCP SPT=58084 DPT=3313 WINDOW=1024 RES=0x00 SYN URGP=0 ",
			event: "block", level: "info", at: "2026-09-19T17:27:55.138263Z", lens: "firewall",
			attrs: map[string]string{"client": "203.0.113.28", "dst": "198.51.100.87", "len": "40", "proto": "TCP",
				"spt": "58084", "dpt": "3313", "iface": "ens3", "flags": "SYN"},
		},
		sysWant{
			text:  "2026-09-19T17:27:56.876962+00:00 web-1 kernel: V8Worker invoked oom-killer: gfp_mask=0xcc0(GFP_KERNEL), order=0, oom_score_adj=0",
			event: "oom", level: "error", at: "2026-09-19T17:27:56.876962Z",
			attrs: map[string]string{"program": "V8Worker"},
		},
		cont("2026-09-19T17:27:56.876995+00:00 web-1 kernel: CPU: 1 UID: 1000 PID: 3964373 Comm: V8Worker Tainted: G        W          6.14.0-37-generic #37-Ubuntu", "2026-09-19T17:27:56.876995Z"),
		cont("2026-09-19T17:27:56.876996+00:00 web-1 kernel: Tainted: [W]=WARN", "2026-09-19T17:27:56.876996Z"),
		cont("2026-09-19T17:27:56.877000+00:00 web-1 kernel: Call Trace:", "2026-09-19T17:27:56.877Z"),
		cont("2026-09-19T17:27:56.877001+00:00 web-1 kernel:  <TASK>", "2026-09-19T17:27:56.877001Z"),
		cont("2026-09-19T17:27:56.877003+00:00 web-1 kernel:  show_stack+0x49/0x60", "2026-09-19T17:27:56.877003Z"),
		cont("2026-09-19T17:27:56.877089+00:00 web-1 kernel: Tasks state (memory values in pages):", "2026-09-19T17:27:56.877089Z"),
		cont("2026-09-19T17:27:56.877090+00:00 web-1 kernel: [  pid  ]   uid  tgid total_vm      rss rss_anon rss_file rss_shmem pgtables_bytes swapents oom_score_adj name", "2026-09-19T17:27:56.87709Z"),
		cont("2026-09-19T17:27:56.877091+00:00 web-1 kernel: [3963629]  1000 3963629  5541272   431979   403849    28130         0  7561216        0             0 next-build (v16", "2026-09-19T17:27:56.877091Z"),
		cont("2026-09-19T17:27:56.877094+00:00 web-1 kernel: oom-kill:constraint=CONSTRAINT_MEMCG,nodemask=(null),cpuset=user.slice,mems_allowed=0,oom_memcg=/user.slice/user-1000.slice/user@1000.service/app.slice/run-p3963628-i54286731.scope,task_memcg=/user.slice/user-1000.slice/user@1000.service/app.slice/run-p3963628-i54286731.scope,task=next-build (v16,pid=3963629,uid=1000", "2026-09-19T17:27:56.877094Z"),
		sysWant{
			// A comm is cut at fifteen bytes, and this one has a bracket in it.
			text:  "2026-09-19T17:27:56.877095+00:00 web-1 kernel: Memory cgroup out of memory: Killed process 3963629 (next-build (v16) total-vm:22165088kB, anon-rss:1615396kB, file-rss:112520kB, shmem-rss:0kB, UID:1000 pgtables:7384kB oom_score_adj:0",
			event: "oom_kill", level: "error", at: "2026-09-19T17:27:56.877095Z",
			attrs: map[string]string{"pid": "3963629", "program": "next-build (v16", "memory": "1769385984",
				"unit": "run-p3963628-i54286731.scope"},
		},
		sysWant{
			text:  "2026-09-19T17:27:58.101512+00:00 web-1 kernel: [UFW BLOCK] IN=ens3 OUT= " + fwMAC + " SRC=203.0.113.29 DST=198.51.100.87 LEN=40 TOS=0x00 PREC=0x00 TTL=237 ID=13007 PROTO=TCP SPT=55110 DPT=8443 WINDOW=1024 RES=0x00 SYN URGP=0 ",
			event: "block", level: "info", at: "2026-09-19T17:27:58.101512Z", lens: "firewall",
			attrs: map[string]string{"client": "203.0.113.29", "dst": "198.51.100.87", "len": "40", "proto": "TCP",
				"spt": "55110", "dpt": "8443", "iface": "ens3", "flags": "SYN"},
		},
	)
}

// A report cut short — no Killed process line, as when the kernel finds
// nothing it may kill — does not swallow the lines after it.
func TestLensKernelClosesAnUnfinishedReport(t *testing.T) {
	sysRead(t, "kernel",
		sysWant{
			text:  "2026-09-16T05:28:18.660823+00:00 web-1 kernel: chromium invoked oom-killer: gfp_mask=0xcc0(GFP_KERNEL), order=0, oom_score_adj=300",
			event: "oom", level: "error", at: "2026-09-16T05:28:18.660823Z",
			attrs: map[string]string{"program": "chromium"},
		},
		sysWant{
			text: "2026-09-16T05:28:18.660850+00:00 web-1 kernel: Call Trace:",
			at:   "2026-09-16T05:28:18.66085Z", level: "error", cont: true,
		},
		sysWant{
			text: "2026-09-16T05:28:21.114031+00:00 web-1 kernel: docker0: port 6(veth63d8774) entered blocking state",
			at:   "2026-09-16T05:28:21.114031Z",
		},
	)
}

func TestLensKernelNamesTheContainerAnOOMKilled(t *testing.T) {
	sysRead(t, "kernel",
		sysWant{
			text: "2026-09-15T05:34:05.102892+00:00 web-1 kernel: oom-kill:constraint=CONSTRAINT_MEMCG,nodemask=(null),cpuset=docker-ffd801b7c3463ed41cafc58fc507bc3b10f0a49675ffa4043c21c4bd6c7cf97a.scope,mems_allowed=0,oom_memcg=/system.slice/docker-ffd801b7c3463ed41cafc58fc507bc3b10f0a49675ffa4043c21c4bd6c7cf97a.scope,task_memcg=/system.slice/docker-ffd801b7c3463ed41cafc58fc507bc3b10f0a49675ffa4043c21c4bd6c7cf97a.scope,task=chromium,pid=198991,uid=0",
			at:   "2026-09-15T05:34:05.102892Z",
		},
		sysWant{
			text:  "2026-09-15T05:34:05.102898+00:00 web-1 kernel: Memory cgroup out of memory: Killed process 198991 (chromium) total-vm:1518437708kB, anon-rss:105352kB, file-rss:127700kB, shmem-rss:0kB, UID:0 pgtables:2400kB oom_score_adj:300",
			event: "oom_kill", level: "error", at: "2026-09-15T05:34:05.102898Z",
			attrs: map[string]string{"pid": "198991", "program": "chromium", "memory": "238645248", "container": "ffd801b7c346"},
		},
		sysWant{
			// mm/oom_kill.c's reaper line, as the research quotes it.
			text: "2026-09-15T05:34:05.103417+00:00 web-1 kernel: oom_reaper: reaped process 198991 (chromium), now anon-rss:0kB, file-rss:0kB, shmem-rss:0kB",
			at:   "2026-09-15T05:34:05.103417Z", level: "error", cont: true,
		},
	)
}

func TestLensKernelReadsCrashesAndHardware(t *testing.T) {
	sysInZone(t)
	sysRead(t, "kernel",
		sysWant{
			text:  "2026-09-25T05:24:26.696327+00:00 web-1 kernel: zsh[1466818]: segfault at 5f84460a77f9 ip 00005f81b9c67132 sp 00007ffc4a4bb320 error 4 in zsh[68132,5f81b9c16000+be000] likely on CPU 1 (core 0, socket 1)",
			event: "segfault", level: "error", at: "2026-09-25T05:24:26.696327Z",
			attrs: map[string]string{"program": "zsh", "pid": "1466818"},
		},
		sysWant{
			text: "2026-09-25T05:24:26.696356+00:00 web-1 kernel: Code: 8b 3f 48 85 ff 75 23 eb 6e 0f 1f 80 00 00 00 00 45 85 ed 0f 84 87 00 00 00 48 89 df e8 c7 ff fa ff 49 8b 3c 24 48 85 ff 74 4d <48> 8b 07 48 8b 5f 10 49 89 04 24 48 85 c0 74 2e 4c 89 60 08 e8 a5",
			at:   "2026-09-25T05:24:26.696356Z", level: "error", cont: true,
		},
		sysWant{
			// Headless Chrome aborting on purpose, hundreds a day: left as
			// text, with the level its "error:0" earns it.
			text: "2026-09-27T12:12:37.154248+00:00 web-1 kernel: traps: Compositor[4053605] trap int3 ip:5d4644df0628 sp:784641df4830 error:0 in chrome-headless-shell[7562628,5d463f354000+9a6d000]",
			at:   "2026-09-27T12:12:37.154248Z", level: "error",
		},
		sysWant{
			// arch/x86/kernel/traps.c's format.
			text:  "2026-09-27T12:14:02.511230+00:00 web-1 kernel: traps: node[48211] general protection fault ip:7f1c2a4b3c5d sp:7ffd4f0c0a40 error:0 in libc.so.6[7f1c2a400000+195000]",
			event: "segfault", level: "error", at: "2026-09-27T12:14:02.51123Z",
			attrs: map[string]string{"program": "node", "pid": "48211"},
		},
		sysWant{
			text:  "2026-09-25T14:06:32.259295+00:00 web-1 kernel: audit: type=1400 audit(1790345192.254:1397): apparmor=\"DENIED\" operation=\"capable\" class=\"cap\" profile=\"unprivileged_userns\" pid=4023139 comm=\"3\" capability=8  capname=\"setpcap\"",
			event: "apparmor_denied", at: "2026-09-25T14:06:32.259295Z",
			attrs: map[string]string{"pid": "4023139", "program": "3"},
		},
		// The rest are the research's examples, checked there against the
		// kernel source that prints them.
		sysWant{
			text:  "2026-09-27T03:10:44.120331+00:00 web-1 kernel: I/O error, dev sda, sector 123456 op 0x0:(READ) flags 0x80700 phys_seg 1 prio class 2",
			event: "io_error", level: "error", at: "2026-09-27T03:10:44.120331Z", attrs: map[string]string{"dev": "sda"},
		},
		sysWant{
			text:  "2026-09-27T03:10:44.120390+00:00 web-1 kernel: critical medium error, dev nvme0n1, sector 9876 op 0x1:(WRITE) flags 0x0 phys_seg 1 prio class 0",
			event: "io_error", level: "error", at: "2026-09-27T03:10:44.12039Z", attrs: map[string]string{"dev": "nvme0n1"},
		},
		sysWant{
			text:  "2026-09-27T03:10:45.002114+00:00 web-1 kernel: EXT4-fs error (device sda1): ext4_find_entry:1455: inode #2: comm ls: reading directory lblock 0",
			event: "fs_error", level: "error", at: "2026-09-27T03:10:45.002114Z", attrs: map[string]string{"dev": "sda1"},
		},
		sysWant{
			text:  "2026-09-27T03:10:45.002201+00:00 web-1 kernel: EXT4-fs (sda1): Remounting filesystem read-only",
			event: "readonly_fs", level: "critical", at: "2026-09-27T03:10:45.002201Z", attrs: map[string]string{"dev": "sda1"},
		},
		sysWant{
			// "INFO:" is the prefix; the kernel prints it at KERN_ERR.
			text:  "2026-09-27T03:12:48.771005+00:00 web-1 kernel: INFO: task postgres:1234 blocked for more than 120 seconds.",
			event: "hung_task", level: "error", at: "2026-09-27T03:12:48.771005Z",
			attrs: map[string]string{"program": "postgres", "pid": "1234"},
		},
		sysWant{
			text:  "Sep  7 03:12:01 web-1 kernel: [ 1234.567890] ens3: Link is Down",
			event: "link_down", level: "warn", at: sysBSDWant(time.September, 7, 3, 12, 1),
			attrs: map[string]string{"dev": "ens3"},
		},
		sysWant{
			text:  "Sep  7 03:12:05 web-1 kernel: [ 1238.901234] e1000e 0000:00:19.0 eth0: NIC Link is Up 1000 Mbps Full Duplex, Flow Control: None",
			event: "link_up", at: sysBSDWant(time.September, 7, 3, 12, 5),
			attrs: map[string]string{"dev": "eth0"},
		},
		sysWant{
			// arch/x86/kernel/cpu/mce/core.c: pr_fmt "mce: " and HW_ERR.
			text:  "2026-09-27T03:20:00.000412+00:00 web-1 kernel: mce: [Hardware Error]: Machine check events logged",
			event: "mce", level: "critical", at: "2026-09-27T03:20:00.000412Z",
		},
	)
}

func BenchmarkLensKernel(b *testing.B) {
	benchmarkSysLens(b, "kernel", []string{
		"2026-09-20T01:06:07.297757+00:00 web-1 kernel: docker0: port 6(veth63d8774) entered blocking state",
		"2026-09-20T01:06:07.297801+00:00 web-1 kernel: veth63d8774: entered promiscuous mode",
		"2026-09-20T01:06:07.297845+00:00 web-1 kernel: docker0: port 6(veth63d8774) entered disabled state",
		"2026-09-27T00:21:14.156279+00:00 web-1 kernel: [UFW BLOCK] IN=ens3 OUT= " + fwMAC + " SRC=203.0.113.11 DST=198.51.100.87 LEN=40 TOS=0x00 PREC=0x00 TTL=238 ID=50812 PROTO=TCP SPT=54703 DPT=4448 WINDOW=1024 RES=0x00 SYN URGP=0 ",
		"2026-09-25T14:06:32.259295+00:00 web-1 kernel: audit: type=1400 audit(1790345192.254:1397): apparmor=\"AUDIT\" operation=\"userns_create\" class=\"namespace\" info=\"Userns create - transitioning profile\" profile=\"unconfined\" pid=4023139 comm=\"3\" requested=\"userns_create\" target=\"unprivileged_userns\"",
	})
}
