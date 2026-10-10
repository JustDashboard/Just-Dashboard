import { describe, expect, test } from "bun:test"
import {
  bindingOf,
  bucketCounts,
  eventFacts,
  exitCodeOf,
  exitWords,
  networkRates,
  networkShortName,
  serviceChanges,
  serviceReadings,
  stackNetworks,
  stackVerdict,
  stateDetail,
  waysIn,
} from "./stack-service-readings"
import { foldRestarts } from "@/lib/docker-events"

const NOW = Date.UTC(2026, 9, 8, 12, 0, 0)
const ago = (ms) => new Date(NOW - ms).toISOString()
const SEC = 1000
const MIN = 60 * SEC

const service = (over) => ({
  name: "web",
  container: "c-web",
  state: "running",
  status: "Up 2 hours",
  image: "nginx:alpine",
  ports: [],
  ...over,
})

const container = (over) => ({
  id: "c-web",
  name: "shop-web-1",
  names: ["shop-web-1"],
  image: "nginx:alpine",
  state: "running",
  status: "Up 2 hours",
  uptimeSeconds: 7200,
  ports: [],
  labels: {},
  networks: ["shop_default"],
  composeStack: "shop",
  composeService: "web",
  exposure: [],
  hasHealthcheck: false,
  inspected: true,
  ...over,
})

const event = (msAgo, action, over = {}) => ({
  time: ago(msAgo),
  type: "container",
  action,
  name: "shop-worker-1",
  id: "c-worker",
  stack: "shop",
  service: "worker",
  message: "",
  level: action === "die" && over.exitCode && over.exitCode !== "0" ? "error" : "info",
  source: "compose",
  ...over,
})

const read = (over) =>
  serviceReadings({
    services: [service()],
    orphans: [],
    containers: [],
    stats: {},
    rates: {},
    trends: new Map(),
    events: [],
    now: NOW,
    ...over,
  })

describe("exit codes", () => {
  test("are read from the exited and the restarting status", () => {
    expect(exitCodeOf("Exited (137) 3 minutes ago")).toBe(137)
    expect(exitCodeOf("Restarting (1) 8 seconds ago")).toBe(1)
    expect(exitCodeOf("Up 2 hours")).toBeUndefined()
  })

  test("are said in words", () => {
    expect(exitWords(0)).toBe("exited cleanly")
    expect(exitWords(137)).toBe("killed")
    expect(exitWords(137, true)).toBe("killed for memory")
    expect(exitWords(1)).toBe("exit 1")
  })
})

