import { describe, expect, test } from "bun:test"
import {
  certificateLeft,
  diagnosisLinks,
  failureSteps,
  plainVerdict,
  termLeft,
  termText,
} from "./tls-report"

const http = (over) => ({
  service: "http",
  statusCode: 200,
  plainRedirects: false,
  redirectChain: [],
  headers: [],
  ...over,
})

describe("plainVerdict", () => {
  test("a refused port 80 is the only one called refused", () => {
    expect(plainVerdict(http({ plainError: "x", plainErrorKind: "refused" }))).toEqual({
      verdict: "notice",
      label: "port 80 refused the connection",
    })
    expect(plainVerdict(http({ plainError: "x", plainErrorKind: "timeout" })).label).toBe(
      "port 80 did not answer in time",
    )
  })

  test("HTTPS on the same host, straight or in hops", () => {
    const one = [{ url: "http://a.test/", status: 301, location: "https://a.test/" }]
    expect(plainVerdict(http({ redirectVerdict: "same-host", redirectChain: one }))).toEqual({
      verdict: "ok",
      label: "redirects to HTTPS",
    })
    const two = [
      { url: "http://a.test/", status: 301, location: "/login" },
      { url: "http://a.test/login", status: 301, location: "https://a.test/login" },
    ]
    expect(plainVerdict(http({ redirectVerdict: "same-host", redirectChain: two })).label).toBe(
      "redirects to HTTPS in 2 hops",
    )
  })

  test("HTTPS through another host names it", () => {
    const chain = [
      { url: "http://a.test/", status: 301, location: "http://www.a.test/" },
      { url: "http://www.a.test/", status: 301, location: "https://www.a.test/" },
    ]
    expect(plainVerdict(http({ redirectVerdict: "other-host", redirectChain: chain }))).toEqual({
      verdict: "ok",
      label: "reaches HTTPS on www.a.test in 2 hops",
    })
  })

  test("every way of never reaching HTTPS says which", () => {
    const hop = (over) => ({ url: "http://a.test/", status: 301, location: "/b", ...over })
    const cases = [
      ["loop", [hop(), hop({ url: "http://a.test/b", location: "/" })], "redirects in a loop"],
      ["too-many", [hop(), hop(), hop(), hop(), hop()], "5 redirects, none to HTTPS"],
      ["dead-end", [hop(), { url: "http://a.test/b", error: "EOF" }], "a redirect leads nowhere"],
      ["dead-end", [hop({ location: "ftp://a.test/" })], "redirects, never to HTTPS"],
      [
        "stays-http",
        [hop({ status: 200, location: undefined })],
        "answers 200 without redirecting",
      ],
      [
        "stays-http",
        [hop(), { url: "http://a.test/b", status: 200 }],
        "stays on HTTP, answering 200",
      ],
    ]
    for (const [redirectVerdict, redirectChain, label] of cases) {
      expect(plainVerdict(http({ redirectVerdict, redirectChain }))).toEqual({
        verdict: "critical",
        label,
      })
    }
    expect(
      plainVerdict(
        http({ redirectVerdict: "internal", redirectChain: [hop(), { url: "x", internal: true }] }),
      ),
    ).toEqual({ verdict: "notice", label: "not followed to an internal address" })
  })
})

const failed = (failure, domain = "app.example.com") => ({
  domain,
  port: 443,
  reachable: false,
  failure,
})

describe("failureSteps", () => {
  test("a name with no record fails first and the rest are not reached", () => {
    const steps = failureSteps(failed({ stage: "dns", reason: "no-such-host" }))
    expect(steps.map((s) => [s.label, s.value, s.tone])).toEqual([
      ["Name", "no record", "danger"],
      ["Port 443", "—", "default"],
      ["Handshake", "—", "default"],
    ])
    expect(steps[1].hint).toBe("not reached")
  })

  test("a refused connection shows where it went", () => {
    const steps = failureSteps(
      failed({
        stage: "connect",
        reason: "refused",
        address: "203.0.113.4:443",
        where: "here",
        dns: { addresses: ["203.0.113.4"] },
      }),
    )
    expect(steps[0]).toEqual({
      label: "Name",
      value: "resolves",
      hint: "203.0.113.4",
      tone: "default",
    })
    expect(steps[1]).toEqual({
      label: "Port 443",
      value: "refused",
      hint: "203.0.113.4:443 · this server",
      tone: "danger",
    })
    expect(steps[2].value).toBe("—")
  })

  test("an address has nothing to resolve", () => {
    const steps = failureSteps(
      failed({ stage: "connect", reason: "timeout", address: "[::1]:443" }, "::1"),
    )
    expect(steps[0].value).toBe("address")
    expect(steps[1].value).toBe("timed out")
  })

  test("a handshake failure says what answered", () => {
    const plain = failureSteps(
      failed({ stage: "handshake", reason: "plain-http", answer: "HTTP/", address: "a:443" }),
    )
    expect(plain[1].value).toBe("open")
    expect(plain[2]).toEqual({
      label: "Handshake",
      value: "plain HTTP",
      hint: "answered “HTTP/”",
      tone: "danger",
    })
    const alert = failureSteps(
      failed({ stage: "handshake", reason: "alert", alert: "unrecognized name" }),
    )
    expect([alert[2].value, alert[2].hint]).toEqual(["refused", "unrecognized name"])
  })

  test("a scan that did not fail has no steps", () => {
    expect(failureSteps({ domain: "a", port: 443, reachable: true })).toEqual([])
  })
})

