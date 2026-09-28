import { describe, expect, test } from "bun:test"
import { ApiError } from "../../lib/api"
import {
  bootState,
  diagnosticVerdict,
  engineRun,
  failureSummary,
  isMasked,
  openableFile,
  refusalHeadline,
  refusalOf,
  stoppedLabel,
} from "./engine-lifecycle"

const unit = (overrides = {}) => ({
  name: "nginx.service",
  description: "A high performance web server",
  loadState: "loaded",
  activeState: "active",
  subState: "running",
  unitFileState: "enabled",
  enabled: true,
  ...overrides,
})

describe("which command leads", () => {
  test("a running engine is reloaded, a stopped or failed one started", () => {
    expect(engineRun(unit())).toBe("running")
    expect(engineRun(unit({ activeState: "reloading" }))).toBe("running")
    expect(engineRun(unit({ activeState: "inactive", subState: "dead" }))).toBe("stopped")
    expect(engineRun(unit({ activeState: "failed", subState: "failed" }))).toBe("stopped")
  })

  // systemd between states, including the wait before it restarts a crash.
  test("an engine on its way up or down is neither", () => {
    expect(engineRun(unit({ activeState: "activating", subState: "auto-restart" }))).toBe(
      "changing",
    )
    expect(engineRun(unit({ activeState: "deactivating", subState: "stop-sigterm" }))).toBe(
      "changing",
    )
  })

  test("a stopped engine says what systemd's words mean", () => {
    expect(stoppedLabel(unit({ activeState: "inactive", subState: "dead" }))).toBe("not running")
    expect(stoppedLabel(unit({ activeState: "failed", subState: "failed" }))).toBe("failed")
    expect(stoppedLabel(unit({ activeState: "activating", subState: "auto-restart" }))).toBe(
      "activating (auto-restart)",
    )
  })
})

describe("whether it comes back after a reboot", () => {
  test("an enabled unit starts at boot and needs nothing", () => {
    expect(bootState(unit())).toEqual({ label: "starts at boot", warn: false, canEnable: false })
  })

  // enabled-runtime lives under /run, which a reboot empties.
  test("a disabled unit, or one enabled for this boot only, does not", () => {
    for (const state of ["disabled", "enabled-runtime"]) {
      expect(bootState(unit({ unitFileState: state }))).toEqual({
        label: "won't start at boot",
        warn: true,
        canEnable: true,
      })
    }
  })

  // `systemctl enable` refuses a masked unit, so it is not offered.
  test("a masked unit cannot start at all, and enabling it would fail", () => {
    expect(bootState(unit({ unitFileState: "masked" }))).toEqual({
      label: "masked, so it cannot start",
      warn: true,
      canEnable: false,
    })
    expect(isMasked(unit({ unitFileState: "masked-runtime" }))).toBe(true)
    expect(isMasked(unit())).toBe(false)
  })

  test("a state that does not answer the question is not claimed either way", () => {
    for (const state of ["static", "indirect", "generated", "linked", "transient", "", undefined]) {
      expect(bootState(unit({ unitFileState: state }))).toBeUndefined()
    }
  })
})

describe("why a failed engine failed", () => {
  test("the result is said in words, with systemd's own and its restarts beside it", () => {
    expect(
      failureSummary("nginx", unit({ activeState: "failed", result: "exit-code", restarts: 3 })),
    ).toEqual({
      title: "nginx exited with an error",
      facts: ["result exit-code", "restarted 3 times by systemd"],
    })
    expect(
      failureSummary(
        "nginx",
        unit({ activeState: "failed", result: "start-limit-hit", restarts: 1 }),
      ).title,
    ).toBe("nginx failed to start too often, so systemd stopped trying")
    expect(failureSummary("nginx", unit({ result: "oom-kill" })).title).toBe(
      "nginx was killed for running out of memory",
    )
  })

  test("a result it has no words for is still named, and none says only that it failed", () => {
    expect(failureSummary("Caddy", unit({ result: "cgroup-oddity" }))).toEqual({
      title: "Caddy failed",
      facts: ["result cgroup-oddity"],
    })
    expect(failureSummary("nginx", unit({ result: "success", restarts: 0 }))).toEqual({
      title: "nginx failed",
      facts: [],
    })
    expect(failureSummary("nginx", unit({ restarts: 1 })).facts).toEqual([
      "restarted once by systemd",
    ])
  })
})

describe("a start or restart the config test refused", () => {
  const validation = {
    valid: false,
    output: 'nginx: [emerg] unknown directive "frobnicate" in /etc/nginx/sites-enabled/app:3',
    command: "nginx -t",
    diagnostics: [
      {
        level: "emerg",
        message: 'unknown directive "frobnicate"',
        file: "/etc/nginx/sites-enabled/app",
        line: 3,
      },
    ],
  }
  const message = `nginx was not restarted: its configuration test failed.\n${validation.output}`

  test("carries the test beside the server's sentence", () => {
    const err = new ApiError(422, "invalid_config", message, undefined, {
      body: { error: { code: "invalid_config", message }, validation },
    })
    const refusal = refusalOf(err)
    expect(refusal).toEqual({ message, validation })
    expect(refusalHeadline(refusal)).toBe("nginx was not restarted: its configuration test failed.")
  })

  test("without the test it is still the sentence", () => {
    const err = new ApiError(422, "invalid_config", message, undefined, { body: { error: {} } })
    expect(refusalOf(err)).toEqual({ message, validation: undefined })
  })

  test("any other failure is not a refusal", () => {
    expect(refusalOf(new ApiError(502, "command_failed", "systemctl exited 1"))).toBeUndefined()
    expect(refusalOf(new Error("network down"))).toBeUndefined()
    expect(refusalOf(undefined)).toBeUndefined()
  })
})

describe("which of the test's files the editor may open", () => {
  const roots = ["/etc/nginx", "/etc/caddy/"]

  test("a file under the proxy's own directories", () => {
    expect(openableFile("/etc/nginx/sites-available/app", roots)).toBe(true)
    expect(openableFile("/etc/caddy/Caddyfile", roots)).toBe(true)
  })

  test("not the directory's neighbour, a relative path, a climb out, or no file at all", () => {
    expect(openableFile("/etc/nginx-old/nginx.conf", roots)).toBe(false)
    expect(openableFile("/usr/share/nginx/modules/mod-stream.conf", roots)).toBe(false)
    expect(openableFile("sites-enabled/app", roots)).toBe(false)
    expect(openableFile("/etc/nginx/../shadow", roots)).toBe(false)
    expect(openableFile(undefined, roots)).toBe(false)
    expect(openableFile("/etc/nginx/nginx.conf", [""])).toBe(false)
  })
})

describe("a diagnostic's dot", () => {
  test("errors are critical, warnings warn, the rest are notes", () => {
    for (const level of ["emerg", "alert", "crit", "error", "panic", "fatal"]) {
      expect(diagnosticVerdict(level)).toBe("critical")
    }
    expect(diagnosticVerdict("warn")).toBe("warning")
    expect(diagnosticVerdict("notice")).toBe("notice")
    expect(diagnosticVerdict("info")).toBe("notice")
  })
})
