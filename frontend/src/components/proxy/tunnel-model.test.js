import { describe, expect, test } from "bun:test"
import {
  cnameTarget,
  connectorWords,
  defaultSetup,
  dnsWords,
  tokenFromPaste,
  tokenProblem,
  tunnelCounts,
  tunnelHealth,
  tunnelLines,
  tunnelNameProblem,
} from "./tunnel-model"

const token = "eyJhIjoiYWNjMTIzIiwidCI6IjExMTExMTExIiwicyI6ImMyVmpjbVYwIn0="

const connected = (overrides = {}) => ({
  connected: true,
  connection: { mode: "token", tunnelId: "t1", accountId: "a1", createdAt: "", updatedAt: "" },
  connector: { state: "running", version: "2025.10.0" },
  ready: true,
  readyConnections: 4,
  checkedAt: "2026-10-29T10:00:00Z",
  api: { available: true },
  hostnames: [],
  ...overrides,
})

test("the tunnel's health is the connector's and the edge's", () => {
  expect(tunnelHealth(undefined)).toEqual({ tone: "unknown", label: "Not connected" })
  expect(tunnelHealth(connected())).toEqual({ tone: "running", label: "Connected" })
  expect(tunnelHealth(connected({ ready: false, readyConnections: 0 })).label).toBe(
    "No edge connection",
  )
  expect(tunnelHealth(connected({ connector: { state: "restarting", version: "" } })).tone).toBe(
    "danger",
  )
  expect(tunnelHealth(connected({ connector: { state: "exited", version: "" } })).label).toBe(
    "Connector exited",
  )
  expect(tunnelHealth(connected({ checkedAt: undefined })).label).toBe("Checking")
})

test("a record is said as what happened to it", () => {
  expect(dnsWords({ dns: "created" }).tone).toBe("running")
  expect(dnsWords({ dns: "manual" })).toEqual({
    tone: "warning",
    label: "Record to add in Cloudflare",
  })
  expect(dnsWords({ dns: "error" }).tone).toBe("danger")
  expect(dnsWords({ dns: "" }).tone).toBe("unknown")
  expect(cnameTarget("t1")).toBe("t1.cfargotunnel.com")
})

test("the token is read out of the command Cloudflare shows it in", () => {
  expect(tokenFromPaste(`  ${token}\n`)).toBe(token)
  expect(tokenFromPaste(`sudo cloudflared service install ${token}`)).toBe(token)
  expect(
    tokenFromPaste(
      `docker run cloudflare/cloudflared:latest tunnel --no-autoupdate run --token ${token}`,
    ),
  ).toBe(token)
  expect(tokenFromPaste(`cloudflared tunnel run --token "${token}"`)).toBe(token)
  expect(tokenProblem(`sudo cloudflared service install ${token}`)).toBeUndefined()
  expect(tokenProblem("")).toContain("Paste")
  expect(tokenProblem("not a token")).toContain("eyJ")
})

test("a tunnel's name is up to 64 characters", () => {
  expect(tunnelNameProblem("production")).toBeUndefined()
  expect(tunnelNameProblem(" ")).toBe("Name the tunnel.")
  expect(tunnelNameProblem("a/b")).toContain("64")
  expect(tunnelNameProblem("x".repeat(65))).toContain("64")
})

test("the hostnames are counted by what is left to do", () => {
  const status = connected({
    hostnames: [
      { hostname: "a", routed: true, dns: "created" },
      { hostname: "b", routed: true, dns: "error", error: "refused" },
      { hostname: "c", routed: false, dns: "manual" },
    ],
  })
  expect(tunnelCounts(status)).toEqual({ hostnames: 3, routed: 2, records: 1, problems: 1 })
})

describe("the picture's lines", () => {
  const base = {
    connected: true,
    connector: { state: "running", version: "2025.10.0" },
    ready: true,
    readyConnections: 4,
    checkedAt: "2026-11-13T00:00:00Z",
    api: { available: true },
    hostnames: [],
  }
  const host = (extra) => ({
    hostname: "a.example.com",
    projectId: 1,
    environmentId: 1,
    project: "a",
    routed: true,
    dns: "created",
    ...extra,
  })

  test("before a tunnel is connected every line is a dashed promise", () => {
    const lines = tunnelLines({ ...base, connected: false })
    expect(lines.edge).toEqual({ live: false, tone: "default", dashed: true })
    expect(lines.origin.dashed).toBe(true)
    expect(lines.hostname(host()).live).toBe(false)
  })

  test("a ready connector moves every line whose ends are up", () => {
    const lines = tunnelLines(base)
    expect(lines.edge.live).toBe(true)
    expect(lines.origin.live).toBe(true)
    expect(lines.hostname(host()).live).toBe(true)
  })

  test("no edge connection breaks the edge's line and stills the rest", () => {
    const lines = tunnelLines({ ...base, ready: false })
    expect(lines.edge).toEqual({ live: false, tone: "danger", dashed: true })
    expect(lines.origin).toEqual({ live: false, tone: "default", dashed: false })
    expect(lines.hostname(host()).live).toBe(false)
  })

  test("a stopped or foreign connector breaks the line to this server", () => {
    expect(tunnelLines({ ...base, connector: { state: "exited", version: "x" } }).origin.tone).toBe(
      "danger",
    )
    expect(
      tunnelLines({ ...base, connector: { state: "running", version: "x", foreign: true } }).edge
        .live,
    ).toBe(false)
  })

  test("a hostname's line says its record", () => {
    const lines = tunnelLines(base)
    expect(lines.hostname(host({ dns: "error" })).tone).toBe("danger")
    expect(lines.hostname(host({ dns: "manual" }))).toEqual({
      live: false,
      tone: "warning",
      dashed: true,
    })
    expect(lines.hostname(host({ routed: false })).tone).toBe("warning")
  })
})

describe("connectorWords and defaultSetup", () => {
  test("say what the container is doing", () => {
    expect(connectorWords({ state: "running", version: "x" })).toBe("running")
    expect(connectorWords({ state: "exited", version: "x", exitCode: 1 })).toBe(
      "exited with code 1",
    )
    expect(connectorWords({ state: "missing", version: "x" })).toBe("no container")
    expect(connectorWords({ state: "running", version: "x", foreign: true })).toBe(
      "a container this dashboard did not make",
    )
  })

  test("offer the API first where credentials are saved", () => {
    expect(defaultSetup({ api: { available: true } })).toBe("api")
    expect(defaultSetup({ api: { available: false } })).toBe("token")
    expect(defaultSetup(undefined)).toBe("token")
  })
})
