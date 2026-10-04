import { describe, expect, test } from "bun:test"
import {
  failureTone,
  mountTargetProduct,
  publishedPorts,
  runtimeContainer,
  shortDigest,
  wantsFailureReading,
} from "./runtime-model"

const port = (overrides) => ({
  hostIp: "0.0.0.0",
  hostPort: 8080,
  containerPort: 80,
  protocol: "tcp",
  scope: "all",
  label: "",
  summary: "bound to every interface",
  ...overrides,
})

describe("publishedPorts", () => {
  test("leaves out ports nothing publishes", () => {
    const ports = publishedPorts([
      port({ hostPort: undefined, scope: "internal", containerPort: 5432 }),
      port(),
    ])
    expect(ports.map((one) => one.label)).toEqual(["8080 → 80"])
  })

  test("lists a port once when Docker binds it on both address families", () => {
    const ports = publishedPorts([
      port({ hostIp: "::", ipv6: true }),
      port({ hostIp: "127.0.0.1", scope: "loopback" }),
      port(),
    ])
    expect(ports).toHaveLength(1)
    expect(ports[0].scope).toBe("all")
  })

  test("says the protocol only where it is not TCP", () => {
    const [dns] = publishedPorts([port({ hostPort: 53, containerPort: 53, protocol: "udp" })])
    expect(dns.label).toBe("53 → 53/udp")
  })

  test("keeps tcp and udp on one number as two ports", () => {
    const ports = publishedPorts([
      port({ hostPort: 53, containerPort: 53 }),
      port({ hostPort: 53, containerPort: 53, protocol: "udp" }),
    ])
    expect(ports).toHaveLength(2)
  })

  test("puts the widest reach first, then the lowest port", () => {
    const ports = publishedPorts([
      port({ hostPort: 9000, scope: "loopback" }),
      port({ hostPort: 8081, scope: "all" }),
      port({ hostPort: 7000, scope: "private" }),
      port({ hostPort: 8080, scope: "all" }),
    ])
    expect(ports.map((one) => one.hostPort)).toEqual([8080, 8081, 7000, 9000])
  })

  test("reads a container with no exposure as having none", () => {
    expect(publishedPorts(undefined)).toEqual([])
  })
})

describe("shortDigest", () => {
  test("drops the algorithm and keeps twelve digits", () => {
    expect(shortDigest(`sha256:${"ab12".repeat(16)}`)).toBe("ab12ab12ab12")
  })
  test("says nothing for an image the engine could not name", () => {
    expect(shortDigest("")).toBeUndefined()
    expect(shortDigest(undefined)).toBeUndefined()
  })
})

describe("mountTargetProduct", () => {
  test("names the program whose data directory it is", () => {
    expect(mountTargetProduct("/var/lib/postgresql/data")).toBe("postgresql")
    expect(mountTargetProduct("/var/lib/mysql/")).toBe("mysql")
    expect(mountTargetProduct("/data/db")).toBe("mongodb")
  })
  test("does not guess at a path many images share", () => {
    expect(mountTargetProduct("/data")).toBeUndefined()
    expect(mountTargetProduct("/var/lib/postgresql-backup")).toBeUndefined()
  })
})

describe("wantsFailureReading", () => {
  test("asks about the states that stopped or keep stopping", () => {
    for (const state of ["restarting", "exited", "dead"]) {
      expect(wantsFailureReading(state)).toBe(true)
    }
    for (const state of ["running", "paused", "created"]) {
      expect(wantsFailureReading(state)).toBe(false)
    }
  })
})

describe("failureTone", () => {
  const diagnosis = (state, evidence = []) => ({ state, evidence })
  const exit = (value) => ({
    label: "Exit code",
    value,
    source: "docker inspect",
    weight: "decisive",
  })

  test("a loop and a failing check are red, a restart in progress is amber", () => {
    expect(failureTone(diagnosis("looping"))).toBe("danger")
    expect(failureTone(diagnosis("unhealthy"))).toBe("danger")
    expect(failureTone(diagnosis("flapping"))).toBe("warning")
  })

  test("a kill by the kernel is red whatever the exit code says", () => {
    const killed = diagnosis("stopped", [
      exit("137 — killed"),
      { label: "OOM killed", value: "yes", source: "the kernel", weight: "decisive" },
    ])
    expect(failureTone(killed)).toBe("danger")
  })

  test("a crash is amber and a clean stop has nothing to explain", () => {
    expect(failureTone(diagnosis("stopped", [exit("1 — general error")]))).toBe("warning")
    expect(failureTone(diagnosis("stopped", [exit("0 — exited cleanly")]))).toBeUndefined()
    expect(failureTone(diagnosis("running"))).toBeUndefined()
  })
})

describe("runtimeContainer", () => {
  const service = {
    containerId: "abc123",
    name: "web",
    releaseId: 4,
    liveRelease: true,
    state: "running",
    health: "healthy",
    imageId: "sha256:1",
    stack: "shop",
    service: "web",
    image: "nginx:1",
  }

  test("is complete from the release engine's record alone", () => {
    const container = runtimeContainer(service, undefined)
    expect(container).toMatchObject({
      id: "abc123",
      name: "web",
      state: "running",
      composeStack: "shop",
      image: "nginx:1",
    })
    expect(container.exposure).toEqual([])
  })

  test("takes the engine's state over a listing a minute old", () => {
    const listed = {
      ...runtimeContainer(service, undefined),
      state: "exited",
      status: "Exited (1)",
    }
    const container = runtimeContainer(service, listed)
    expect(container.state).toBe("running")
    expect(container.status).toBe("running")
  })
})
