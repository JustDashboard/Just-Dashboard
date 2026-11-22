import { describe, expect, test } from "bun:test"
import {
  alertCover,
  backupCover,
  namesLine,
  nightlyJob,
  runsByItself,
  WATCHED_KINDS,
} from "./protection"

function entry(id, name, overrides = {}) {
  return { id, name, ok: true, state: "running", driver: "postgres", ...overrides }
}

function job(id, overrides = {}) {
  return {
    id,
    name: `job ${id}`,
    sources: [],
    excludes: [],
    schedule: "0 3 * * *",
    enabled: true,
    databaseDumps: [],
    ...overrides,
  }
}

function rule(id, overrides = {}) {
  return { id, connectionId: null, kind: "down", channels: [], enabled: true, ...overrides }
}

function channel(id, name, enabled = true) {
  return { id, name, enabled }
}

const FLEET = [entry(1, "shop"), entry(2, "blog"), entry(3, "cache", { driver: "redis" })]
const everyone = () => true

describe("backupCover", () => {
  test("a fleet with no job has every database uncovered and nothing to join", () => {
    const cover = backupCover(FLEET, [], everyone)
    expect(cover.uncovered.map((one) => one.name)).toEqual(["shop", "blog", "cache"])
    expect(cover.jobs).toEqual([])
    expect(cover.home).toBeUndefined()
  })

  test("only a job that runs by itself covers its databases", () => {
    const jobs = [
      job(1, { databaseDumps: [1] }),
      job(2, { databaseDumps: [2], enabled: false }),
      job(3, { databaseDumps: [3], schedule: "" }),
      job(4, { databaseDumps: [2, 3], imported: true }),
    ]
    const cover = backupCover(FLEET, jobs, everyone)
    expect(cover.uncovered.map((one) => one.name)).toEqual(["blog", "cache"])
    expect(cover.jobs.map((one) => one.id)).toEqual([1])
    expect(jobs.map(runsByItself)).toEqual([true, false, false, false])
  })

  test("a database the dashboard cannot dump, or cannot open, is not counted against it", () => {
    const fleet = [...FLEET, entry(4, "legacy", { state: "broken" }), entry(5, "files")]
    const cover = backupCover(fleet, [], (one) => one.name !== "files")
    expect(cover.dumpable.map((one) => one.name)).toEqual(["shop", "blog", "cache"])
  })

  test("the uncovered join the database-only job that holds the most, never one with files", () => {
    const jobs = [
      job(1, { databaseDumps: [1], sources: ["/srv/shop"] }),
      job(2, { databaseDumps: [2] }),
      job(3, { databaseDumps: [1, 2] }),
      job(4, { databaseDumps: [3], sqlitePaths: ["/srv/app.db"] }),
    ]
    expect(backupCover(FLEET, jobs, everyone).home?.id).toBe(3)
    // A paused database job is not one to add to: what is added would not run.
    const paused = [job(1, { databaseDumps: [1], enabled: false })]
    expect(backupCover(FLEET, paused, everyone).home).toBeUndefined()
  })

  test("the covering jobs run next first, with a failed and a running one picked out", () => {
    const jobs = [
      job(1, { databaseDumps: [1], nextRun: "2026-11-23T03:00:00Z" }),
      job(2, {
        databaseDumps: [2, 3],
        nextRun: "2026-11-22T21:00:00Z",
        lastRun: { status: "failed" },
      }),
    ]
    const cover = backupCover(FLEET, jobs, everyone)
    expect(cover.uncovered).toEqual([])
    expect(cover.jobs.map((one) => one.id)).toEqual([2, 1])
    expect(cover.failed?.id).toBe(2)
    expect(cover.running).toBeUndefined()
  })

  test("the job the press makes is the backup form's defaults with the databases in it", () => {
    expect(nightlyJob([1, 3])).toEqual({
      name: "Databases",
      sources: [],
      excludes: [],
      targetKind: "local",
      target: { path: "/var/backups/just-dashboard" },
      schedule: "0 3 * * *",
      retention: 7,
      retentionDays: 0,
      enabled: true,
      databaseDumps: [1, 3],
    })
  })
})

describe("alertCover", () => {
  test("no rule leaves every database unwatched and every kind to add", () => {
    const cover = alertCover(FLEET, [], [], [])
    expect(cover.unwatched.map((one) => one.name)).toEqual(["shop", "blog", "cache"])
    expect(cover.missing).toEqual(WATCHED_KINDS)
    expect(cover.reached).toEqual([])
  })

  test("a rule over every database watches them all; one of a database's own watches it", () => {
    expect(alertCover(FLEET, [rule(1)], [], []).unwatched).toEqual([])
    const own = alertCover(FLEET, [rule(1, { connectionId: 2, kind: "disk" })], [], [])
    expect(own.unwatched.map((one) => one.name)).toEqual(["shop", "cache"])
    // A database's own rule is not a rule over every database.
    expect(own.missing).toEqual(WATCHED_KINDS)
  })

  test("a paused rule watches nothing, but is not added again", () => {
    const cover = alertCover(FLEET, [rule(1, { enabled: false })], [], [])
    expect(cover.unwatched).toHaveLength(3)
    expect(cover.missing).not.toContain("down")
  })

  test("the rules reach the channels they name, or every switched-on one", () => {
    const channels = [channel(1, "Slack"), channel(2, "Email"), channel(3, "Old", false)]
    const named = alertCover(FLEET, [rule(1, { channels: [2, 3] })], [], channels)
    expect(named.reached?.map((one) => one.name)).toEqual(["Email"])
    const all = alertCover(FLEET, [rule(1)], [], channels)
    expect(all.reached?.map((one) => one.name)).toEqual(["Slack", "Email"])
    expect(alertCover(FLEET, [rule(1)], [], undefined).reached).toBeUndefined()
  })

  test("what is firing puts a database's own first, and forgets a database that is gone", () => {
    const subjects = [
      { ruleId: 1, subject: "job:4", jobId: 4, label: "Databases", state: "firing" },
      { ruleId: 1, subject: "db:9", connectionId: 9, label: "gone", state: "firing" },
      { ruleId: 1, subject: "db:2", connectionId: 2, label: "blog", state: "firing" },
      { ruleId: 1, subject: "db:1", connectionId: 1, label: "shop", state: "quiet" },
    ]
    const cover = alertCover(FLEET, [rule(1)], subjects, [])
    expect(cover.firing.map((one) => one.label)).toEqual(["blog", "Databases"])
  })
})

describe("namesLine", () => {
  test("says a few names whole and counts the rest", () => {
    expect(namesLine([])).toBe("")
    expect(namesLine(["shop"])).toBe("shop")
    expect(namesLine(["shop", "blog"])).toBe("shop and blog")
    expect(namesLine(["shop", "blog", "cache"])).toBe("shop, blog and cache")
    expect(namesLine(["shop", "blog", "cache", "app", "notes"])).toBe(
      "shop, blog, cache and 2 more",
    )
  })
})
