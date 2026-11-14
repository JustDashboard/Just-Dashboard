import { describe, expect, test } from "bun:test"
import { pm2Apps } from "./pm2-shared"
import {
  pm2AppHref,
  pm2AppState,
  pm2AppTargets,
  pm2Lead,
  pm2LegacyHref,
  pm2Matches,
  pm2ModeWords,
  pm2Positions,
  pm2Reading,
  pm2Resolve,
  pm2RestartWords,
  pm2VerbWords,
} from "./pm2-facts"

const proc = (over) => ({
  id: 0,
  daemonId: "deploy",
  name: "api",
  status: "online",
  pid: 100,
  cpu: 0,
  memory: 0,
  scriptPath: "/srv/api/server.js",
  execMode: "fork_mode",
  ...over,
})

const cluster = [
  proc({ id: 0, pid: 100, execMode: "cluster_mode", cpu: 10, memory: 100 }),
  proc({ id: 2, pid: 102, execMode: "cluster_mode", cpu: 5, memory: 50 }),
]

// The address is what every other page knows of an application: its name.
describe("pm2AppHref", () => {
  test("is the name, with the view and the account only where they say something", () => {
    expect(pm2AppHref({ name: "api" })).toBe("/processes/pm2/api")
    expect(pm2AppHref({ name: "api" }, { tab: "overview" })).toBe("/processes/pm2/api")
    expect(pm2AppHref({ name: "api", daemonId: "deploy" }, { tab: "logs", worker: 2 })).toBe(
      "/processes/pm2/api?tab=logs&worker=2",
    )
    expect(pm2AppHref({ name: "api", daemonId: "deploy" }, { account: true })).toBe(
      "/processes/pm2/api?account=deploy",
    )
  })

  test("keeps a name with a slash or a space in one segment", () => {
    expect(pm2AppHref({ name: "web/api v2" })).toBe("/processes/pm2/web%2Fapi%20v2")
  })
})

describe("pm2Resolve", () => {
  test("finds a cluster by its name as one application", () => {
    const found = pm2Resolve([...cluster, proc({ id: 1, name: "worker" })], "api")
    expect(found.kind).toBe("found")
    expect(found.app.instances).toHaveLength(2)
  })

  // The same name under two accounts is two applications: the page asks which
  // rather than opening one of them, and the account in the address answers.
  test("says a name two accounts share is ambiguous until the account is named", () => {
    const both = [proc({}), proc({ daemonId: "web" })]
    const bare = pm2Resolve(both, "api")
    expect(bare.kind).toBe("ambiguous")
    expect(bare.apps.map((a) => a.daemonId)).toEqual(["deploy", "web"])
    expect(pm2Resolve(both, "api", "web")).toMatchObject({
      kind: "found",
      app: { daemonId: "web" },
    })
  })

  test("says what is gone is missing", () => {
    expect(pm2Resolve([proc({})], "old")).toEqual({ kind: "missing" })
    expect(pm2Resolve([proc({})], "api", "web")).toEqual({ kind: "missing" })
  })
})

// `?app=` opened the sheet; every link written before 0.8.7 still carries it.
describe("pm2LegacyHref", () => {
  test("sends an exact identity and a bare name to the application's page", () => {
    expect(pm2LegacyHref(cluster, "deploy:2")).toBe("/processes/pm2/api")
    expect(pm2LegacyHref(cluster, "api")).toBe("/processes/pm2/api")
  })

  test("names the account where another account runs the same name", () => {
    const both = [...cluster, proc({ daemonId: "web", id: 0 })]
    expect(pm2LegacyHref(both, "web:0")).toBe("/processes/pm2/api?account=web")
    // A bare name cannot choose; the page it lands on asks.
    expect(pm2LegacyHref(both, "api")).toBe("/processes/pm2/api")
  })

  test("still goes somewhere that can say the application is gone", () => {
    expect(pm2LegacyHref(cluster, "deploy:9")).toBe("/processes/pm2/deploy%3A9")
  })
})

describe("pm2Matches", () => {
  const none = { query: "", state: "", app: "" }
  test("narrows by state, by application and by what is typed", () => {
    const errored = proc({ name: "worker", status: "errored" })
    expect(pm2Matches(errored, none)).toBe(true)
    expect(pm2Matches(errored, { ...none, state: "online" })).toBe(false)
    expect(pm2Matches(errored, { ...none, state: "errored" })).toBe(true)
    expect(pm2Matches(errored, { ...none, app: "deploy/api" })).toBe(false)
    expect(pm2Matches(errored, { ...none, query: " SRV/api " })).toBe(true)
    expect(pm2Matches(errored, { ...none, query: "nginx" })).toBe(false)
  })
})

describe("pm2AppState", () => {
  const app = (processes) => pm2Apps(processes)[0]
  test("is online only while every worker is", () => {
    expect(pm2AppState(app(cluster))).toEqual({ status: "online", word: "online", partial: false })
    expect(pm2AppState(app([cluster[0], { ...cluster[1], status: "stopped" }]))).toEqual({
      status: "stopped",
      word: "1 of 2 online",
      partial: true,
    })
  })

  test("takes the failing worker's word when none is up", () => {
    expect(pm2AppState(app([proc({ status: "errored" })])).word).toBe("errored")
    expect(pm2AppState(app([proc({ status: "stopped" })])).word).toBe("stopped")
  })
})

