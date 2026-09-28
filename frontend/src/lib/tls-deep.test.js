import { describe, expect, test } from "bun:test"
import {
  alpnReading,
  browserGroupReading,
  deepHref,
  effectiveFilter,
  groupReading,
  http3Reading,
  newDeepFindings,
  ratingCounts,
  resumptionReading,
  sniReading,
  suiteFacts,
  suiteGroups,
  versionCounts,
  versionReading,
} from "./tls-deep"

const suite = (openssl, rating, extra = {}) => ({
  id: openssl.length,
  name: openssl,
  openssl,
  kex: "ECDHE",
  auth: "RSA",
  cipher: "AES-128-GCM",
  bits: 128,
  forwardSecrecy: true,
  aead: true,
  rating,
  reasons: rating === "strong" ? [] : ["CBC mode"],
  ...extra,
})

const deep = {
  domain: "app.example.com",
  port: 443,
  checkedAt: "2026-09-28T10:00:00Z",
  reachable: true,
  address: "203.0.113.4:443",
  where: "here",
  connections: 38,
  versions: [
    {
      name: "TLS 1.3",
      status: "accepted",
      complete: true,
      order: "client",
      suites: [suite("TLS_AES_128_GCM_SHA256", "strong", { kex: "any", auth: "any" })],
    },
    {
      name: "TLS 1.2",
      status: "accepted",
      complete: true,
      order: "server",
      suites: [
        suite("ECDHE-RSA-AES128-GCM-SHA256", "strong"),
        suite("ECDHE-RSA-AES128-SHA", "weak"),
        suite("RC4-SHA", "insecure"),
      ],
    },
    { name: "TLS 1.1", status: "refused", complete: true, detail: "protocol version", suites: [] },
    {
      name: "TLS 1.0",
      status: "accepted",
      complete: false,
      detail: "The listing stopped after 1: The server did not answer within 5 seconds.",
      suites: [suite("ECDHE-RSA-AES128-SHA", "weak")],
    },
    { name: "SSL 3.0", status: "unknown", complete: false, detail: "timed out", suites: [] },
  ],
  groups: [],
  resumption: [],
  sni: [],
  findings: [
    {
      id: "tls.old-protocol.TLS 1.0",
      level: "warning",
      title: "TLS 1.0 is still offered",
      detail: "",
    },
    { id: "tls.cipher.insecure", level: "critical", title: "1 insecure cipher suite", detail: "" },
  ],
}

describe("the suite list", () => {
  test("counts accepted suites per version and per rating", () => {
    expect(versionCounts(deep)).toEqual([
      { name: "TLS 1.3", count: 1 },
      { name: "TLS 1.2", count: 3 },
      { name: "TLS 1.0", count: 1 },
    ])
    expect(ratingCounts(deep, "all")).toEqual({ strong: 2, weak: 2, insecure: 1 })
    expect(ratingCounts(deep, "TLS 1.2")).toEqual({ strong: 1, weak: 1, insecure: 1 })
  })

  test("everything shows the versions that accepted nothing too", () => {
    const groups = suiteGroups(deep, { version: "all", rating: "any" })
    expect(groups.map((g) => g.name)).toEqual([
      "TLS 1.3",
      "TLS 1.2",
      "TLS 1.1",
      "TLS 1.0",
      "SSL 3.0",
    ])
  })

  test("a rating keeps only its suites, in the versions that have them", () => {
    const groups = suiteGroups(deep, { version: "all", rating: "weak" })
    expect(groups.map((g) => [g.name, g.suites.map((s) => s.openssl)])).toEqual([
      ["TLS 1.2", ["ECDHE-RSA-AES128-SHA"]],
      ["TLS 1.0", ["ECDHE-RSA-AES128-SHA"]],
    ])
    expect(suiteGroups(deep, { version: "TLS 1.2", rating: "insecure" })[0].suites).toHaveLength(1)
  })

  test("a remembered choice that matches nothing here shows everything instead", () => {
    expect(effectiveFilter(deep, { version: "TLS 1.1", rating: "any" })).toEqual({
      version: "all",
      rating: "any",
    })
    expect(effectiveFilter(deep, { version: "TLS 1.3", rating: "insecure" })).toEqual({
      version: "TLS 1.3",
      rating: "any",
    })
    expect(suiteGroups(deep, { version: "SSL 3.0", rating: "weak" })).toHaveLength(2)
  })
})

