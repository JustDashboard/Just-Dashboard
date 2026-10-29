import { expect, test } from "bun:test"
import {
  cnameTarget,
  dnsWords,
  tokenFromPaste,
  tokenProblem,
  tunnelCounts,
  tunnelHealth,
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
