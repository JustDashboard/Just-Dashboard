import { expect, test } from "bun:test"
import { NAV, groupedChildren, navLocation, shownFor, tabTitle } from "./nav"

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

test("the Network rail is the overview and three headed groups, not nineteen siblings", () => {
  const network = NAV.flatMap((group) => group.items).find((entry) => entry.href === "/network")
  const groups = groupedChildren(network.children)
  expect(groups.map((group) => group.label)).toEqual([
    undefined,
    "Setup",
    "Activity",
    "Diagnostics",
  ])
  expect(groups[0].items.map((item) => item.title)).toEqual(["Overview"])
  expect(groups.map((group) => group.items.length)).toEqual([1, 9, 4, 5])
  // Nothing was dropped or moved out of the section by the grouping.
  expect(network.children).toHaveLength(19)
  expect(groups.find((group) => group.label === "Activity").items.map((i) => i.title)).toEqual([
    "Traffic",
    "Socket history",
    "Connections",
    "Drift",
  ])
})

test("a section that names no heading is the one list it always was", () => {
  const docker = NAV.flatMap((group) => group.items).find((entry) => entry.href === "/docker")
  expect(groupedChildren(docker.children)).toEqual([{ label: undefined, items: docker.children }])
  expect(groupedChildren([])).toEqual([])
})

test("no two pages of one section share a glyph", () => {
  const sections = NAV.flatMap((group) => group.items).flatMap((entry) =>
    entry.href === undefined ? [entry, ...entry.children] : [entry],
  )
  for (const section of sections) {
    const glyphs = (section.children ?? []).map((child) => child.icon)
    const shared = (section.children ?? [])
      .filter((child, index) => glyphs.indexOf(child.icon) !== index)
      .map((child) => child.title)
    expect({ section: section.title, shared }).toEqual({ section: section.title, shared: [] })
  }
})