describe("readings", () => {
  test("a version says how many suites and whose order, and a listing cut short why", () => {
    expect(versionReading(deep.versions[1])).toEqual({
      verdict: "ok",
      label: "3 suites · server's order",
      detail: undefined,
    })
    expect(versionReading(deep.versions[3])).toMatchObject({
      verdict: "critical",
      label: "1 suite",
    })
    expect(versionReading(deep.versions[3]).detail).toContain("stopped after 1")
    expect(versionReading(deep.versions[2])).toMatchObject({ verdict: "ok", label: "refused" })
    expect(versionReading({ ...deep.versions[2], name: "TLS 1.3" }).verdict).toBe("notice")
    expect(versionReading({ ...deep.versions[2], name: "TLS 1.2" })).toMatchObject({
      tone: "stopped",
    })
    expect(versionReading(deep.versions[4])).toMatchObject({ tone: "unknown", label: "unknown" })
  })

  test("a suite names its cipher, its key exchange and its certificate", () => {
    expect(suiteFacts(deep.versions[1].suites[0])).toBe(
      "AES-128-GCM, ECDHE key exchange, RSA certificate",
    )
    expect(suiteFacts(deep.versions[0].suites[0])).toBe(
      "AES-128-GCM, the group decides the key exchange",
    )
    expect(suiteFacts(suite("ADH-AES128-SHA", "insecure", { kex: "DHE", auth: "anon" }))).toContain(
      "no certificate",
    )
  })

  test("the key exchange a browser gets is post-quantum or not", () => {
    expect(
      browserGroupReading({
        ...deep,
        browserGroup: "X25519MLKEM768",
        browserGroupVersion: "TLS 1.3",
      }),
    ).toMatchObject({
      verdict: "ok",
      label: "X25519MLKEM768",
    })
    expect(
      browserGroupReading({ ...deep, browserGroup: "X25519", browserGroupVersion: "TLS 1.3" })
        .verdict,
    ).toBe("notice")
    expect(
      browserGroupReading({ ...deep, browserGroup: "P-256", browserGroupVersion: "TLS 1.2" })
        .detail,
    ).toContain("without TLS 1.3")
    expect(browserGroupReading(deep)).toMatchObject({ tone: "unknown", label: "not read" })
  })

  test("HTTP/2 is held against the site form", () => {
    const offered = ["h2", "http/1.1"]
    expect(alpnReading({ offered, negotiated: "h2" })).toMatchObject({ verdict: "ok", label: "h2" })
    expect(
      alpnReading({ offered, negotiated: "http/1.1", site: { name: "app", http2: true } }),
    ).toEqual({
      verdict: "warning",
      label: "http/1.1",
      detail: "Offered h2 and http/1.1. app's site form has HTTP/2 on.",
    })
    expect(alpnReading({ offered, negotiated: "" })).toMatchObject({
      verdict: "notice",
      label: "none chosen",
    })
  })

  test("HTTP/3 as advertised and as answered", () => {
    expect(
      http3Reading({
        answered: true,
        advertised: true,
        port: 443,
        quic: { port: 443, answered: true, versions: ["QUIC v1"] },
      }),
    ).toEqual({
      verdict: "ok",
      label: "answers",
      detail: "Alt-Svc offers h3 on UDP 443, and QUIC answered there (QUIC v1).",
    })
    expect(
      http3Reading({
        answered: true,
        advertised: true,
        port: 443,
        quic: { port: 443, answered: false, detail: "Two packets went unanswered." },
      }),
    ).toMatchObject({
      verdict: "warning",
      label: "no QUIC answer",
      detail: "Alt-Svc offers h3 on UDP 443. Two packets went unanswered.",
    })
    expect(
      http3Reading({ answered: true, advertised: false, quic: { port: 443, answered: true } }),
    ).toMatchObject({
      verdict: "notice",
      label: "not advertised",
    })
    expect(
      http3Reading({ answered: true, advertised: true, host: "alt.example.com", port: 8443 })
        .detail,
    ).toContain("alt.example.com:8443")
    expect(
      http3Reading({ answered: true, advertised: false, quic: { port: 443, answered: false } }),
    ).toMatchObject({
      tone: "unknown",
      label: "not offered",
    })
    expect(http3Reading({ answered: false, advertised: false, error: "EOF" }).detail).toBe(
      "HTTPS gave no HTTP answer: EOF.",
    )
  })

  test("resumption without a ticket is a finding in TLS 1.3 and untested in TLS 1.2", () => {
    expect(resumptionReading({ version: "TLS 1.3", status: "no-ticket", detail: "" }).verdict).toBe(
      "notice",
    )
    expect(resumptionReading({ version: "TLS 1.2", status: "no-ticket", detail: "" }).tone).toBe(
      "unknown",
    )
    expect(resumptionReading({ version: "TLS 1.3", status: "resumed", detail: "" }).verdict).toBe(
      "ok",
    )
  })

  test("a client that names no site is refused, or handed a certificate", () => {
    expect(
      sniReading({
        kind: "none",
        status: "refused",
        detail: "unrecognized name",
        sameAsNamed: false,
      }),
    ).toMatchObject({
      title: "No name",
      verdict: "ok",
      label: "refused",
    })
    expect(
      sniReading({
        kind: "unknown",
        status: "certificate",
        subject: "internal.example.com",
        names: ["internal.example.com", "10.0.0.2"],
        sameAsNamed: false,
      }),
    ).toMatchObject({
      title: "Unknown name",
      verdict: "notice",
      detail: "internal.example.com's certificate, for internal.example.com, 10.0.0.2.",
    })
    expect(
      sniReading({
        kind: "none",
        status: "certificate",
        subject: "app",
        names: ["app"],
        sameAsNamed: true,
      }).detail,
    ).toBe("The scanned name's own certificate, for app.")
  })
})

