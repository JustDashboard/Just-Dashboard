import { describe, expect, test } from "bun:test"
import { managerHref } from "./shared"

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
