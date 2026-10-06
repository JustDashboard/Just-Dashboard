import { describe, expect, test } from "bun:test"
import {
  authUnit,
  cores,
  cronLogSource,
  groupName,
  managerHref,
  processCaption,
  uncontrollable,
} from "./shared"

// Where a process row says what is supervising it. Every branch is an address
// that has to exist: a wrong one is a dead link on a page that otherwise looks
// right. The container branch moved when a container became its own page.
describe("managerHref", () => {
  test("points at the unit, the app, or the container", () => {
    expect(managerHref({ manager: "systemd", managerName: "nginx.service" })).toBe(
      "/processes/services?unit=nginx.service",
    )
    expect(managerHref({ manager: "pm2", managerName: "api" })).toBe("/processes/pm2?app=api")
    expect(managerHref({ manager: "container", managerName: "web" })).toBe("/docker/containers/web")
  })

  // The container's name is a path segment now rather than a query value, and
  // compose writes names with a slash in them.
  test("encodes a name that would otherwise open a second segment", () => {
    expect(managerHref({ manager: "container", managerName: "stack/web" })).toBe(
      "/docker/containers/stack%2Fweb",
    )
  })

  test("has nowhere to point for an unsupervised process", () => {
    expect(managerHref({ manager: "none", managerName: "" })).toBeNull()
    expect(managerHref({ manager: "systemd", managerName: "" })).toBeNull()
  })
})

// The units whose journal the server reads to administrators only, as
// `authJournalUnit` names them: a sheet that opened one for anyone else would
// sit on a socket the server refuses, retrying.
describe("authUnit", () => {
  test("is sshd's unit under either name, and its per-connection instances", () => {
    for (const unit of ["ssh.service", "sshd.service", "ssh", "sshd@3-10.0.0.1:22.service"]) {
      expect(authUnit(unit)).toBe(true)
    }
    for (const unit of ["sshguard.service", "nginx.service", "ssh-keygen.service"]) {
      expect(authUnit(unit)).toBe(false)
    }
  })
})

// The Scheduled page's cron log. The unit's journal holds every job and the
// daemon's own lines, so it wins wherever it exists; a Red Hat host with
// rsyslog also has /var/log/cron, which would be the same lines twice.
describe("cronLogSource", () => {
  const journal = { id: "journal:", label: "systemd journal", kind: "journal", rotated: false }
  const cronFile = {
    id: "file:/var/log/cron",
    label: "cron",
    kind: "system",
    path: "/var/log/cron",
    lens: "cron",
    rotated: true,
  }
  const index = (sources, units = []) => ({ sources, units, roots: [], missing: {} })

  test("reads the daemon's unit first", () => {
    const unit = { name: "crond.service", description: "Command Scheduler", active: "active" }
    expect(cronLogSource(index([journal, cronFile], [unit]))?.id).toBe("journal:crond.service")
  })

  test("then a cron file, then the jobs' lines by program", () => {
    expect(cronLogSource(index([journal, cronFile]))?.id).toBe("file:/var/log/cron")
    expect(cronLogSource(index([journal]))?.id).toBe("journal-id:CRON,crond")
  })

  test("has nothing to offer a host with no journal and no file, or no answer yet", () => {
    expect(cronLogSource(index([]))).toBeUndefined()
    expect(cronLogSource(undefined)).toBeUndefined()
    // An API that answered with something else is not a list of sources.
    expect(cronLogSource([])).toBeUndefined()
  })
})

// An empty command line is three different things, and the row said the same
// word for all three: a zombie read as a kernel thread on a host with ten.
describe("processCaption", () => {
  test("names what an empty command line means", () => {
    expect(processCaption({ cmdline: "", state: "sleeping", manager: "kernel" })).toBe(
      "kernel thread",
    )
    expect(processCaption({ cmdline: "", state: "zombie", manager: "session" })).toMatch(/reap/)
    expect(processCaption({ cmdline: "", state: "sleeping", manager: "unmanaged" })).toBe(
      "command line not readable",
    )
    expect(processCaption({ cmdline: "nginx: worker", state: "zombie", manager: "kernel" })).toBe(
      "nginx: worker",
    )
  })
})

describe("uncontrollable", () => {
  test("is a zombie or a kernel thread, and nothing else", () => {
    expect(uncontrollable({ state: "zombie", manager: "session" })).toBe("zombie")
    expect(uncontrollable({ state: "sleeping", manager: "kernel" })).toBe("kernel")
    expect(uncontrollable({ state: "running", manager: "systemd" })).toBeNull()
  })
})

describe("cores", () => {
  test("reads a sum of per-core shares as cores", () => {
    expect(cores(340)).toBe("3.4 cores")
    expect(cores(1200)).toBe("12 cores")
    expect(cores(42)).toBe("0.42 cores")
    expect(cores(0.2)).toBe("idle")
  })
})

describe("groupName", () => {
  test("a container by its name, a unit without its suffix", () => {
    const base = { key: "", count: 1, cpuPercent: 0, memory: 0, ioRate: 0, pid: 1 }
    expect(groupName({ ...base, manager: "container", name: "3f9a1c0b7d2e", label: "api" })).toBe(
      "api",
    )
    expect(groupName({ ...base, manager: "container", name: "3f9a1c0b7d2e" })).toBe("3f9a1c0b7d2e")
    expect(groupName({ ...base, manager: "systemd", name: "nginx.service" })).toBe("nginx")
  })
})
