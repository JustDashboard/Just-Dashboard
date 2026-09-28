import { describe, expect, test } from "bun:test"
import {
  afterReload,
  authorityName,
  certbotRunning,
  expiredAgo,
  hookFailureText,
  lineageActivity,
  parseDomains,
  renewalMethod,
  renewalReading,
  renewalReload,
  replacingTestCertificate,
  runPhrase,
  runTime,
  sameNames,
  sinceDay,
  standingFailures,
  stillServingTest,
  testCertificateReplaced,
  testRunPassed,
  unreloadedRenewals,
} from "./certificates"

const now = Date.parse("2026-09-27T12:00:00Z")
const job = (overrides) => ({
  id: "j1",
  kind: "certbot.renew",
  title: "Renewing app.example.com",
  target: "app.example.com",
  status: "running",
  exitCode: 0,
  startedAt: "2026-09-27T11:59:00Z",
  lines: 0,
  ...overrides,
})

describe("how long ago a certificate expired", () => {
  test("hours ago is today, not 0 days ago", () => {
    expect(expiredAgo("2026-09-27T09:00:00Z", now)).toBe("today")
    expect(expiredAgo("2026-09-27T11:59:59Z", now)).toBe("today")
  })

  test("a day or more is counted in whole days", () => {
    expect(expiredAgo("2026-09-26T11:00:00Z", now)).toBe("yesterday")
    expect(expiredAgo("2026-09-24T12:00:00Z", now)).toBe("3 days ago")
  })
})

describe("a certbot run in the console", () => {
  test("only a running certbot job holds certbot", () => {
    expect(certbotRunning(job())).toBe(true)
    expect(certbotRunning(job({ status: "succeeded" }))).toBe(false)
    expect(certbotRunning(job({ kind: "packages.install" }))).toBe(false)
    expect(certbotRunning(null)).toBe(false)
  })

  const app = { name: "app.example.com", domains: ["app.example.com", "www.app.example.com"] }
  const shop = { name: "shop.example.com", domains: ["shop.example.com"] }

  test("the lineage a job acts on says what is happening to it", () => {
    expect(lineageActivity(job(), app)).toBe("Renewing…")
    expect(lineageActivity(job({ title: "Dry run: renewing app.example.com" }), app)).toBe(
      "Testing renewal…",
    )
    expect(lineageActivity(job({ kind: "certbot.revoke" }), app)).toBe("Revoking…")
    // Another lineage, a renew-all, or a finished job says nothing.
    expect(lineageActivity(job(), shop)).toBeUndefined()
    expect(lineageActivity(job({ target: "every certificate due" }), app)).toBeUndefined()
    expect(lineageActivity(job({ status: "failed" }), app)).toBeUndefined()
  })

  // The issuance names the certificate by its names, not the lineage's.
  const replacement = (overrides) =>
    job({
      kind: "certbot.issue",
      title: "Replacing the test certificate for www.app.example.com, app.example.com",
      target: "www.app.example.com, app.example.com",
      ...overrides,
    })

  test("a lineage whose test certificate is being replaced says so", () => {
    expect(lineageActivity(replacement(), app)).toBe("Replacing…")
    expect(
      replacingTestCertificate(replacement(), ["APP.example.com", "www.app.example.com"]),
    ).toBe(true)
    // A subset of its names is another certificate to certbot.
    expect(replacingTestCertificate(replacement(), ["app.example.com"])).toBe(false)
    expect(lineageActivity(replacement(), shop)).toBeUndefined()
    // A plain issuance for the same names replaces nothing, and a finished one is done.
    expect(
      lineageActivity(
        replacement({ title: "Issuing a certificate for www.app.example.com, app.example.com" }),
        app,
      ),
    ).toBeUndefined()
    expect(lineageActivity(replacement({ status: "succeeded" }), app)).toBeUndefined()
  })
})

