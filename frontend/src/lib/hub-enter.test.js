import { describe, expect, test } from "bun:test"
import { readdirSync, readFileSync, statSync } from "node:fs"
import { join } from "node:path"
import {
  HUB_ENTER_PATH,
  HUB_ENTER_REFUSALS,
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

  // The document loads no stylesheet, so it repeats the tokens it draws with. Each is the value
  // globals.css gives it on :root, its --muted being the app's --muted-foreground
  // (design-system.md §1).
  test("its colours are globals.css's own", () => {
    const block = doc.slice(doc.indexOf(":root{"), doc.indexOf("}", doc.indexOf(":root{")))
    const own = Object.fromEntries(
      [...block.matchAll(/--([a-z0-9-]+):([^;}]+)/g)].map((m) => [m[1], m[2].trim()]),
    )
    const css = readFileSync(join(import.meta.dir, "..", "app", "globals.css"), "utf8")
    const root = css.slice(css.indexOf(":root {"), css.indexOf("}", css.indexOf(":root {")))
    const app = Object.fromEntries(
      [...root.matchAll(/--([a-z0-9-]+):\s*([^;]+);/g)].map((m) => [m[1], m[2].trim()]),
    )
    const names = Object.keys(own)
    expect(names).toContain("brand")
    for (const name of names) {
      const appName = name === "muted" ? "muted-foreground" : name
      expect(`${name}: ${own[name]}`).toBe(`${name}: ${app[appName]}`)
    }
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
    expect(HUB_ENTER_SCRIPT).toContain(
      "JSON.stringify(replace ? { ticket: ticket, replace: true } : { ticket: ticket })",
    )
    expect(HUB_ENTER_SCRIPT.match(/fetch\(/g)).toHaveLength(2)
  })
})

/**
 * Runs the page's script against a stand-in document: the session read answers with `session`
 * (a status and a body, or "offline" for no answer), and the redeem with `redeemed`, 200 unless
 * a test says otherwise.
 */
async function enter(session, userId = 1, redeemed = { status: 200, body: { next: "/landed" } }) {
  const elements = {}
  const el = (id) =>
    (elements[id] ??= {
      hidden: false,
      textContent: "",
      listeners: {},
      addEventListener(type, fn) {
        this.listeners[type] = fn
      },
      focus() {},
      click() {
        this.listeners.click?.()
      },
    })
  const world = { redeems: [], went: [] }
  const answer = (status, body) =>
    Promise.resolve({ status, ok: status >= 200 && status < 300, json: async () => body })
  const fetch = (url, options) => {
    if (url === "/api/v1/auth/session") {
      return session === "offline"
        ? Promise.reject(new TypeError("offline"))
        : answer(session.status, session.body)
    }
    world.redeems.push(JSON.parse(options.body))
    return answer(redeemed.status, redeemed.body)
  }
  const document = { readyState: "complete", getElementById: el, addEventListener() {} }
  const location = {
    hash: `#ticket=v1.k.${payload({ userId })}.s`,
    replace: (to) => world.went.push(to),
  }
  const history = { replaceState() {} }
  new Function("document", "location", "history", "fetch", HUB_ENTER_SCRIPT)(
    document,
    location,
    history,
    fetch,
  )
  for (let i = 0; i < 10; i++) await new Promise((resolve) => setTimeout(resolve, 0))
  const asking = !el("ask").hidden && el("working").hidden
  return { world, el, asking }
}

const signedInAs = (id, username, more = {}) => ({
  status: 200,
  body: { authenticated: true, user: { id, username }, ...more },
})

