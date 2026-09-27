package logsx

import (
	"math"
	"strconv"
	"strings"
)

// The systemd lens reads what the service manager says about a unit: it
// started, its main process exited with status 1, it failed with result
// 'exit-code', a restart is scheduled and the counter is at 159,216. Those
// lines are what turn a unit's journal from output into runs — the Runs view
// groups them by invocation — and they are the ones a crash loop repeats.
//
// A journal entry carries MESSAGE_ID, systemd's stable name for the message,
// and that is read first: the English sentence has changed between releases
// ("Started foo.service" gained " - Description" in 250) and a translated
// host writes another one entirely. The sentence is the fallback for syslog
// files and the values it holds (exit status, restart counter, CPU and
// memory) come from it either way.
func init() {
	register(&Lens{
		ID: "systemd",
		Events: []string{"starting", "started", "stopping", "stopped", "deactivated", "reloading",
			"reloaded", "exited", "killed", "failed", "restart_scheduled", "start_limit", "oom",
			"resources", "core_dumped"},
		Attrs: []string{"unit", "exit_code", "exit_status", "signal", "result", "restarts", "cpu",
			"memory", "invocation"},
		New: func() Reader { return &systemdReader{} },
	})
}

// systemdMessageIDs is sd-messages.h's catalogue for the unit lifecycle. The
// process-exit id covers a clean exit, a kill and a core dump alike; which
// one it was is in EXIT_CODE.
var systemdMessageIDs = map[string]string{
	"7d4958e842da4a758f6c1cdc7b36dcc5": "starting",
	"39f53479d3a045ac8e11786248231fbf": "started",
	"be02cf6855d2428ba40df7e9d022f03d": "failed",
	"de5b426a63be47a7b6ac3eaac82e2f6f": "stopping",
	"9d1aaa27d60140bd96365438aad20286": "stopped",
	"d34d037fff1847e6ae669a370e694725": "reloading",
	"7b05ebc668384222baa8881179cfda54": "reloaded",
	"98e322203f7a4ed290d09fe03c09fe15": "exited",
	"d9b373ed55a64feb8242e02dbe79a49c": "failed",
	"5eb03494b6584870a536b337290809b3": "restart_scheduled",
	"7ad2d189f7e94e70a38c781354912448": "deactivated",
	"ae8f7b866b0347b9af31fe1c80b127c0": "resources",
	"fe6faa94e7774663a0da52717891d8ef": "oom",
	"d989611b15e44c9dbf31e3c81256e4ed": "oom",
	"fc2e22bc6ee647b6b90729ab34a250b1": "core_dumped",
}

// A start that fails is reported twice, "foo.service: Failed with result
// 'exit-code'." and then the job's "Failed to start foo.service", one line
// apart. A service that dies after it started gets only the first, so that is
// the failure, and the job's line is named only when nothing just did.
const systemdEchoLines = 8

type systemdReader struct {
	// failedUnit is the unit of the last failure named, sinceFail the lines
	// read since.
	failedUnit string
	sinceFail  int
}

func (r *systemdReader) Read(l *Line) {
	m := sysEnvelope(l)
	r.sinceFail++
	event, unit := systemdText(l, m)
	if byID, ok := systemdMessageIDs[l.Attrs["message_id"]]; ok {
		switch {
		case byID == "exited" && (event == "killed" || event == "exited"):
			// The sentence has already told a kill from an exit.
		case byID == "exited" && l.Attrs["exit_code"] != "exited" && l.Attrs["exit_code"] != "":
			event = "killed"
		default:
			event = byID
		}
	}
	if unit != "" {
		sysSetMissing(l, "unit", unit)
	}
	if event == "failed" {
		unit := l.Attrs["unit"]
		_, byResult := l.Attrs["result"]
		if !byResult && unit != "" && unit == r.failedUnit && r.sinceFail <= systemdEchoLines {
			// The job's echo of the failure named just above.
			l.SetLevel("error")
			return
		}
		r.failedUnit, r.sinceFail = unit, 0
	}
	if event == "" {
		return
	}
	l.Event = event
	switch event {
	case "failed", "oom", "start_limit", "core_dumped":
		l.SetLevel("error")
	case "exited":
		if status := l.Attrs["exit_status"]; status != "" && status != "0" {
			l.SetLevel("error")
		}
	case "killed", "restart_scheduled":
		l.SetLevel("warn")
	}
}

