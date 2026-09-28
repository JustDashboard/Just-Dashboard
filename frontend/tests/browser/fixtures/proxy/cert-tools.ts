import { json, now, type ProxyRoutes } from "./shared"

/**
 * The certificates this server makes itself: signing requests waiting for an
 * authority's answer, and the local CA with what it issued. The plain host
 * has neither; the showcase has both, so the layout checks draw every panel.
 */

const day = 86_400_000
const at = (days: number) => new Date(Date.now() + days * day).toISOString()

export const CSR = `-----BEGIN CERTIFICATE REQUEST-----
MIHXMH8CAQAwGzEZMBcGA1UEAxMQc2hvcC5leGFtcGxlLmNvbTBZMBMGByqGSM49
AgEGCCqGSM49AwEHA0IABHN0YXJ0IG9mIGEgdGVzdCBrZXkgdGhhdCBpcyBub3Qg
cmVhbCBhdCBhbGwgYnV0IGxvb2tzIGxpa2Ugb25lIG9rYXmgADAKBggqhkjOPQQD
AgNIADBFAiEA3q2+7wAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAACIF3q2+7w
AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
-----END CERTIFICATE REQUEST-----
`

/** A signing request waiting for its certificate, with whatever a test needs changed. */
export function signingRequest(overrides: Record<string, unknown> = {}) {
  return {
    name: "shop.example.com",
    domains: ["shop.example.com", "www.shop.example.com"],
    subject: {},
    keyType: "ecdsa-p256",
    created: at(-2),
    csr: CSR,
    keyPath: "/etc/ssl/just-dashboard-private/requests/shop.example.com/privkey.pem",
    ...overrides,
  }
}

/** A certificate the local CA issued, as its panel lists one. */
export function localLeaf(overrides: Record<string, unknown> = {}) {
  return {
    name: "nas.lan",
    path: "/etc/ssl/just-dashboard/nas.lan/fullchain.pem",
    domains: ["nas.lan", "192.168.1.10"],
    notBefore: at(-30),
    notAfter: at(366),
    daysLeft: 366,
    renewsAt: at(321),
    usedBy: ["nas.lan"],
    ...overrides,
  }
}

/** The local CA on the mocked host: none unless a test says otherwise. */
export function localCA(overrides: Record<string, unknown> = {}) {
  return {
    exists: false,
    leaves: [],
    renewBefore: 45,
    ...overrides,
  }
}

/** A local CA that exists, with one certificate and a check that ran this morning. */
export function existingLocalCA(overrides: Record<string, unknown> = {}) {
  return localCA({
    exists: true,
    name: "Just Dashboard local CA (edge-1)",
    notBefore: at(-30),
    notAfter: at(3620),
    fingerprint:
      "3A:9F:12:C4:5B:77:E0:1D:8A:2B:6C:4E:9F:10:AB:CD:EF:01:23:45:67:89:AB:CD:EF:01:23:45:67:89:AB:CD",
    leaves: [localLeaf()],
    lastCheck: { at: now, renewed: [], reloaded: [], failed: [] },
    nextCheck: at(1),
    ...overrides,
  })
}

/** What issuing, self-signing or completing a request answers with. */
export function issued(name: string, domains: string[], overrides: Record<string, unknown> = {}) {
  return {
    name,
    certPath: `/etc/ssl/just-dashboard/${name}/fullchain.pem`,
    keyPath: `/etc/ssl/just-dashboard/${name}/privkey.pem`,
    certificate: {
      name,
      path: `/etc/ssl/just-dashboard/${name}/fullchain.pem`,
      domains,
      issuer: "Just Dashboard local CA (edge-1)",
      notBefore: now,
      notAfter: at(397),
      daysLeft: 396,
      expired: false,
      expiring: false,
      selfSigned: false,
      source: "imported",
      usedBy: [],
      localCA: true,
    },
    chainComplete: true,
    replaced: false,
    warnings: [],
    ...overrides,
  }
}

export const routes: ProxyRoutes = {
  "/certificates/csr": (route) => json(route, []),
  "/certificates/local-ca": (route) => json(route, localCA()),
}

export const showcase: ProxyRoutes = {
  "/certificates/csr": (route) =>
    json(route, [
      signingRequest(),
      signingRequest({
        name: "portal",
        domains: ["portal.example.com"],
        keyType: "rsa-3072",
        replaces: {
          name: "portal",
          path: "/etc/ssl/just-dashboard/portal/fullchain.pem",
          domains: ["portal.example.com"],
          issuer: "Sectigo RSA Domain Validation Secure Server CA",
          notBefore: at(-340),
          notAfter: at(25),
          daysLeft: 25,
          expired: false,
          expiring: true,
          selfSigned: false,
          source: "imported",
          usedBy: ["portal.example.com"],
        },
      }),
    ]),
  "/certificates/local-ca": (route) => json(route, existingLocalCA()),
}
