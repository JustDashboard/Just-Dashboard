package logsx

import (
	"testing"
	"time"
)

func TestLensSystemdReadsSyslogSentences(t *testing.T) {
	unit := func(name string, more map[string]string) map[string]string {
		attrs := map[string]string{"unit": name}
		for k, v := range more {
			attrs[k] = v
		}
		return attrs
	}
	sysRead(t, "systemd",
		sysWant{
			text:  "2026-09-20T00:54:38.653921+00:00 web-1 systemd[1]: nordvpnd.service: Scheduled restart job, restart counter is at 48215.",
			event: "restart_scheduled", level: "warn", at: "2026-09-20T00:54:38.653921Z",
			attrs: unit("nordvpnd.service", map[string]string{"restarts": "48215"}),
		},
		sysWant{
			text:  "2026-09-20T00:54:38.656060+00:00 web-1 systemd[1]: Starting nordvpnd.socket - NordVPN Daemon Socket...",
			event: "starting", at: "2026-09-20T00:54:38.65606Z", attrs: unit("nordvpnd.socket", nil),
		},
		sysWant{
			text:  "2026-09-20T00:54:38.676466+00:00 web-1 systemd[1]: Listening on nordvpnd.socket - NordVPN Daemon Socket.",
			event: "started", at: "2026-09-20T00:54:38.676466Z", attrs: unit("nordvpnd.socket", nil),
		},
		sysWant{
			text:  "2026-09-20T00:54:38.704397+00:00 web-1 systemd[1]: nordvpnd-killswitch.service: Main process exited, code=exited, status=1/FAILURE",
			event: "exited", level: "error", at: "2026-09-20T00:54:38.704397Z",
			attrs: unit("nordvpnd-killswitch.service", map[string]string{"exit_code": "exited", "exit_status": "1"}),
		},
		sysWant{
			text:  "2026-09-20T00:54:38.704611+00:00 web-1 systemd[1]: nordvpnd-killswitch.service: Failed with result 'exit-code'.",
			event: "failed", level: "error", at: "2026-09-20T00:54:38.704611Z",
			attrs: unit("nordvpnd-killswitch.service", map[string]string{"result": "exit-code"}),
		},
		sysWant{
			// The job's echo of the failure just named: an error, not a
			// second failure.
			text:  "2026-09-20T00:54:38.705305+00:00 web-1 systemd[1]: Failed to start nordvpnd-killswitch.service - Nordvpn Daemon launched in killswitch mode.",
			level: "error", at: "2026-09-20T00:54:38.705305Z", attrs: unit("nordvpnd-killswitch.service", nil),
		},
		sysWant{
			text:  "2026-09-20T00:54:38.708720+00:00 web-1 systemd[1]: Started nordvpnd.service - NordVPN Daemon.",
			event: "started", at: "2026-09-20T00:54:38.70872Z", attrs: unit("nordvpnd.service", nil),
		},
		sysWant{
			text:  "2026-09-20T00:54:38.775391+00:00 web-1 systemd[1]: Closed nordvpnd.socket - NordVPN Daemon Socket.",
			event: "stopped", at: "2026-09-20T00:54:38.775391Z", attrs: unit("nordvpnd.socket", nil),
		},
		sysWant{
			text:  "2026-09-20T00:54:34.900460+00:00 web-1 systemd[1]: logrotate.service: Deactivated successfully.",
			event: "deactivated", at: "2026-09-20T00:54:34.90046Z", attrs: unit("logrotate.service", nil),
		},
		sysWant{
			text:  "2026-09-20T00:54:34.901830+00:00 web-1 systemd[1]: logrotate.service: Consumed 6.514s CPU time, 466.9M memory peak.",
			event: "resources", at: "2026-09-20T00:54:34.90183Z",
			attrs: unit("logrotate.service", map[string]string{"cpu": "6514", "memory": "489580134"}),
		},
		sysWant{
			text:  "2026-09-20T01:06:08.486930+00:00 web-1 systemd[1]: docker-b48e04a0555f7e5d338015384f75c3497330f19975ef0339747d654ec3168b70.scope: Consumed 203ms CPU time, 17M memory peak, 1.2M read from disk, 8K written to disk.",
			event: "resources", at: "2026-09-20T01:06:08.48693Z",
			attrs: unit("docker-b48e04a0555f7e5d338015384f75c3497330f19975ef0339747d654ec3168b70.scope",
				map[string]string{"cpu": "203", "memory": "17825792"}),
		},
		sysWant{
			text:  "2026-09-20T02:06:51.250404+00:00 web-1 systemd[1]: session-c178.scope: Consumed 11h 42min 40.022s CPU time, 1.4G memory peak, 182M memory swap peak.",
			event: "resources", at: "2026-09-20T02:06:51.250404Z",
			attrs: unit("session-c178.scope", map[string]string{"cpu": "42160022", "memory": "1503238554"}),
		},
		sysWant{
			text:  "2026-09-21T23:34:14.273890+00:00 web-1 systemd[1]: user-1000.slice: A process of this unit has been killed by the OOM killer.",
			event: "oom", level: "error", at: "2026-09-21T23:34:14.27389Z", attrs: unit("user-1000.slice", nil),
		},
		sysWant{
			text:  "2026-09-15T02:57:36.222718+00:00 web-1 systemd[1]: Stopping docker.service - Docker Application Container Engine...",
			event: "stopping", at: "2026-09-15T02:57:36.222718Z", attrs: unit("docker.service", nil),
		},
		sysWant{
			text:  "2026-09-15T02:57:46.441684+00:00 web-1 systemd[1]: Stopped docker.service - Docker Application Container Engine.",
			event: "stopped", at: "2026-09-15T02:57:46.441684Z", attrs: unit("docker.service", nil),
		},
		sysWant{
			text: "2026-09-21T23:34:13.898969+00:00 web-1 systemd[1]: Reload requested from client PID 2026723 ('systemctl') (unit session-12671.scope)...",
			at:   "2026-09-21T23:34:13.898969Z",
		},
		sysWant{
			text:  "2026-09-21T23:34:13.899061+00:00 web-1 systemd[1]: Reloading...",
			event: "reloading", at: "2026-09-21T23:34:13.899061Z",
		},
		sysWant{
			text:  "2026-09-21T23:34:14.251005+00:00 web-1 systemd[1]: Reloading finished in 351 ms.",
			event: "reloaded", at: "2026-09-21T23:34:14.251005Z",
		},
		sysWant{
			text:  "2026-09-20T01:00:04.714768+00:00 web-1 systemd[1]: Finished sysstat-collect.service - system activity accounting tool.",
			event: "started", at: "2026-09-20T01:00:04.714768Z", attrs: unit("sysstat-collect.service", nil),
		},
		sysWant{
			text:  "2026-09-20T01:30:31.717936+00:00 web-1 systemd[546363]: tmux-spawn-e6671631-a044-4fe1-b2cd-70120dda412d.scope: Failed with result 'resources'.",
			event: "failed", level: "error", at: "2026-09-20T01:30:31.717936Z",
			attrs: unit("tmux-spawn-e6671631-a044-4fe1-b2cd-70120dda412d.scope", map[string]string{"result": "resources"}),
		},
		sysWant{
			text: "2026-09-20T05:31:25.304006+00:00 web-1 systemd[546363]: launchpadlib-cache-clean.service - Clean up old files in the Launchpadlib cache was skipped because of an unmet condition check (ConditionPathExists=/home/ubuntu/.launchpadlib/api.launchpad.net/cache).",
			at:   "2026-09-20T05:31:25.304006Z",
		},
	)
}