// systemdText names a manager sentence and records the values in it. It
// answers the unit the sentence is about, which the caller keeps only when
// the journal did not already say.
func systemdText(l *Line, m sysMessage) (event, unit string) {
	msg := m.text
	if m.program == "systemd-coredump" {
		// "Process 1234 (foo) of user 0 dumped core." — the stack trace that
		// follows it in the same entry is not needed to know what happened.
		if strings.HasPrefix(msg, "Process ") && strings.Contains(msg, " dumped core.") {
			return "core_dumped", ""
		}
		return "", ""
	}
	// The manager's own daemon-reload, which every unit on the host lives
	// through: "Reloading..." and "Reloading finished in 390 ms.". The
	// "Reload requested from client PID …" before them is who asked.
	switch {
	case msg == "Reloading...":
		return "reloading", ""
	case strings.HasPrefix(msg, "Reloading finished in "):
		return "reloaded", ""
	}
	// Job results lead with the verb; a unit's own state changes lead with
	// the unit. The verbs are the job-done strings for each unit type.
	for _, verb := range systemdVerbs {
		if strings.HasPrefix(msg, verb.prefix) {
			return verb.event, systemdUnitName(msg[len(verb.prefix):])
		}
	}
	colon := strings.Index(msg, ": ")
	if colon <= 0 || strings.IndexByte(msg[:colon], ' ') >= 0 {
		return "", ""
	}
	unit, rest := msg[:colon], msg[colon+2:]
	switch {
	case strings.HasPrefix(rest, "Main process exited, code="), strings.HasPrefix(rest, "Control process exited, code="):
		// "code=exited, status=1/FAILURE" or "code=killed, status=9/KILL":
		// for a kill the number is the signal's and the name is the signal.
		rest = rest[strings.Index(rest, "code=")+len("code="):]
		code, status, _ := strings.Cut(rest, ", status=")
		number, name, _ := strings.Cut(status, "/")
		sysSetMissing(l, "exit_code", code)
		sysSetMissing(l, "exit_status", number)
		if code == "exited" {
			return "exited", unit
		}
		l.SetAttr("signal", name)
		return "killed", unit
	case strings.HasPrefix(rest, "Failed with result '"):
		sysSetMissing(l, "result", strings.TrimSuffix(rest[len("Failed with result '"):], "'."))
		return "failed", unit
	case strings.HasPrefix(rest, "Scheduled restart job, restart counter is at "):
		l.SetAttr("restarts", sysDigits(rest[len("Scheduled restart job, restart counter is at "):]))
		return "restart_scheduled", unit
	case rest == "Deactivated successfully.":
		return "deactivated", unit
	case strings.HasPrefix(rest, "Consumed "):
		systemdResources(l, rest[len("Consumed "):])
		return "resources", unit
	case strings.HasPrefix(rest, "A process of this unit has been killed by the OOM killer"):
		return "oom", unit
	case rest == "Start request repeated too quickly.":
		return "start_limit", unit
	}
	return "", ""
}

// systemdVerbs are the job sentences, longest first where one prefix holds
// another. A socket's start reads "Listening on", a oneshot's "Finished", a
// mount's "Mounted": each is that unit type's "started".
var systemdVerbs = []struct{ prefix, event string }{
	{"Failed to start ", "failed"},
	{"Starting ", "starting"},
	{"Started ", "started"},
	{"Listening on ", "started"},
	{"Finished ", "started"},
	{"Reached target ", "started"},
	{"Mounting ", "starting"},
	{"Mounted ", "started"},
	{"Stopping ", "stopping"},
	{"Stopped target ", "stopped"},
	{"Stopped ", "stopped"},
	{"Closed ", "stopped"},
	{"Unmounting ", "stopping"},
	{"Unmounted ", "stopped"},
	{"Reloading ", "reloading"},
	{"Reloaded ", "reloaded"},
}

// systemdUnitName reads the unit out of "nordvpnd.socket - NordVPN Daemon
// Socket..." or "sysstat-collect.service.". Before systemd 250 the sentence
// held only the description ("Started Session 5 of user root."), and then
// there is no unit to read; the journal's UNIT says it instead.
func systemdUnitName(s string) string {
	name := sysUntil(s, ' ')
	name = strings.TrimRight(name, ".")
	dot := strings.LastIndexByte(name, '.')
	if dot <= 0 {
		return ""
	}
	switch name[dot+1:] {
	case "service", "socket", "timer", "scope", "slice", "mount", "automount", "target", "path",
		"device", "swap":
		return name
	}
	return ""
}

// systemdResources reads "6.552s CPU time, 511.3M memory peak, 6.8M read from
// disk" into cpu in milliseconds and memory in bytes, so both sort and
// compare as numbers.
func systemdResources(l *Line, s string) {
	cpu, rest, ok := strings.Cut(s, " CPU time")
	if !ok {
		return
	}
	if ms, ok := systemdTimespan(cpu); ok {
		l.SetAttrNumber("cpu", ms)
	}
	if at := strings.Index(rest, " memory peak"); at >= 0 {
		size := rest[:at]
		size = size[strings.LastIndexByte(size, ' ')+1:]
		if bytes, ok := systemdBytes(size); ok {
			l.SetAttrNumber("memory", bytes)
		}
	}
}

// systemdTimespan reads format_timespan's "1h 2min 3.456s" or "919ms" as
// milliseconds.
func systemdTimespan(s string) (float64, bool) {
	var total float64
	for _, part := range strings.Fields(s) {
		end := 0
		for end < len(part) && (part[end] == '.' || (part[end] >= '0' && part[end] <= '9')) {
			end++
		}
		n, err := strconv.ParseFloat(part[:end], 64)
		if err != nil {
			return 0, false
		}
		unit, ok := systemdSpanUnits[part[end:]]
		if !ok {
			return 0, false
		}
		total += n * unit
	}
	return math.Round(total*1000) / 1000, true
}

var systemdSpanUnits = map[string]float64{
	"y": 31557600000, "month": 2629800000, "w": 604800000, "d": 86400000, "h": 3600000,
	"min": 60000, "s": 1000, "ms": 1, "us": 0.001, "μs": 0.001,
}

// systemdBytes reads format_bytes' "511.3M", which counts in powers of 1024.
func systemdBytes(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	scale := 1.0
	switch s[len(s)-1] {
	case 'B':
	case 'K':
		scale = 1 << 10
	case 'M':
		scale = 1 << 20
	case 'G':
		scale = 1 << 30
	case 'T':
		scale = 1 << 40
	default:
		return 0, false
	}
	n, err := strconv.ParseFloat(s[:len(s)-1], 64)
	if err != nil {
		return 0, false
	}
	return math.Round(n * scale), true
}
