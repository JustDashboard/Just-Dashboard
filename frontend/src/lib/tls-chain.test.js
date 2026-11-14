import { describe, expect, test } from "bun:test"
import {
  chainNodes,
  failureNodes,
  fleetFacts,
  fleetLife,
  fleetRank,
  gradeVerdict,
  pendingNodes,
  serverProduct,
  timeLeft,
} from "./tls-chain"

const now = Date.parse("2026-11-13T12:00:00Z")
const day = 86_400_000

const scan = {
  domain: "app.example.com",
  port: 443,
  checkedAt: new Date(now).toISOString(),
  reachable: true,
  address: "203.0.113.4:443",
  addressKind: "here",
  grade: "B",
  summary: "",
  negotiated: "TLS 1.3",
  legacyOnly: false,
  protocols: [],
  certificate: {
    name: "app.example.com",
    daysLeft: 44,
    expired: false,
    expiring: false,
    notAfter: new Date(now + 44 * day).toISOString(),
  },
  chain: [
    {
      subject: "app.example.com",
      issuer: "R11",
      selfIssued: false,
      signatureAlgorithm: "ECDSA-SHA256",
    },
    { subject: "R11", issuer: "ISRG Root X1", selfIssued: false, signatureAlgorithm: "SHA256-RSA" },
  ],
  trustPath: [
    { subject: "app.example.com", sent: true, root: false },
    { subject: "R11", sent: true, root: false },
    { subject: "ISRG Root X1", sent: false, root: true },
  ],
  chainComplete: true,
  trusted: true,
  nameMatches: true,
  keyType: "ECDSA",
  keyBits: 256,
  ocspStapled: false,
  http: { service: "http", server: "nginx", headers: [], redirectChain: [] },
  origin: { state: "current", site: "app.example.com", detail: "" },
  findings: [],
  checks: [],
}

describe("chainNodes", () => {
  test("a healthy scan reads visitor to root, every step good", () => {
    const nodes = chainNodes(scan, now)
    expect(nodes.map((n) => n.kind)).toEqual([
      "visitor",
      "domain",
      "proxy",
      "leaf",
      "intermediate",
      "root",
    ])
    expect(nodes.map((n) => n.tone)).toEqual([
      "neutral",
      "success",
      "success",
      "success",
      "success",
      "success",
    ])
    expect(nodes[1].hint).toBe("203.0.113.4 · this server")
    expect(nodes[2]).toMatchObject({
      title: "app.example.com",
      product: "nginx",
      site: "app.example.com",
    })
    expect(nodes[3].hint).toBe("44 days left · ECDSA 256")
    expect(nodes[5]).toMatchObject({ title: "ISRG Root X1", hint: "trusted, from the store" })
  })

  test("a certificate in its renewal window is amber and its wire too", () => {
    const leaf = chainNodes(
      { ...scan, certificate: { ...scan.certificate, daysLeft: 9, expiring: true } },
      now,
    )[3]
    expect(leaf.tone).toBe("warning")
    expect(leaf.wire.tone).toBe("warning")
  })

  test("an incomplete chain draws the missing intermediate on a dashed amber line", () => {
    const nodes = chainNodes({ ...scan, chainComplete: false, chain: [scan.chain[0]] }, now)
    const missing = nodes.find((n) => n.kind === "intermediate")
    expect(missing).toMatchObject({ title: "not sent", tone: "warning" })
    expect(missing.wire).toEqual({ tone: "warning", dashed: true })
  })

  test("an untrusted chain ends on a red root", () => {
    const root = chainNodes({ ...scan, trusted: false, trustPath: undefined }, now).at(-1)
    expect(root).toMatchObject({ title: "ISRG Root X1", tone: "danger", hint: "not trusted" })
    expect(root.wire.dashed).toBe(true)
  })

  test("a stale file is said on the proxy and turns it amber", () => {
    const proxy = chainNodes(
      { ...scan, origin: { state: "stale", site: "app", detail: "" } },
      now,
    )[2]
    expect(proxy.tone).toBe("warning")
    expect(proxy.hint).toBe("TLS 1.3 · stale, renewed on disk")
  })

  test("a legacy-only server is red where it answered", () => {
    const proxy = chainNodes({ ...scan, legacyOnly: true, negotiated: "TLS 1.2" }, now)[2]
    expect(proxy.tone).toBe("danger")
  })

  test("with no traced origin the Server header names what answered", () => {
    const proxy = chainNodes(
      { ...scan, origin: undefined, http: { ...scan.http, server: "cloudflare" } },
      now,
    )[2]
    expect(proxy).toMatchObject({ title: "cloudflare", product: "cloudflare" })
  })

  test("another address serving another certificate makes the domain amber", () => {
    const domain = chainNodes(
      {
        ...scan,
        fingerprint: "aa",
        addresses: [
          { address: "203.0.113.4:443", reachable: true, fingerprint: "aa" },
          { address: "203.0.113.5:443", reachable: true, fingerprint: "bb" },
        ],
      },
      now,
    )[1]
    expect(domain).toMatchObject({ tone: "warning", hint: "1 of 2 addresses differ" })
  })
})

