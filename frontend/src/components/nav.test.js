import { expect, test } from "bun:test"
import { NAV, groupedChildren, navLocation, shownFor, tabTitle } from "./nav"

test("a page in the nav names the tab", () => {
  expect(tabTitle("/files")).toBe("Files · Just Dashboard")
  expect(tabTitle("/")).toBe("Overview · Just Dashboard")
  expect(tabTitle("/docker/images")).toBe("Images · Just Dashboard")
  expect(tabTitle("/account")).toBe("Profile · Just Dashboard")
  expect(tabTitle("/account/sessions")).toBe("Sessions · Just Dashboard")
})

test("two rows that share a word carry their section", () => {
  const pairs = [
    ["/metrics/alerts", "Alerts · Monitoring", "/proxy/alerts", "Alerts · Proxy & TLS"],
    ["/proxy/traffic", "Traffic · Proxy & TLS", "/network/traffic", "Traffic · Network"],
    ["/map", "Map", "/databases/map", "Map · Databases"],
    ["/security", "Security", "/account/security", "Security · Account"],
    [
      "/dashboard/configuration",
      "Configuration · Settings",
      "/proxy/config",
      "Configuration · Proxy & TLS",
    ],
  ]
  for (const [a, titleA, b, titleB] of pairs) {
    expect(tabTitle(a)).toBe(`${titleA} · Just Dashboard`)
    expect(tabTitle(b)).toBe(`${titleB} · Just Dashboard`)
  }
})

test("no two rows of the rail give their tabs the same title", () => {
  const seen = new Map()
  const walk = (entries) => {
    for (const entry of entries) {
      if (entry.href !== undefined) {
        const title = tabTitle(entry.href)
        // A section's landing row has the section's address, and its title.
        if (seen.has(title) && seen.get(title) !== entry.href)
          throw new Error(`${entry.href} and ${seen.get(title)} are both "${title}"`)
        seen.set(title, entry.href)
      }
      if (entry.children) walk(entry.children)
    }
  }
  walk(NAV.flatMap((group) => group.items))
  expect(seen.size).toBeGreaterThan(60)
})

test("an address under a row takes that row, or the section it sits in", () => {
  expect(tabTitle("/docker/containers/3f2a9c")).toBe("Containers · Just Dashboard")
  expect(tabTitle("/deploy/7")).toBe("Deployments · Just Dashboard")
  expect(tabTitle("/deploy/7/logs")).toBe("Logs · Deployments · Just Dashboard")
  expect(tabTitle("/account/no-such-page")).toBe("Account · Just Dashboard")
})

test("the item a page is about leads the title", () => {
  const heading = (...parts) => ({ parts, explicit: false })
  const said = (...parts) => ({ parts, explicit: true })
  const title = (path, name) => tabTitle(path, undefined, name)
  expect(title("/docker/containers/3f2a9c", heading("nginx"))).toBe(
    "nginx · Containers · Just Dashboard",
  )
  expect(title("/databases/1/query", heading("orders · Query"))).toBe(
    "orders · Query · Databases · Just Dashboard",
  )
  expect(title("/deploy/7", heading("shop"))).toBe("shop · Deployments · Just Dashboard")
  expect(title("/deploy/7/logs", heading("shop"))).toBe(
    "shop · Logs · Deployments · Just Dashboard",
  )
  expect(title("/deploy/7/settings/domains", heading("shop"))).toBe(
    "shop · Domains · Deployments · Just Dashboard",
  )
  expect(title("/deploy/7/runs/84", said("Deployment #42", "shop"))).toBe(
    "Deployment #42 · shop · Deployments · Just Dashboard",
  )
  expect(title("/deploy/new", heading("New project"))).toBe(
    "New project · Deployments · Just Dashboard",
  )
  expect(title("/no-such-page", heading("Page not found"))).toBe("Page not found · Just Dashboard")
  expect(title("/docker/no-such-page", heading("Page not found"))).toBe(
    "Page not found · Docker · Just Dashboard",
  )
  // The server's name still follows the page.
  expect(tabTitle("/docker/containers/3f2a9c", "atlas", heading("nginx"))).toBe(
    "nginx · Containers · atlas · Just Dashboard",
  )
})

test("a list page keeps its rail entry's word unless the page says otherwise on purpose", () => {
  const title = (path, name) => tabTitle(path, undefined, name)
  expect(title("/docker/images", { parts: ["Container images"], explicit: false })).toBe(
    "Images · Just Dashboard",
  )
  expect(title("/docker/images", { parts: ["nginx:1.27"], explicit: true })).toBe(
    "nginx:1.27 · Images · Just Dashboard",
  )
  // A word said twice is said once, whatever its case.
  expect(title("/deploy/7/deployments", { parts: ["shop"], explicit: false })).toBe(
    "shop · Deployments · Just Dashboard",
  )
  expect(title("/databases/9", { parts: ["databases"], explicit: false })).toBe(
    "databases · Just Dashboard",
  )
  expect(title("/files", { parts: [], explicit: false })).toBe("Files · Just Dashboard")
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
