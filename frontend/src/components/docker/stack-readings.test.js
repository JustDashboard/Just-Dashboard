import { describe, expect, test } from "bun:test"
import { exitCode, stackBucket, stackChanges, stackLines } from "./stack-readings"

const service = (name, over = {}) => ({
  name,
  container: `${name}-id`,
  state: "running",
  status: "Up 2 hours",
  image: `${name}:latest`,
  ports: [],
  ...over,
})

const stack = (name, over = {}) => ({
  name,
  workingDir: `/srv/${name}`,
  configFiles: [`/srv/${name}/compose.yaml`],
  services: [service("api"), service("db")],
  running: 2,
  total: 2,
  managed: true,
  declared: ["api", "db"],
  containers: 2,
  deployed: true,
  orphans: [],
  state: "running",
  summary: "Running · 2/2 services",
  ...over,
})

const container = (id, over = {}) => ({ id, name: id, state: "running", ...over })
const stat = (id, cpuPercent, memUsage, over = {}) => ({ id, cpuPercent, memUsage, ...over })

describe("stackBucket", () => {
  // Every stack is counted under exactly one chip, so the chips add up to the
  // stack count and none of them double-counts a stack that is up but wrong.
  test("puts each stack in one bucket, attention first", () => {
    expect(stackBucket(stack("a"))).toBe("running")
    expect(stackBucket(stack("a", { state: "partial" }))).toBe("attention")
    expect(stackBucket(stack("a", { orphans: ["old"] }))).toBe("attention")
    expect(stackBucket(stack("a", { services: [service("api", { health: "unhealthy" })] }))).toBe(
      "attention",
    )
    expect(stackBucket(stack("a", { running: 0, state: "stopped" }))).toBe("stopped")
    expect(
      stackBucket(
        stack("a", { running: 0, deployed: false, containers: 0, state: "not-deployed" }),
      ),
    ).toBe("undeployed")
  })
})

describe("stackLines", () => {
  test("orders stacks worst first and by name within a bucket", () => {
    const lines = stackLines(
      [
        stack("zeta"),
        stack("mail", { running: 0, state: "stopped" }),
        stack("alpha"),
        stack("wiki", { running: 0, deployed: false, state: "not-deployed" }),
        stack("monitoring", { state: "partial" }),
      ],
      [],
      {},
    )
    expect(lines.map((l) => l.stack.name)).toEqual(["monitoring", "alpha", "zeta", "mail", "wiki"])
  })

  // The socket moves the moment Docker does; the stack list is a poll, so a
  // container's own state wins wherever the socket has it.
  test("reads each service's state from its live container", () => {
    const [line] = stackLines(
      [stack("shop")],
      [container("api-id", { state: "exited", status: "Exited (1) 1 second ago" })],
      {},
    )
    const api = line.lines.find((l) => l.service.name === "api")
    expect(api.state).toBe("exited")
    expect(line.lines.find((l) => l.service.name === "db").state).toBe("running")
  })

  test("sums the running containers and leaves an unmeasured one out of the processor", () => {
    const [line] = stackLines([stack("shop")], [container("api-id"), container("db-id")], {
      "api-id": stat("api-id", 30, 200),
      "db-id": stat("db-id", 0, 100, { cpuReady: false }),
    })
    expect(line.cpu).toBe(30)
    expect(line.memory).toBe(300)
    expect(line.measured).toBe(true)
  })

  test("is not measured until a running container has a reading", () => {
    const [line] = stackLines([stack("shop")], [container("api-id")], {
      "api-id": stat("api-id", 0, 50, { cpuReady: false }),
    })
    expect(line.measured).toBe(false)
    expect(line.memory).toBe(50)
  })

  test("puts what is failing above what is down, above what was never created", () => {
    const [line] = stackLines(
      [
        stack("shop", {
          services: [
            service("web"),
            service("api", { health: "unhealthy" }),
            service("worker", { state: "exited", status: "Exited (1) 2 minutes ago" }),
            service("cron", { container: "", missing: true, state: "", status: "", image: "" }),
          ],
          orphans: ["web"],
        }),
      ],
      [],
      {},
    )
    expect(line.lines.map((l) => l.service.name)).toEqual(["api", "worker", "cron", "web"])
    expect(line.lines.find((l) => l.service.name === "web").orphan).toBe(true)
    expect(line.lines.find((l) => l.service.name === "cron").key).toBe("shop/cron")
  })
})

describe("exitCode", () => {
  test("reads the code out of Docker's status", () => {
    expect(exitCode("Exited (137) 12 minutes ago")).toBe(137)
    expect(exitCode("Exited (0) 2 days ago")).toBe(0)
    expect(exitCode("Up 2 hours")).toBeUndefined()
  })
})

const event = (seconds, action, over = {}) => ({
  time: new Date(Date.UTC(2026, 9, 8, 12, 0, seconds)).toISOString(),
  type: "container",
  action,
  name: "monitoring-alertmanager-1",
  stack: "monitoring",
  service: "alertmanager",
  message: "",
  level: "info",
  source: "compose",
  ...over,
})

describe("stackChanges", () => {
  test("says what changed whether a service is serving, newest first", () => {
    const changes = stackChanges([
      event(1, "create"),
      event(2, "start"),
      event(3, "health_status: unhealthy"),
      event(4, "kill"),
      event(5, "die", { level: "error", exitCode: "1" }),
      event(6, "die", { exitCode: "0" }),
    ])
    expect(changes.map((c) => [c.verb, c.tone])).toEqual([
      ["exited", "stopped"],
      ["exited 1", "danger"],
      ["unhealthy", "danger"],
      ["started", "running"],
    ])
  })

  // An out-of-memory kill and the exit it causes arrive a moment apart, in
  // either order, and are one thing that happened.
  test("folds an out-of-memory kill and its exit into one line", () => {
    for (const pair of [
      [event(10, "oom"), event(10, "die", { level: "error", exitCode: "137" })],
      [event(10, "die", { level: "error", exitCode: "137" }), event(11, "oom")],
    ]) {
      const changes = stackChanges(pair)
      expect(changes).toHaveLength(1)
      expect(changes[0].verb).toBe("killed for memory")
      expect(changes[0].tone).toBe("danger")
    }
  })

  test("leaves out what happened to no stack, and to other kinds of object", () => {
    expect(
      stackChanges([
        event(1, "start", { stack: undefined }),
        event(2, "connect", { type: "network" }),
      ]),
    ).toEqual([])
  })
})
