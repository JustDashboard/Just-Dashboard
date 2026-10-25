import { expect, test } from "bun:test"
import {
  countdown,
  fieldLabel,
  heartbeatStale,
  NEVER_SENT,
  policyWords,
  scopeAllows,
  scopeName,
  scopeShares,
  sharedGroups,
  sharedItems,
  siteHost,
} from "./account-link"

// The heartbeat of docs/server-linking-plan.md §4.4, at `operate`.
const snapshot = {
  protocol: 1,
  sentAt: 1761350400,
  server: {
    name: "box",
    version: "0.8.2",
    latest: "0.8.3",
    updateAvailable: true,
    updateRun: { status: "running", phase: "building", toVersion: "0.8.3" },
    scope: "operate",
    uptimeSeconds: 864000,
    os: "Ubuntu 24.04",
    kernel: "6.14.0-37-generic",
    arch: "amd64",
  },
  access: {
    kind: "tailscale",
    endpoint: "https://box.tail1234.ts.net:8443",
    port: 8443,
    tailscale: { hostname: "box.tail1234.ts.net", ip4: "100.101.102.103", running: true },
  },
  metrics: {
    cpuPercent: 23.4,
    memPercent: 61,
    memUsed: 4_100_000_000,
    memTotal: 8_000_000_000,
    diskMount: "/",
    netRx: 1200.5,
  },
  containers: {
    running: 12,
    list: [
      { id: "3f2", name: "shop-web", image: "ghcr.io/x/shop", state: "running", health: "healthy" },
      { id: "9a1", name: "shop-db", state: "running" },
    ],
  },
  alerts: [{ kind: "disk_filling", subject: "/var", reading: "91%", since: 1761340000 }],
  backups: { jobs: 2, lastRun: { status: "success", at: 1761300000 }, failed: 0 },
}

const rows = (key) => sharedGroups(snapshot).find((group) => group.key === key).rows
const value = (key, label) => rows(key).find((row) => row.label === label)?.value

test("each scope names what it shares and what it lets the site do", () => {
  expect(scopeName("observe")).toBe("Observe")
  expect(scopeShares("observe")).toContain("Access path (tailnet address or SSH tunnel port)")
  expect(scopeShares("observe")).toContain("Alerts firing")
  expect(scopeShares("observe")).not.toContain("project names")
  expect(scopeShares("operate")).toContain("backup-job and last-deployed project names")
  expect(scopeShares("maintain")).toContain("backup-job and last-deployed project names")
  expect(scopeAllows("observe")).toBe("Refresh the snapshot.")
  expect(scopeAllows("operate")).toContain("restart a stack")
  expect(scopeAllows("operate")).not.toContain("update")
  expect(scopeAllows("maintain")).toContain("install a dashboard update")
  expect(scopeAllows("maintain")).toContain("restart the dashboard")
})

test("names are shared from operate up, and nothing else is added there", () => {
  const observe = sharedItems("observe")
  expect(observe).toHaveLength(7)
  expect(sharedItems("operate")).toEqual([
    ...observe,
    "Container, stack, backup-job and last-deployed project names",
  ])
  expect(sharedItems("maintain")).toEqual(sharedItems("operate"))
  expect(NEVER_SENT).toContain("API keys")
})

test("the site is named by its host", () => {
  expect(siteHost("https://just-dashboard.com")).toBe("just-dashboard.com")
  expect(siteHost("http://localhost:3000")).toBe("localhost:3000")
  expect(siteHost("not a url")).toBe("not a url")
})

test("a request counts down in minutes and seconds, and says when it is over", () => {
  const now = Date.parse("2026-10-25T12:00:00Z")
  const at = (seconds) => new Date(now + seconds * 1000).toISOString()
  expect(countdown(at(15 * 60), now)).toEqual({ label: "15:00", expired: false })
  expect(countdown(at(872.4), now)).toEqual({ label: "14:33", expired: false })
  expect(countdown(at(65), now)).toEqual({ label: "01:05", expired: false })
  expect(countdown(at(1), now)).toEqual({ label: "00:01", expired: false })
  expect(countdown(at(3725), now)).toEqual({ label: "1:02:05", expired: false })
  expect(countdown(at(0), now)).toEqual({ label: "Expired", expired: true })
  expect(countdown(at(-30), now)).toEqual({ label: "Expired", expired: true })
  // A deadline nobody can read must not lock the Approve button.
  expect(countdown("soon", now)).toEqual({ label: "—", expired: false })
})

test("a heartbeat is stale after ten missed", () => {
  const now = Date.parse("2026-10-25T12:00:00Z")
  const ago = (seconds) => new Date(now - seconds * 1000).toISOString()
  expect(heartbeatStale(ago(30), now)).toBe(false)
  expect(heartbeatStale(ago(299), now)).toBe(false)
  expect(heartbeatStale(ago(301), now)).toBe(true)
  // Just linked: nothing sent yet is not a fault.
  expect(heartbeatStale(undefined, now)).toBe(false)
})