// The research's shapes for what this host has not logged — a kill, a core
// dump, the start limit, a unit reload — in the BSD envelope.
func TestLensSystemdReadsKillsAndDumps(t *testing.T) {
	sysInZone(t)
	sysRead(t, "systemd",
		sysWant{
			// Before systemd 250 the job line held only the description: no
			// unit to match an earlier failure against, so it is the failure.
			text:  "Sep  7 03:10:00 web-1 systemd[1]: Failed to start The Apache HTTP Server.",
			event: "failed", level: "error", at: sysBSDWant(time.September, 7, 3, 10, 0),
		},
		sysWant{
			text:  "Sep  7 03:12:01 web-1 systemd[1]: app.service: Main process exited, code=killed, status=9/KILL",
			event: "killed", level: "warn", at: sysBSDWant(time.September, 7, 3, 12, 1),
			attrs: map[string]string{"unit": "app.service", "exit_code": "killed", "exit_status": "9", "signal": "KILL"},
		},
		sysWant{
			text:  "Sep  7 03:12:31 web-1 systemd[1]: app.service: Main process exited, code=dumped, status=11/SEGV",
			event: "killed", level: "warn", at: sysBSDWant(time.September, 7, 3, 12, 31),
			attrs: map[string]string{"unit": "app.service", "exit_code": "dumped", "exit_status": "11", "signal": "SEGV"},
		},
		sysWant{
			text:  "Sep  7 03:12:32 web-1 systemd-coredump[4121]: Process 4118 (app) of user 1000 dumped core.",
			event: "core_dumped", level: "error", at: sysBSDWant(time.September, 7, 3, 12, 32),
		},
		sysWant{
			text:  "Sep  7 03:12:40 web-1 systemd[1]: app.service: Start request repeated too quickly.",
			event: "start_limit", level: "error", at: sysBSDWant(time.September, 7, 3, 12, 40),
			attrs: map[string]string{"unit": "app.service"},
		},
		sysWant{
			text:  "Sep  7 03:12:40 web-1 systemd[1]: app.service: Failed with result 'start-limit-hit'.",
			event: "failed", level: "error", at: sysBSDWant(time.September, 7, 3, 12, 40),
			attrs: map[string]string{"unit": "app.service", "result": "start-limit-hit"},
		},
		sysWant{
			text:  "Sep  7 03:15:00 web-1 systemd[1]: Reloading nginx.service - A high performance web server and a reverse proxy server...",
			event: "reloading", at: sysBSDWant(time.September, 7, 3, 15, 0), attrs: map[string]string{"unit": "nginx.service"},
		},
		sysWant{
			text:  "Sep  7 03:15:00 web-1 systemd[1]: Reloaded nginx.service - A high performance web server and a reverse proxy server.",
			event: "reloaded", at: sysBSDWant(time.September, 7, 3, 15, 0), attrs: map[string]string{"unit": "nginx.service"},
		},
	)
}

