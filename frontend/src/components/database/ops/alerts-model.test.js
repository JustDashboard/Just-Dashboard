import { describe, expect, test } from "bun:test"
import {
  ALERT_KINDS,
  ALERT_KIND_ORDER,
  cooldownWords,
  draftBody,
  draftErrors,
  draftFor,
  draftRule,
  durationWords,
  eventOutcome,
  focusOf,
  isBackupKind,
  isFiring,
  meterOf,
  sentence,
  withKind,
} from "./alerts-model"
import catalogue from "../../../../../backend/internal/api/testdata/database-drivers.json"

const rule = (over = {}) => ({
  id: 3,
  connectionId: 1,
  kind: "disk",
  threshold: 90,
  minutes: 5,
  cooldownMinutes: 30,
  notifyRecovery: true,
  channels: [],
  enabled: true,
  createdAt: "2026-10-17T03:00:00Z",
  updatedAt: "2026-10-17T03:00:00Z",
  ...over,
})

const subject = (state, over = {}) => ({
  ruleId: 3,
  subject: "connection:1",
  connectionId: 1,
  label: "shop",
  state,
  reading: 50,
  detail: "",
  ...over,
})

describe("a rule as a sentence", () => {
  test("each kind reads as its label and its predicate", () => {
    expect(sentence(rule())).toBe("Disk filling up: above 90% for 5 min")
    expect(sentence(rule({ minutes: 0 }))).toBe("Disk filling up: above 90%")
    expect(sentence(rule({ kind: "down", threshold: 0, minutes: 2 }))).toBe(
      "Server down: no answer for 2 min",
    )
    expect(sentence(rule({ kind: "connections", threshold: 80 }))).toBe(
      "Connections near the limit: above 80% of the limit for 5 min",
    )
    expect(sentence(rule({ kind: "replication", threshold: 60, minutes: 2 }))).toBe(
      "Replication behind: more than 60 s behind for 2 min, or stopped",
    )
    expect(sentence(rule({ kind: "backup_failed", threshold: 0, minutes: 0 }))).toBe(
      "Backup failed: the last run of a backup job fails",
    )
    expect(sentence(rule({ kind: "backup_missed", threshold: 0, minutes: 90 }))).toBe(
      "Backup missed: no successful run 1 h 30 min after one is due",
    )
  })

  test("minutes read as the shortest words that say them", () => {
    expect(durationWords(1)).toBe("1 min")
    expect(durationWords(60)).toBe("1 h")
    expect(durationWords(150)).toBe("2 h 30 min")
    expect(durationWords(1440)).toBe("1 day")
    expect(durationWords(10080)).toBe("7 days")
    expect(cooldownWords(0)).toBe("every time it crosses")
    expect(cooldownWords(30)).toBe("at most once every 30 min")
  })

  test("the vocabulary is the server's", () => {
    const served = new Set(catalogue.flatMap((driver) => driver.capabilities.alertKinds))
    expect(ALERT_KIND_ORDER.toSorted()).toEqual([...served].toSorted())
    expect(isBackupKind("backup_missed")).toBe(true)
    expect(isBackupKind("disk")).toBe(false)
  })
})

