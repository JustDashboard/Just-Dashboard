import { describe, expect, test } from "bun:test"
import { transferKind, transferWord } from "./backups-model"
import {
  blockers,
  endedWords,
  isUpgradeReading,
  keptProblem,
  keptWords,
  lastWords,
  reportOf,
  reportWords,
  upgradeJobKind,
  upgradeResultOf,
  upgradeSteps,
} from "./settings-version-model"

const minutesAgo = (minutes) => new Date(Date.now() - minutes * 60_000).toISOString()

const check = (id, verdict, title = id) => ({ id, verdict, title, detail: `${title} detail` })

const report = (checks, over = {}) => ({
  from: "16",
  to: "17",
  server: "16.15",
  target: "17.11",
  image: "postgres:17-alpine",
  bytes: 48_234_496,
  databases: [{ name: "shop_main", bytes: 41_000_000, template: false }],
  checks,
  ready: checks.every((one) => one.verdict !== "blocker"),
  checkedAt: minutesAgo(3),
  ...over,
})

const record = (over = {}) => ({
  id: 4,
  state: "upgraded",
  engine: "postgres",
  fromVersion: "16",
  toVersion: "17",
  fromImage: "postgres:16-alpine",
  toImage: "postgres:17-alpine",
  container: "shop-db",
  previous: "shop-db-pg16",
  previousState: "exited",
  previousVolume: "shop-db-data",
  volume: "shop-db-pg17-data",
  safetyDump: "shop_main-20261017-0712.dump",
  startedBy: "operator",
  startedAt: minutesAgo(140),
  upgradedAt: minutesAgo(132),
  ...over,
})

const lines = (value) => [
  { seq: 1, stream: "status", text: "Reading shop-db" },
  { seq: 2, stream: "result", text: JSON.stringify(value) },
]

describe("an upgrade's jobs", () => {
  test("are told apart by the end of their kind, and are transfers of the connection", () => {
    expect(upgradeJobKind({ kind: "database.transfer.1.major-upgrade" })).toBe("upgrade")
    expect(upgradeJobKind({ kind: "database.transfer.1.major-upgrade-check" })).toBe("check")
    expect(upgradeJobKind({ kind: "database.transfer.1.major-upgrade-revert" })).toBe("revert")
    expect(upgradeJobKind({ kind: "database.transfer.1.backup" })).toBeUndefined()
    // The Backups page shows one running and names it, since a dump waits for it.
    expect(transferKind({ kind: "database.transfer.1.major-upgrade" })).toBe("major-upgrade")
    expect(transferWord("major-upgrade")).toBe("Upgrade")
    expect(transferWord("major-upgrade-revert")).toBe("Revert")
    // A package upgrade is not a database's.
    expect(transferKind({ kind: "packages.upgrade" })).toBeUndefined()
  })

  test("end on a line the page reads as their report or their result", () => {
    const checked = report([check("version", "ok")])
    expect(reportOf(lines(checked))).toEqual(checked)
    expect(reportOf(lines({ file: "x.dump" }))).toBeUndefined()
    expect(reportOf([{ seq: 1, stream: "stdout", text: "no result yet" }])).toBeUndefined()
    const done = { from: "16", to: "17", previous: "shop-db-pg16", summary: "1 database matches" }
    expect(upgradeResultOf(lines(done))).toEqual(done)
    expect(upgradeResultOf(lines(checked))).toBeUndefined()
  })

  test("are announced in words that say what happened to the server", () => {
    expect(endedWords("upgrade", { status: "succeeded" }, "shop").title).toBe("shop is upgraded")
    expect(endedWords("revert", { status: "succeeded" }, "shop").title).toBe(
      "shop is back on its old release",
    )
    expect(endedWords("upgrade", { status: "failed", error: "port taken" }, "shop")).toEqual({
      tone: "error",
      title: "The upgrade of shop failed",
      description: "port taken",
    })
    expect(endedWords("check", { status: "cancelled" }, "shop")).toEqual({
      tone: "info",
      title: "The check was stopped",
    })
  })
})

describe("what the checks found", () => {
  test("is read off the report in the head's few words", () => {
    expect(reportWords(undefined)).toBe("Not checked yet")
    expect(reportWords(report([check("version", "ok")]))).toBe("Checked 3m ago · ready")
    expect(reportWords(report([check("version", "ok"), check("sessions", "warning")]))).toBe(
      "Checked 3m ago · ready, 1 thing to know",
    )
    const stopped = report([check("extensions", "blocker"), check("disk", "blocker")])
    expect(reportWords(stopped)).toBe("Checked 3m ago · 2 things to fix first")
    expect(blockers(stopped).map((one) => one.id)).toEqual(["extensions", "disk"])
  })

  test("and what the upgrade will do is said before it is pressed, with the size it copies", () => {
    const steps = upgradeSteps(
      { label: "PostgreSQL", from: "16.15", to: "17", name: "shop", database: "shop_main" },
      report([]),
    )
    expect(steps).toHaveLength(4)
    expect(steps[0]).toBe("A dump of shop_main is taken first and kept with its other dumps.")
    expect(steps[1]).toContain("copied into it: 46.0 MB of databases.")
    expect(steps[2]).toContain("shop takes reads only")
    expect(steps[3]).toBe(
      "PostgreSQL 16.15 is kept, stopped, with its data, until you finish the upgrade or revert it.",
    )
  })
})

describe("the server kept after an upgrade", () => {
  test("says when and by whom the upgrade was made", () => {
    expect(keptWords(record())).toBe("Upgraded 2h ago by operator")
    expect(keptWords(record({ upgradedAt: undefined, startedBy: undefined }))).toBe("Upgraded")
  })

  test("is no way back once it is gone or has been started by hand", () => {
    expect(keptProblem(record())).toBeUndefined()
    expect(keptProblem(record({ previousState: "missing" }))).toContain(
      "nothing to revert to. Finish the upgrade to close it.",
    )
    expect(keptProblem(record({ previousState: "running" }))).toContain("has been started by hand")
  })

  test("and an upgrade that ended badly or was taken back is a line of its own", () => {
    const failed = lastWords(
      record({
        state: "failed",
        endedAt: minutesAgo(20),
        error: "the copy does not match the old server: app: public.orders has 119 rows, not 120",
      }),
      "PostgreSQL",
    )
    expect(failed?.tone).toBe("danger")
    expect(failed?.title).toBe("The upgrade to PostgreSQL 17 failed 20m ago and was undone")
    expect(failed?.body).toContain("The safety dump shop_main-20261017-0712.dump is kept")
    const reverted = lastWords(record({ state: "reverted", endedAt: minutesAgo(5) }), "PostgreSQL")
    expect(reverted?.title).toBe("Reverted 5m ago to PostgreSQL 16")
    expect(lastWords(record({ state: "finished", endedAt: minutesAgo(5) }), "PostgreSQL")).toBe(
      undefined,
    )
    expect(lastWords(undefined, "PostgreSQL")).toBeUndefined()
  })
})

test("a reading in another shape is not taken for the route's", () => {
  expect(isUpgradeReading({ offered: false, targets: [], reason: "x" })).toBe(true)
  expect(isUpgradeReading([])).toBe(false)
  expect(isUpgradeReading({ offered: true })).toBe(false)
})
