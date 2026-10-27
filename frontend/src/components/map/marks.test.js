import { describe, expect, test } from "bun:test"
import { branchOf, itemProduct, strongestProducts } from "./marks"

const thing = (extra) => ({ id: "x", name: "x", tone: "ok", state: "", href: "/", ...extra })

describe("itemProduct", () => {
  test("a computer is drawn as the system Remote Desktop draws it", () => {
    expect(itemProduct(thing({ product: "macos" }), "computers")).toBe("apple")
    expect(itemProduct(thing({ product: "linux" }), "computers")).toBe("linux")
    expect(itemProduct(thing({ product: "windows" }), "computers")).toBe("windows")
  })

  test("a firewall is netfilter's only where its engine is netfilter's own tool", () => {
    expect(itemProduct(thing({ product: "nftables" }), "security")).toBe("netfilter")
    expect(itemProduct(thing({ product: "iptables" }), "security")).toBe("netfilter")
    // ufw has no mark here and keeps its own name, which draws a glyph.
    expect(itemProduct(thing({ product: "ufw" }), "security")).toBe("ufw")
  })

  test("a certificate with no product is read by its issuer", () => {
    expect(itemProduct(thing({ detail: "R11" }), "certificates")).toBe("lets-encrypt")
    expect(itemProduct(thing({ detail: "Some Private CA" }), "certificates")).toBeUndefined()
  })

  test("an API key is read by its name", () => {
    expect(itemProduct(thing({ name: "github-actions" }), "keys")).toBe("github")
    expect(itemProduct(thing({ name: "laptop" }), "keys")).toBeUndefined()
  })

  test("a kind with no reading of its own keeps the product the server sent", () => {
    expect(itemProduct(thing({ product: "docker" }), "volumes")).toBe("docker")
    expect(itemProduct(thing({ product: "nginx" }), "domains")).toBe("nginx")
    expect(itemProduct(thing({}), "accounts")).toBeUndefined()
  })
})

describe("strongestProducts", () => {
  test("most-named first, each once, only marks there is a file for", () => {
    const ids = ["docker", "postgresql", "docker", "not-a-product", undefined, "redis", "docker"]
    expect(strongestProducts(ids, 5)).toEqual(["docker", "postgresql", "redis"])
  })

  test("ties are broken by name, and the count is capped", () => {
    expect(strongestProducts(["redis", "nginx", "caddy"], 2)).toEqual(["caddy", "nginx"])
  })
})

describe("branchOf", () => {
  test("a kind's hue is its branch's", () => {
    expect(branchOf("backups")).toBe("jobs")
    expect(branchOf("accounts")).toBe("people")
    expect(branchOf("nothing")).toBeUndefined()
  })
})
