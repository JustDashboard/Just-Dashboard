import { describe, expect, test } from "bun:test"
import {
  clientConfig,
  grantList,
  grantsOf,
  grantsWithin,
  grantSummary,
  sameGrants,
} from "./mcp-server"

describe("clientConfig", () => {
  test("adds the server to Claude Code with the key as a header", () => {
    expect(clientConfig("claude-code", "https://dash.example.com", "vpsd_abc")).toBe(
      'claude mcp add --transport http just-dashboard https://dash.example.com/api/v1/mcp --header "Authorization: Bearer vpsd_abc"',
    )
  })

  test("writes each client's own file shape", () => {
    const cursor = JSON.parse(clientConfig("cursor", "https://dash.example.com", "vpsd_abc"))
    expect(cursor.mcpServers["just-dashboard"]).toEqual({
      url: "https://dash.example.com/api/v1/mcp",
      headers: { Authorization: "Bearer vpsd_abc" },
    })
    const vscode = JSON.parse(clientConfig("vscode", "https://dash.example.com", "vpsd_abc"))
    expect(vscode.servers["just-dashboard"].type).toBe("http")
    expect(vscode.servers["just-dashboard"].headers.Authorization).toBe("Bearer vpsd_abc")
  })

  test("leaves a placeholder where no key has been made", () => {
    expect(clientConfig("claude-code", "https://dash.example.com")).toContain("Bearer vpsd_…")
  })
})

describe("grants", () => {
  const token = {
    connections: [
      { connectionId: 7, access: "write" },
      { connectionId: 2, access: "read" },
    ],
  }

  test("round-trip in id order", () => {
    expect(grantList(grantsOf(token))).toEqual([
      { connectionId: 2, access: "read" },
      { connectionId: 7, access: "write" },
    ])
    expect(grantsOf(undefined)).toEqual({})
  })

  test("compare by database and access", () => {
    expect(sameGrants({ 2: "read", 7: "write" }, grantsOf(token))).toBe(true)
    expect(sameGrants({ 2: "read", 7: "read" }, grantsOf(token))).toBe(false)
    expect(sameGrants({ 2: "read" }, grantsOf(token))).toBe(false)
  })

  test("narrow to reads for an owner who can no longer change a database", () => {
    expect(grantsWithin({ 2: "read", 7: "write" }, false)).toEqual({ 2: "read", 7: "read" })
    expect(grantsWithin({ 7: "write" }, true)).toEqual({ 7: "write" })
  })
})

describe("grantSummary", () => {
  const names = { 1: "shop", 2: "notes", 3: "audit", 4: "billing", 5: "crm" }
  const nameOf = (id) => names[id]

  test("says what is read and what is changed", () => {
    const token = {
      connections: [
        { connectionId: 1, access: "read" },
        { connectionId: 2, access: "write" },
      ],
    }
    expect(grantSummary(token, nameOf)).toBe("reads shop, changes notes")
  })

  test("counts past three names", () => {
    const token = {
      connections: [1, 2, 3, 4, 5].map((id) => ({ connectionId: id, access: "read" })),
    }
    expect(grantSummary(token, nameOf)).toBe("reads audit, billing and 3 more")
  })

  test("skips a database the list no longer has", () => {
    expect(grantSummary({ connections: [{ connectionId: 99, access: "read" }] }, nameOf)).toBe(
      "no databases",
    )
  })
})
