import { describe, expect, test } from "bun:test"
import { DANGEROUS_PORTS, findingAction, foldProxyFindings, unreadableSource } from "./attention"

// The fold as it stood before each area's findings moved into findings/:
// these outputs were taken from that version, and the split must not change
// a word of them, their order, or which of them tie.

const cert = (overrides) => ({
  name: "app.example.com",
  path: "/etc/letsencrypt/live/app.example.com/fullchain.pem",
  domains: ["app.example.com"],
  issuer: "R11",
  notBefore: "2026-09-01T00:00:00Z",
  notAfter: "2026-12-01T00:00:00Z",
  daysLeft: 60,
  expired: false,
  expiring: false,
  selfSigned: false,
  source: "certbot",
  usedBy: [],
  ...overrides,
})
const vhost = (overrides) => ({
  name: "app.example.com",
  kind: "nginx",
  path: "/etc/nginx/sites-available/app.example.com",
  enabledPath: "/etc/nginx/sites-enabled/app.example.com",
  enabled: true,
  serverNames: ["app.example.com"],
  listen: ["443 ssl"],
  upstreams: ["http://127.0.0.1:3000"],
  tls: true,
  modified: "2026-09-01T00:00:00Z",
  size: 100,
  ...overrides,
})
const stream = (overrides) => ({
  name: "db",
  listen: 5432,
  protocol: "tcp",
  upstream: "10.0.0.5:5432",
  proxyProtocol: false,
  allowFrom: [],
  ...overrides,
})
const listener = (overrides) => ({
  protocol: "tcp",
  address: "0.0.0.0",
  port: 443,
  pid: 1,
  process: "nginx",
  exposed: true,
  ...overrides,
})
const inputs = {
  empty: {},
  healthy: {
    certs: [cert({})],
    certbot: {
      available: true,
      certs: [{ name: "a", domains: [], expiry: "", daysLeft: 60, valid: true }],
      autoRenew: true,
    },
    vhosts: [vhost({})],
    streams: {
      included: true,
      snippet: "",
      dir: "/etc/nginx/stream.d",
      streams: [stream({ allowFrom: ["10.0.0.0/8"] })],
    },
    ports: [listener({})],
  },
  everything: {
    certs: [
      cert({ name: "healthy.example.com", path: "/h" }),
      cert({
        name: "week.example.com",
        path: "/w",
        daysLeft: 20,
        expiring: true,
        usedBy: ["week", "week-api"],
      }),
      cert({ name: "soon.example.com", path: "/s", daysLeft: 1, expiring: true }),
      cert({ name: "gone.example.com", path: "/g", daysLeft: -1, expired: true, usedBy: ["gone"] }),
      cert({ name: "old.example.com", path: "/o", daysLeft: -12, expired: true }),
      cert({ name: "broken", path: "", error: "not a PEM certificate" }),
      cert({ name: "broken-with-path", path: "/b", error: "permission denied" }),
    ],
    certbot: {
      available: true,
      certs: [{ name: "a", domains: [], expiry: "", daysLeft: 60, valid: true }],
      autoRenew: false,
      renewUnit: "certbot.timer",
    },
    vhosts: [
      vhost({ name: "off", enabled: false }),
      vhost({ name: "conf.d-off", enabled: false, enabledPath: undefined }),
      vhost({ name: "plain", tls: false, serverNames: [] }),
      vhost({
        name: "plain-named",
        tls: false,
        upstreams: ["http://127.0.0.1:8080", "http://127.0.0.1:8081"],
      }),
      vhost({ name: "static", tls: false, upstreams: [] }),
      vhost({ name: "caddy-plain", kind: "caddy", tls: false }),
      vhost({ name: "tls" }),
    ],
    streams: {
      included: false,
      snippet: "",
      dir: "/etc/nginx/stream.d",
      streams: [
        stream({ name: "pg", listen: 5432 }),
        stream({ name: "game", listen: 25565, protocol: "udp" }),
        stream({ name: "private", listen: 6379, allowFrom: ["10.0.0.0/8"] }),
      ],
    },
    ports: [
      listener({ port: 5432, process: "postgres" }),
      listener({ port: 6379, process: "", protocol: "tcp6" }),
      listener({ port: 3306, exposed: false }),
      listener({}),
    ],
  },
  oneStreamNotIncluded: {
    streams: {
      included: false,
      snippet: "",
      dir: "/etc/nginx/stream.d",
      streams: [stream({ allowFrom: ["1.2.3.4"] })],
    },
    ports: [listener({ port: 27017, process: "mongod" })],
    certbot: {
      available: true,
      certs: [{ name: "a", domains: [], expiry: "", daysLeft: 60, valid: true }],
      autoRenew: false,
    },
  },
  certbotMissing: {
    certbot: null,
    certs: [cert({ daysLeft: 7, expiring: true })],
  },
}

