import { expect, test } from "bun:test"
import {
  MOST_UPSTREAMS,
  dnssecWords,
  failing,
  identify,
  lookupWords,
  port53Of,
  resolverOf,
  shapeOf,
  upstreamWords,
  upstreamsOf,
  viaStub,
} from "./reading"

const cloudflare = {
  id: "cloudflare",
  name: "Cloudflare",
  product: "cloudflare",
  servers: ["1.1.1.1", "1.0.0.1"],
  tlsName: "cloudflare-dns.com",
  tlsServers: ["1.1.1.1#cloudflare-dns.com", "1.0.0.1#cloudflare-dns.com"],
  blocksAds: false,
  blocksMalware: false,
}
const quad9 = {
  ...cloudflare,
  id: "quad9",
  name: "Quad9",
  product: "quad9",
  servers: ["9.9.9.9"],
  blocksMalware: true,
}

const scope = (patch = {}) => ({
  protocols: [],
  dnsOverTLS: "no",
  dnssec: "",
  dnssecSupported: false,
  servers: [],
  fallbackServers: [],
  domains: [],
  ...patch,
})

const stats = (patch = {}) => ({
  cacheSize: 214,
  cacheHits: 4214,
  cacheMisses: 1188,
  hitPercent: 78,
  transactions: 9631,
  currentTransactions: 2,
  timeouts: 3,
  failures: 11,
  dnssecSecure: 312,
  dnssecInsecure: 4871,
  dnssecBogus: 0,
  dnssecIndeterminate: 4,
  ...patch,
})

const view = (patch = {}) => ({
  owner: undefined,
  resolvConf: {
    path: "/etc/resolv.conf",
    mode: "stub",
    nameservers: ["127.0.0.53"],
    search: [],
    options: [],
  },
  resolved: {
    installed: true,
    active: true,
    global: scope({
      dnsOverTLS: "opportunistic",
      currentServer: "1.1.1.1#cloudflare-dns.com",
      servers: ["1.1.1.1#cloudflare-dns.com", "1.0.0.1#cloudflare-dns.com"],
    }),
    links: [
      {
        ...scope({ servers: ["100.100.100.100"], domains: ["~ts.net", "~100.in-addr.arpa"] }),
        name: "tailscale0",
        index: 4,
        scopes: ["DNS"],
        defaultRoute: false,
      },
    ],
    linksTotal: 3,
    statistics: stats(),
  },
  listeners: [],
  adblock: [],
  managed: {
    path: "",
    exists: false,
    servers: [],
    fallback: [],
    domains: [],
    dnssec: "",
    dnsOverTLS: "",
    cache: "",
  },
  presets: [cloudflare, quad9],
  ...patch,
})

const direct = (nameservers) =>
  view({
    resolvConf: { path: "/etc/resolv.conf", mode: "static", nameservers, search: [], options: [] },
    resolved: {
      ...view().resolved,
      installed: false,
      active: false,
      links: [],
      statistics: undefined,
    },
  })

test("behind the stub the upstreams are resolved's: global, each link's, then the fallback", () => {
  const ups = upstreamsOf(
    view({
      resolved: {
        ...view().resolved,
        global: scope({
          dnsOverTLS: "yes",
          servers: ["9.9.9.9#dns.quad9.net"],
          fallbackServers: ["1.1.1.1"],
        }),
      },
    }),
  )
  expect(ups.map((u) => [u.scope, u.fallback, u.encryption])).toEqual([
    ["global", false, "required"],
    ["tailscale0", false, "plain"],
    ["global", true, "required"],
  ])
  // A reverse zone is not what the link is the answer for.
  expect(ups[1].domains).toEqual(["~ts.net"])
})

test("without the stub programs ask resolv.conf's servers, always in the clear", () => {
  const host = direct(["198.51.100.53", "198.51.100.54"])
  expect(viaStub(host)).toBe(false)
  expect(upstreamsOf(host).map((u) => [u.server, u.encryption, u.current])).toEqual([
    ["198.51.100.53", "plain", true],
    ["198.51.100.54", "plain", false],
  ])
})

test("an upstream is named only by what its address proves", () => {
  const host = view({
    listeners: [{ address: "10.0.4.9", protocol: "udp", loopback: false, kind: "adguardhome" }],
    adblock: [{ kind: "adguardhome", name: "AdGuard Home", runsAs: "container", answering: true }],
  })
  const up = (server, scope = "global") => ({ server, scope, domains: [], encryption: "plain" })
  expect(identify(up("1.1.1.1#cloudflare-dns.com"), host)).toMatchObject({ name: "Cloudflare" })
  expect(identify(up("9.9.9.9"), host)).toMatchObject({ name: "Quad9", detail: "blocks malware" })
  expect(identify(up("10.0.4.9"), host)).toMatchObject({
    name: "AdGuard Home",
    detail: "blocks ads",
    local: true,
  })
  expect(identify(up("100.100.100.100", "tailscale0"), host)).toMatchObject({
    name: "MagicDNS",
    tunnel: true,
  })
  expect(identify(up("203.0.113.9"), host)).toEqual({ name: "203.0.113.9", bare: true })
})

