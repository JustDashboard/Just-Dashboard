import { describe, expect, test } from "bun:test"
import {
  addressIdentity,
  localDestination,
  parseSearch,
  recentDestinations,
  rememberDestination,
  scopeQuery,
  searchItems,
} from "./model"

const items = [
  { id: "page", kind: "page", title: "Sites", keywords: ["proxy", "domains"] },
  { id: "api", kind: "project", title: "api", detail: "api.example.test" },
  { id: "api-worker", kind: "container", title: "api-worker", detail: "running · acme/api:latest" },
  { id: "site", kind: "site", title: "storefront", keywords: ["shop.example.test"] },
  { id: "db", kind: "database", title: "Production", detail: "postgres · shop" },
  { id: "duplicate", kind: "database", title: "Production", detail: "mysql · analytics" },
  { id: "recent", kind: "recent", title: "Git", href: "/git?repo=%2Fsrv%2Fapi" },
]

describe("command search", () => {
  test("exact names and addresses beat incidental matches", () => {
    expect(searchItems(items, "api").items.map((item) => item.id)).toEqual(["api", "api-worker"])
    expect(searchItems(items, "https://shop.example.test").items[0].id).toBe("site")
  })

  test("scopes support aliases, whitespace and a queryless inventory", () => {
    expect(parseSearch("  DoMaIn: shop")).toEqual({ scope: "site", text: "shop" })
    expect(parseSearch("pm2: shop")).toEqual({ scope: "app", text: "shop" })
    expect(searchItems(items, "db: shop").items.map((item) => item.id)).toEqual(["db"])
    expect(searchItems(items, "container:").items.map((item) => item.id)).toEqual(["api-worker"])
    expect(parseSearch("unknown: api")).toEqual({ scope: "all", text: "unknown: api" })
    expect(scopeQuery("domain: shop", "database")).toBe("database: shop")
    expect(scopeQuery("db: shop", "all")).toBe("shop")
  })

  test("all terms must match, with punctuation, accents and small typos tolerated", () => {
    expect(searchItems(items, "container: api running").items[0].id).toBe("api-worker")
    for (const query of ["prodution", "prdouction", "productoin", "productionn", "próduction"]) {
      expect(searchItems(items, query).total).toBe(2)
    }
    expect(searchItems(items, "api missing").total).toBe(0)
    expect(searchItems(items, "://").total).toBe(0)
  })

  test("duplicates have distinct identities, limits report the full match count", () => {
    const result = searchItems(items, "production", 1)
    expect(result.total).toBe(2)
    expect(result.items).toHaveLength(1)
    expect(result.items[0].id).toBe("db")
  })

  test("the empty menu prioritises recent destinations and does not dump live inventories", () => {
    expect(searchItems(items, "").items.map((item) => item.id)).toEqual(["page", "recent"])
  })

  test("URLs contribute no userinfo, query tokens or fragments", () => {
    expect(addressIdentity("https://admin:secret@shop.example.test/path?token=secret#secret")).toBe(
      "shop.example.test/path",
    )
    expect(addressIdentity("shop.example.test")).toBe("shop.example.test")
  })
})

describe("recent destinations", () => {
  test("filter edits update a place without displacing the previous page", () => {
    let history = rememberDestination([], { href: "/account", title: "Profile" })
    history = rememberDestination(history, { href: "/git?repo=%2Fsrv%2Fshop", title: "shop" })
    history = rememberDestination(history, {
      href: "/git?q=changed&repo=%2Fsrv%2Fshop&tab=history",
      title: "Git",
    })
    expect(history).toHaveLength(2)
    expect(history[0].title).toBe("shop")
    expect(recentDestinations(history, "/git?repo=%2Fsrv%2Fshop")[0].href).toBe("/account")
    history = rememberDestination(history, { href: "/git?repo=%2Fsrv%2Fother", title: "other" })
    expect(history).toHaveLength(3)
  })

  test("returns the last distinct place, retains selections and bounds memory", () => {
    let history = []
    for (let i = 0; i < 20; i++) {
      history = rememberDestination(history, { href: `/deploy/${i}`, title: `Project ${i}` })
    }
    expect(history).toHaveLength(12)
    const same = rememberDestination(history, history[0])
    expect(same).toBe(history)
    history = rememberDestination(history, { href: "/git?repo=%2Fsrv%2Fapi", title: "api" })
    history = rememberDestination(history, { href: "/deploy/19", title: "Project 19" })
    expect(recentDestinations(history, "/deploy/19")[0].href).toBe("/git?repo=%2Fsrv%2Fapi")
    expect(history.filter((entry) => entry.href === "/deploy/19")).toHaveLength(1)
  })

  test("only local application destinations may be navigated", () => {
    for (const href of ["https://example.test", "//example.test", "/\\example.test", "/\nfoo"]) {
      expect(localDestination(href)).toBe(false)
    }
    expect(localDestination("/docker/containers/a%2Fb")).toBe(true)
  })
})
