import { describe, expect, test } from "bun:test"
import { rehearsalVerdict, replayVerdict } from "./run-verdicts"

const replay = (overrides) => ({
  replayed: true,
  read: 400,
  eligible: 120,
  requests: 50,
  answered: 50,
  drifted: 0,
  driftPercent: 0,
  maxStatusDrift: 2,
  latencyFactor: 2,
  latencyRecorded: true,
  latencyJudged: true,
  recordedP95: 42,
  candidateP95: 38,
  verdict: { code: "within_drift", tripped: false, reason: "" },
  drifts: [],
  concurrency: 4,
  timeoutSeconds: 60,
  timedOut: false,
  durationMs: 1840,
  ...overrides,
})
const texts = (verdict) => verdict.facts.map((fact) => fact.text)

describe("replayVerdict", () => {
  test("a clean replay says every request matched, then what was measured", () => {
    const verdict = replayVerdict(replay())
    expect(verdict.tone).toBe("success")
    expect(verdict.title).toBe("All 50 replayed requests matched the record")
    expect(texts(verdict)).toEqual([
      "None drifted, 2% allowed",
      "19 in 20 inside 38 ms, 42 ms recorded",
      "Took 1.8 s, 4 at a time",
      "From 120 recorded requests",
    ])
    expect(verdict.facts.every((fact) => !fact.tone)).toBe(true)
  })

  test("a drift past the line leads with how many matched, in the failure's colour", () => {
    const verdict = replayVerdict(
      replay({
        drifted: 3,
        driftPercent: 6,
        verdict: { code: "status_drift", tripped: true, reason: "" },
      }),
    )
    expect(verdict.tone).toBe("danger")
    expect(verdict.title).toBe("47 of 50 replayed requests matched the record")
    expect(verdict.facts[0]).toEqual({
      key: "drift",
      text: "3 drifted (6%), 2% allowed",
      tone: "danger",
    })
  })

  test("a drift inside the line is amber and the release went on", () => {
    const verdict = replayVerdict(replay({ drifted: 1, driftPercent: 2 }))
    expect(verdict.tone).toBe("warning")
    expect(verdict.facts[0].tone).toBe("warning")
  })

  test("a slow candidate colours its time, and one request is not a plural", () => {
    const slow = replayVerdict(
      replay({ candidateP95: 190, verdict: { code: "latency", tripped: true, reason: "" } }),
    )
    expect(slow.facts[1]).toEqual({
      key: "latency",
      text: "19 in 20 inside 190 ms, 42 ms recorded",
      tone: "danger",
    })
    expect(replayVerdict(replay({ requests: 1, eligible: 1 })).title).toBe(
      "The 1 replayed request matched the record",
    )
  })

  test("a speed that was not compared says why, and a replay out of time says so", () => {
    const verdict = replayVerdict(
      replay({ latencyJudged: false, latencyRecorded: false, timedOut: true, durationMs: 60000 }),
    )
    expect(texts(verdict).slice(1, 3)).toEqual([
      "Speed not judged: the ingress records no times",
      "Took 60.0 s, out of its 60 s",
    ])
    expect(verdict.facts[2].tone).toBe("warning")
  })
})

const rehearsal = (overrides) => ({
  rehearsed: true,
  database: { connectionId: 5, name: "orders-db", engine: "postgres", database: "app", image: "" },
  source: { kind: "dump", backupRunId: 9, takenAt: "2026-10-27T02:15:00Z", bytes: 4096 },
  restoreMs: 4400,
  tasks: [{ name: "migrate", durationMs: 2100, exitCode: 0 }],
  locks: {
    read: true,
    samples: 12,
    maxWaiting: 0,
    waitedMs: 0,
    held: [{ relation: "items", mode: "AccessExclusiveLock", heldMs: 2000 }],
  },
  cleanedUp: true,
  totalMs: 9000,
  ...overrides,
})
const at = (iso) => iso.slice(11, 16)
const full = (iso) => iso

describe("rehearsalVerdict", () => {
  test("a passing rehearsal names the task and the copy, then the copy's story", () => {
    const verdict = rehearsalVerdict(rehearsal(), at, full)
    expect(verdict.tone).toBe("success")
    expect(verdict.title).toBe("migrate passed against a copy of orders-db")
    expect(texts(verdict)).toEqual([
      "Restored in 4.4 s from the dump of 02:15",
      "Longest lock 2.0 s, ACCESS EXCLUSIVE",
      "Copy removed with its network",
    ])
    expect(verdict.facts[0].basis).toBe("From the backup of 2026-10-27T02:15:00Z")
  })

  test("a failed task leads, with its exit code first among the facts", () => {
    const verdict = rehearsalVerdict(
      rehearsal({
        tasks: [
          { name: "seed", durationMs: 100, exitCode: 0 },
          { name: "migrate", durationMs: 900, exitCode: 1, error: "boom" },
        ],
      }),
      at,
      full,
    )
    expect(verdict.tone).toBe("danger")
    expect(verdict.title).toBe("migrate failed against a copy of orders-db")
    expect(verdict.facts[0]).toEqual({ key: "exit", text: "Exit 1", tone: "danger" })
  })

  test("several tasks are counted, no lock and a copy left behind are said", () => {
    const verdict = rehearsalVerdict(
      rehearsal({
        tasks: [
          { name: "seed", durationMs: 100, exitCode: 0 },
          { name: "migrate", durationMs: 900, exitCode: 0 },
        ],
        source: { kind: "pitr" },
        locks: { read: true, samples: 3, maxWaiting: 0, waitedMs: 0, held: [] },
        cleanedUp: false,
      }),
      at,
      full,
    )
    expect(verdict.tone).toBe("warning")
    expect(verdict.title).toBe("2 release tasks passed against a copy of orders-db")
    expect(texts(verdict)).toEqual([
      "Restored in 4.4 s from the archive",
      "No table locked",
      "Copy left until the next start",
    ])
    expect(
      texts(
        rehearsalVerdict(
          rehearsal({ source: undefined, locks: { ...rehearsal().locks, read: false, held: [] } }),
          at,
          full,
        ),
      ).slice(0, 2),
    ).toEqual(["Restored in 4.4 s from a backup", "Locks not read"])
  })
})
