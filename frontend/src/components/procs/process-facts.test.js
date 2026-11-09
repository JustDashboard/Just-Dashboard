import { describe, expect, test } from "bun:test"
import {
  oomWords,
  processHref,
  processWarnings,
  signalEffect,
  signalSpec,
  staleCode,
} from "./process-facts"

const masks = (over = {}) => ({
  ready: true,
  caught: [],
  ignored: [],
  blocked: [],
  pending: [],
  ...over,
})

// The Control tab says what a signal will do to this process before it is
// sent. A hang-up to a daemon that handles it is a reload; to one that does
// not, it is the end of it — and the button looks the same either way.
describe("signalEffect", () => {
  test("a handled hang-up is the program's own, an unhandled one ends it", () => {
    const hup = signalSpec("SIGHUP")
    expect(signalEffect(hup, "nginx", masks({ caught: ["SIGHUP"] })).word).toBe("handled")
    const unhandled = signalEffect(hup, "worker", masks())
    expect(unhandled.word).toBe("ends it")
    expect(unhandled.tone).toBe("danger")
  })

  test("an ignored signal changes nothing, and Kill and Pause cannot be caught", () => {
    expect(signalEffect(signalSpec("SIGINT"), "x", masks({ ignored: ["SIGINT"] })).word).toBe(
      "ignored",
    )
    expect(signalEffect(signalSpec("SIGKILL"), "x", masks({ caught: ["SIGKILL"] })).word).toBe(
      "always ends it",
    )
    expect(signalEffect(signalSpec("SIGSTOP"), "x", undefined).word).toBe("always pauses it")
  })

  test("gunicorn's worker signals pause anything that does not handle them", () => {
    expect(signalEffect(signalSpec("SIGTTIN"), "python3", masks()).word).toBe("pauses it")
  })

  test("without the masks it says the default applies unless handled", () => {
    expect(signalEffect(signalSpec("SIGTERM"), "x", { ...masks(), ready: false }).word).toBe(
      "ends it unless handled",
    )
  })
})

describe("oomWords", () => {
  test("reads the score and the bias as how soon it would be ended", () => {
    expect(oomWords(0, -1000).word).toBe("never ended for memory")
    expect(oomWords(700, 0).tone).toBe("warning")
    expect(oomWords(30, 0).word).toBe("an unlikely choice")
  })
})

describe("staleCode", () => {
  test("names a replaced binary and the libraries an upgrade swapped", () => {
    expect(staleCode({ exeDeleted: false, libraries: [{ path: "/lib/a.so" }] })).toBeNull()
    const stale = staleCode({
      exeDeleted: true,
      libraries: [{ path: "/lib/libssl.so.3", deleted: true }, { path: "/lib/a.so" }],
    })
    expect(stale.binary).toBe(true)
    expect(stale.libraries).toHaveLength(1)
  })
})

test("a process's address carries its tab, and Overview is the address itself", () => {
  expect(processHref(812)).toBe("/processes/812")
  expect(processHref(812, "overview")).toBe("/processes/812")
  expect(processHref(812, "files")).toBe("/processes/812?tab=files")
})

describe("processWarnings", () => {
  const row = {
    state: "sleeping",
    manager: "pm2",
    fdReady: true,
    fileDescriptors: 1000,
    openFilesLimit: 1024,
  }
  const inspect = { files: { deleted: 1 }, signals: { pending: [], blocked: [] } }
  test("warns of descriptors near the limit and deleted files held open", () => {
    expect(processWarnings(row, inspect).map((w) => w.key)).toEqual(["fds", "deleted"])
  })
  test("says nothing of either for a zombie or a kernel thread", () => {
    expect(processWarnings({ ...row, state: "zombie" }, inspect)).toEqual([])
    expect(processWarnings({ ...row, manager: "kernel" }, inspect)).toEqual([])
  })
})
