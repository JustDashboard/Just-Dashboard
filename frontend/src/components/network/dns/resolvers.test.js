import { expect, test } from "bun:test"
import {
  encryptionOf,
  missingTLSNames,
  parseList,
  presetChosen,
  presetFor,
  sameServer,
  splitServer,
} from "./resolvers"

const quad9 = {
  id: "quad9",
  name: "Quad9",
  product: "quad9",
  servers: ["9.9.9.9", "149.112.112.112"],
  tlsName: "dns.quad9.net",
  tlsServers: ["9.9.9.9#dns.quad9.net", "149.112.112.112#dns.quad9.net"],
  blocksAds: false,
  blocksMalware: true,
}

test("a server is its address, whatever name or zone resolved prints after it", () => {
  expect(splitServer("1.1.1.1#cloudflare-dns.com")).toEqual({
    address: "1.1.1.1",
    tlsName: "cloudflare-dns.com",
  })
  expect(splitServer("fe80::1%eth0")).toEqual({ address: "fe80::1", tlsName: undefined })
  expect(sameServer("1.1.1.1#cloudflare-dns.com", "1.1.1.1")).toBe(true)
  expect(sameServer(undefined, "1.1.1.1")).toBe(false)
})

test("a preset is recognised by its addresses in either spelling, and only when all are there", () => {
  expect(presetFor("9.9.9.9#dns.quad9.net", [quad9])?.id).toBe("quad9")
  expect(presetFor("8.8.8.8", [quad9])).toBeUndefined()
  expect(presetChosen(quad9, quad9.tlsServers)).toBe(true)
  expect(presetChosen(quad9, quad9.servers)).toBe(true)
  expect(presetChosen(quad9, ["9.9.9.9"])).toBe(false)
  expect(presetChosen(quad9, [...quad9.servers, "8.8.8.8"])).toBe(false)
})

test("servers are typed one per line, or with commas and spaces, each once", () => {
  expect(parseList("1.1.1.1\n1.0.0.1, 1.1.1.1  8.8.8.8\n")).toEqual([
    "1.1.1.1",
    "1.0.0.1",
    "8.8.8.8",
  ])
  expect(parseList("")).toEqual([])
})

test("required TLS needs a name on every server", () => {
  expect(missingTLSNames(["9.9.9.9#dns.quad9.net", "149.112.112.112"])).toEqual(["149.112.112.112"])
  expect(missingTLSNames([])).toEqual([])
})

test("resolved's DNS over TLS words map to what the page says", () => {
  expect(encryptionOf("yes")).toBe("required")
  expect(encryptionOf("opportunistic")).toBe("opportunistic")
  expect(encryptionOf("no")).toBe("plain")
  expect(encryptionOf(undefined)).toBe("plain")
})
