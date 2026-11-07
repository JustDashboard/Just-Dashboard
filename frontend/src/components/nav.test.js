import { expect, test } from "bun:test"
import { NAV, navLocation, shownFor, tabTitle } from "./nav"

test("a page in the nav names the tab", () => {
  expect(tabTitle("/files")).toBe("Files · Just Dashboard")
  expect(tabTitle("/")).toBe("Overview · Just Dashboard")
  expect(tabTitle("/docker/images")).toBe("Images · Just Dashboard")
  expect(tabTitle("/account/security")).toBe("Security · Just Dashboard")
})

test("a detail page takes the section it sits in", () => {
  expect(tabTitle("/deploy/7/logs")).toBe("Deployments · Just Dashboard")
  expect(tabTitle("/account/no-such-page")).toBe("Account · Just Dashboard")
})

test("an address under no section falls back to the product's name", () => {
  expect(tabTitle("/no-such-page")).toBe("Just Dashboard")
})

test("a server linked to a hub names itself after the page", () => {
  expect(tabTitle("/docker", "atlas")).toBe("Docker · atlas · Just Dashboard")
  expect(tabTitle("/no-such-page", "atlas")).toBe("atlas · Just Dashboard")
  // Unlinked, or before the hub has named it, the title is what it always was.
  expect(tabTitle("/docker", undefined)).toBe("Docker · Just Dashboard")
  expect(tabTitle("/docker", "  ")).toBe("Docker · Just Dashboard")
})

test("Settings → Hub is in the rail only on a linked server, or while you are on it", () => {
  const settings = NAV.flatMap((group) => group.items).find((entry) => entry.href === "/dashboard")
  const hub = settings.children.find((child) => child.href === "/dashboard/hub")
  const admin = () => true
  const none = new Set()
  expect(hub.hubLink).toBe(true)
  expect(shownFor(hub, none, "/dashboard", admin)).toBe(false)
  expect(shownFor(hub, none, "/dashboard", admin, true)).toBe(true)
  expect(shownFor(hub, none, "/dashboard/hub", admin)).toBe(true)
  // Connect beside it is drawn either way, and the role still decides both.
  const connect = settings.children.find((child) => child.href === "/dashboard/link")
  expect(shownFor(connect, none, "/dashboard", admin)).toBe(true)
  expect(shownFor(hub, none, "/dashboard", () => false, true)).toBe(false)
  expect(navLocation("/dashboard/hub")?.title).toBe("Hub")
  expect(tabTitle("/dashboard/hub")).toBe("Hub · Just Dashboard")
})