describe("failureNodes", () => {
  test("the failed step is red and the ones after it are not reached", () => {
    const nodes = failureNodes({
      ...scan,
      reachable: false,
      failure: { stage: "connect", reason: "refused", address: "203.0.113.4:443", where: "here" },
    })
    expect(nodes.map((n) => [n.eyebrow, n.title, n.tone])).toEqual([
      ["Visitor", "This server", "neutral"],
      ["Name", "resolves", "success"],
      ["Port 443", "refused", "danger"],
      ["Handshake", "—", "neutral"],
    ])
    expect(nodes[3].wire.dashed).toBe(true)
  })
})

test("pending nodes name the target and judge nothing", () => {
  const nodes = pendingNodes("app.example.com")
  expect(nodes[1].title).toBe("app.example.com")
  expect(nodes.every((n) => n.tone === "neutral")).toBe(true)
  expect(pendingNodes(undefined)).toHaveLength(nodes.length)
})

test("time left reads in days, and in hours in a certificate's last day and a half", () => {
  expect(timeLeft({ daysLeft: 44 }, now)).toBe("44 days left")
  expect(
    timeLeft({ daysLeft: 1, notAfter: new Date(now + 28 * 3_600_000).toISOString() }, now),
  ).toBe("28 hours left")
  expect(timeLeft({ daysLeft: 0, expired: true }, now)).toBe("expired")
})

test("grades read as verdicts, worst first in the fleet", () => {
  expect(["A+", "A", "A-", "B", "F", undefined].map(gradeVerdict)).toEqual([
    "success",
    "success",
    "warning",
    "warning",
    "danger",
    "neutral",
  ])
  const targets = [
    { host: "a", port: 443, sites: [], watched: false },
    { host: "b", port: 443, sites: [], watched: false, scan: { grade: "A+" } },
    { host: "c", port: 443, sites: [], watched: false, scan: { grade: "F" } },
  ]
  expect(targets.toSorted((x, y) => fleetRank(x) - fleetRank(y)).map((t) => t.host)).toEqual([
    "c",
    "b",
    "a",
  ])
})

test("a fleet row's certificate and the fleet's facts", () => {
  const base = { reachable: true, expiring: false, expired: false, issues: 0 }
  const targets = [
    { host: "a", port: 443, sites: [], watched: false },
    {
      host: "b",
      port: 443,
      sites: [],
      watched: false,
      scan: { ...base, grade: "A", daysLeft: 60, checkedAt: "2026-11-13T10:00:00Z" },
    },
    {
      host: "c",
      port: 443,
      sites: [],
      watched: false,
      scan: { ...base, grade: "B", daysLeft: 9, expiring: true, checkedAt: "2026-11-13T11:00:00Z" },
    },
    {
      host: "d",
      port: 443,
      sites: [],
      watched: false,
      scan: { ...base, grade: "F", reachable: false, checkedAt: "2026-11-13T09:00:00Z" },
    },
  ]
  expect(targets.map((t) => fleetLife(t).words)).toEqual([
    "not scanned",
    "60 days left",
    "9 days left",
    "unreachable",
  ])
  expect(fleetFacts(targets)).toEqual({
    total: 4,
    scanned: 3,
    belowA: 2,
    expiring: 1,
    unreachable: 1,
    newest: "2026-11-13T11:00:00Z",
  })
})

test("a Server header names the engine where this product draws it", () => {
  expect(serverProduct("nginx/1.26.3")).toBe("nginx")
  expect(serverProduct("cloudflare")).toBe("cloudflare")
  expect(serverProduct("gws")).toBeUndefined()
  expect(serverProduct(undefined)).toBeUndefined()
})