describe("what a finished issuance means for the page", () => {
  test("a test run that passed offers the real issuance for its names", () => {
    const passed = job({
      kind: "certbot.issue",
      title: "Test issuance for app.example.com, www.app.example.com",
      target: "app.example.com, www.app.example.com",
      status: "succeeded",
    })
    expect(testRunPassed(passed)).toBe("app.example.com, www.app.example.com")
    expect(parseDomains(testRunPassed(passed))).toEqual(["app.example.com", "www.app.example.com"])
    expect(testRunPassed({ ...passed, status: "failed" })).toBeUndefined()
    expect(testRunPassed({ ...passed, title: "Issuing a certificate for app.example.com" })).toBe(
      undefined,
    )
    expect(testRunPassed(null)).toBeUndefined()
  })

  test("a replaced test certificate is named, so the page can find the sites still serving it", () => {
    const replaced = job({
      kind: "certbot.issue",
      title: "Replacing the test certificate for app.example.com, www.app.example.com",
      target: "app.example.com, www.app.example.com",
      status: "succeeded",
    })
    expect(testCertificateReplaced(replaced)).toEqual(["app.example.com", "www.app.example.com"])
    expect(testCertificateReplaced({ ...replaced, status: "running" })).toBeUndefined()
    expect(testCertificateReplaced({ ...replaced, status: "failed" })).toBeUndefined()
    expect(
      testCertificateReplaced({ ...replaced, title: "Issuing a certificate for app.example.com" }),
    ).toBeUndefined()
  })

  test("only a site that answered with a test certificate is still serving one", () => {
    // What each site that names the certificate answered over a handshake:
    // a deploy hook may have reloaded nginx already, and a site that could
    // not be asked, or serves something else entirely, is claimed neither way.
    const served = [
      { site: "stale", name: "app.example.com", current: false, staging: true },
      { site: "reloaded", name: "app.example.com", current: true },
      { site: "down", name: "app.example.com", current: false, error: "connection refused" },
      { site: "elsewhere", name: "app.example.com", current: false, issuer: "Company CA" },
    ]
    expect(stillServingTest(served)).toEqual(["stale"])
    expect(stillServingTest(served.slice(1))).toEqual([])
    expect(stillServingTest(undefined)).toEqual([])
  })

  test("a reload is reported by what the sites answered afterwards", () => {
    const current = (site) => ({ site, name: "app.example.com", current: true })
    const test = (site) => ({ site, name: "app.example.com", current: false, staging: true })
    expect(afterReload(["app", "www"], [current("app"), current("www")])).toEqual({
      ok: true,
      description: "app, www serve the real certificate now.",
    })
    expect(afterReload(["app", "www"], [current("app"), test("www")])).toEqual({
      ok: false,
      description: "app serves the real certificate now. www still serves the test certificate.",
    })
    // A site that no longer answers, or is no longer asked, is not claimed.
    expect(
      afterReload(
        ["app", "www"],
        [{ site: "app", name: "app.example.com", current: false, error: "refused" }],
      ),
    ).toEqual({ ok: false, description: "What app, www serve could not be checked." })
  })

  test("names are the same set whatever their order or case", () => {
    expect(sameNames(["a.example.com", "B.example.com"], ["b.example.com", "a.example.com"])).toBe(
      true,
    )
    expect(sameNames(["a.example.com"], ["a.example.com", "b.example.com"])).toBe(false)
    expect(sameNames([], [])).toBe(true)
  })
})

describe("a configured ACME authority", () => {
  test("is named by its host", () => {
    expect(authorityName("https://ca.internal:9000/acme/acme/directory")).toBe("ca.internal:9000")
    expect(authorityName("https://acme.zerossl.com/v2/DV90")).toBe("acme.zerossl.com")
  })

  test("a value that is not a URL is shown as it is", () => {
    expect(authorityName("not a url")).toBe("not a url")
  })
})

describe("the names typed into the issue form", () => {
  test("any mix of spaces, commas and new lines separates them", () => {
    expect(parseDomains(" app.example.com,www.app.example.com\n*.example.com  ")).toEqual([
      "app.example.com",
      "www.app.example.com",
      "*.example.com",
    ])
    expect(parseDomains("   ")).toEqual([])
  })
})

// Local times, so the readings are checked in whatever zone the test runs.
const local = (month, day, hour, minute) =>
  new Date(2026, month - 1, day, hour, minute).toISOString()
const localNow = new Date(2026, 8, 28, 2, 30).getTime()

describe("when a renewal run was or will be", () => {
  test("the hour today, the day named around it, the date beyond the week", () => {
    expect(runTime(local(9, 28, 1, 5), localNow)).toBe("01:05")
    expect(runTime(local(9, 27, 21, 13), localNow)).toBe("yesterday 21:13")
    expect(runTime(local(9, 29, 9, 12), localNow)).toBe("tomorrow 09:12")
    expect(runTime(local(9, 24, 9, 0), localNow)).toBe("Thu 09:00")
    expect(runTime(local(7, 2, 7, 20), localNow)).toBe("2 Jul 07:20")
  })

  test("in a sentence, and where a streak began", () => {
    expect(runPhrase(local(9, 28, 1, 5), localNow)).toBe("at 01:05")
    expect(runPhrase(local(9, 27, 21, 13), localNow)).toBe("yesterday at 21:13")
    expect(runPhrase(local(9, 24, 9, 0), localNow)).toBe("on Thu at 09:00")
    expect(runPhrase(local(7, 2, 7, 20), localNow)).toBe("on 2 Jul at 07:20")
    expect(sinceDay(local(9, 28, 1, 5), localNow)).toBe("01:05")
    expect(sinceDay(local(9, 27, 21, 13), localNow)).toBe("yesterday")
    expect(sinceDay(local(7, 2, 7, 20), localNow)).toBe("2 Jul")
  })
})