describe("key exchange groups", () => {
  test("a refusal is said once, by its label; a retry and an unknown keep their reason", () => {
    const g = { id: 29, name: "X25519", postQuantum: false }
    expect(
      groupReading({ ...g, status: "refused", detail: "The server answered: handshake failure." }),
    ).toEqual({
      tone: "stopped",
      label: "refused",
    })
    expect(
      groupReading({
        ...g,
        status: "accepted",
        detail: "Taken after asking the client for a key share.",
      }).detail,
    ).toContain("key share")
    expect(
      groupReading({
        ...g,
        status: "unknown",
        detail: "The server did not answer within 5 seconds.",
      }),
    ).toMatchObject({
      tone: "unknown",
      detail: "The server did not answer within 5 seconds.",
    })
  })
})

describe("the report around it", () => {
  test("a finding the quick report already lists is not listed twice", () => {
    const scan = { findings: [{ id: "tls.old-protocol.TLS 1.0" }] }
    expect(newDeepFindings(deep, scan).map((f) => f.id)).toEqual(["tls.cipher.insecure"])
    expect(newDeepFindings(deep, null)).toHaveLength(2)
  })

  test("the deep scan is asked for in the address, keeping the rest of it", () => {
    const params = new URLSearchParams("domain=app.example.com%3A8443")
    expect(deepHref(params, true)).toBe("/proxy/tls?domain=app.example.com%3A8443&deep=1")
    expect(deepHref(new URLSearchParams("domain=a.test&deep=1"), false)).toBe(
      "/proxy/tls?domain=a.test",
    )
    expect(deepHref(new URLSearchParams("deep=1"), false)).toBe("/proxy/tls")
  })
})