describe("the form's draft", () => {
  test("a new rule starts from its kind's own line and minutes", () => {
    const draft = draftFor(undefined, "connections", "this")
    expect(draft).toMatchObject({
      threshold: "80",
      minutes: "5",
      cooldown: "30",
      notifyRecovery: true,
    })
    // Another kind takes its own line: a percentage is not seconds of lag.
    const lag = withKind({ ...draft, threshold: "95", channels: [4] }, "replication")
    expect(lag).toMatchObject({ kind: "replication", threshold: "60", minutes: "2", channels: [4] })
  })

  test("an existing rule is drafted as it is saved", () => {
    expect(draftFor(rule({ connectionId: null, channels: [2] }), "down", "this")).toEqual({
      kind: "disk",
      scope: "every",
      threshold: "90",
      minutes: "5",
      cooldown: "30",
      notifyRecovery: true,
      channels: [2],
    })
  })

  test("the server's ranges are refused before anything is sent", () => {
    const draft = draftFor(undefined, "disk", "this")
    expect(draftErrors(draft)).toEqual({})
    expect(Object.keys(draftErrors({ ...draft, threshold: "100" }))).toEqual(["threshold"])
    expect(Object.keys(draftErrors({ ...draft, threshold: "" }))).toEqual(["threshold"])
    expect(Object.keys(draftErrors({ ...draft, minutes: "2.5" }))).toEqual(["minutes"])
    expect(Object.keys(draftErrors({ ...draft, cooldown: "-1" }))).toEqual(["cooldown"])
    const down = draftFor(undefined, "down", "this")
    expect(Object.keys(draftErrors({ ...down, minutes: "0" }))).toEqual(["minutes"])
    // A kind with no line has nothing to refuse there.
    expect(
      draftErrors({ ...draftFor(undefined, "backup_failed", "this"), threshold: "x" }),
    ).toEqual({})
  })

  test("it is sent with only the figures its kind has", () => {
    const disk = draftBody(draftFor(undefined, "disk", "this"), 7)
    expect(disk).toEqual({
      connectionId: 7,
      kind: "disk",
      threshold: 90,
      minutes: 5,
      cooldownMinutes: 30,
      notifyRecovery: true,
      channels: [],
      enabled: true,
    })
    const failed = draftBody(
      { ...draftFor(undefined, "backup_failed", "every"), cooldown: "0" },
      7,
      false,
    )
    expect(failed).toEqual({
      connectionId: null,
      kind: "backup_failed",
      cooldownMinutes: 0,
      notifyRecovery: true,
      channels: [],
      enabled: false,
    })
    expect(draftRule({ ...draftFor(undefined, "disk", "this"), threshold: "abc" })).toEqual({
      kind: "disk",
      threshold: 0,
      minutes: 5,
    })
  })
})

describe("where a rule stands", () => {
  test("the row is about its worst subject", () => {
    const quiet = subject("quiet", { label: "weekly" })
    const firing = subject("firing", { label: "nightly" })
    expect(focusOf([quiet, subject("unknown"), firing])?.label).toBe("nightly")
    expect(focusOf([])).toBeUndefined()
    expect(isFiring([quiet, subject("held")])).toBe(true)
    expect(isFiring([quiet, subject("pending")])).toBe(false)
  })

  test("the meter draws the reading against the line, where the kind has one", () => {
    expect(meterOf(rule(), subject("quiet", { reading: 45 }))).toEqual({
      value: 45,
      mark: 90,
      label: "45% used against a 90% line",
    })
    const lag = meterOf(
      rule({ kind: "replication", threshold: 60 }),
      subject("firing", { reading: 150 }),
    )
    expect(lag.mark).toBeCloseTo((60 / 165) * 100)
    expect(lag.label).toBe("150 s behind against a 60 s line")
    expect(
      meterOf(rule({ kind: "replication", threshold: 60 }), subject("firing", { reading: -1 })),
    ).toBeUndefined()
    expect(
      meterOf(rule({ kind: "down", threshold: 0 }), subject("firing", { reading: 1 })),
    ).toBeUndefined()
    expect(meterOf(rule(), subject("unknown"))).toBeUndefined()
  })

  test("a reading reads in its kind's words", () => {
    expect(ALERT_KINDS.down.read(1)).toBe("not answering")
    expect(ALERT_KINDS.disk.read(4.25)).toBe("4.3% used")
    expect(ALERT_KINDS.replication.read(-1)).toBe("stopped")
    expect(ALERT_KINDS.backup_failed.read(0)).toBe("last run succeeded")
  })

  test("what became of a message", () => {
    expect(eventOutcome({ event: "fired", delivered: 2, failed: 0 })).toBe("told 2 channels")
    expect(eventOutcome({ event: "fired", delivered: 1, failed: 1 })).toBe(
      "told 1 channel, 1 failed",
    )
    expect(eventOutcome({ event: "fired", delivered: 0, failed: 0 })).toBe("no channel to tell")
    expect(eventOutcome({ event: "fired", delivered: 0, failed: 0, kept: "cooldown" })).toBe(
      "kept back by the cooldown",
    )
    expect(eventOutcome({ event: "recovered", delivered: 0, failed: 0, kept: "recovery" })).toBe(
      "not told: the rule tells only when it fires",
    )
  })
})