const certbotState = (overrides = {}) => ({
  available: true,
  certs: [],
  autoRenew: true,
  renewSource: "certbot.timer",
  ...overrides,
})
const health = (overrides = {}) => ({
  source: "certbot.service",
  service: "certbot.service",
  state: "ok",
  lastRun: local(9, 27, 21, 13),
  nextRun: local(9, 28, 9, 12),
  failures: [],
  ...overrides,
})

describe("the Renewal tile", () => {
  test("an active timer whose runs fail reads failing, not scheduled", () => {
    const failing = health({
      state: "failed",
      exitStatus: 1,
      failures: [{ lineage: "betbots.site", reason: "Some challenges have failed." }],
    })
    expect(renewalReading(certbotState({ health: failing }), false, localNow)).toEqual({
      value: "Failing",
      hint: "last run yesterday 21:13 · 1 failed",
      tone: "danger",
    })
    const lock = health({
      state: "failed",
      reason: "Another instance of Certbot is already running.",
    })
    expect(renewalReading(certbotState({ health: lock }), false, localNow).hint).toBe(
      "last run yesterday 21:13 failed",
    )
  })

  test("a failure renewed since is recovered, and a passing run is healthy until the next", () => {
    const recovered = health({
      state: "recovered",
      failures: [{ lineage: "betbots.site", reason: "x", renewedSince: true }],
    })
    expect(renewalReading(certbotState({ health: recovered }), false, localNow)).toEqual({
      value: "Recovered",
      hint: "renewed since the yesterday 21:13 failure",
      tone: "default",
    })
    expect(renewalReading(certbotState({ health: health() }), false, localNow)).toEqual({
      value: "Healthy",
      hint: "next run 09:12",
      tone: "success",
    })
    // A cron host has no next run to name.
    const cron = health({
      source: "/var/log/letsencrypt/letsencrypt.log",
      service: undefined,
      nextRun: undefined,
    })
    expect(
      renewalReading(
        certbotState({ renewSource: "/etc/cron.d/certbot", health: cron }),
        false,
        localNow,
      ).hint,
    ).toBe("last run yesterday 21:13")
  })

  test("running, never run, unknown, off and absent each say so", () => {
    expect(
      renewalReading(certbotState({ health: health({ state: "running" }) }), false, localNow),
    ).toEqual({
      value: "Running",
      hint: "certbot.service is renewing now",
      tone: "default",
    })
    expect(
      renewalReading(
        certbotState({ health: health({ state: "never", lastRun: undefined }) }),
        false,
        localNow,
      ).hint,
    ).toBe("first run 09:12")
    expect(
      renewalReading(certbotState({ health: health({ state: "unknown" }) }), false, localNow),
    ).toEqual({
      value: "Scheduled",
      hint: "via certbot.timer",
      tone: "default",
    })
    expect(
      renewalReading(
        certbotState({ autoRenew: false, renewUnit: "certbot.timer", certs: [{}] }),
        false,
      ),
    ).toEqual({ value: "Off", hint: "certbot.timer is off", tone: "danger" })
    expect(renewalReading(undefined, true).value).toBe("No certbot")
  })

  test("a run that passed while a hook failed reads the hook, not healthy", () => {
    const hooked = health({
      hookFailures: [
        {
          kind: "deploy-hook",
          command: "/etc/letsencrypt/renewal-hooks/deploy/50-just-dashboard-reload-nginx",
          code: 1,
          output: "nginx -t failed, so nginx was not reloaded for /etc/letsencrypt/live/x.test",
        },
      ],
    })
    expect(renewalReading(certbotState({ health: hooked }), false, localNow)).toEqual({
      value: "Hook failed",
      hint: "last run yesterday 21:13",
      tone: "warning",
    })
    expect(hookFailureText(hooked.hookFailures[0])).toBe(
      "deploy-hook 50-just-dashboard-reload-nginx exited 1: nginx -t failed, so nginx was not reloaded for /etc/letsencrypt/live/x.test",
    )
    expect(hookFailureText({ kind: "post-hook", command: "systemctl reload nginx", code: 1 })).toBe(
      "post-hook systemctl reload nginx exited 1.",
    )
    expect(hookFailureText({ kind: "deploy-hook", code: 2 })).toBe("deploy-hook exited 2.")
  })

  test("only the failures nothing has renewed since still stand", () => {
    expect(
      standingFailures(
        health({
          failures: [
            { lineage: "a", reason: "x" },
            { lineage: "b", reason: "y", renewedSince: true },
          ],
        }),
      ).map((f) => f.lineage),
    ).toEqual(["a"])
  })
})