describe("foldProxyFindings", () => {
  test("a quiet proxy has nothing to say", () => {
    expect(foldProxyFindings(inputs.empty)).toEqual([])
    expect(foldProxyFindings(inputs.healthy)).toEqual([])
  })

  test("every condition, worst first, ties in the order the areas are gathered", () => {
    expect(foldProxyFindings(inputs.everything)).toEqual([
      {
        id: "cert.expiring./s",
        level: "critical",
        title: "soon.example.com expires in 1 day",
        detail: "Inside Let's Encrypt's renewal window and still not renewed.",
        advice:
          "certbot renews at thirty days. A certificate still here a week later means the timer is not running.",
        meta: "certificate",
        href: "/proxy/certificates",
      },
      {
        id: "cert.expired./g",
        level: "critical",
        title: "gone.example.com has expired",
        detail: "Expired 1 day ago; every browser refuses it now. Used by gone.",
        advice: "Renew it, then find out why the renewal did not run on its own.",
        meta: "certificate",
        href: "/proxy/certificates",
      },
      {
        id: "cert.expired./o",
        level: "critical",
        title: "old.example.com has expired",
        detail: "Expired 12 days ago; every browser refuses it now.",
        advice: "Renew it, then find out why the renewal did not run on its own.",
        meta: "certificate",
        href: "/proxy/certificates",
      },
      {
        id: "certbot.no-timer",
        level: "critical",
        title: "Nothing is scheduled to renew certbot's certificates",
        detail: "No certbot timer and no cron entry was found for 1 certificate.",
        advice:
          "certbot.timer is installed but not running. Turn it on from the Certificates page.",
        meta: "renewal",
        href: "/proxy/certificates",
      },
      {
        id: "cert.expiring./w",
        level: "warning",
        title: "week.example.com expires in 20 days",
        detail:
          "Inside Let's Encrypt's renewal window and still not renewed. Used by week, week-api.",
        advice:
          "certbot renews at thirty days. A certificate still here a week later means the timer is not running.",
        meta: "certificate",
        href: "/proxy/certificates",
      },
      {
        id: "cert.error.broken",
        level: "warning",
        title: "broken could not be read",
        detail: "not a PEM certificate",
        advice:
          "A site pointing at a certificate nginx cannot read fails its next reload. Fix or replace the file, or point the site elsewhere.",
        meta: "certificate",
        href: "/proxy/certificates",
      },
      {
        id: "cert.error./b",
        level: "warning",
        title: "broken-with-path could not be read",
        detail: "permission denied",
        advice:
          "A site pointing at a certificate nginx cannot read fails its next reload. Fix or replace the file, or point the site elsewhere.",
        meta: "certificate",
        href: "/proxy/certificates",
      },
      {
        id: "site.plain.plain",
        level: "warning",
        title: "plain serves an application in plain text",
        detail:
          "plain proxies to http://127.0.0.1:3000 with no TLS, so anything typed into it crosses the network readable.",
        advice:
          "Issue a certificate from the Certificates page, then turn TLS on in the site's form.",
        meta: "site",
        href: "/proxy/sites?site=plain",
      },
      {
        id: "site.plain.plain-named",
        level: "warning",
        title: "plain-named serves an application in plain text",
        detail:
          "app.example.com proxies to http://127.0.0.1:8080 with no TLS, so anything typed into it crosses the network readable.",
        advice:
          "Issue a certificate from the Certificates page, then turn TLS on in the site's form.",
        meta: "site",
        href: "/proxy/sites?site=plain-named",
      },
      {
        id: "streams.not-included",
        level: "warning",
        title: "3 streams are written but nginx is not reading them",
        detail: "nginx.conf has no stream block including /etc/nginx/stream.d.",
        advice:
          "Add the include the Streams page prints, at the top level of nginx.conf beside the http block.",
        meta: "streams",
        href: "/proxy/streams",
      },
      {
        id: "stream.open.pg",
        level: "warning",
        title: "Stream pg forwards port 5432 to anyone",
        detail: "TCP 5432 → 10.0.0.5:5432 with no allow list, and 5432 is PostgreSQL.",
        advice:
          "A stream has no authentication of its own. Restrict the source unless the service behind it authenticates for itself.",
        meta: "stream",
        href: "/proxy/streams",
      },
      {
        id: "ports.dangerous",
        level: "warning",
        title: "2 database or control ports answer on every interface",
        detail: "5432/tcp postgres, 6379/tcp6 unknown",
        advice:
          "Bind these to loopback or a private address, or close them in the firewall. A database port on the internet is the commonest way a server is emptied.",
        meta: "ports",
        href: "/proxy/ports",
      },
      {
        id: "site.disabled.off",
        level: "notice",
        title: "off is on disk but not serving",
        detail: "The file is in sites-available with no link in sites-enabled.",
        advice: "Enable it from Sites if it is meant to serve, or delete it if it is not.",
        meta: "site",
        href: "/proxy/sites?site=off",
      },
      {
        id: "stream.open.game",
        level: "notice",
        title: "Stream game forwards port 25565 to anyone",
        detail: "UDP 25565 → 10.0.0.5:5432 with no allow list.",
        advice:
          "A stream has no authentication of its own. Restrict the source unless the service behind it authenticates for itself.",
        meta: "stream",
        href: "/proxy/streams",
      },
    ])
  })

  test("one stream and one database port read in the singular", () => {
    expect(foldProxyFindings(inputs.oneStreamNotIncluded)).toEqual([
      {
        id: "certbot.no-timer",
        level: "critical",
        title: "Nothing is scheduled to renew certbot's certificates",
        detail: "No certbot timer and no cron entry was found for 1 certificate.",
        advice:
          "Install certbot's timer or a cron entry; without one every certificate here expires in ninety days.",
        meta: "renewal",
        href: "/proxy/certificates",
      },
      {
        id: "streams.not-included",
        level: "warning",
        title: "1 stream is written but nginx is not reading it",
        detail: "nginx.conf has no stream block including /etc/nginx/stream.d.",
        advice:
          "Add the include the Streams page prints, at the top level of nginx.conf beside the http block.",
        meta: "streams",
        href: "/proxy/streams",
      },
      {
        id: "ports.dangerous",
        level: "warning",
        title: "MongoDB answers on every interface",
        detail: "27017/tcp mongod",
        advice:
          "Bind these to loopback or a private address, or close them in the firewall. A database port on the internet is the commonest way a server is emptied.",
        meta: "ports",
        href: "/proxy/ports",
      },
    ])
  })

  test("certbot missing is not a finding, a week-old expiry is critical", () => {
    expect(foldProxyFindings(inputs.certbotMissing)).toEqual([
      {
        id: "cert.expiring./etc/letsencrypt/live/app.example.com/fullchain.pem",
        level: "critical",
        title: "app.example.com expires in 7 days",
        detail: "Inside Let's Encrypt's renewal window and still not renewed.",
        advice:
          "certbot renews at thirty days. A certificate still here a week later means the timer is not running.",
        meta: "certificate",
        href: "/proxy/certificates",
      },
    ])
  })

  test("the dangerous ports are still exported for the pages that draw them", () => {
    expect(DANGEROUS_PORTS[5432]).toBe("PostgreSQL")
    expect(DANGEROUS_PORTS[443]).toBeUndefined()
  })
})

