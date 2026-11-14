import { expect, test } from "bun:test"
import {
  certificateWords,
  dockerWords,
  gitWords,
  packageWords,
  remoteWords,
  unitWords,
  versionWords,
} from "./service-words"

test("Docker says what runs and what stopped, in one sentence", () => {
  expect(dockerWords(11, 9)).toEqual({
    sentence: "9 containers running, 2 stopped",
    state: { tone: "running", label: "Running" },
  })
  expect(dockerWords(1, 1).sentence).toBe("1 container running")
  expect(dockerWords(3, 0)).toEqual({
    sentence: "3 containers, none running",
    state: { tone: "stopped", label: "Stopped" },
  })
  expect(dockerWords(0, 0).sentence).toBe("No containers yet")
})

test("certificates lead with the one that expired, else the next renewal", () => {
  expect(certificateWords(3, { daysLeft: 12, expired: false })).toEqual({
    sentence: "3 certificates, the next renews in 12 days",
    state: { tone: "running", label: "Valid" },
  })
  expect(certificateWords(1, { daysLeft: 1, expired: false }).sentence).toBe(
    "1 certificate, renews in 1 day",
  )
  expect(certificateWords(3, { daysLeft: -2, expired: true })).toEqual({
    sentence: "3 certificates, one expired",
    state: { tone: "danger", label: "Expired" },
  })
  expect(certificateWords(3, { daysLeft: 12, expired: false, expiring: true }).state).toEqual({
    tone: "warning",
    label: "Expiring",
  })
  expect(certificateWords(0, undefined).state.label).toBe("None")
})

const report = (packages, more = {}) => ({
  packages: Array.from({ length: packages }),
  securityCount: 0,
  securityFiltering: true,
  rebootRequired: false,
  ...more,
})

test("packages say what waits, what of it is security, and a restart that is owed", () => {
  expect(packageWords(report(7, { securityCount: 2 }))).toEqual({
    sentence: "7 updates waiting, 2 for security",
    state: { tone: "warning", label: "Security updates" },
  })
  expect(packageWords(report(4))).toEqual({
    sentence: "4 updates waiting, none for security",
    state: { tone: "notice", label: "Updates" },
  })
  // A manager that cannot tell a security update from another says nothing of it.
  expect(packageWords(report(4, { securityFiltering: false })).sentence).toBe("4 updates waiting")
  expect(packageWords(report(0))).toEqual({
    sentence: "Nothing to update",
    state: { tone: "running", label: "Up to date" },
  })
  expect(packageWords(report(0, { rebootRequired: true }))).toEqual({
    sentence: "Nothing to update, restart needed",
    state: { tone: "warning", label: "Restart needed" },
  })
})

test("a failed unit leads the sentence", () => {
  expect(unitWords({ running: 6, failed: 1 })).toEqual({
    sentence: "1 failed, 6 services running",
    state: { tone: "danger", label: "Failed" },
  })
  expect(unitWords({ running: 6, failed: 0 }).sentence).toBe("6 services running, none failed")
})

const repo = (more = {}) => ({ dirty: false, behind: 0, conflicts: 0, ...more })

test("Git says what is waiting in the checkouts, a conflict first", () => {
  expect(gitWords([repo({ dirty: true }), repo({ behind: 2 }), repo(), repo()])).toEqual({
    sentence: "4 repositories, 1 uncommitted, 1 behind",
    state: { tone: "notice", label: "Uncommitted" },
  })
  expect(gitWords([repo({ conflicts: 3 })])).toEqual({
    sentence: "1 repository, 1 in conflict, all committed",
    state: { tone: "warning", label: "In conflict" },
  })
  expect(gitWords([repo()]).state.label).toBe("Clean")
  expect(gitWords([]).sentence).toBe("No repositories yet")
})

test("remote computers say which are open, or whether one could be", () => {
  expect(remoteWords(2, 1, true).sentence).toBe("2 computers, 1 open now")
  expect(remoteWords(2, 0, true).state.label).toBe("Ready")
  expect(remoteWords(2, 0, false)).toEqual({
    sentence: "2 computers, the engine is not running",
    state: { tone: "stopped", label: "Stopped" },
  })
  expect(remoteWords(0, 0, false).sentence).toBe("No computers yet")
})

test("the dashboard's own version says how far behind it is", () => {
  expect(versionWords("0.7.1", 0, false).sentence).toBe("Version 0.7.1, the newest")
  expect(versionWords("0.7.1", 2, false)).toEqual({
    sentence: "Version 0.7.1, 2 releases behind",
    state: { tone: "notice", label: "Update" },
  })
  expect(versionWords("0.7.1", 1, true)).toEqual({
    sentence: "Version 0.7.1, 1 release behind with a breaking change",
    state: { tone: "warning", label: "Update" },
  })
})
