import { expect, test } from "bun:test"
import { BOOT_STATE, changeStatus, RUNTIME_STATE, WATCHDOG_STATE } from "./change-reading"

const saved = {
  phase: "saved",
  boot: "enabled",
  runtime: "applied",
  persistence: "written",
  watchdog: "completed",
}

test("saved host status says saved without claiming connection confirmation", () => {
  expect(changeStatus(saved)).toEqual({ label: "Saved", tone: "running" })
})

test("failed and unreadable recovery outcomes remain visible even after persistence succeeds", () => {
  for (const change of [
    { ...saved, boot: "failed" },
    { ...saved, watchdog: "failed_to_arm" },
    { ...saved, phase: "unreadable" },
    { ...saved, recoveryErrors: ["Could not restore files"] },
  ])
    expect(changeStatus(change).tone).toBe("danger")
  expect(BOOT_STATE.failed).toBe("Restore unit could not be enabled")
  expect(RUNTIME_STATE.undo_attempted).toContain("verify current state")
})

test("an armed or unsupported watchdog never looks like a completed protected change", () => {
  expect(changeStatus({ ...saved, phase: "runtime_applied", watchdog: "armed" }).tone).toBe(
    "warning",
  )
  expect(changeStatus({ ...saved, watchdog: "unsupported" }).tone).toBe("warning")
  expect(WATCHDOG_STATE.unsupported).toContain("unavailable")
})

test("a saved label with unknown runtime or persistence never gets a success tone", () => {
  expect(changeStatus({ ...saved, runtime: "unknown" }).tone).toBe("warning")
  expect(changeStatus({ ...saved, persistence: "unknown" }).tone).toBe("warning")
})

test("runtime-only changes report applied rather than saved for boot", () => {
  expect(changeStatus({ ...saved, persistence: "not_applicable", boot: "not_applicable" })).toEqual(
    { label: "Runtime applied", tone: "running" },
  )
})