describe("sources the overview could not read", () => {
  const unreadable = [
    { source: "sites", message: "could not determine Docker ingress ownership" },
    { source: "certificates", message: "internal error" },
    { source: "renewal", message: "certbot certificates timed out" },
    { source: "streams", message: "Service Unavailable" },
    { source: "ports", message: "Failed to fetch" },
  ]

  // With every source failing there is nothing to judge, and the list used
  // to come up empty — the green "all within limits" about a host it could
  // not see. Each failure is a finding instead, with its reason.
  test("each is a finding of its own, so the list is never empty", () => {
    const findings = foldProxyFindings({ unreadable })
    expect(findings.map((f) => f.title)).toEqual([
      "Sites could not be read",
      "Certificates could not be read",
      "Certificate renewal could not be read",
      "Streams could not be read",
      "Listening ports could not be read",
    ])
    expect(findings[0]).toEqual({
      id: "source.unreadable.sites",
      level: "warning",
      title: "Sites could not be read",
      detail: "could not determine Docker ingress ownership",
      advice:
        "Until it can be read, this list cannot show a site that is disabled or serves plain HTTP.",
      meta: "sites",
      href: "/proxy/sites",
    })
    expect(findings.map(unreadableSource)).toEqual([
      "sites",
      "certificates",
      "renewal",
      "streams",
      "ports",
    ])
  })

  test("an unreadable source ranks with the warnings the others raise", () => {
    const findings = foldProxyFindings({
      ...inputs.oneStreamNotIncluded,
      unreadable: [{ source: "sites", message: "internal error" }],
    })
    expect(findings.map((f) => f.id)).toEqual([
      "certbot.no-timer",
      "streams.not-included",
      "ports.dangerous",
      "source.unreadable.sites",
    ])
    expect(findings.filter((f) => unreadableSource(f)).length).toBe(1)
  })

  test("nothing failing adds nothing", () => {
    expect(foldProxyFindings({ ...inputs.healthy, unreadable: [] })).toEqual([])
  })
})

