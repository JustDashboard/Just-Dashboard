import { expect, test } from "bun:test"
import {
  gatesHere,
  hstsOffered,
  hstsPolicyState,
  routeAccessState,
  savedHSTS,
  signInConflicts,
  signInThrough,
  withPreload,
} from "./route-access"
import { chooseSsoProvider } from "@/lib/sso-providers"

const authelia = {
  provider: "authelia",
  verify: "http://127.0.0.1:9091/api/authz/auth-request",
  signIn: "https://auth.example.test/",
}

test("the state says who reaches the domains: an address list, a sign-in, or both", () => {
  expect(routeAccessState([], undefined)).toBe("Everyone · every domain above")
  expect(routeAccessState(["203.0.113.7"], undefined)).toBe("Only 1 address · every domain above")
  expect(routeAccessState([], authelia)).toBe("Signed in through Authelia · every domain above")
  expect(routeAccessState(["10.0.0.0/8", "203.0.113.7"], authelia)).toBe(
    "Only 2 addresses, signed in through Authelia",
  )
  expect(routeAccessState([], { ...authelia, provider: "custom" })).toBe(
    "Signed in through an auth server · every domain above",
  )
})

test("a sign-in for previews leaves this route open, unless it is a preview's", () => {
  const previews = { ...authelia, previewsOnly: true }
  expect(gatesHere(previews, false)).toBe(false)
  expect(gatesHere(previews, true)).toBe(true)
  expect(gatesHere(undefined, true)).toBe(false)
  expect(routeAccessState([], previews)).toBe("Everyone here · previews sign in through Authelia")
  expect(routeAccessState(["203.0.113.7"], previews)).toBe(
    "Only 1 address here · previews sign in through Authelia",
  )
  expect(routeAccessState([], previews, true)).toBe(
    "Signed in through Authelia · every domain above",
  )
})

test("a password or a plain-HTTP name on a gated route is named before the save", () => {
  const domains = [
    { hostname: "app.example.test", https: true, ownership: "managed" },
    {
      hostname: "old.example.test",
      https: false,
      ownership: "managed",
      redirectTo: "app.example.test",
    },
  ]
  expect(signInConflicts(domains, authelia)).toEqual([])
  expect(signInConflicts(domains, undefined)).toEqual([])
  const guarded = [{ ...domains[0], protection: { username: "team" } }, domains[1]]
  expect(signInConflicts(guarded, authelia)).toEqual([
    "Signing in replaces the password on app.example.test. Remove it under the domain first.",
  ])
  const plain = [{ ...domains[0], https: false }, domains[1]]
  expect(signInConflicts(plain, authelia)[0]).toStartWith(
    "app.example.test is served over plain HTTP.",
  )
  expect(signInConflicts(plain, { ...authelia, previewsOnly: true })).toEqual([])
})

test("choosing a provider moves a preset check address and keeps a typed one", () => {
  expect(chooseSsoProvider(authelia, "oauth2-proxy")).toEqual({
    provider: "oauth2-proxy",
    verify: "http://127.0.0.1:4180/oauth2/auth",
  })
  expect(chooseSsoProvider({ verify: "http://10.0.0.5:9091/check" }, "authentik")).toEqual({
    provider: "authentik",
  })
  expect(signInThrough({ ...authelia, provider: "oauth2-proxy" })).toBe("oauth2-proxy")
})

test("HSTS is offered with a served HTTPS domain, or while a policy is saved", () => {
  const https = { hostname: "app.example.test", https: true, ownership: "managed" }
  const plain = { hostname: "old.example.test", https: false, ownership: "managed" }
  const redirect = { ...https, hostname: "www.app.example.test", redirectTo: "app.example.test" }
  expect(hstsOffered([https, plain], undefined)).toBe(true)
  expect(hstsOffered([plain], undefined)).toBe(false)
  // A redirecting HTTPS name serves nothing to send the header on.
  expect(hstsOffered([plain, redirect], undefined)).toBe(false)
  expect(hstsOffered([plain], { includeSubDomains: true })).toBe(true)
  expect(hstsOffered([], {})).toBe(true)
})

test("the policy's state says how long and what it covers, and it saves without its defaults", () => {
  expect(hstsPolicyState(undefined)).toBe("Off")
  expect(hstsPolicyState({})).toBe("6 months")
  expect(hstsPolicyState({ maxAge: 300, includeSubDomains: true })).toBe("5 minutes · subdomains")
  expect(hstsPolicyState({ maxAge: 31536000, includeSubDomains: true, preload: true })).toBe(
    "1 year · subdomains · preload",
  )
  expect(hstsPolicyState({ maxAge: 4000 })).toBe("4000 seconds")
  expect(savedHSTS(undefined)).toBeUndefined()
  expect(savedHSTS({ maxAge: 15552000, includeSubDomains: false })).toEqual({})
  expect(savedHSTS({ maxAge: 86400, includeSubDomains: true, preload: false })).toEqual({
    maxAge: 86400,
    includeSubDomains: true,
  })
  // Preload raises the policy to what the browsers' list requires.
  expect(withPreload({ maxAge: 86400 })).toEqual({
    maxAge: 31536000,
    includeSubDomains: true,
    preload: true,
  })
  expect(withPreload({ maxAge: 63072000, includeSubDomains: true })).toEqual({
    maxAge: 63072000,
    includeSubDomains: true,
    preload: true,
  })
})
