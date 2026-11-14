import { describe, expect, test } from "bun:test"
import {
  certificateHref,
  certificatesHref,
  certificateTab,
  findCertificate,
  pageTab,
  sheetRedirect,
} from "./certificate-paths"

const lineage = {
  name: "app.example.com",
  path: "/etc/letsencrypt/live/app.example.com/fullchain.pem",
}
const copy = { name: "app.example.com", path: "/etc/nginx/ssl/app.example.com.pem" }
const shop = {
  name: "shop.example.com",
  path: "/etc/letsencrypt/live/shop.example.com/fullchain.pem",
}
const unnamed = { name: "", path: "/srv/certs/a.pem" }

describe("a certificate's address", () => {
  test("is its name where the name says which certificate", () => {
    expect(certificateHref(shop, [lineage, copy, shop])).toBe(
      "/proxy/certificates/shop.example.com",
    )
  })

  test("carries the file where two certificates share the name", () => {
    const href = certificateHref(copy, [lineage, copy, shop])
    expect(href).toBe(`/proxy/certificates/app.example.com?path=${encodeURIComponent(copy.path)}`)
  })

  test("carries the file where the certificate has no name", () => {
    expect(certificateHref(unnamed, [unnamed])).toBe(
      `/proxy/certificates/certificate?path=${encodeURIComponent(unnamed.path)}`,
    )
  })

  test("carries the view, except the first", () => {
    expect(certificateHref(shop, [shop], "chain")).toBe(
      "/proxy/certificates/shop.example.com?tab=chain",
    )
    expect(certificateHref(shop, [shop], "overview")).toBe("/proxy/certificates/shop.example.com")
  })

  test("escapes a name that is not a plain word", () => {
    expect(certificateHref({ name: "*.example.com", path: "/x" }, [])).toBe(
      "/proxy/certificates/*.example.com",
    )
    expect(certificateHref({ name: "a b", path: "/x" }, [])).toBe("/proxy/certificates/a%20b")
  })
})

describe("finding the certificate an address names", () => {
  const all = [lineage, copy, shop, unnamed]

  test("by file first, which is unique", () => {
    expect(findCertificate(all, "app.example.com", copy.path)).toBe(copy)
  })

  test("by name otherwise, the first of that name", () => {
    expect(findCertificate(all, "app.example.com")).toBe(lineage)
    expect(findCertificate(all, "certificate")).toBe(unnamed)
  })

  test("nothing before the list has been read, or for a name nobody holds", () => {
    expect(findCertificate(undefined, "app.example.com")).toBeUndefined()
    expect(findCertificate(all, "gone.example.com")).toBeUndefined()
  })
})

describe("the sheet's old address", () => {
  test("lands on the certificate's page", () => {
    expect(sheetRedirect([shop], shop.path)).toBe("/proxy/certificates/shop.example.com")
  })

  test("lands on the list when the file is no longer listed", () => {
    expect(sheetRedirect([shop], "/gone.pem")).toBe("/proxy/certificates")
  })

  test("waits for the list, and is nothing without a file", () => {
    expect(sheetRedirect(undefined, shop.path)).toBeUndefined()
    expect(sheetRedirect([shop], null)).toBeUndefined()
  })
})

describe("views in the address", () => {
  test("an unknown view is the first", () => {
    expect(pageTab("renewal")).toBe("renewal")
    expect(pageTab("nonsense")).toBe("certificates")
    expect(pageTab(null)).toBe("certificates")
    expect(certificateTab("chain")).toBe("chain")
    expect(certificateTab(undefined)).toBe("overview")
  })

  test("the list's first view is its bare address", () => {
    expect(certificatesHref()).toBe("/proxy/certificates")
    expect(certificatesHref("certificates")).toBe("/proxy/certificates")
    expect(certificatesHref("dns")).toBe("/proxy/certificates?tab=dns")
  })
})
