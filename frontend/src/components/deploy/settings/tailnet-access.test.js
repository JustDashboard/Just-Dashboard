import { expect, test } from "bun:test"
import {
  TAILNET_MAX_ALLOW,
  isTailnetTag,
  readTailnetEntry,
  tailnetAccessState,
  tailnetBlockers,
  tailnetRuntime,
  tailnetSwitchClears,
} from "./tailnet-access"

test("a login is kept in lower case, a tag as written, and anything else is named", () => {
  expect(readTailnetEntry(" Alice@Example.com ")).toEqual({
    entry: "alice@example.com",
    tag: false,
  })
  expect(readTailnetEntry("bob@github")).toEqual({ entry: "bob@github", tag: false })
  expect(readTailnetEntry("tag:CI-2")).toEqual({ entry: "tag:CI-2", tag: true })
  expect(readTailnetEntry("TAG:ci").error).toContain("A tag is tag:")
  expect(readTailnetEntry("tag:2ci").error).toContain("A tag is tag:")
  expect(readTailnetEntry("alice").error).toBe(
    "alice is neither a Tailscale login (user@example.com) nor a tag (tag:name)",
  )
  expect(readTailnetEntry("*@example.com").error).toContain("neither")
  expect(readTailnetEntry("  ").error).toBe("Type a Tailscale login or a tag")
})

test("the same login twice, in any case, and a list past the limit are refused", () => {
  expect(readTailnetEntry("ALICE@example.com", ["alice@example.com"]).error).toBe(
    "alice@example.com is already on the list",
  )
  // Tags are compared exactly, as the tailnet's policy compares them.
  expect(readTailnetEntry("tag:CI", ["tag:ci"])).toEqual({ entry: "tag:CI", tag: true })
  const full = Array.from({ length: TAILNET_MAX_ALLOW }, (_, i) => `user${i}@example.com`)
  expect(readTailnetEntry("one@example.com", full).error).toBe("At most 64 logins and tags")
  expect(isTailnetTag("tag:ci")).toBe(true)
  expect(isTailnetTag("alice@example.com")).toBe(false)
})

test("the state says who opens the environment", () => {
  expect(tailnetAccessState(undefined)).toBe("Off · answers on its public domains")
  expect(tailnetAccessState({})).toBe("Everyone on the tailnet")
  expect(tailnetAccessState({ allow: ["alice@example.com"] })).toBe("Only alice@example.com")
  expect(tailnetAccessState({ allow: ["alice@example.com", "tag:ci"] })).toBe(
    "Only 2 logins and tags",
  )
})

const publicConfiguration = {
  domains: [
    { hostname: "app.example.com", https: true, ownership: "managed" },
    {
      hostname: "www.app.example.com",
      https: true,
      ownership: "managed",
      redirectTo: "app.example.com",
    },
  ],
  runtime: {
    strategy: "blue_green",
    bindAddress: "0.0.0.0",
    internalPort: 3000,
    forwardAuth: { provider: "authelia", verify: "http://127.0.0.1:9091/x", signIn: "https://a/" },
    allowFrom: ["203.0.113.7"],
    hsts: {},
    errorPages: [502],
  },
}

test("turning it on says what only a public route has and takes it away", () => {
  expect(tailnetSwitchClears(publicConfiguration)).toEqual([
    "the domains app.example.com, www.app.example.com",
    "the sign-in",
    "the allowed addresses",
    "HSTS",
    "the error pages",
    "the bind address 0.0.0.0, so the container is published on loopback",
  ])
  const runtime = tailnetRuntime(publicConfiguration.runtime, ["alice@example.com"])
  expect(runtime.tailnet).toEqual({ allow: ["alice@example.com"] })
  expect(runtime.bindAddress).toBe("127.0.0.1")
  for (const field of ["forwardAuth", "allowFrom", "hsts", "errorPages", "responseHeaders"])
    expect(runtime[field]).toBeUndefined()
  expect(runtime.internalPort).toBe(3000)
  expect(tailnetRuntime(publicConfiguration.runtime, []).tailnet).toEqual({})
  expect(tailnetRuntime({ ...runtime }, undefined).tailnet).toBeUndefined()
})

test("what the switch leaves alone because it was chosen under Runtime is named", () => {
  expect(tailnetBlockers({ hostNetwork: true, hostPort: 8080, ports: [{}] })).toEqual([
    "it runs on the host's network",
    "it publishes host port 8080",
    "it publishes 1 extra port",
  ])
  expect(tailnetBlockers({})).toEqual([])
})