// The journal's own entries from this host: MESSAGE_ID decides the event,
// and the structured EXIT_STATUS and UNIT_RESULT are kept as the engine put
// them.
func TestLensSystemdKeysOnMessageID(t *testing.T) {
	manager := func(id, unit, invocation string, more map[string]string) map[string]string {
		fields := map[string]string{"program": "systemd", "pid": "1", "message_id": id, "unit": unit, "invocation": invocation}
		for k, v := range more {
			fields[k] = v
		}
		return fields
	}
	const (
		exitID   = "98e322203f7a4ed290d09fe03c09fe15"
		resultID = "d9b373ed55a64feb8242e02dbe79a49c"
		jobID    = "be02cf6855d2428ba40df7e9d022f03d"
		ks       = "nordvpnd-killswitch.service"
		run      = "6c15d5520bf3474484a5f3da27a371a0"
	)
	entries := []sysEntry{
		{"nordvpnd.service: Scheduled restart job, restart counter is at 171911.", 6,
			manager("5eb03494b6584870a536b337290809b3", "nordvpnd.service", "9eca31f99c2c43209614dd099a3d6d28", nil)},
		{"Starting nordvpnd.socket - NordVPN Daemon Socket...", 6,
			manager("7d4958e842da4a758f6c1cdc7b36dcc5", "nordvpnd.socket", "f8e3db490e34465a88914fa80bc77a17", nil)},
		{"nordvpnd-killswitch.service: Main process exited, code=exited, status=1/FAILURE", 5,
			manager(exitID, ks, run, map[string]string{"exit_code": "exited", "exit_status": "1"})},
		{"nordvpnd-killswitch.service: Failed with result 'exit-code'.", 4,
			manager(resultID, ks, run, map[string]string{"result": "exit-code"})},
		{"Failed to start nordvpnd-killswitch.service - Nordvpn Daemon launched in killswitch mode.", 3,
			manager(jobID, ks, run, nil)},
		{"docker-99d7a5731b2c3588af0ba07079f4fe4d80752f02eed88039df2d6f608fe12b27.scope: Consumed 919ms CPU time, 38.8M memory peak, 6.8M read from disk.", 6,
			manager("ae8f7b866b0347b9af31fe1c80b127c0", "docker-99d7a5731b2c3588af0ba07079f4fe4d80752f02eed88039df2d6f608fe12b27.scope", "8530c8d4aa51426297784a55c4eefda0", nil)},
		// A host writing another language: the sentence is unreadable, the
		// id and the structured fields are not.
		{"app.service: Hauptprozess beendet, code=killed, status=9/KILL", 5,
			manager(exitID, "app.service", "0d1e2f3a4b5c6d7e8f90a1b2c3d4e5f6", map[string]string{"exit_code": "killed", "exit_status": "9"})},
		{"app.service: Hauptprozess beendet, code=exited, status=2/INVALIDARGUMENT", 5,
			manager(exitID, "app.service", "0d1e2f3a4b5c6d7e8f90a1b2c3d4e5f6", map[string]string{"exit_code": "exited", "exit_status": "2"})},
	}
	with := func(fields map[string]string, more map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range fields {
			out[k] = v
		}
		for k, v := range more {
			out[k] = v
		}
		return out
	}
	sysCheck(t, sysReadJournal(t, "systemd", entries...), []sysWant{
		{text: "restart", event: "restart_scheduled", level: "warn", attrs: with(entries[0].fields, map[string]string{"restarts": "171911"})},
		{text: "starting", event: "starting", level: "info", attrs: entries[1].fields},
		{text: "exited", event: "exited", level: "error", attrs: entries[2].fields},
		{text: "failed", event: "failed", level: "error", attrs: entries[3].fields},
		{text: "job", level: "error", attrs: entries[4].fields},
		{text: "resources", event: "resources", level: "info", attrs: with(entries[5].fields, map[string]string{"cpu": "919", "memory": "40684749"})},
		{text: "killed", event: "killed", level: "warn", attrs: entries[6].fields},
		{text: "exited", event: "exited", level: "error", attrs: entries[7].fields},
	})
}

