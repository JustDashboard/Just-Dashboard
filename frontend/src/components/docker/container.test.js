import { describe, expect, test } from "bun:test"
import {
  companyOf,
  containerVerdict,
  crashed,
  exitWords,
  peak,
  portRows,
  reachWords,
  restartWords,
  sinceWords,
  splitImage,
  trendOf,
  upWords,
} from "./container"

const container = (over) => ({
  id: "a",
  name: "a",
  state: "running",
  networks: ["bridge"],
  exitCode: 0,
  hasHealthcheck: false,
  ...over,
})

describe("exitWords", () => {
  // The three signals a stop sends read as a stop, not as a failure.
  test("names the signals a stop sends", () => {
    expect(exitWords(0)).toBe("exited cleanly")
    expect(exitWords(130)).toBe("interrupted")
    expect(exitWords(137)).toBe("killed")
    expect(exitWords(143)).toBe("stopped")
    expect(exitWords(1)).toBe("exit 1")
  })

  test("calls only the program's own failures a crash", () => {
    expect(crashed(1)).toBe(true)
    expect(crashed(139)).toBe(true)
    for (const code of [0, 130, 137, 143]) expect(crashed(code)).toBe(false)
  })
})

describe("containerVerdict", () => {
  test("says whether anything checks a running container", () => {
    expect(containerVerdict(container({ health: "healthy", hasHealthcheck: true }))).toEqual({
      tone: "running",
      word: "Running",
      detail: "healthy",
      live: true,
    })
    expect(containerVerdict(container({})).detail).toBe("no health check")
    expect(containerVerdict(container({ health: "starting", hasHealthcheck: true })).tone).toBe(
      "warning",
    )
  })

  // Docker calls both of these running; the reader would not.
  test("reads a failing check and a loop as failing", () => {
    expect(containerVerdict(container({ health: "unhealthy" }))).toMatchObject({
      tone: "danger",
      word: "Failing its health check",
    })
    const loop = { state: "looping", restarts: { looping: true } }
    expect(containerVerdict(container({}), loop).word).toBe("Restarting in a loop")
    expect(containerVerdict(container({ state: "restarting", exitCode: 1 }), loop)).toMatchObject({
      tone: "danger",
      detail: "exit 1",
    })
    // A restart nobody is looping through is amber, on its way somewhere.
    expect(containerVerdict(container({ state: "restarting" })).tone).toBe("warning")
  })

  test("tells a crash from a stop", () => {
    expect(containerVerdict(container({ state: "exited", exitCode: 1 }))).toMatchObject({
      tone: "danger",
      word: "Crashed",
      detail: "exit 1",
      live: false,
    })
    expect(containerVerdict(container({ state: "exited", exitCode: 143 }))).toMatchObject({
      tone: "stopped",
      word: "Stopped",
      detail: "stopped",
    })
    expect(containerVerdict(container({ state: "created" })).word).toBe("Never started")
  })
})

describe("restartWords", () => {
  test("says the policy as what it does", () => {
    expect(restartWords("unless-stopped")).toBe("restarted unless stopped")
    expect(restartWords("always")).toBe("always restarted")
    expect(restartWords("on-failure:5")).toBe("restarted on failure, up to 5 times")
    expect(restartWords("no")).toBe("never restarted")
    expect(restartWords(undefined)).toBe("never restarted")
  })
})

describe("upWords", () => {
  test("ticks from the start against the clock it is given", () => {
    const now = Date.UTC(2026, 9, 8, 12, 0, 0)
    expect(upWords(new Date(now - 90 * 60_000).toISOString(), now)).toBe("up 1h 30m")
    expect(upWords(undefined, now)).toBeUndefined()
    // Docker's zero time for a container that never started.
    expect(upWords("0001-01-01T00:00:00Z", now)).toBeUndefined()
  })
})

