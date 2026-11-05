import { describe, expect, test } from "bun:test"
import { readdirSync, readFileSync, statSync } from "node:fs"
import { join } from "node:path"
import {
  HUB_ENTER_PATH,
  HUB_ENTER_SCRIPT,
  hubEnterDocument,
  safeNext,
  ticketFromHash,
  ticketUserId,
} from "./hub-enter.ts"

const payload = (fields) =>
  Buffer.from(JSON.stringify(fields))
    .toString("base64")
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "")

describe("the fragment", () => {
  test("only ticket= is read, whatever else the fragment holds", () => {
    expect(ticketFromHash("#ticket=v1.k.p.s")).toBe("v1.k.p.s")
    expect(ticketFromHash("#a=1&ticket=v1.k.p.s&b=2")).toBe("v1.k.p.s")
    expect(ticketFromHash("#xticket=v1")).toBe("")
    expect(ticketFromHash("")).toBe("")
    expect(ticketFromHash("#ticket=%E0%A4%A")).toBe("")
  })

  test("the account is read for the question alone, and anything odd reads as nobody", () => {
    expect(ticketUserId(`v1.k.${payload({ userId: 7 })}.s`)).toBe(7)
    expect(ticketUserId(`v1.k.${payload({ userId: "7" })}.s`)).toBe(0)
    expect(ticketUserId("v1.k.not-json.s")).toBe(0)
    expect(ticketUserId("garbage")).toBe(0)
  })

  test("next is a path on this server or the overview", () => {
    expect(safeNext("/docker")).toBe("/docker")
    expect(safeNext("//evil.example")).toBe("/dashboard")
    expect(safeNext("https://evil.example")).toBe("/dashboard")
    expect(safeNext(undefined)).toBe("/dashboard")
  })
})

describe("the document", () => {
  const doc = hubEnterDocument("bm9uY2U=")

  test("its first script is the inline one with this response's nonce, and it has no other", () => {
    const scripts = [...doc.matchAll(/<script\b[^>]*>/g)].map((m) => m[0])
    expect(scripts).toEqual(['<script nonce="bm9uY2U=">'])
    const head = doc.slice(doc.indexOf("<head>"), doc.indexOf("<script"))
    // Nothing but the charset comes before it.
    expect(head.replace(/\s+/g, "")).toBe('<head><metacharset="utf-8">')
  })

  test("it loads nothing from anywhere", () => {
    expect(doc).not.toMatch(/\b(src|href)="(https?:)?\/\//)
    expect(doc).not.toMatch(/<link\b(?![^>]*href="data:,")/)
    expect(doc).not.toMatch(/@import|url\(/)
  })

  test("the ticket leaves the address before the script does anything else", () => {
    const read = HUB_ENTER_SCRIPT.indexOf("location.hash")
    const cleared = HUB_ENTER_SCRIPT.indexOf(`history.replaceState(null, "", "${HUB_ENTER_PATH}")`)
    const firstFetch = HUB_ENTER_SCRIPT.indexOf("fetch(")
    expect(read).toBeGreaterThan(0)
    expect(cleared).toBeGreaterThan(read)
    expect(firstFetch).toBeGreaterThan(cleared)
    // Read once, and never written anywhere a later script, a URL or a log could find it.
    expect(HUB_ENTER_SCRIPT.match(/location\.hash/g)).toHaveLength(1)
    expect(HUB_ENTER_SCRIPT).not.toMatch(
      /localStorage|sessionStorage|console\.|window\.\w+\s*=|innerHTML/,
    )
  })

  test("the ticket travels only in the redeem's body, which says it is not a form post", () => {
    expect(HUB_ENTER_SCRIPT).toContain('fetch("/api/v1/auth/hub/enter"')
    expect(HUB_ENTER_SCRIPT).toContain('"X-JD-CSRF": "1"')
    expect(HUB_ENTER_SCRIPT).toContain("JSON.stringify({ ticket: ticket })")
    expect(HUB_ENTER_SCRIPT.match(/fetch\(/g)).toHaveLength(2)
  })
})

// No other code reads a sign-in ticket out of an address: the fragment is read for one in this
// module alone (hub-link-protocol §9.5, step 1).
test("nothing else in the frontend reads ticket= from a fragment", () => {
  const root = join(import.meta.dir, "..")
  const offenders = []
  const walk = (dir) => {
    for (const name of readdirSync(dir)) {
      const path = join(dir, name)
      if (statSync(path).isDirectory()) walk(path)
      else if (/\.(ts|tsx|js)$/.test(name) && !/hub-enter(\.test)?\.(ts|js)$/.test(name)) {
        if (/ticket=/.test(readFileSync(path, "utf8"))) offenders.push(path)
      }
    }
  }
  walk(root)
  expect(offenders).toEqual([])
})