describe("diagnosisLinks", () => {
  const links = (failure) => diagnosisLinks(failed(failure)).map((l) => [l.label, l.href])
  test("a refusal here points at this server's ports", () => {
    expect(links({ stage: "connect", reason: "refused", where: "here" })).toEqual([
      ["Listening ports", "/proxy/ports?q=:443"],
    ])
  })
  test("a timeout adds the firewall, and a handshake the sites", () => {
    expect(links({ stage: "connect", reason: "timeout", where: "unknown" })).toEqual([
      ["Listening ports", "/proxy/ports?q=:443"],
      ["Firewall", "/network/firewall"],
    ])
    expect(links({ stage: "handshake", reason: "plain-http", where: "here" })).toEqual([
      ["Listening ports", "/proxy/ports?q=:443"],
      ["Sites", "/proxy/sites"],
    ])
  })
  test("plain HTTP on port 80 is sent to 443, wherever it answered", () => {
    for (const where of ["here", "unknown", "elsewhere", "cloudflare"]) {
      const scan = { ...failed({ stage: "handshake", reason: "plain-http", where }), port: 80 }
      expect(diagnosisLinks(scan).map((l) => [l.label, l.href])).toEqual([
        ["Scan port 443", "/proxy/tls?domain=app.example.com"],
      ])
    }
    const address = {
      ...failed({ stage: "handshake", reason: "plain-http", where: "here" }, "::1"),
      port: 80,
    }
    expect(diagnosisLinks(address)[0].href).toBe("/proxy/tls?domain=%3A%3A1")
  })
  test("nothing on this server fixes a fault elsewhere, in DNS or with no route", () => {
    expect(links({ stage: "connect", reason: "refused", where: "elsewhere" })).toEqual([])
    expect(links({ stage: "connect", reason: "refused", where: "cloudflare" })).toEqual([])
    expect(links({ stage: "dns", reason: "no-such-host" })).toEqual([])
    expect(links({ stage: "connect", reason: "unreachable", where: "here" })).toEqual([])
  })
})

describe("a certificate's term", () => {
  test("in days, or in hours when short-lived", () => {
    expect(termText({ lifetimeHours: 2160, renewalWindowHours: 720 })).toBe(
      "90 days, renewal due in the last 30 days",
    )
    expect(termText({ lifetimeHours: 160, renewalWindowHours: 80 })).toBe(
      "160 hours, renewal due in the last 80 hours",
    )
    expect(termText({})).toBeUndefined()
  })

  test("the figure left is whole days, then hours for the last two", () => {
    const now = Date.parse("2026-09-28T00:00:00Z")
    expect(certificateLeft({ daysLeft: 30, notAfter: "2026-10-28T00:00:00Z" }, now)).toBe("30d")
    expect(certificateLeft({ daysLeft: 1, notAfter: "2026-09-29T05:30:00Z" }, now)).toBe("29h")
    expect(certificateLeft({ daysLeft: 0, notAfter: "2026-09-28T00:20:00Z" }, now)).toBe("<1h")
  })

  test("the share of the term left", () => {
    const now = Date.parse("2026-09-28T00:00:00Z")
    const cert = { notBefore: "2026-08-29T00:00:00Z", notAfter: "2026-11-27T00:00:00Z" }
    expect(Math.round(termLeft(cert, now))).toBe(67)
    expect(termLeft({ ...cert, notAfter: "2026-09-01T00:00:00Z" }, now)).toBe(0)
    expect(termLeft({ notBefore: "x", notAfter: "y" }, now)).toBe(0)
  })
})
