import { certs } from "./certs"
import { inThirtyDays, json, now, type ProxyRoutes } from "./shared"

const day = 86_400_000

/**
 * The scanned leaf: a ninety-day certificate halfway through its term, so it
 * reads as the backend would judge it — not yet in its last thirty days.
 */
export const leaf = {
  ...certs[0],
  source: "live",
  usedBy: [],
  notBefore: new Date(Date.now() - 45 * day).toISOString(),
  notAfter: new Date(Date.now() + 45 * day - 1000).toISOString(),
  daysLeft: 44,
  expiring: false,
}

export const scan = {
  domain: "app.example.com",
  port: 443,
  checkedAt: now,
  reachable: true,
  grade: "B",
  summary: "A trusted certificate with headers to improve",
  negotiated: "TLS 1.3",
  cipherSuite: "TLS_AES_128_GCM_SHA256",
  legacyOnly: false,
  certificate: leaf,
  trusted: true,
  chainComplete: true,
  nameMatches: true,
  keyType: "ECDSA",
  keyBits: 256,
  signatureAlgorithm: "ECDSA-SHA256",
  serial: "04:D3:51:AA:12:FE:90:81",
  fingerprint: "ab:cd:".repeat(31) + "ef",
  ocspStapled: false,
  crlUrls: ["http://r11.c.lencr.org/12.crl"],
  spkiPin: "C5+lpZ7tcVwmwQIMcRtPbsQtWLABXhQzejna0wHFr8M=",
  lifetimeHours: 2160,
  renewalWindowHours: 720,
  protocols: [
    {
      name: "TLS 1.0",
      status: "unknown",
      detail: "This dashboard's TLS library will not ask for it, so the server was never asked.",
    },
    {
      name: "TLS 1.1",
      status: "refused",
      detail: "The server answered: protocol version not supported.",
    },
    { name: "TLS 1.2", status: "offered" },
    { name: "TLS 1.3", status: "offered" },
  ],
  chain: [
    {
      subject: "app.example.com",
      issuer: "R11",
      notAfter: inThirtyDays,
      isCa: false,
      selfIssued: false,
      keyType: "ECDSA",
      keyBits: 256,
    },
    {
      subject: "R11",
      issuer: "ISRG Root X1",
      notAfter: inThirtyDays,
      isCa: true,
      selfIssued: false,
      keyType: "RSA",
      keyBits: 2048,
    },
  ],
  // A subdomain on 443 is measured against the preload list as its parent is.
  preload: {
    domain: "example.com",
    eligible: false,
    rules: [
      {
        id: "registrable",
        title: "A registrable domain",
        passed: false,
        detail:
          "The list takes whole registrable domains: app.example.com is covered by submitting example.com, which preloads every name under it.",
      },
    ],
  },
  findings: [
    {
      id: "hsts",
      level: "warning",
      title: "HSTS is not set",
      detail: "The endpoint does not send Strict-Transport-Security.",
      advice: "Enable HSTS after verifying HTTPS for all covered domains.",
    },
  ],
  checks: [
    {
      id: "tls.untrusted",
      category: "certificate",
      title: "The chain is trusted",
      passed: true,
      na: false,
      cap: "F",
    },
    {
      id: "tls.old-protocol",
      category: "protocol",
      title: "TLS 1.0 and 1.1 are refused",
      passed: false,
      na: true,
      cap: "C",
    },
    {
      id: "tls.hsts",
      category: "http",
      title: "HSTS is set for at least six months",
      passed: false,
      na: false,
      cap: "A",
    },
  ],
  http: {
    service: "http",
    statusCode: 200,
    server: "nginx",
    plainRedirects: true,
    plainStatus: 301,
    plainLocation: "https://app.example.com/",
    redirectChain: [
      { url: "http://app.example.com/", status: 301, location: "https://app.example.com/" },
    ],
    redirectVerdict: "same-host",
    headers: [
      {
        name: "X-Content-Type-Options",
        present: true,
        value: "nosniff",
        level: "important",
        detail: "Prevents MIME sniffing",
      },
      {
        name: "Content-Security-Policy",
        present: false,
        level: "important",
        detail: "No content security policy was sent",
      },
      {
        name: "Referrer-Policy",
        present: true,
        value: "strict-origin-when-cross-origin",
        level: "optional",
        detail: "Controls referrers",
      },
    ],
  },
}

export const routes: ProxyRoutes = {
  "/certificates/watched": (route) => json(route, []),
}

export const showcase: ProxyRoutes = {
  "/certificates/scan": (route) => json(route, scan),
  "/certificates/watched": (route) =>
    json(route, [
      { id: 1, domain: "mail.example.com", port: 993, checkedAt: now, certificate: certs[0] },
    ]),
}