describe("serviceReadings", () => {
  test("reads a service from the socket's container before the poll", () => {
    const [web] = read({
      services: [service({ state: "running" })],
      containers: [container({ state: "exited", status: "Exited (1) a minute ago" })],
    })
    expect(web.state).toBe("exited")
    expect(web.bucket).toBe("failing")
    expect(web.word).toBe("Crashed")
    expect(web.exitCode).toBe(1)
  })

  test("a deliberate stop is stopped, not crashed", () => {
    const [web] = read({
      services: [service({ state: "exited", status: "Exited (143) 2 days ago" })],
    })
    expect(web.bucket).toBe("stopped")
    expect(web.word).toBe("Stopped")
  })

  test("a 137 the OOM killer caused is out of memory", () => {
    const [web] = read({
      services: [service({ state: "exited", status: "Exited (137) a minute ago" })],
      events: [
        event(60 * SEC, "oom", { id: "c-web", service: "web" }),
        event(60 * SEC - 200, "die", { id: "c-web", service: "web", exitCode: "137" }),
      ],
    })
    expect(web.oomKilled).toBe(true)
    expect(web.bucket).toBe("failing")
    expect(web.word).toBe("Out of memory")
  })

  test("unhealthy and still starting are told apart from running", () => {
    const [a, b, c] = read({
      services: [
        service({ name: "a", container: "1", health: "unhealthy" }),
        service({ name: "b", container: "2", health: "starting" }),
        service({ name: "c", container: "3", health: "healthy" }),
      ],
    })
    expect([a.bucket, b.bucket, c.bucket]).toEqual(["failing", "starting", "running"])
    expect([a.word, b.word, c.word]).toEqual(["Unhealthy", "Starting", "Running"])
  })

  test("a service compose never created is missing, and has no container", () => {
    const [search] = read({
      services: [service({ name: "search", container: "", state: "missing", missing: true })],
    })
    expect(search.bucket).toBe("missing")
    expect(search.word).toBe("Not created")
    expect(search.containerId).toBeUndefined()
  })

  test("orders worst first, then by name, and joins the live frame", () => {
    const stat = { id: "c-web", cpuPercent: 3, memUsage: 1 }
    const readings = read({
      services: [
        service({ name: "web" }),
        service({ name: "db", container: "c-db", state: "restarting", status: "Restarting (1)" }),
        service({ name: "api", container: "c-api" }),
      ],
      stats: { "c-web": stat },
    })
    expect(readings.map((r) => r.key)).toEqual(["db", "api", "web"])
    expect(readings.find((r) => r.key === "web").stat).toBe(stat)
    expect(readings.find((r) => r.key === "db").stat).toBeUndefined()
  })

  test("marks an orphan and gives every service its lane", () => {
    const [web] = read({ orphans: ["web"] })
    expect(web.orphan).toBe(true)
    expect(web.lane).toMatch(/^var\(--tag-/)
  })
})

describe("eventFacts", () => {
  /** Exits 1 five times and comes back, then is down between two tries. */
  const loop = [
    event(9 * MIN, "start"),
    ...[483, 388, 293, 198, 103].flatMap((s) => [
      event(s * SEC, "die", { exitCode: "1" }),
      event((s - 4) * SEC, "start"),
    ]),
    event(8 * SEC, "die", { exitCode: "1" }),
  ]

  test("finds the restart loop behind the newest exit", () => {
    const facts = eventFacts(foldRestarts(loop), NOW)
    expect(facts.get("c-worker").loop?.times).toBe(5)
    expect(facts.get("c-worker").loop?.exitCode).toBe("1")
    expect(facts.get("c-worker").oomKilled).toBe(false)
  })

  test("a loop long over is history", () => {
    const facts = eventFacts(foldRestarts(loop), NOW + 60 * MIN)
    expect(facts.get("c-worker").loop).toBeUndefined()
  })

  test("a restarting service carries its loop", () => {
    const [worker] = read({
      services: [
        service({
          name: "worker",
          container: "c-worker",
          state: "restarting",
          status: "Restarting (1) 8 seconds ago",
        }),
      ],
      events: loop,
    })
    expect(worker.word).toBe("Restarting")
    expect(worker.loop?.times).toBe(5)
  })
})

describe("stateDetail", () => {
  test("a running service says how long, ticking from its start, and what checks it", () => {
    const [web] = read({
      containers: [container({ startedAt: ago(26 * MIN + 5 * SEC), health: "healthy" })],
    })
    expect(stateDetail(web, NOW)).toEqual({ text: "up 26m 5s · healthy" })
    expect(stateDetail(web, NOW + 10 * SEC)).toEqual({ text: "up 26m 15s · healthy" })
  })

  test("says when nothing is checking it, and when its check fails", () => {
    const [unchecked] = read({ containers: [container({ uptimeSeconds: 7200 })] })
    expect(stateDetail(unchecked, NOW)?.text).toBe("up 2h · no health check")
    const [failing] = read({
      containers: [container({ uptimeSeconds: 7200, health: "unhealthy" })],
    })
    expect(stateDetail(failing, NOW)).toEqual({ text: "failing its check · up 2h", tone: "danger" })
  })

  test("a crash and a clean stop say their exit and when", () => {
    const [crashed] = read({
      containers: [container({ state: "exited", status: "Exited (1) About a minute ago" })],
    })
    expect(stateDetail(crashed, NOW)).toEqual({ text: "exit 1 · a minute ago", tone: "danger" })
    const [stopped] = read({
      containers: [container({ state: "exited", status: "Exited (0) 2 days ago" })],
    })
    expect(stateDetail(stopped, NOW)).toEqual({ text: "exited cleanly · 2 days ago" })
  })

  test("a restart loop is counted", () => {
    const [worker] = read({
      services: [service({ name: "worker", container: "c-worker", state: "restarting" })],
      containers: [
        container({
          id: "c-worker",
          composeService: "worker",
          state: "restarting",
          status: "Restarting (1) 8 seconds ago",
        }),
      ],
      events: [
        event(9 * MIN, "start"),
        event(7 * MIN, "die", { exitCode: "1" }),
        event(7 * MIN - 4 * SEC, "start"),
        event(5 * MIN, "die", { exitCode: "1" }),
        event(5 * MIN - 4 * SEC, "start"),
        event(8 * SEC, "die", { exitCode: "1" }),
      ],
    })
    expect(stateDetail(worker, NOW)).toEqual({
      text: "restarted ×2 in 2m 4s · exit 1",
      tone: "danger",
    })
  })

  test("a service never created says so", () => {
    const [search] = read({
      services: [service({ name: "search", container: "", state: "missing", missing: true })],
    })
    expect(stateDetail(search, NOW)?.text).toBe("in the compose file, never created")
  })
})

describe("stackVerdict", () => {
  const reading = (bucket, over = {}) => ({ bucket, orphan: false, ...over })

  test("says the worst thing, counted", () => {
    expect(
      stackVerdict([reading("failing"), reading("missing"), reading("running")], true),
    ).toEqual({ tone: "danger", label: "1 service failing", bucket: "failing" })
    expect(stackVerdict([reading("missing"), reading("running")], true).label).toBe(
      "1 service not created",
    )
    expect(stackVerdict([reading("running"), reading("running")], true).label).toBe("All 2 running")
    expect(stackVerdict([reading("stopped"), reading("running")], true).label).toBe(
      "1 of 2 stopped",
    )
    expect(stackVerdict([reading("stopped")], true).label).toBe("Stopped")
  })

  test("a stack with nothing created is not deployed", () => {
    expect(stackVerdict([reading("missing")], false).label).toBe("Not deployed")
    expect(stackVerdict([], true).label).toBe("Not deployed")
  })

  test("counts an orphan after everything that is failing", () => {
    expect(stackVerdict([reading("running", { orphan: true })], true).label).toBe(
      "1 service not in the compose file",
    )
  })

  test("buckets count every service once", () => {
    const counts = bucketCounts([reading("failing"), reading("running"), reading("running")])
    expect(counts).toEqual({
      failing: 1,
      starting: 0,
      missing: 0,
      stopped: 0,
      paused: 0,
      running: 2,
    })
  })
})

describe("serviceChanges", () => {
  test("folds a loop into one line and keeps the exit that ended it", () => {
    const changes = serviceChanges([
      event(9 * MIN, "start"),
      event(7 * MIN, "die", { exitCode: "1" }),
      event(7 * MIN - 4 * SEC, "start"),
      event(5 * MIN, "die", { exitCode: "1" }),
      event(5 * MIN - 4 * SEC, "start"),
      event(10 * SEC, "die", { exitCode: "1" }),
    ])
    expect(changes.map((c) => c.verb)).toEqual([
      "exited 1",
      "restarted ×2 in 2 min · exit 1",
      "started",
    ])
    expect(changes[0].tone).toBe("danger")
  })

  test("says an OOM kill on the exit it caused", () => {
    const changes = serviceChanges([
      event(MIN, "oom"),
      event(MIN - 300, "die", { exitCode: "137", level: "error" }),
    ])
    expect(changes).toHaveLength(1)
    expect(changes[0].verb).toBe("killed for memory")
  })

  test("a passing check is news only after a failing one", () => {
    const changes = serviceChanges([
      event(20 * MIN, "health_status: healthy"),
      event(14 * MIN, "health_status: unhealthy"),
      event(13 * MIN, "health_status: healthy"),
    ])
    expect(changes.map((c) => c.verb)).toEqual(["passing its check again", "failing its check"])
  })

  test("a clean exit and a deliberate stop are not failures", () => {
    const [clean] = serviceChanges([event(MIN, "die", { exitCode: "0" })])
    expect(clean).toMatchObject({ verb: "exited cleanly", tone: "stopped" })
    const [stopped] = serviceChanges([event(MIN, "die", { exitCode: "143", level: "error" })])
    expect(stopped).toMatchObject({ verb: "stopped", tone: "stopped" })
  })

  test("leaves out what only leads to an exit or a start", () => {
    expect(serviceChanges([event(MIN, "create"), event(MIN, "kill"), event(MIN, "stop")])).toEqual(
      [],
    )
  })
})

describe("networkRates", () => {
  const frame = (ts, rx, tx, over = {}) => ({ id: "c", ts, netRx: rx, netTx: tx, ...over })

  test("measures bytes a second between two frames", () => {
    const rates = networkRates({ c: frame(ago(2000), 1000, 0) }, [frame(ago(0), 5000, 2000)])
    expect(rates.c).toEqual({ rx: 2000, tx: 1000 })
  })

  test("has no rate across a restart, a frame too close, or the host's network", () => {
    expect(networkRates({ c: frame(ago(2000), 5000, 0) }, [frame(ago(0), 10, 0)]).c).toBeUndefined()
    expect(networkRates({ c: frame(ago(100), 0, 0) }, [frame(ago(0), 10, 0)]).c).toBeUndefined()
    expect(
      networkRates({ c: frame(ago(2000), 0, 0) }, [
        frame(ago(0), 10, 0, { networkAvailable: false }),
      ]).c,
    ).toBeUndefined()
  })
})

describe("ways in and networks", () => {
  test("a port bound to every interface over IPv4 and IPv6 is one way in", () => {
    const ways = waysIn([
      {
        key: "web",
        service: service({
          ports: [
            { ip: "0.0.0.0", privatePort: 80, publicPort: 8080, type: "tcp" },
            { ip: "::", privatePort: 80, publicPort: 8080, type: "tcp" },
            { privatePort: 9000, type: "tcp" },
          ],
        }),
      },
      {
        key: "api",
        service: service({
          ports: [{ ip: "127.0.0.1", privatePort: 3000, publicPort: 3000, type: "tcp" }],
        }),
      },
    ])
    expect(ways.map((w) => [w.hostPort, w.binding, w.targets])).toEqual([
      [3000, "loopback", [{ service: "api", port: 3000 }]],
      [8080, "all", [{ service: "web", port: 80 }]],
    ])
  })

  test("binds name the three answers that differ", () => {
    expect(bindingOf(undefined)).toBe("all")
    expect(bindingOf("::")).toBe("all")
    expect(bindingOf("127.0.0.1")).toBe("loopback")
    expect(bindingOf("10.0.0.4")).toBe("address")
  })

  test("networks list their services, the busiest first", () => {
    const networks = stackNetworks([
      { key: "web", container: container({ networks: ["shop_frontend"] }) },
      { key: "api", container: container({ networks: ["shop_frontend", "shop_backend"] }) },
      { key: "db", container: container({ networks: ["shop_backend"] }) },
      { key: "search", container: undefined },
    ])
    expect(networks).toEqual([
      { name: "shop_backend", services: ["api", "db"] },
      { name: "shop_frontend", services: ["api", "web"] },
    ])
    expect(networkShortName("shop_backend", "shop")).toBe("backend")
    expect(networkShortName("proxy", "shop")).toBe("proxy")
  })
})
