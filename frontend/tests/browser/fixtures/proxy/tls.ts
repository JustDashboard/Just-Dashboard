import { certs } from "./certs"
import { inThirtyDays, json, now, type ProxyRoutes } from "./shared"

export const scan = {
  domain: "app.example.com",
  port: 443,
  checkedAt: now,
  reachable: true,
  grade: "B",
  summary: "A trusted certificate with headers to improve",
  negotiated: "TLS 1.3",
  cipherSuite: "TLS_AES_128_GCM_SHA256",
  certificate: certs[0],
  trusted: true,
  chainComplete: true,
  nameMatches: true,
  keyType: "ECDSA",
  keyBits: 256,
  signatureAlgorithm: "ECDSA-SHA256",
  serial: "04:D3:51:AA:12:FE:90:81",
  fingerprint: "ab:cd:".repeat(31) + "ef",
  ocspStapled: false,
  protocols: [
    { name: "TLS 1.0", status: "unknown", detail: "This client cannot test this version" },
    { name: "TLS 1.1", status: "refused" },
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
  findings: [
    {
      id: "hsts",
      level: "warning",
      title: "HSTS is not set",
      detail: "The endpoint does not send Strict-Transport-Security.",
      advice: "Enable HSTS after verifying HTTPS for all covered domains.",
    },
  ],
  http: {
    statusCode: 200,
    plainRedirects: true,
    plainStatus: 301,
    plainLocation: "https://app.example.com/",
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
    json(route, [{ id: 1, domain: "mail.example.com", port: 993, certificate: certs[0] }]),
}
