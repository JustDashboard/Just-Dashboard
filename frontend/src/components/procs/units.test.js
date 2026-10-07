import { describe, expect, test } from "bun:test"
import {
  ago,
  execCommands,
  exitSentence,
  exitWords,
  failureWords,
  recentChanges,
  unitBucket,
  unitChange,
  unitStateWord,
  unitTone,
} from "./units"

const NOW = Date.UTC(2026, 9, 7, 12, 0, 0)
const at = (secondsAgo) => Math.floor(NOW / 1000) - secondsAgo
const BOOT = at(3 * 86_400)

const unit = (over) => ({
  name: "nginx.service",
  description: "nginx",
  loadState: "loaded",
  activeState: "active",
  subState: "running",
  unitFileState: "enabled",
  enabled: true,
  ...over,
})

describe("unitStateWord", () => {
  // systemd's pair read as the question it answers. "exited" alone reads as
  // a crash for a oneshot that ran and is kept active on purpose.
  test("says what a reader asks", () => {
    expect(unitStateWord(unit({}))).toBe("running")
    expect(unitStateWord(unit({ subState: "exited" }))).toBe("done")
    expect(unitStateWord(unit({ activeState: "activating", subState: "auto-restart" }))).toBe(
      "restarting",
    )
    expect(unitStateWord(unit({ activeState: "activating", subState: "start-pre" }))).toBe(
      "starting",
    )
    expect(unitStateWord(unit({ activeState: "deactivating", subState: "stop-sigterm" }))).toBe(
      "stopping",
    )
    expect(unitStateWord(unit({ activeState: "inactive", subState: "dead" }))).toBe("inactive")
  })

  // A failed unit is the one to act on: red, where the shared vocabulary
  // gives every "failed" amber.
  test("tones a failure as one", () => {
    expect(unitTone(unit({ activeState: "failed" }))).toBe("danger")
    expect(unitTone(unit({ activeState: "activating" }))).toBe("warning")
    expect(unitBucket(unit({ activeState: "reloading" }))).toBe("changing")
    expect(unitTone(unit({ activeState: "inactive" }))).toBe("stopped")
  })
})

describe("how a unit ended", () => {
  test("names the exit, the signal, or the core", () => {
    expect(exitWords({ exitCode: "exited", exitStatus: 1 })).toBe("exit 1")
    expect(exitWords({ exitCode: "killed", exitStatus: 9 })).toBe("killed by SIGKILL")
    expect(exitSentence({ exitCode: "dumped", exitStatus: 11 })).toBe("dumped core on SIGSEGV")
    expect(exitSentence({ exitCode: "killed", exitStatus: 64 })).toBe("was killed by signal 64")
    expect(exitWords({})).toBeNull()
  })

  // The result says more than the exit when it is more than "exited badly".
  test("prefers systemd's result where it says more", () => {
    expect(failureWords({ result: "start-limit-hit", exitCode: "exited", exitStatus: 1 })).toBe(
      "start limit hit",
    )
    expect(failureWords({ result: "exit-code", exitCode: "exited", exitStatus: 2 })).toBe("exit 2")
    expect(failureWords({ result: "oom-kill" })).toBe("killed for memory")
  })
})

describe("recent changes", () => {
  test("reads each state as what happened to it", () => {
    expect(unitChange(unit({ activeSince: at(600), changedAt: at(60) }), NOW, BOOT)).toMatchObject(
      // A reload moves the state-change time; the start is when it became active.
      { verb: "started", at: at(600), tone: "running" },
    )
    expect(
      unitChange(unit({ activeState: "failed", changedAt: at(720) }), NOW, BOOT),
    ).toMatchObject({ verb: "failed", tone: "danger" })
    expect(
      unitChange(
        unit({ activeState: "inactive", type: "oneshot", result: "success", changedAt: at(60) }),
        NOW,
        BOOT,
      ),
    ).toMatchObject({ verb: "finished" })
    expect(
      unitChange(
        unit({ activeState: "inactive", type: "notify", result: "success", changedAt: at(60) }),
        NOW,
        BOOT,
      ),
    ).toMatchObject({ verb: "stopped" })
    expect(
      unitChange(
        unit({ activeState: "activating", subState: "auto-restart", changedAt: at(5) }),
        NOW,
        BOOT,
      ),
    ).toMatchObject({ verb: "restarting", tone: "warning" })
  })

  // Everything the boot started would otherwise be the list, and a week-old
  // stop is not recent.
  test("leaves out the boot and anything older than a day", () => {
    expect(unitChange(unit({ activeSince: BOOT + 9 }), NOW, BOOT)).toBeNull()
    expect(
      unitChange(unit({ activeState: "inactive", changedAt: at(2 * 86_400) }), NOW, BOOT),
    ).toBeNull()
    expect(unitChange(unit({}), NOW, BOOT)).toBeNull()
  })

  test("orders them newest first", () => {
    const list = recentChanges(
      [
        unit({ name: "a.service", activeSince: at(3600) }),
        unit({ name: "b.service", activeState: "failed", changedAt: at(60) }),
        unit({ name: "c.service", activeSince: BOOT }),
      ],
      NOW,
      BOOT,
    )
    expect(list.map((c) => c.unit.name)).toEqual(["b.service", "a.service"])
  })

  test("are told to the minute", () => {
    expect(ago(at(10), NOW)).toBe("just now")
    expect(ago(at(12 * 60 + 16), NOW)).toBe("12m ago")
    expect(ago(at(50), NOW)).toBe("1m ago")
    expect(ago(at(2 * 3600 + 40 * 60 + 9), NOW)).toBe("2h 40m ago")
  })
})

describe("execCommands", () => {
  // nginx's argv carries " ; " of its own; splitting on it cut the command
  // in half, and the sheet printed systemd's whole record instead.
  test("reads the argv up to the field after it", () => {
    expect(
      execCommands(
        "{ path=/usr/sbin/nginx ; argv[]=/usr/sbin/nginx -g daemon on; master_process on; ; ignore_errors=no ; start_time=[n/a] ; stop_time=[n/a] ; pid=0 ; code=(null) ; status=0/0 }",
      ),
    ).toEqual(["/usr/sbin/nginx -g daemon on; master_process on;"])
  })

  test("reads every command of a oneshot, and the Ex form's flags", () => {
    expect(
      execCommands(
        "{ path=/bin/a ; argv[]=/bin/a one ; ignore_errors=no ; start_time=[n/a] } { path=/bin/b ; argv[]=/bin/b two ; flags= ; start_time=[n/a] }",
      ),
    ).toEqual(["/bin/a one", "/bin/b two"])
  })

  test("keeps a value it cannot read rather than hiding it", () => {
    expect(execCommands("/usr/bin/thing --flag")).toEqual(["/usr/bin/thing --flag"])
    expect(execCommands("")).toEqual([])
    expect(execCommands(undefined)).toEqual([])
  })
})