describe("companyOf", () => {
  const web = container({
    id: "web",
    name: "shop-web-1",
    composeStack: "shop",
    composeService: "web",
  })
  const db = container({ id: "db", name: "shop-db-1", composeStack: "shop", composeService: "db" })
  const other = container({ id: "x", name: "x", composeStack: "blog" })

  test("is the compose project, in the order of its services, with the container in it", () => {
    const company = companyOf(web, [web, other, db])
    expect(company.kind).toBe("stack")
    expect(company.name).toBe("shop")
    expect(company.containers.map((one) => one.id)).toEqual(["db", "web"])
  })

  test("falls back to its own networks, never to the default bridge", () => {
    const api = container({ id: "api", networks: ["backend"] })
    const cache = container({ id: "cache", networks: ["backend", "bridge"] })
    const lonely = container({ id: "lonely", networks: ["bridge"] })
    expect(companyOf(api, [api, cache, lonely])).toMatchObject({ kind: "network", name: "backend" })
    expect(companyOf(api, [api, cache, lonely]).containers).toHaveLength(2)
    expect(companyOf(lonely, [api, cache, lonely])).toBeUndefined()
  })

  test("is nothing for a project of one", () => {
    expect(companyOf(other, [web, db, other])).toBeUndefined()
  })
})

describe("portRows", () => {
  const exposure = [
    {
      hostIp: "127.0.0.1",
      hostPort: 5678,
      containerPort: 5678,
      protocol: "tcp",
      scope: "loopback",
    },
    { hostIp: "0.0.0.0", hostPort: 9464, containerPort: 9464, protocol: "tcp", scope: "all" },
    { hostIp: "::1", hostPort: 8080, containerPort: 80, protocol: "tcp", scope: "loopback" },
    { containerPort: 3000, protocol: "tcp", scope: "internal" },
  ]

  test("writes each binding as it is typed", () => {
    expect(portRows(exposure, undefined).map((row) => row.published)).toEqual([
      "127.0.0.1:5678",
      "9464",
      "[::1]:8080",
      "not published",
    ])
  })

  test("joins each binding to the route traced from it", () => {
    const routes = [
      { hostIp: "127.0.0.1", hostPort: 5678, protocol: "tcp", reach: "proxied", vhost: "n8n.test" },
      { hostIp: "0.0.0.0", hostPort: 9464, protocol: "tcp", reach: "external" },
    ]
    const rows = portRows(exposure, routes)
    expect(reachWords(rows[0])).toEqual({ word: "through n8n.test", tone: "running" })
    expect(reachWords(rows[1])).toEqual({ word: "from anywhere", tone: "warning" })
    // Not traced yet: the binding alone still says something true.
    expect(reachWords(rows[2]).word).toBe("from this server only")
    expect(reachWords(rows[3]).word).toBe("from its networks only")
  })
})

describe("trendOf", () => {
  test("leaves out a bucket with no reading rather than drawing a fall to zero", () => {
    const points = [{ cpu: null }, { cpu: 12 }, { cpu: 30 }]
    expect(trendOf(points, (point) => point.cpu)).toEqual([12, 30])
    expect(trendOf(undefined, (point) => point.cpu)).toEqual([])
    expect(peak([12, 30, 4])).toBe(30)
    expect(peak([])).toBeUndefined()
  })
})

describe("splitImage", () => {
  // A registry's port is a colon too; the tag is after the last slash.
  test("finds the tag after the repository, not the registry's port", () => {
    expect(splitImage("docker.n8n.io/n8nio/n8n:1.98.2")).toEqual([
      "docker.n8n.io/n8nio/n8n",
      "1.98.2",
    ])
    expect(splitImage("registry.local:5000/team/api")).toEqual([
      "registry.local:5000/team/api",
      undefined,
    ])
    expect(splitImage("postgres:16-alpine")).toEqual(["postgres", "16-alpine"])
    expect(splitImage("nginx@sha256:0123456789abcdef0123")[0]).toBe("nginx")
  })
})

describe("sinceWords", () => {
  test("counts from Docker's status without repeating its exit code", () => {
    expect(sinceWords("exited", "Exited (1) 3 hours ago")).toBe("stopped 3 hours ago")
    expect(sinceWords("exited", "Exited (0) About an hour ago")).toBe("stopped about an hour ago")
    expect(sinceWords("restarting", "Restarting (1) 8 seconds ago")).toBe("last exit 8 seconds ago")
    expect(sinceWords("created", "Created")).toBeUndefined()
  })
})
