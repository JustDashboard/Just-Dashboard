import { expect, test } from "bun:test"
import {
  hostnameFromInput,
  hostnameProblem,
  httpsTip,
  ownershipTip,
  passwordProblem,
  passwordTip,
  protectionIncomplete,
  protectionUserProblem,
  protectionsIncomplete,
  redirectTargets,
  sharedUser,
  tunnelTip,
  withRedirect,
  withoutDomain,
} from "./domain-rows"

const name = (hostname, rest = {}) => ({ hostname, https: true, ownership: "managed", ...rest })

test("a pasted address is reduced to the name in it", () => {
  expect(hostnameFromInput("  HTTPS://App.Example.com:8443/path?x=1#y ")).toBe("app.example.com")
  expect(hostnameFromInput("app.example.com.")).toBe("app.example.com")
  expect(hostnameFromInput("*.Example.com")).toBe("*.example.com")
})

test("a name the server would refuse is refused first, and an empty one is not yet wrong", () => {
  const domains = [name("a.example.com")]
  expect(hostnameProblem("", domains)).toBeUndefined()
  expect(hostnameProblem("b.example.com", domains)).toBeUndefined()
  expect(hostnameProblem("*.example.com", domains)).toBeUndefined()
  expect(hostnameProblem("localhost", domains)).toContain("at least one dot")
  expect(hostnameProblem("bad_name.example.com", domains)).toContain("letters, digits")
  expect(hostnameProblem("a.example.com", domains)).toContain("already on this environment")
})

test("a password is held to the server's bounds, in bytes at the top", () => {
  expect(passwordProblem("")).toBeUndefined()
  expect(passwordProblem("short")).toBe("At least 8 characters")
  expect(passwordProblem("a".repeat(72))).toBeUndefined()
  expect(passwordProblem("a".repeat(73))).toBe("At most 72 characters")
  expect(passwordProblem("é".repeat(37))).toBe("At most 72 characters")
})

test("a user name is held to the server's characters", () => {
  expect(protectionUserProblem("")).toBeUndefined()
  expect(protectionUserProblem("team.a-1_b@x")).toBeUndefined()
  expect(protectionUserProblem("two words")).toContain("Letters, digits")
})

test("a protection the server would drop or refuse holds the save", () => {
  const open = name("a.example.com")
  expect(protectionIncomplete(open, "")).toBe(false)
  // The server reads this as no protection at all and would say Saved.
  expect(protectionIncomplete({ ...open, protection: { username: "" } }, "")).toBe(true)
  expect(protectionIncomplete({ ...open, protection: { username: "team" } }, "")).toBe(true)
  expect(protectionIncomplete({ ...open, protection: { username: "team" } }, "short")).toBe(true)
  expect(protectionIncomplete({ ...open, protection: { username: "team" } }, "long enough")).toBe(
    false,
  )
  const saved = { ...open, protection: { username: "team", hash: "sealed" } }
  expect(protectionIncomplete(saved, "")).toBe(false)
  expect(protectionIncomplete(saved, "short")).toBe(true)
  expect(protectionsIncomplete([open, saved], {})).toBe(false)
  expect(protectionsIncomplete([open, saved], { "a.example.com": "short" })).toBe(true)
})

test("two names with one user name and different passwords are named", () => {
  const domains = [
    name("a.example.com", { protection: { username: "team", hash: "one" } }),
    name("b.example.com", { protection: { username: "team", hash: "two" } }),
    name("c.example.com", { protection: { username: "other", hash: "one" } }),
  ]
  expect(sharedUser(domains, {}, "a.example.com")).toEqual(["b.example.com"])
  expect(sharedUser(domains, {}, "c.example.com")).toEqual([])
  // A password typed on one makes them differ, and typing the same on both does not.
  const same = domains.map((domain) => ({
    ...domain,
    protection: { ...domain.protection, hash: "one" },
  }))
  expect(sharedUser(same, {}, "a.example.com")).toEqual([])
  expect(sharedUser(same, { "a.example.com": "typed-secret" }, "a.example.com")).toEqual([
    "b.example.com",
  ])
})

test("a redirect lands only on a name that serves and is not a wildcard", () => {
  const domains = [
    name("*.example.com"),
    name("a.example.com"),
    name("b.example.com", { redirectTo: "a.example.com" }),
    name("c.example.com"),
  ]
  expect(redirectTargets(domains, "a.example.com")).toEqual(["c.example.com"])
  expect(redirectTargets(domains, "c.example.com")).toEqual(["a.example.com"])
})

test("a name that starts redirecting takes the names pointing at it along", () => {
  const domains = [
    name("a.example.com"),
    name("b.example.com", { redirectTo: "a.example.com", redirectCode: 301 }),
    name("c.example.com"),
  ]
  const next = withRedirect(domains, "a.example.com")
  expect(next[0].redirectTo).toBe("c.example.com")
  // The server refuses a chain, so b goes straight there, keeping its own code.
  expect(next[1].redirectTo).toBe("c.example.com")
  expect(next[1].redirectCode).toBe(301)
  expect(next[2].redirectTo).toBeUndefined()
  // The last name serving has nowhere to go.
  expect(withRedirect([name("a.example.com")], "a.example.com")).toEqual([name("a.example.com")])
})

test("a removed name's redirects go back to serving", () => {
  const next = withoutDomain(
    [
      name("a.example.com"),
      name("b.example.com", { redirectTo: "a.example.com", redirectCode: 301 }),
    ],
    "a.example.com",
  )
  expect(next).toHaveLength(1)
  expect(next[0].redirectTo).toBeUndefined()
  expect(next[0].redirectCode).toBeUndefined()
})

test("each segment's tooltip says what it does on this name, or why it is held", () => {
  expect(httpsTip({ https: true, tunnel: false, gated: false })).toContain("redirected to HTTPS")
  expect(httpsTip({ https: false, tunnel: false, gated: false })).toContain("plain HTTP")
  expect(httpsTip({ https: true, tunnel: true, gated: false })).toContain("Cloudflare's edge")
  expect(httpsTip({ https: true, tunnel: false, gated: true })).toContain("sign-in")
  // The password is the route's: it is asked on every name, not this one alone.
  expect(passwordTip({ guarded: false, gated: false })).toContain("every name")
  expect(passwordTip({ guarded: false, gated: true })).toContain("sign-in")
  expect(passwordTip({ guarded: true, gated: true })).toContain("every name")
  expect(tunnelTip({ tunnel: false, wildcard: true, fenced: false })).toContain("wildcard")
  expect(tunnelTip({ tunnel: false, wildcard: false, fenced: true })).toContain("connector")
  // Already through the tunnel, it can still be turned off, so it says what it does.
  expect(tunnelTip({ tunnel: true, wildcard: false, fenced: true })).toContain("no port is opened")
  expect(
    ownershipTip([
      { word: "Managed", hint: "removed with the project" },
      { word: "Linked", hint: "never removed" },
    ]),
  ).toBe("Managed: removed with the project. Linked: never removed.")
})
