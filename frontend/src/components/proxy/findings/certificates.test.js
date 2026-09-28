import { describe, expect, test } from "bun:test"
import { runPhrase, sinceDay } from "@/lib/certificates"
import { certificateFindings } from "./certificates"

const expired = (hoursAgo) => ({
  name: "gone.example.com",
  path: "/etc/ssl/just-dashboard/gone/fullchain.pem",
  domains: ["gone.example.com"],
  issuer: "R11",
  notBefore: new Date(Date.now() - 90 * 86_400_000).toISOString(),
  notAfter: new Date(Date.now() - hoursAgo * 3_600_000).toISOString(),
  // What the backend sends for a certificate that ran out this morning:
  // days left truncate toward zero.
  daysLeft: 0,
  expired: true,
  expiring: false,
  selfSigned: false,
  source: "imported",
  usedBy: [],
})

describe("an expired certificate's finding", () => {
  test("hours after expiry it says today, not 0 days ago", () => {
    const [finding] = certificateFindings({ certs: [expired(3)] })
    expect(finding.title).toBe("gone.example.com has expired")
    expect(finding.detail).toBe("Expired today; every browser refuses it now.")
  })

  test("days after, it counts them", () => {
    const [finding] = certificateFindings({ certs: [{ ...expired(5 * 24 + 2), daysLeft: -5 }] })
    expect(finding.detail).toBe("Expired 5 days ago; every browser refuses it now.")
  })
})

const staging = (overrides = {}) => ({
  ...expired(0),
  name: "test.example.com",
  path: "/etc/letsencrypt/live/test.example.com/fullchain.pem",
  domains: ["test.example.com"],
  issuer: "(STAGING) Riddling Rhubarb R12",
  notAfter: new Date(Date.now() + 80 * 86_400_000).toISOString(),
  daysLeft: 80,
  expired: false,
  source: "certbot",
  staging: true,
  ...overrides,
})

describe("a test certificate's finding", () => {
  test("a site serving it is an outage, with its sites named", () => {
    const findings = certificateFindings({ certs: [staging({ usedBy: ["test.example.com"] })] })
    expect(findings).toHaveLength(1)
    expect(findings[0]).toMatchObject({
      id: "cert.staging./etc/letsencrypt/live/test.example.com/fullchain.pem",
      level: "critical",
      title: "test.example.com is a test certificate",
      detail:
        "A staging authority signed it, so every browser refuses it. Used by test.example.com.",
    })
    expect(findings[0].advice).toContain("Replace it with a real certificate")
  })

  test("one nothing serves is a warning to clear up", () => {
    const [finding] = certificateFindings({ certs: [staging()] })
    expect(finding.level).toBe("warning")
  })

  test("one expired or expiring raises the test-certificate finding only", () => {
    expect(
      certificateFindings({ certs: [staging({ expired: true, daysLeft: -2 })] }).map((f) => f.id),
    ).toEqual(["cert.staging./etc/letsencrypt/live/test.example.com/fullchain.pem"])
    expect(
      certificateFindings({ certs: [staging({ expiring: true, daysLeft: 5 })] }).map(
        (f) => f.title,
      ),
    ).toEqual(["test.example.com is a test certificate"])
  })

  test("a file a site names elsewhere is issued for, not replaced", () => {
    const [finding] = certificateFindings({
      certs: [staging({ source: "nginx:test.example.com", usedBy: ["test.example.com"] })],
    })
    expect(finding.advice).toContain("point the site at it")
  })

  // The Certificates page offers no real issuance then, so the advice may not
  // send the operator to it for one.
  test("with a staging authority configured, the way out is the directory", () => {
    const certbot = { available: true, autoRenew: true, certs: [], testAuthority: true }
    const [lineage, named] = certificateFindings({
      certs: [
        staging({ usedBy: ["test.example.com"] }),
        staging({ source: "nginx:test.example.com", path: "/etc/nginx/ssl/test.crt" }),
      ],
      certbot,
    })
    expect(lineage.advice).toBe(
      "JD_ACME_DIRECTORY names a staging authority, so what this dashboard issues is a test certificate too. Clear it or point it at a production directory and restart the dashboard, then replace this one.",
    )
    expect(named.advice).toEndWith("then issue a real one and point the site at it.")
    for (const finding of [lineage, named]) {
      expect(finding.advice).not.toContain("from the Certificates page")
    }
  })

  // Without certbot the page has Import and no issuance verb.
  test("with certbot not installed, the way out is an import", () => {
    const findings = certificateFindings({
      certs: [staging(), staging({ source: "nginx:test.example.com", path: "/etc/nginx/ssl/t" })],
      certbot: null,
    })
    for (const finding of findings) {
      expect(finding.advice).toBe(
        "certbot is not installed, so this dashboard cannot issue a real certificate. Import one for these names from the Certificates page and point the site at it.",
      )
    }
  })
})

