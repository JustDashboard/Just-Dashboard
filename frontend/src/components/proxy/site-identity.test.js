import { describe, expect, test } from "bun:test"
import {
  deriveIdentity,
  fileNameFor,
  fileNameProblem,
  fixedFor,
  FOLLOW_DOMAINS,
  lineageFor,
} from "./site-identity"

/** Replays typing the domains one character at a time, as the field does. */
function typeDomains(text, fixed = FOLLOW_DOMAINS, start = { name: "" }) {
  let identity = start
  for (let i = 1; i <= text.length; i++) {
    const domains = text
      .slice(0, i)
      .split(/[\s,]+/)
      .filter(Boolean)
    identity = { ...identity, ...deriveIdentity(domains, identity, fixed) }
  }
  return identity
}

describe("site identity", () => {
  test("typing a domain names the site after all of it, not its first letter", () => {
    expect(typeDomains("app.example.com www.app.example.com")).toEqual({
      name: "app.example.com",
      certPath: "/etc/letsencrypt/live/app.example.com/fullchain.pem",
      keyPath: "/etc/letsencrypt/live/app.example.com/privkey.pem",
    })
  })

  test("a wildcard is named after, and certified for, its parent zone", () => {
    expect(fileNameFor("*.example.com")).toBe("example.com")
    expect(lineageFor("*.Example.com")).toBe("example.com")
    expect(typeDomains("*.example.com").certPath).toBe(
      "/etc/letsencrypt/live/example.com/fullchain.pem",
    )
  })

  test("clearing the domains clears what followed them", () => {
    const typed = typeDomains("app.example.com")
    expect(deriveIdentity([], typed, FOLLOW_DOMAINS)).toEqual({
      name: "",
      certPath: undefined,
      keyPath: undefined,
    })
  })

  test("a fixed part is never overwritten", () => {
    const start = { name: "legacy", certPath: "/etc/ssl/site.crt", keyPath: "/etc/ssl/site.key" }
    const identity = typeDomains("new.example.com", fixedFor(start, true), start)
    expect(identity).toEqual(start)
  })

  test("a loaded site without certificate paths still takes them from its domains", () => {
    const fixed = fixedFor({ name: "plain" }, true)
    expect(fixed).toEqual({ name: true, certPath: false, keyPath: false })
    expect(typeDomains("plain.example.com", fixed, { name: "plain" })).toEqual({
      name: "plain",
      certPath: "/etc/letsencrypt/live/plain.example.com/fullchain.pem",
      keyPath: "/etc/letsencrypt/live/plain.example.com/privkey.pem",
    })
  })

  test("a file name keeps only what the server accepts", () => {
    expect(fileNameFor("App.Example.COM")).toBe("app.example.com")
    expect(fileNameFor("-_.x")).toBe("x")
    expect(fileNameFor("a".repeat(80))).toHaveLength(64)
  })

  test("a typed file name is checked against the server's rule", () => {
    expect(fileNameProblem("app.example.com")).toBeUndefined()
    expect(fileNameProblem("a_b-c.1")).toBeUndefined()
    expect(fileNameProblem("")).toBe("A site needs a file name.")
    for (const name of ["App", ".hidden", "a/b", "a b", "a".repeat(65)]) {
      expect(fileNameProblem(name)).toContain("Lowercase letters")
    }
    // Whatever a domain turns into is always a name the server accepts.
    for (const domain of ["*.Example.com", "-x.example.com", "xn--bcher-kva.example"]) {
      expect(fileNameProblem(fileNameFor(domain))).toBeUndefined()
    }
  })
})