describe("who the browser is signed in as (§9.5 step 2)", () => {
  test("signed out, or the same account: it redeems at once, replacing nobody's session", async () => {
    for (const session of [
      { status: 401, body: {} },
      signedInAs(1, "ion"),
      // Still owing a second factor is not signed in.
      {
        status: 200,
        body: { authenticated: false, needsTotp: true, user: { id: 2, username: "maria" } },
      },
      // A 200 that names no account is the server saying nobody is; were it wrong, the server
      // still refuses to end another account's session unasked.
      { status: 200, body: "not an object" },
    ]) {
      const { world, asking } = await enter(session)
      expect(asking).toBe(false)
      expect(world.redeems).toHaveLength(1)
      expect(world.redeems[0].replace).toBeUndefined()
    }
  })

  test("another account, one owing a new password included: it asks, and Continue says so", async () => {
    for (const session of [
      signedInAs(2, "maria"),
      {
        status: 200,
        body: {
          authenticated: false,
          needsPasswordChange: true,
          user: { id: 2, username: "maria" },
        },
      },
    ]) {
      const { world, el, asking } = await enter(session)
      expect(asking).toBe(true)
      expect(el("as").textContent).toBe("maria")
      expect(el("ask-known").hidden).toBe(false)
      expect(world.redeems).toHaveLength(0)
      el("continue").click()
      await new Promise((resolve) => setTimeout(resolve, 0))
      expect(world.redeems).toHaveLength(1)
      expect(world.redeems[0].replace).toBe(true)
    }
  })

  test("an answer that names nobody it can trust asks without a name, and never redeems unasked", async () => {
    for (const session of [
      { status: 429, body: { error: { code: "rate_limited" } } },
      { status: 500, body: {} },
      "offline",
    ]) {
      const { world, el, asking } = await enter(session)
      expect(asking).toBe(true)
      expect(el("ask-unknown").hidden).toBe(false)
      expect(el("ask-known").hidden).toBe(true)
      expect(world.redeems).toHaveLength(0)
    }
  })
})

// Every refusal of §9.7, as the backend names them (hublink/ticket.go), and the two answers that can
// come before the redeem runs: the sign-in rate limit (§9.6) and a server error.
const backendCodes = () => {
  const ticket = readFileSync(
    join(import.meta.dir, "../../../backend/internal/hublink/ticket.go"),
    "utf8",
  )
  const block = ticket.slice(
    ticket.indexOf("codeSignInDisabled"),
    ticket.indexOf(")", ticket.indexOf("codeSignInDisabled")),
  )
  return [...block.matchAll(/code\w+\s*=\s*"([a-z_]+)"/g)].map((m) => m[1])
}

describe("a refusal, as the page says it", () => {
  test("every code has a sentence of the page's own, naming the page's button", () => {
    const codes = backendCodes()
    expect(codes).toContain("ticket_used")
    expect(codes).toContain("other_account_signed_in")
    for (const code of [...codes, "rate_limited", "internal"]) {
      const said = HUB_ENTER_REFUSALS[code]
      expect(said, code).toBeString()
      expect(said, code).toMatch(/^[A-Z].*\.$/)
      // The button under it says "Sign in on this server"; "here" names nothing on the page.
      expect(said, code).not.toMatch(/sign in here|slow down/i)
      if (/sign in on/i.test(said)) expect(said, code).toMatch(/[Ss]ign in on this server/)
    }
  })

  test("the page shows its own sentence, not the API's lower-case message", async () => {
    for (const [code, status] of [
      ["ticket_used", 401],
      ["hub_signin_disabled", 403],
      ["other_account_signed_in", 409],
      ["rate_limited", 429],
      ["internal", 500],
    ]) {
      const { el } = await enter({ status: 401, body: {} }, 1, {
        status,
        body: { error: { code, message: "too many requests, slow down" } },
      })
      expect(el("failed").hidden).toBe(false)
      expect(el("message").textContent).toBe(HUB_ENTER_REFUSALS[code])
    }
  })

  test("a code the page does not know is the server's message as a sentence, and no answer the page's own", async () => {
    const odd = await enter({ status: 401, body: {} }, 1, {
      status: 403,
      body: {
        error: { code: "csrf_required", message: "this request needs the X-JD-CSRF header" },
      },
    })
    expect(odd.el("message").textContent).toBe("This request needs the X-JD-CSRF header.")
    const bare = await enter({ status: 401, body: {} }, 1, { status: 502, body: null })
    expect(bare.el("message").textContent).toBe(
      "This server did not accept the sign-in link. Open it again from Just Hub, or sign in on this server.",
    )
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
