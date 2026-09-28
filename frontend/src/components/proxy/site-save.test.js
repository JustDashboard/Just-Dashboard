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

  test("a kind's own switch goes only with that kind", () => {
    // Left on by a preset or a click under another kind, where it is not drawn.
    expect(sendableSpec(spec({ kind: "proxy", spa: true }))).not.toHaveProperty("spa")
    expect(sendableSpec(spec({ kind: "redirect", spa: true }))).not.toHaveProperty("spa")
    expect(sendableSpec(spec({ kind: "static", spa: true })).spa).toBe(true)
    expect(sendableSpec(spec({ kind: "static", spa: false })).spa).toBe(false)
    expect(sendableSpec(spec({ kind: "proxy", permanent: true }))).not.toHaveProperty("permanent")
    expect(sendableSpec(spec({ kind: "static", permanent: true }))).not.toHaveProperty("permanent")
    expect(sendableSpec(spec({ kind: "redirect", permanent: true })).permanent).toBe(true)
    expect(sendableSpec(spec({ kind: "static" }))).not.toHaveProperty("spa")
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

  test("a disabled site is enabled only when the operator says so", () => {
    expect(saveRequest(spec(), { existing: true, reload: false })).toMatchObject({
      enable: "keep",
      reload: false,
    })
    expect(saveRequest(spec(), { existing: true, reload: true, enable: true })).toMatchObject({
      enable: "enable",
      overwrite: true,
      reload: true,
    })
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
    const reloadError =
      'nginx: [error] open() "/run/nginx.pid" failed (2: No such file or directory)'
    const outcome = saveOutcome(result({ reloaded: false, reloadError }), { existing: true })
    expect(outcome).toEqual({
      tone: "warning",
      title: "app.example.com saved and tested; reload failed",
      description: `nginx did not pick it up; start or reload nginx to apply it. ${reloadError}`,
    })
    // The usual cause is an nginx that is not running, which serves nothing:
    // the toast must not say what nginx is serving.
    expect(outcome.description).not.toMatch(/serving|served/)
  })

  test("a disabled site tested as enabled says so, and that it stays disabled", () => {
    const tested = { enabled: false, reloaded: false, testedAsEnabled: true }
    expect(
      saveOutcome(result({ ...tested, validation: { valid: true, output: "", command: "" } }), {
        existing: true,
      }),
    ).toEqual({
      tone: "success",
      title: "app.example.com saved",
      description: "nginx tested it as if enabled. It stays disabled until it is enabled.",
    })
  })

  test("a disabled site that would fail its test enabled says where", () => {
    const failing = (diagnostics, output = "") =>
      saveOutcome(
        result({
          enabled: false,
          reloaded: false,
          testedAsEnabled: true,
          validation: { valid: false, output, command: "nginx -t", diagnostics },
        }),
        { existing: true },
      )
    const own = failing([
      { level: "warn", message: "protocol options redefined", line: 2 },
      {
        level: "emerg",
        message: 'unknown directive "frobnicate"',
        file: "/etc/nginx/sites-available/app.example.com",
        line: 14,
      },
    ])
    expect(own).toEqual({
      tone: "warning",
      title: "app.example.com saved; enabling it would fail nginx's test",
      description:
        'Line 14: unknown directive "frobnicate". It stays disabled until it is enabled.',
    })
    // A broken neighbour fails the same test: its file is named, not "line 3".
    expect(
      failing([
        {
          level: "emerg",
          message: 'cannot load certificate "/x.pem"',
          file: "/etc/nginx/sites-available/other",
          line: 3,
        },
      ]).description,
    ).toBe(
      '/etc/nginx/sites-available/other:3: cannot load certificate "/x.pem". It stays disabled until it is enabled.',
    )
    expect(
      failing(
        [],
        "nginx: something odd\nnginx: configuration file /etc/nginx/nginx.conf test failed",
      ).description,
    ).toBe(
      "nginx: configuration file /etc/nginx/nginx.conf test failed. It stays disabled until it is enabled.",
    )
  })

  test("a disabled site's name conflicts and warnings are what enabling it would meet", () => {
    const tested = {
      enabled: false,
      reloaded: false,
      testedAsEnabled: true,
      validation: { valid: true, output: "", command: "nginx -t" },
    }
    const conflict = (effect) => [
      { domain: "app.example.com", listen: "0.0.0.0:80", site: "legacy", effect },
    ]
    expect(
      saveOutcome(result({ ...tested, conflicts: conflict("takes") }), { existing: true }),
    ).toEqual({
      tone: "warning",
      title: "app.example.com saved with a name conflict once enabled",
      description:
        "Enabled, it would take app.example.com on 0.0.0.0:80 from legacy. It stays disabled until it is enabled.",
    })
    expect(
      saveOutcome(result({ ...tested, conflicts: conflict("ignored") }), { existing: true })
        .description,
    ).toBe(
      "Enabled, its claim to app.example.com on 0.0.0.0:80 would be ignored: nginx answers it from legacy. It stays disabled until it is enabled.",
    )
    expect(
      saveOutcome(result({ ...tested, testWarnings: [{ level: "warn", message: "x", line: 4 }] }), {
        existing: true,
      }),
    ).toEqual({
      tone: "warning",
      title: "app.example.com saved with a warning",
      description: "nginx warns: Line 4: x. It stays disabled until it is enabled.",
    })
  })

  test("a disabled site nginx could not test says why", () => {
    const note =
      "sites-enabled/app.example.com already enables /etc/nginx/sites-available/app.conf, so nginx could not test this site as enabled."
    expect(
      saveOutcome(
        result({
          enabled: false,
          reloaded: false,
          validation: { valid: true, output: "", command: "nginx -t", note },
        }),
        { existing: true },
      ),
    ).toEqual({
      tone: "warning",
      title: "app.example.com saved",
      description: `${note} It stays disabled.`,
    })
  })

  test("a conflict saved anyway and the test's warnings are warnings", () => {
    const conflicts = [
      { domain: "app.example.com", listen: "0.0.0.0:80", site: "legacy", effect: "takes" },
    ]
    expect(saveOutcome(result({ conflicts }), { existing: false })).toEqual({
      tone: "warning",
      title: "app.example.com is live with a name conflict",
      description:
        "nginx now answers app.example.com on 0.0.0.0:80 from app.example.com, not legacy.",
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
    // Both at once: the warnings are not dropped behind the conflict.
    expect(saveOutcome(result({ conflicts, testWarnings }), { existing: false }).description).toBe(
      "nginx now answers app.example.com on 0.0.0.0:80 from app.example.com, not legacy. nginx also warns: Line 12: protocol options redefined; x.",
    )
  })

  test("saved without a reload says what nginx is serving", () => {
    expect(saveOutcome(result({ reloaded: false }), { existing: true }).description).toBe(
      "nginx serves the previous version until it reloads.",
    )
    expect(saveOutcome(result({ reloaded: false }), { existing: false }).description).toContain(
      "on disk but not serving",
    )
  })

  test("a conflict says which site nginx answers the name from", () => {
    const conflict = (effect) => [{ domain: "v.test", listen: "0.0.0.0:80", site: "va", effect }]
    const live = { name: "a0", reloaded: true }
    expect(conflictsText(conflict("takes"), live)).toBe(
      "nginx now answers v.test on 0.0.0.0:80 from a0, not va.",
    )
    expect(conflictsText(conflict("takes"), { name: "a0", reloaded: false })).toBe(
      "After a reload nginx answers v.test on 0.0.0.0:80 from a0, not va.",
    )
    expect(conflictsText(conflict("ignored"), live)).toBe(
      "nginx answers v.test on 0.0.0.0:80 from va, not a0.",
    )
    expect(conflictsText(conflict("keeps"), live)).toBe(
      "nginx goes on answering v.test on 0.0.0.0:80 from a0 and ignores va's claim.",
    )
    // Never "is also served by" the other site, which was false whenever the
    // site saved anyway sorted first.
    for (const effect of ["takes", "ignored", "keeps", undefined]) {
      expect(conflictsText(conflict(effect), live)).not.toContain("served by")
    }
  })

  test("a conflict whose order is unknown says only that both claim it", () => {
    expect(
      conflictsText([{ domain: "a.example.com", listen: "[::]:443" }], {
        name: "app",
        reloaded: true,
      }),
    ).toBe(
      "a.example.com on [::]:443 is also claimed by another server block; nginx answers it from only one of the two.",
    )
  })
})
