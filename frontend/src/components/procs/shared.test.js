import { describe, expect, test } from "bun:test"
import { cronLogSource, managerHref } from "./shared"

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
