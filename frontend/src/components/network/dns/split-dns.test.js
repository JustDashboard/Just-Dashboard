import { expect, test } from "bun:test"
import { splitDNSDraft, splitDNSEffect, splitDNSIntent, splitDNSSuffix } from "./split-dns"

const family = (method, extra = {}) => ({
  method,
  addresses: method === "manual" ? ["192.0.2.10/24"] : [],
  dns: [],
  domains: [],
  ignoreAutoDns: false,
  ignoreAutoRoutes: false,
  routes: [{ destination: "0.0.0.0/0", gateway: "192.0.2.1", metric: 100, table: 254 }],
  ...extra,
})

const intent = {
  ipv4: family("manual", { dns: ["192.0.2.53"], domains: ["~corp.example", "lan.example"] }),
  ipv6: family("auto", { domains: ["~corp.example", "lan.example"] }),
}

test("the draft is link-level: servers of both families, routing and search domains apart", () => {
  expect(splitDNSDraft(intent)).toEqual({
    servers: "192.0.2.53",
    routing: "corp.example",
    search: "lan.example",
    exclusive: false,
  })
})

test("only DNS changes; servers split by family and every enabled family gets the same domains", () => {
  const { intent: next, error } = splitDNSIntent(intent, {
    servers: "10.0.0.53 fd00::53, 10.0.0.53",
    routing: "~Corp.Example. lab.example .",
    search: "",
    exclusive: true,
  })
  expect(error).toBeUndefined()
  expect(next.ipv4.dns).toEqual(["10.0.0.53"])
  expect(next.ipv6.dns).toEqual(["fd00::53"])
  expect(next.ipv4.domains).toEqual(["~corp.example", "~lab.example", "~."])
  expect(next.ipv6.domains).toEqual(next.ipv4.domains)
  expect(next.ipv4.ignoreAutoDns).toBe(true)
  expect(next.ipv4.addresses).toEqual(intent.ipv4.addresses)
  expect(next.ipv4.routes).toEqual(intent.ipv4.routes)
  expect(next.ipv6.method).toBe("auto")
})

test("a disabled family cannot carry a server, and malformed values are refused", () => {
  const v4only = { ipv4: intent.ipv4, ipv6: family("disabled", { routes: [] }) }
  expect(
    splitDNSIntent(v4only, { servers: "fd00::53", routing: "", search: "", exclusive: false })
      .error,
  ).toContain("IPv6 is disabled")
  const kept = splitDNSIntent(v4only, {
    servers: "10.0.0.53",
    routing: "corp.example",
    search: "",
    exclusive: false,
  })
  expect(kept.intent.ipv6.domains).toEqual([])
  expect(
    splitDNSIntent(intent, { servers: "dns.example", routing: "", search: "", exclusive: false })
      .error,
  ).toContain("not an IP address")
  expect(
    splitDNSIntent(intent, { servers: "", routing: "bad_name", search: "", exclusive: false })
      .error,
  ).toContain("not a DNS suffix")
  expect(
    splitDNSIntent(intent, {
      servers: "10.0.0.1 10.0.0.2 10.0.0.3 10.0.0.4 10.0.0.5 10.0.0.6 10.0.0.7 10.0.0.8 10.0.0.9",
      routing: "",
      search: "",
      exclusive: false,
    }).error,
  ).toContain("eight")
  expect(splitDNSSuffix("lan")).toBe(true)
  expect(splitDNSSuffix("-bad.example")).toBe(false)
})

test("the effect says which names go to the link and whether it stays a default route", () => {
  const routed = splitDNSEffect(
    { servers: "10.0.0.53", routing: "corp.example", search: "", exclusive: true },
    "wg0",
  )
  expect(routed.join(" ")).toContain("Names under corp.example go only to wg0's servers")
  expect(routed.join(" ")).toContain("stops using wg0 for names nothing claims")
  const everything = splitDNSEffect(
    { servers: "10.0.0.53", routing: ".", search: "", exclusive: false },
    "wg0",
  )
  expect(everything.join(" ")).toContain("default route for every name")
  const none = splitDNSEffect({ servers: "", routing: "", search: "", exclusive: true }, "wg0")
  expect(none[0]).toContain("no DNS server")
})