const lineage = (overrides = {}) => ({
  name: "betbots.site",
  domains: ["betbots.site"],
  expiry: new Date(Date.now() + 10 * 86_400_000).toISOString(),
  daysLeft: 10,
  valid: true,
  certPath: "/etc/letsencrypt/live/betbots.site/fullchain.pem",
  authenticator: "nginx",
  installer: "nginx",
  ...overrides,
})
const certbot = (overrides = {}) => ({
  available: true,
  certs: [lineage()],
  autoRenew: true,
  renewSource: "certbot.timer",
  nginxReloads: true,
  reloadHook: {
    path: "/etc/letsencrypt/renewal-hooks/deploy/50-just-dashboard-reload-nginx",
    state: "missing",
    others: [],
  },
  ...overrides,
})
const lastRun = "2026-09-27T21:13:11Z"

describe("a renewal that failed", () => {
  test("is critical, with the certificate it failed on and the streak", () => {
    const findings = certificateFindings({
      certbot: certbot({
        health: {
          source: "certbot.service",
          service: "certbot.service",
          state: "failed",
          lastRun,
          exitStatus: 1,
          failingSince: "2026-07-02T02:26:01Z",
          failures: [{ lineage: "betbots.site", reason: "Some challenges have failed." }],
        },
      }),
    })
    expect(findings).toHaveLength(1)
    expect(findings[0]).toMatchObject({
      id: "certbot.renewal-failing",
      level: "critical",
      title: "certbot's last renewal failed",
      detail: `certbot.service failed ${runPhrase(lastRun)}. betbots.site: Some challenges have failed. Every run since ${sinceDay("2026-07-02T02:26:01Z")} has failed.`,
    })
  })

  test("with no certificate to blame, the run's own reason; renewed since, nothing", () => {
    const failed = (overrides) =>
      certificateFindings({
        certbot: certbot({
          health: {
            source: "certbot.service",
            service: "certbot.service",
            lastRun,
            failures: [],
            ...overrides,
          },
        }),
      })
    expect(
      failed({ state: "failed", reason: "Another instance of Certbot is already running." })[0]
        .detail,
    ).toBe(
      `certbot.service failed ${runPhrase(lastRun)}. Another instance of Certbot is already running.`,
    )
    expect(failed({ state: "failed", exitStatus: 1 })[0].detail).toBe(
      `certbot.service failed ${runPhrase(lastRun)}. It exited 1.`,
    )
    expect(
      failed({
        state: "recovered",
        failures: [{ lineage: "betbots.site", reason: "x", renewedSince: true }],
      }),
    ).toEqual([])
  })
})

describe("a renewal that will fail", () => {
  test("inside the renewal window it is critical, before it a warning", () => {
    const willFail = [
      "certbot renews it with the dns-cloudflare plugin, which the host's, certbot 2.11.0 does not have.",
    ]
    const [due] = certificateFindings({ certbot: certbot({ certs: [lineage({ willFail })] }) })
    expect(due).toMatchObject({
      id: "certbot.will-fail.betbots.site",
      level: "critical",
      title: "betbots.site will fail to renew",
      detail: willFail[0],
    })
    const [later] = certificateFindings({
      certbot: certbot({ certs: [lineage({ willFail, daysLeft: 70 })] }),
    })
    expect(later.level).toBe("warning")
  })
})

describe("renewals nginx never reloads for", () => {
  const served = [
    {
      path: "/etc/letsencrypt/live/betbots.site/fullchain.pem",
      usedBy: ["betbots"],
      daysLeft: 10,
      expiring: false,
      expired: false,
      name: "betbots.site",
      domains: [],
      issuer: "R11",
      notBefore: "",
      notAfter: "",
      selfSigned: false,
      source: "certbot",
    },
  ]

  test("a webroot lineage a site serves, with no hook, is a warning naming the site", () => {
    const findings = certificateFindings({
      certs: served,
      certbot: certbot({ certs: [lineage({ authenticator: "webroot", installer: undefined })] }),
    })
    expect(findings).toHaveLength(1)
    expect(findings[0]).toMatchObject({
      id: "certbot.no-reload",
      level: "warning",
      detail:
        "certbot renews betbots.site without reloading nginx, so betbots keeps serving the old certificate until it expires.",
    })
  })

  test("nothing when certbot's nginx plugin or the hook reloads it", () => {
    expect(certificateFindings({ certs: served, certbot: certbot() })).toEqual([])
    expect(
      certificateFindings({
        certs: served,
        certbot: certbot({
          certs: [lineage({ installer: undefined })],
          reloadHook: { path: "x", state: "installed", others: [] },
        }),
      }),
    ).toEqual([])
  })
})