describe("pm2Lead", () => {
  test("is the first worker running, or the first when none is", () => {
    const [first, second] = cluster
    expect(pm2Lead(pm2Apps([{ ...first, status: "stopped", pid: 0 }, second])[0]).id).toBe(2)
    expect(pm2Lead(pm2Apps([second, { ...first, status: "stopped" }])[0]).id).toBe(2)
    expect(
      pm2Lead(
        pm2Apps([
          { ...second, status: "errored" },
          { ...first, status: "stopped" },
        ])[0],
      ).id,
    ).toBe(0)
  })
})

describe("the words", () => {
  test("say how it runs and how often it restarted", () => {
    expect(pm2ModeWords(pm2Apps(cluster)[0])).toBe("cluster of 2")
    expect(pm2ModeWords(pm2Apps([proc({})])[0])).toBe("fork")
    expect(pm2RestartWords(0)).toBe("Never restarted")
    expect(pm2RestartWords(1)).toBe("Restarted once")
    expect(pm2RestartWords(16)).toBe("Restarted 16 times")
  })

  test("number a cluster's workers from one and leave a fork unnumbered", () => {
    const positions = pm2Positions([...cluster, proc({ id: 1, name: "worker" })])
    expect(positions.get("deploy:0")).toEqual([1, 2])
    expect(positions.get("deploy:2")).toEqual([2, 2])
    expect(positions.has("deploy:1")).toBe(false)
  })
})

describe("pm2Reading", () => {
  const row = (pid, over = {}) => ({
    pid,
    cpuPercent: 20,
    cpuReady: true,
    rss: 300,
    threads: 4,
    ioReady: true,
    ioReadRate: 1,
    ioWriteRate: 2,
    connections: 3,
    listening: [{ proto: "tcp", address: "0.0.0.0", port: 3000 }],
    history: [
      { cpu: 1, rss: 10, read: 1, write: 1 },
      { cpu: 2, rss: 20, read: 2, write: 2 },
    ],
    ...over,
  })

  test("sums the running workers from the process table's reading of each", () => {
    const reading = pm2Reading(pm2Apps(cluster)[0], [row(100), row(102)])
    expect(reading).toMatchObject({
      cpu: 40,
      rss: 600,
      threads: 8,
      connections: 6,
      io: { read: 2, write: 4 },
      measured: true,
    })
    // Both workers listen on the one port: it is one way in.
    expect(reading.listening).toHaveLength(1)
    expect(reading.history).toEqual({ cpu: [2, 4], rss: [20, 40], io: [4, 8] })
  })

  test("falls back to PM2's own figure for a worker the table has not read", () => {
    const reading = pm2Reading(pm2Apps(cluster)[0], [row(100), null])
    expect(reading.cpu).toBe(25)
    expect(reading.rss).toBe(350)
    expect(pm2Reading(pm2Apps(cluster)[0], []).measured).toBe(false)
  })

  test("leaves out a worker that is not running", () => {
    const stopped = [cluster[0], { ...cluster[1], status: "stopped" }]
    expect(pm2Reading(pm2Apps(stopped)[0], [row(100), row(102)]).cpu).toBe(20)
  })

  test("reads the fullest worker against the limit PM2 restarts it at", () => {
    const limited = cluster.map((p) => ({ ...p, maxMemoryRestart: 400 }))
    const reading = pm2Reading(pm2Apps(limited)[0], [row(100), row(102, { rss: 100 })])
    expect(reading.limitShare).toBe(75)
    expect(pm2Reading(pm2Apps(cluster)[0], [row(100)]).limitShare).toBeUndefined()
  })

  test("sums the history only over the stretch every worker has", () => {
    const young = row(102, { history: [{ cpu: 5, rss: 50, read: 0, write: 0 }] })
    expect(pm2Reading(pm2Apps(cluster)[0], [row(100), young]).history.cpu).toEqual([7])
  })
})

// A verb pressed on the application's page goes to the workers it means: the
// sheet sent it to one and said the application's name.
describe("pm2AppTargets", () => {
  const mixed = pm2Apps([
    cluster[1],
    { ...cluster[0], id: 4, status: "stopped", pid: 0 },
    { ...cluster[0], id: 1, status: "errored", pid: 0 },
  ])[0]
  const ids = (action) => pm2AppTargets(mixed, action).map((p) => p.id)

  test("starts what is down and reloads or stops what is up, in PM2's order", () => {
    expect(ids("start")).toEqual([1, 4])
    expect(ids("reload")).toEqual([2])
    expect(ids("stop")).toEqual([2])
  })

  test("restarts, resets and deletes every worker", () => {
    expect(ids("restart")).toEqual([1, 2, 4])
    expect(ids("reset")).toEqual([1, 2, 4])
    expect(ids("delete")).toEqual([1, 2, 4])
  })

  // PM2 flushes by name, and the workers share their files.
  test("flushes once", () => {
    expect(ids("flush")).toEqual([2])
  })

  test("says what a verb does in the terms of what it runs as", () => {
    expect(pm2VerbWords("reload", true)).toBe("Replaces its workers one at a time.")
    expect(pm2VerbWords("reload", false)).toBe("Restarts it, with a short gap.")
    expect(pm2VerbWords("cwd", false)).toBe("")
  })
})
