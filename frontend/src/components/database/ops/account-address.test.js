import { describe, expect, test } from "bun:test"
import {
  accessHref,
  accountFromPath,
  accountHref,
  formerAccountHref,
  isAccountTab,
} from "./account-address"

const former = (address) => {
  const url = new URL(address, "http://localhost")
  return formerAccountHref(url.pathname.split("/")[2], url.searchParams)
}

/** The account a written address reads back as, the way the page reads it. */
const readBack = (href) => {
  const url = new URL(href, "http://localhost")
  return { name: accountFromPath(url.pathname), host: url.searchParams.get("host") }
}

describe("an account's own address", () => {
  test("is the list's address and the account's name", () => {
    expect(accountHref(1, { name: "reporting" })).toBe("/databases/1/access/reporting")
  })

  test("writes the first view bare and names the others", () => {
    expect(accountHref(1, { name: "app" }, "grants")).toBe("/databases/1/access/app")
    expect(accountHref(1, { name: "app" }, "sessions")).toBe(
      "/databases/1/access/app?tab=sessions",
    )
    expect(accountHref(1, { name: "app" }, "settings")).toBe(
      "/databases/1/access/app?tab=settings",
    )
  })

  test("keeps a host beside the name, an empty one included", () => {
    expect(accountHref(2, { name: "blogger", host: "10.%" })).toBe(
      "/databases/2/access/blogger?host=10.%25",
    )
    expect(accountHref(2, { name: "root", host: "" })).toBe("/databases/2/access/root?host=")
    expect(accountHref(2, { name: "root", host: "%" }, "settings")).toBe(
      "/databases/2/access/root?host=%25&tab=settings",
    )
  })

  test("a name the path cannot hold as written comes back as it was", () => {
    for (const name of [
      "ops@night",
      "50%",
      "cron%40night",
      "a/b",
      "sales team",
      "mira@example.com",
      "ünïcode",
      "q?x=1#y",
    ]) {
      for (const host of [undefined, "%", "10.%", "localhost", "app_main"]) {
        expect(readBack(accountHref(1, { name, host }))).toEqual({ name, host: host ?? null })
      }
    }
  })

  test("a name is one segment of the path, whatever it holds", () => {
    expect(accountHref(1, { name: "a/b" })).toBe("/databases/1/access/a%2Fb")
    expect(accountHref(1, { name: "ops@night" })).toBe("/databases/1/access/ops%40night")
    // Already-encoded text is a name like any other: it is encoded once more, not trusted.
    expect(accountHref(1, { name: "cron%40night" })).toBe("/databases/1/access/cron%2540night")
  })
})

describe("reading the account out of a path", () => {
  test("only an account's page names one", () => {
    expect(accountFromPath("/databases/1/access")).toBeNull()
    expect(accountFromPath("/databases/1/access/")).toBeNull()
    expect(accountFromPath("/databases/1/settings/network")).toBeNull()
    expect(accountFromPath("/databases/1/access/app/more")).toBeNull()
    expect(accountFromPath("/databases/1/access/app")).toBe("app")
    expect(accountFromPath("/databases/1/access/app/")).toBe("app")
  })

  test("is decoded once", () => {
    expect(accountFromPath("/databases/1/access/cron%2540night")).toBe("cron%40night")
    expect(accountFromPath("/databases/1/access/ops%40night")).toBe("ops@night")
  })

  test("a stray percent typed by hand is the name as typed", () => {
    expect(accountFromPath("/databases/1/access/50%")).toBe("50%")
  })
})

describe("addresses from when an account opened in a sheet", () => {
  test("land on the account's page", () => {
    expect(former("/databases/1/access?account=reporting")).toBe("/databases/1/access/reporting")
    expect(former("/databases/1/access?show=admins&account=app")).toBe("/databases/1/access/app")
  })

  test("carry the host, and a document user's database", () => {
    expect(former("/databases/2/access?account=blogger&host=10.%25")).toBe(
      "/databases/2/access/blogger?host=10.%25",
    )
    expect(former("/databases/5/access?account=api&host=app_main")).toBe(
      "/databases/5/access/api?host=app_main",
    )
  })

  test("an address that names no account is not one of them", () => {
    expect(former("/databases/1/access")).toBeNull()
    expect(former("/databases/1/access?show=blocked")).toBeNull()
    expect(former("/databases/1/access?account=")).toBeNull()
  })
})

test("the way back carries the list's filter", () => {
  expect(accessHref(1)).toBe("/databases/1/access")
  expect(accessHref(1, "")).toBe("/databases/1/access")
  expect(accessHref(1, "admins")).toBe("/databases/1/access?show=admins")
})

test("only the three views are views", () => {
  expect(isAccountTab("grants")).toBe(true)
  expect(isAccountTab("sessions")).toBe(true)
  expect(isAccountTab("settings")).toBe(true)
  expect(isAccountTab("overview")).toBe(false)
  expect(isAccountTab("")).toBe(false)
  expect(isAccountTab(null)).toBe(false)
})