// Forced onto a unit's journal, the lens meets the program's own lines as
// well as the manager's, and a program's "Closed redis connection" or
// "Starting worker pool..." is not its unit stopping or starting. Only what
// systemd and systemd-coredump write is named; a line with no program to go
// by is still read, as a syslog file's bare sentence is.
func TestLensSystemdNamesOnlyTheManagersLines(t *testing.T) {
	app := func(program string) map[string]string {
		return map[string]string{"program": program, "pid": "4211", "unit": "worker.service", "invocation": "3f1c"}
	}
	entries := []sysEntry{
		{"Closed redis connection", 6, app("node")},
		{"Starting worker pool...", 6, app("node")},
		{"Stopped target queue consumer.", 6, app("worker")},
		{"worker.service: Failed with result 'exit-code'.", 4, app("worker-wrapper")},
		{"Started worker.service - Queue worker.", 6,
			map[string]string{"program": "systemd", "pid": "1", "unit": "worker.service", "invocation": "3f1c"}},
	}
	sysCheck(t, sysReadJournal(t, "systemd", entries...), []sysWant{
		{text: "closed", level: "info", attrs: entries[0].fields},
		{text: "starting", level: "info", attrs: entries[1].fields},
		{text: "stopped", level: "info", attrs: entries[2].fields},
		{text: "failed", level: "warn", attrs: entries[3].fields},
		{text: "started", event: "started", level: "info", attrs: entries[4].fields},
	})
	sysRead(t, "systemd",
		sysWant{text: "2026-09-20T01:02:03.000000+00:00 web-1 worker[4211]: Stopping consumers...", at: "2026-09-20T01:02:03Z"},
		sysWant{text: "Started the queue worker.", event: "started"},
	)
}

func BenchmarkLensSystemd(b *testing.B) {
	benchmarkSysLens(b, "systemd", []string{
		"2026-09-20T00:54:38.653921+00:00 web-1 systemd[1]: nordvpnd.service: Scheduled restart job, restart counter is at 48215.",
		"2026-09-20T00:54:38.656060+00:00 web-1 systemd[1]: Starting nordvpnd.socket - NordVPN Daemon Socket...",
		"2026-09-20T00:54:38.704397+00:00 web-1 systemd[1]: nordvpnd-killswitch.service: Main process exited, code=exited, status=1/FAILURE",
		"2026-09-20T00:54:38.704611+00:00 web-1 systemd[1]: nordvpnd-killswitch.service: Failed with result 'exit-code'.",
		"2026-09-20T00:54:38.705305+00:00 web-1 systemd[1]: Failed to start nordvpnd-killswitch.service - Nordvpn Daemon launched in killswitch mode.",
		"2026-09-20T00:54:34.900460+00:00 web-1 systemd[1]: logrotate.service: Deactivated successfully.",
	})
}
