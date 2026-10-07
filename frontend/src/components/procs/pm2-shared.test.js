import { describe, expect, test } from "bun:test"
import { memoryLimitShare, pm2Apps, pm2Order, pm2Select, savedDrift } from "./pm2-shared"

const proc = (over) => ({
  id: 0,
  daemonId: "deploy",
  name: "api",
  status: "online",
  cpu: 0,
  memory: 0,
  ...over,
})

// A cluster is one entry per instance in `pm2 jlist`; the band reads it as
// one application.
describe("pm2Apps", () => {
  test("sums a cluster's instances and counts the ones online", () => {
    const apps = pm2Apps([
      proc({ id: 0, cpu: 10, memory: 100 }),
      proc({ id: 1, cpu: 5, memory: 50, status: "stopped" }),
      proc({ id: 2, name: "worker", cpu: 1, memory: 10 }),
    ])
    expect(apps.map((a) => a.name)).toEqual(["api", "worker"])
    expect(apps[0]).toMatchObject({ cpu: 15, memory: 150, online: 1 })
    expect(apps[0].instances).toHaveLength(2)
  })

  test("keeps the same name under two accounts apart", () => {
    const apps = pm2Apps([proc({ daemonId: "deploy" }), proc({ daemonId: "web", id: 0 })])
    expect(apps.map((a) => a.key)).toEqual(["deploy/api", "web/api"])
  })
})

describe("pm2Order", () => {
  test("puts what is crashing first and keeps a cluster in its order", () => {
    const ordered = pm2Order([
      proc({ id: 3, name: "api" }),
      proc({ id: 0, name: "api" }),
      proc({ id: 5, name: "web" }),
      proc({ id: 6, name: "worker", status: "errored" }),
      proc({ id: 7, name: "scraper", status: "stopped" }),
    ])
    expect(ordered.map((p) => `${p.name}:${p.id}`)).toEqual([
      "worker:6",
      "api:0",
      "api:3",
      "scraper:7",
      "web:5",
    ])
  })
})

// "Resurrects on boot" was said of a list saved before the last three
// applications were started; the drift is what makes it true or not.
describe("savedDrift", () => {
  const running = [proc({ name: "api" }), proc({ id: 1, name: "api" }), proc({ name: "queue" })]

  test("names what a reboot would lose and what it would bring back", () => {
    expect(savedDrift({ account: "deploy", savedApps: ["api", "old-cron"] }, running)).toEqual({
      unsaved: ["queue"],
      removed: ["old-cron"],
    })
  })

  test("is nothing when the list matches", () => {
    expect(savedDrift({ account: "deploy", savedApps: ["queue", "api"] }, running)).toEqual({
      unsaved: [],
      removed: [],
    })
  })

  test("reads only the daemon's own applications", () => {
    const other = [...running, proc({ daemonId: "web", name: "site" })]
    expect(savedDrift({ account: "deploy", savedApps: ["api", "queue"] }, other)).toEqual({
      unsaved: [],
      removed: [],
    })
  })

  test("claims nothing when the list could not be read", () => {
    expect(savedDrift({ account: "deploy", savedApps: null }, running)).toBeNull()
    expect(savedDrift({ account: "deploy" }, running)).toBeNull()
  })
})

describe("memoryLimitShare", () => {
  test("is a share of the restart limit, and nothing without one", () => {
    expect(memoryLimitShare({ memory: 256, maxMemoryRestart: 512 })).toBe(50)
    expect(memoryLimitShare({ memory: 256 })).toBeUndefined()
    expect(memoryLimitShare({ memory: 256, maxMemoryRestart: 0 })).toBeUndefined()
  })
})

// The live table's owner link carries a bare name, and a cluster's name is
// every instance: it opened nothing.
describe("pm2Select", () => {
  const list = [
    proc({ id: 2, status: "stopped" }),
    proc({ id: 1 }),
    proc({ id: 3 }),
    proc({ id: 4, name: "worker" }),
    proc({ id: 0, daemonId: "web", name: "site" }),
    proc({ id: 1, daemonId: "other", name: "site" }),
  ]

  test("an exact identity wins", () => {
    expect(pm2Select(list, "deploy:3")?.id).toBe(3)
  })

  test("a cluster's name opens its first running instance", () => {
    expect(pm2Select(list, "api")?.id).toBe(1)
    expect(pm2Select(list, "worker")?.id).toBe(4)
  })

  test("a name two accounts share opens nothing", () => {
    expect(pm2Select(list, "site")).toBeNull()
    expect(pm2Select(list, "missing")).toBeNull()
    expect(pm2Select(list, null)).toBeNull()
  })
})