test("the identity line says where a lookup goes and how", () => {
  expect(upstreamWords(view())).toEqual({
    words: "Cloudflare over opportunistic TLS",
    product: "cloudflare",
  })
  const required = view({
    resolved: {
      ...view().resolved,
      global: scope({ dnsOverTLS: "yes", servers: ["1.1.1.1#cloudflare-dns.com", "9.9.9.9#x"] }),
    },
  })
  expect(upstreamWords(required)).toEqual({ words: "Cloudflare and Quad9 over TLS" })
  const perLink = view({ resolved: { ...view().resolved, global: scope() } })
  expect(upstreamWords(perLink).words).toBe("MagicDNS inside the tailnet, per link")
  const none = view({ resolved: { ...view().resolved, global: scope(), links: [] } })
  expect(upstreamWords(none).words).toBe("no upstream")
})

test("a host with no stub does not say its nameserver twice", () => {
  expect(upstreamWords(direct(["198.51.100.53"])).words).toBe("asked directly, unencrypted")
  expect(upstreamWords(direct(["198.51.100.53", "198.51.100.54", "198.51.100.55"])).words).toBe(
    "asked directly with 2 more, unencrypted",
  )
  expect(upstreamWords(direct(["1.1.1.1"])).words).toBe("Cloudflare unencrypted")
  const cached = direct(["127.0.0.1"])
  cached.listeners = [{ address: "127.0.0.1", protocol: "udp", loopback: true, kind: "dnsmasq" }]
  expect(upstreamWords(cached).words).toBe("dnsmasq on this server")
})

test("the page is about the stub where programs ask it, and the file where they do not", () => {
  expect(resolverOf(view())).toMatchObject({
    name: "systemd-resolved",
    address: "127.0.0.53",
    state: { tone: "running", label: "Answering" },
  })
  expect(resolverOf(direct(["198.51.100.53"]))).toMatchObject({
    name: "/etc/resolv.conf",
    path: true,
    address: "198.51.100.53",
    state: { label: "No local resolver" },
  })
  expect(resolverOf(direct([])).state).toEqual({ tone: "warning", label: "No nameserver" })
  const stopped = direct(["198.51.100.53"])
  stopped.resolved.installed = true
  expect(resolverOf(stopped).state.label).toBe("systemd-resolved is not running")
  const skipped = view({
    resolvConf: { ...view().resolvConf, mode: "static", nameservers: ["198.51.100.53"] },
  })
  expect(resolverOf(skipped).state.label).toBe("Programs skip systemd-resolved")
})

test("one program on both protocols is one thing answering on port 53", () => {
  const nodes = port53Of(
    view({
      listeners: [
        {
          address: "127.0.0.53",
          protocol: "udp",
          process: "systemd-resolve",
          pid: 812,
          loopback: true,
          kind: "resolved-stub",
        },
        {
          address: "127.0.0.53",
          protocol: "tcp",
          process: "systemd-resolve",
          pid: 812,
          loopback: true,
          kind: "resolved-stub",
        },
        {
          address: "0.0.0.0",
          protocol: "udp",
          process: "pihole-FTL",
          pid: 9,
          loopback: false,
          kind: "pihole",
        },
      ],
      adblock: [
        { kind: "pihole", name: "Pi-hole", runsAs: "process", answering: true },
        {
          kind: "adguardhome",
          name: "AdGuard Home",
          runsAs: "container",
          container: "adguard",
          answering: false,
        },
      ],
    }),
  )
  expect(nodes.map((n) => [n.title, n.protocols.join(" "), n.blocker, n.open, n.silent])).toEqual([
    ["systemd-resolved", "udp tcp", false, false, false],
    ["Pi-hole", "udp", true, true, false],
    // Installed and holding no socket: named, with what is missing.
    ["AdGuard Home", "", true, false, true],
  ])
  expect(nodes[0].loopback).toBe(true)
  expect(nodes[2].address).toBeUndefined()
})

test("the lookups reading leads with what went wrong", () => {
  expect(lookupWords(stats())).toBe("11 failed, 3 timed out of 9,631")
  expect(lookupWords(stats({ failures: 0 }))).toBe("3 timed out of 9,631")
  expect(lookupWords(stats({ failures: 0, timeouts: 0 }))).toBe("None of 9,631 failed")
  expect(failing(stats())).toBe(false)
  expect(failing(stats({ failures: 400 }))).toBe(true)
})

test("a bogus answer leads the DNSSEC reading", () => {
  expect(dnssecWords(stats())).toBe("312 secure, none bogus")
  expect(dnssecWords(stats({ dnssecBogus: 2 }))).toBe("2 bogus")
  expect(dnssecWords(stats({ dnssecSecure: 0 }))).toBe("No verdicts")
})

test("the remembered shape counts what the picture draws, the +N tile included", () => {
  expect(shapeOf(view())).toEqual({ upstreams: 3, port53: 0, links: 1, readings: true })
  const many = view({
    resolved: {
      ...view().resolved,
      global: scope({ servers: Array.from({ length: 14 }, (_, i) => `203.0.113.${i}`) }),
    },
  })
  expect(shapeOf(many).upstreams).toBe(MOST_UPSTREAMS + 1)
  expect(shapeOf(direct(["198.51.100.53"])).readings).toBe(false)
})
