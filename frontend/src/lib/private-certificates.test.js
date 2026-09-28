import { describe, expect, test } from "bun:test"
import {
  checkOutcome,
  checkTrouble,
  joinNames,
  keyTypeName,
  leafState,
  nameProblem,
  rootExpired,
  suggestName,
} from "./private-certificates"

const now = Date.parse("2026-09-28T12:00:00Z")
const check = (overrides) => ({
  at: "2026-09-28T03:00:00Z",
  renewed: [],
  reloaded: [],
  failed: [],
  ...overrides,
})

describe("suggestName", () => {
  test("is the first name, as a directory can hold it", () => {
    expect(suggestName(["App.Lan.Test", "www.app.lan.test"])).toBe("app.lan.test")
    expect(suggestName(["", "  nas.lan  "])).toBe("nas.lan")
    expect(suggestName(["*.lan.example"])).toBe("wildcard.lan.example")
    expect(suggestName(["192.168.1.10"])).toBe("192.168.1.10")
    expect(suggestName(["fe80::1"])).toBe("fe80-1")
    expect(suggestName(["::1"])).toBe("1")
    expect(suggestName([])).toBe("")
  })

  test("always passes the server's rule", () => {
    for (const names of [
      ["*.lan.example"],
      ["fe80::1"],
      ["x".repeat(80) + ".test"],
      ["a_b.test"],
    ]) {
      expect(nameProblem(suggestName(names))).toBeUndefined()
    }
  })
})

describe("nameProblem", () => {
  test("holds a name to the rule imports follow", () => {
    expect(nameProblem("")).toBeUndefined()
    expect(nameProblem("shop.example.com")).toBeUndefined()
    expect(nameProblem("Shop")).toBeDefined()
    expect(nameProblem("-shop")).toBeDefined()
    expect(nameProblem("../ca")).toBeDefined()
    expect(nameProblem("a".repeat(65))).toBeDefined()
    expect(nameProblem("caddy-0da2f3126af1d760c968313b")).toMatch(/Caddy/)
  })
})

describe("the renewal readings", () => {
  test("say what a pass did in a sentence", () => {
    expect(checkOutcome(check())).toBe("nothing was due")
    expect(checkOutcome(check({ renewed: ["nas"] }))).toBe("renewed nas")
    expect(
      checkOutcome(check({ renewed: ["nas", "grafana", "wiki"], reloaded: ["nas.lan"] })),
    ).toBe("renewed nas, grafana and wiki, and reloaded nginx for nas.lan")
  })

  test("raise a pass that could not finish, or left one unrenewed", () => {
    expect(checkTrouble(undefined)).toBeUndefined()
    expect(checkTrouble(check({ renewed: ["nas"] }))).toBeUndefined()
    expect(checkTrouble(check({ failed: ["nas: its key could not be read"] }))).toEqual({
      tone: "warning",
      title: "A certificate could not be renewed",
      lines: ["nas: its key could not be read"],
    })
    expect(checkTrouble(check({ failed: ["a: x", "b: y"] })).title).toBe(
      "2 certificates could not be renewed",
    )
    const failed = checkTrouble(
      check({ renewed: ["nas"], error: "nginx was not reloaded", failed: ["b: y"] }),
    )
    expect(failed.tone).toBe("danger")
    expect(failed.lines).toEqual(["nginx was not reloaded", "b: y"])
  })

  test("place a certificate of the local CA's", () => {
    const leaf = (daysLeft, error) => ({
      daysLeft,
      error,
      notAfter: new Date(now + daysLeft * 86_400_000 + 3_600_000).toISOString(),
    })
    expect(leafState(leaf(300), 45, now)).toBe("scheduled")
    expect(leafState(leaf(45), 45, now)).toBe("due")
    expect(leafState(leaf(-2), 45, now)).toBe("expired")
    expect(leafState(leaf(300, "its key could not be read"), 45, now)).toBe("cannot-renew")
  })

  test("know when the root itself has run out", () => {
    expect(rootExpired({ notAfter: "2036-01-01T00:00:00Z" }, now)).toBe(false)
    expect(rootExpired({ notAfter: "2026-09-28T11:59:59Z" }, now)).toBe(true)
    expect(rootExpired({}, now)).toBe(false)
  })

  test("name keys and lists of names", () => {
    expect(keyTypeName("rsa-3072")).toBe("RSA 3072")
    expect(keyTypeName(undefined)).toBe("an unusual key")
    expect(joinNames([])).toBe("")
    expect(joinNames(["a"])).toBe("a")
    expect(joinNames(["a", "b"])).toBe("a and b")
  })
})