test("the update policy is read in the words the site sets it in", () => {
  expect(policyWords(undefined)).toEqual({ auto: "Off" })
  expect(policyWords({ auto: "off", window: { start: "03:00", end: "05:00" } })).toEqual({
    auto: "Off",
  })
  expect(policyWords({ auto: "patch", window: { start: "03:00", end: "05:00" } })).toEqual({
    auto: "Patch releases",
    window: "between 03:00 and 05:00, server time",
  })
  expect(policyWords({ auto: "all", window: { start: "", end: "" } })).toEqual({
    auto: "All releases",
    window: undefined,
  })
})

test("field names are read as words, with the unit moved into the value", () => {
  expect(fieldLabel(["cpuPercent"])).toBe("CPU")
  expect(fieldLabel(["memPercent"])).toBe("Memory")
  expect(fieldLabel(["uptimeSeconds"])).toBe("Uptime")
  expect(fieldLabel(["memUsed"])).toBe("Memory used")
  expect(fieldLabel(["tailscale", "ip4"])).toBe("Tailscale IPv4")
  expect(fieldLabel(["updateRun", "toVersion"])).toBe("Update run to version")
  expect(fieldLabel(["sshHost"])).toBe("SSH host")
  expect(fieldLabel(["netRx"])).toBe("Network received")
  expect(fieldLabel(["psiCpu"])).toBe("PSI CPU")
  // A key that is only the unit keeps it.
  expect(fieldLabel(["percent"])).toBe("Percent")
})

test("the heartbeat is grouped as it was sent", () => {
  const groups = sharedGroups(snapshot)
  expect(groups.map((group) => group.key)).toEqual([
    "heartbeat",
    "server",
    "access",
    "metrics",
    "containers",
    "backups",
  ])
  expect(groups.map((group) => group.label)).toEqual([
    "Heartbeat",
    "Server",
    "Access",
    "Metrics",
    "Containers",
    "Backups",
  ])
  expect(sharedGroups(undefined)).toEqual([])
  expect(sharedGroups({})).toEqual([])
})

test("values are read the way a person reads them", () => {
  expect(value("server", "Update available")).toBe("yes")
  expect(value("server", "Uptime")).toBe("10d")
  expect(value("server", "Kernel")).toBe("6.14.0-37-generic")
  expect(value("metrics", "CPU")).toBe("23.4%")
  expect(value("metrics", "Memory")).toBe("61.0%")
  expect(value("metrics", "Memory used")).toBe("3.8 GB")
  expect(value("metrics", "Disk mount")).toBe("/")
  // No unit is guessed for a rate the heartbeat does not label.
  expect(value("metrics", "Network received")).toBe("1200.5")
  expect(value("containers", "Running")).toBe("12")
})

test("nested objects are flattened under their parent's name", () => {
  expect(value("server", "Update run status")).toBe("running")
  expect(value("server", "Update run phase")).toBe("building")
  expect(value("access", "Tailscale hostname")).toBe("box.tail1234.ts.net")
  expect(value("access", "Tailscale IPv4")).toBe("100.101.102.103")
  expect(value("access", "Tailscale running")).toBe("yes")
  expect(value("backups", "Last run status")).toBe("success")
})

test("moments are drawn from unix seconds and keep their ISO form for the locale", () => {
  const sent = rows("heartbeat").find((row) => row.label === "Sent at")
  expect(sent.time).toBe("2025-10-25T00:00:00.000Z")
  expect(sent.value).toBe("2025-10-25 00:00 UTC")
  expect(rows("backups").find((row) => row.label === "Last run at").time).toBe(
    "2025-10-24T10:00:00.000Z",
  )
  // Milliseconds are told from seconds by size.
  const [heartbeat] = sharedGroups({ sentAt: 1761350400000 })
  expect(heartbeat.rows[0].time).toBe("2025-10-25T00:00:00.000Z")
})

test("a list of records is a count with one line per record", () => {
  const list = rows("containers").find((row) => row.label === "List")
  expect(list.value).toBe("2 entries")
  expect(list.items).toEqual([
    "3f2 · shop-web · ghcr.io/x/shop · running · healthy",
    "9a1 · shop-db · running",
  ])
  const alerts = rows("heartbeat").find((row) => row.label === "Alerts")
  expect(alerts.value).toBe("1 entry")
  expect(alerts.items).toEqual(["disk_filling · /var · 91% · 2025-10-24 21:06 UTC"])
})

test("empty and missing values say so instead of vanishing", () => {
  const [group] = sharedGroups({ alerts: [], note: "", gone: null, lists: ["a", "b"], empty: {} })
  expect(group.rows).toEqual([
    { label: "Alerts", value: "none" },
    { label: "Note", value: "—" },
    { label: "Gone", value: "—" },
    { label: "Lists", value: "a, b" },
    { label: "Empty", value: "—" },
  ])
})
