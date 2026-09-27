import { describe, expect, test } from "bun:test"
import { conflictsText, saveOutcome, saveRequest, sendableSpec } from "./site-save"

const spec = (overrides) => ({
  name: "app.example.com",
  domains: ["app.example.com"],
  kind: "proxy",
  upstream: "http://127.0.0.1:3000",
  tls: false,
  forceHttps: true,
  hsts: true,
  http2: true,
  webSockets: true,
  gzip: true,
  blockExploits: true,
  securityHeaders: true,
  allowFrom: [],
  denyFrom: [],
  accessLog: true,
  locations: [],
  ...overrides,
})

const result = (overrides) => ({
  name: "app.example.com",
  path: "/etc/nginx/sites-available/app.example.com",
  content: "",
  warnings: [],
  enabled: true,
  reloaded: true,
  ...overrides,
})

describe("what a save sends", () => {
  test("HSTS goes only with TLS", () => {
    expect(sendableSpec(spec({ tls: false, hsts: true })).hsts).toBe(false)
    expect(sendableSpec(spec({ tls: true, hsts: true })).hsts).toBe(true)
    expect(sendableSpec(spec({ tls: true, hsts: false })).hsts).toBe(false)
  })

  test("an existing site keeps its link and a new one is enabled", () => {
    expect(saveRequest(spec(), { existing: true, reload: true })).toMatchObject({
      enable: "keep",
      overwrite: true,
      reload: true,
    })
    const created = saveRequest(spec(), { existing: false, reload: false })
    expect(created).toMatchObject({ enable: "enable", overwrite: false, reload: false })
    expect(created).not.toHaveProperty("allowConflict")
    expect(
      saveRequest(spec(), { existing: false, reload: true, allowConflict: true }),
    ).toMatchObject({ allowConflict: true })
  })
})

describe("what a save says", () => {
  test("live only when nginx reloaded an enabled site with nothing to say", () => {
    expect(saveOutcome(result(), { existing: false })).toEqual({
      tone: "success",
      title: "app.example.com is live",
    })
  })

  test("a failed reload after a clean test is its own state", () => {
    const outcome = saveOutcome(result({ reloaded: false, reloadError: "invalid PID number" }), {
      existing: true,
    })
    expect(outcome.tone).toBe("warning")
    expect(outcome.title).toBe("app.example.com saved and tested; reload failed")
    expect(outcome.description).toContain("invalid PID number")
  })

  test("a disabled site says it stays disabled", () => {
    const outcome = saveOutcome(result({ enabled: false }), { existing: true })
    expect(outcome.title).toBe("app.example.com saved")
    expect(outcome.description).toContain("stays that way")
  })

  test("a conflict saved anyway and the test's warnings are warnings", () => {
    const conflicts = [{ domain: "app.example.com", listen: "0.0.0.0:80", site: "legacy" }]
    expect(saveOutcome(result({ conflicts }), { existing: false })).toEqual({
      tone: "warning",
      title: "app.example.com is live with a name conflict",
      description:
        "app.example.com is also served by legacy on 0.0.0.0:80. nginx answers each name from one of them.",
    })
    const testWarnings = [
      { level: "warn", message: "protocol options redefined", line: 12 },
      { level: "warn", message: "x" },
    ]
    expect(saveOutcome(result({ reloaded: false, testWarnings }), { existing: true })).toEqual({
      tone: "warning",
      title: "app.example.com saved with 2 warnings",
      description: "Line 12: protocol options redefined; x",
    })
  })

  test("saved without a reload says what nginx is serving", () => {
    expect(saveOutcome(result({ reloaded: false }), { existing: true }).description).toBe(
      "nginx serves the previous version until it reloads.",
    )
    expect(saveOutcome(result({ reloaded: false }), { existing: false }).description).toContain(
      "on disk but not serving",
    )
  })

  test("a conflict with no named owner says another server block", () => {
    expect(conflictsText([{ domain: "a.example.com", listen: "[::]:443" }])).toBe(
      "a.example.com is also served by another server block on [::]:443. nginx answers each name from one of them.",
    )
  })
})