describe("finding actions", () => {
  // The button read `Open ${meta}`: "Open renewal", "Open ports". Every area's
  // label now has words for where the button leads.
  test("every finding the fold can raise names where it leads", () => {
    const labels = Object.fromEntries(
      foldProxyFindings({
        ...inputs.everything,
        unreadable: [
          { source: "sites", message: "x" },
          { source: "certificates", message: "x" },
        ],
      }).map((f) => [f.meta, findingAction(f)]),
    )
    expect(labels).toEqual({
      certificate: "Open certificates",
      renewal: "Open certificates",
      site: "Open site",
      streams: "Open streams",
      stream: "Open streams",
      ports: "Open ports",
      sites: "Open sites",
      certificates: "Open certificates",
    })
  })

  // The label came from a table of the metas above, so a finding another area
  // added with a meta of its own got a wrench-only button with no name.
  test("a finding with a meta no area used before still names where it leads", () => {
    const action = (meta, href) =>
      findingAction({ id: "x", level: "notice", title: "x", meta, href })
    expect(action("tls", "/proxy/tls")).toBe("Open TLS report")
    expect(action("default site", "/proxy/sites?site=_")).toBe("Open site")
    expect(action("drift", "/proxy/sites")).toBe("Open sites")
    expect(action(undefined, "/proxy/certificates#lineage")).toBe("Open certificates")
    expect(action("served cert", "/proxy/ports")).toBe("Open ports")
    expect(action("stream readiness", "/proxy/streams?stream=db")).toBe("Open streams")
    expect(action("config", "/proxy/config?path=/etc/nginx/nginx.conf")).toBe("Open")
  })
})