describe("how a lineage renews", () => {
  test("a webroot names its folders, DNS its provider, the rest their plugin", () => {
    expect(
      renewalMethod({ authenticator: "webroot", webroots: ["/var/www/app", "/srv/static"] }),
    ).toEqual({
      method: "webroot",
      folders: ["/var/www/app", "/srv/static"],
    })
    expect(renewalMethod({ authenticator: "dns-cloudflare", dnsProvider: "Cloudflare" })).toEqual({
      method: "DNS",
      detail: "Cloudflare",
    })
    expect(renewalMethod({ authenticator: "dns-hetzner" })).toEqual({
      method: "DNS",
      detail: "hetzner",
    })
    expect(renewalMethod({ authenticator: "nginx" })).toEqual({ method: "nginx" })
    expect(renewalMethod({})).toBeUndefined()
  })
})

describe("whether nginx reads a renewed certificate", () => {
  const hook = (state, others = []) => ({
    path: "/etc/letsencrypt/renewal-hooks/deploy/50-just-dashboard-reload-nginx",
    state,
    others,
  })

  test("certbot's nginx plugin reloads what it installed, the hook reloads everything", () => {
    expect(
      renewalReload({ installer: "nginx" }, { nginxReloads: true, reloadHook: hook("missing") }),
    ).toBe("certbot")
    // The plugin gone from this certbot renews without deploying.
    expect(
      renewalReload({ installer: "nginx" }, { nginxReloads: false, reloadHook: hook("missing") }),
    ).toBe("none")
    expect(renewalReload({}, { reloadHook: hook("installed") })).toBe("hook")
  })

  test("hooks whose work is not read claim nothing either way", () => {
    expect(renewalReload({ deployHook: true }, { reloadHook: hook("missing") })).toBe("unknown")
    // `certbot --post-hook "systemctl reload nginx"`, the usual way.
    expect(renewalReload({ postHook: true }, { reloadHook: hook("missing") })).toBe("unknown")
    expect(renewalReload({}, { reloadHook: hook("missing", ["reload-haproxy"]) })).toBe("unknown")
    expect(renewalReload({}, { reloadHook: hook("missing", ["post/reload-nginx"]) })).toBe(
      "unknown",
    )
    expect(renewalReload({}, { reloadHook: hook("missing", ["cli.ini's post-hook"]) })).toBe(
      "unknown",
    )
    expect(renewalReload({}, { reloadHook: hook("modified") })).toBe("unknown")
  })

  test("the served lineages nothing reloads for, with the sites left on the old certificate", () => {
    const lineage = (name, overrides = {}) => ({
      name,
      domains: [name],
      expiry: local(12, 1, 0, 0),
      daysLeft: 60,
      valid: true,
      certPath: `/etc/letsencrypt/live/${name}/fullchain.pem`,
      authenticator: "webroot",
      ...overrides,
    })
    // servedBy is the enabled sites only: a disabled one that names
    // idle.example.com serves nothing, so it is not here.
    const state = certbotState({
      reloadHook: hook("missing"),
      nginxReloads: true,
      certs: [
        lineage("app.example.com", { servedBy: ["app", "www"] }),
        lineage("idle.example.com"),
        lineage("nginx.example.com", { installer: "nginx", servedBy: ["n"] }),
        lineage("post.example.com", { postHook: true, servedBy: ["post"] }),
      ],
    })
    expect(unreloadedRenewals(state)).toEqual({
      lineages: ["app.example.com"],
      sites: ["app", "www"],
    })
    expect(unreloadedRenewals({ ...state, reloadHook: hook("installed") }).lineages).toEqual([])
    expect(
      unreloadedRenewals({ ...state, reloadHook: hook("missing", ["post/reload-nginx"]) }).lineages,
    ).toEqual([])
    expect(unreloadedRenewals({ ...state, autoRenew: false }).lineages).toEqual([])
  })
})
