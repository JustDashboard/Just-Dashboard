import { describe, expect, test } from "bun:test"
import { readdirSync, readFileSync } from "node:fs"
import path from "node:path"
import { applyPreset, BLANK, linkedSite, presetById, PRESETS } from "./site-presets"

// The backend renders each of these through a real `nginx -t`
// (TestLivePresetsPassNginxTest), from its own copy.
const TESTDATA = path.resolve(
  import.meta.dir,
  "../../../../backend/internal/proxysvc/testdata/presets",
)

describe("site presets", () => {
  test("every preset is the one the backend tests, and nothing else is", () => {
    const files = readdirSync(TESTDATA).filter((file) => file.endsWith(".json"))
    expect(files.map((file) => file.replace(/\.json$/, "")).sort()).toEqual(
      PRESETS.map((p) => p.id).sort(),
    )
    for (const preset of PRESETS) {
      const tested = JSON.parse(readFileSync(path.join(TESTDATA, `${preset.id}.json`), "utf8"))
      expect({ id: preset.id, spec: tested }).toEqual({ id: preset.id, spec: preset.spec })
    }
  })

  test("a preset sets what it is about and keeps what the operator chose", () => {
    const site = {
      ...BLANK,
      name: "cloud.example.com",
      domains: ["cloud.example.com"],
      tls: true,
      certPath: "/etc/ssl/cloud.crt",
      keyPath: "/etc/ssl/cloud.key",
      allowFrom: ["10.0.0.0/8"],
      basicAuthFile: "/etc/nginx/jd-auth/team",
      locations: [{ path: "/api", upstream: "http://127.0.0.1:4000", webSockets: false }],
    }
    const next = applyPreset(site, presetById("home-assistant"))
    expect(next).toEqual({
      ...site,
      upstream: "http://127.0.0.1:8123",
      webSockets: true,
      proxyTimeout: 300,
      permanent: false,
      spa: false,
      custom: undefined,
    })
  })

  test("a preset left out of a field puts the default back", () => {
    const registry = applyPreset(BLANK, presetById("registry"))
    expect(registry).toMatchObject({ clientMaxBody: "0", proxyTimeout: 900, webSockets: false })
    const grafana = applyPreset(registry, presetById("grafana"))
    expect(grafana).toMatchObject({
      upstream: "http://127.0.0.1:3000",
      clientMaxBody: "50m",
      proxyTimeout: 300,
      webSockets: true,
      custom: undefined,
    })
    const spa = applyPreset(grafana, presetById("spa"))
    expect(spa).toMatchObject({ kind: "static", spa: true })
    expect(applyPreset(spa, presetById("node"))).toMatchObject({ kind: "proxy", spa: false })
    expect(applyPreset(spa, presetById("redirect"))).toMatchObject({
      kind: "redirect",
      permanent: true,
      spa: false,
    })
  })

  test("an upstream the operator gave survives a preset; a default one does not", () => {
    const typed = { ...BLANK, upstream: "http://127.0.0.1:4100" }
    expect(applyPreset(typed, presetById("jellyfin")).upstream).toBe("http://127.0.0.1:4100")
    const jellyfin = applyPreset(BLANK, presetById("jellyfin"))
    expect(jellyfin.upstream).toBe("http://127.0.0.1:8096")
    expect(applyPreset(jellyfin, presetById("minio")).upstream).toBe("http://127.0.0.1:9000")
  })

  test("a preset's own lines are swapped in the extra configuration, the operator's stay", () => {
    const mine = "location /metrics {\n    deny all;\n}"
    const registry = applyPreset({ ...BLANK, custom: mine }, presetById("registry"))
    expect(registry.custom).toBe(`${mine}\n${presetById("registry").spec.custom}`)
    const minio = applyPreset(registry, presetById("minio"))
    expect(minio.custom).toBe(`${mine}\n${presetById("minio").spec.custom}`)
    expect(applyPreset(minio, presetById("node")).custom).toBe(mine)
    expect(applyPreset(applyPreset(BLANK, presetById("minio")), presetById("node")).custom).toBe(
      undefined,
    )
  })

  test("every preset says where it listens and has a logo or a kind to draw", () => {
    for (const preset of PRESETS) {
      expect(preset.label.length).toBeGreaterThan(0)
      expect(preset.detail.length).toBeGreaterThan(0)
      expect(["proxy", "static", "redirect"]).toContain(preset.spec.kind)
    }
  })
})

describe("a link into a new site", () => {
  test("it opens on the upstream and domains it names, with the name following the domain", () => {
    const params = new URLSearchParams(
      "new=1&upstream=http%3A%2F%2F127.0.0.1%3A8081&domain=shop.example.com%20www.shop.example.com",
    )
    expect(linkedSite(params)).toEqual({
      domains: "shop.example.com www.shop.example.com",
      spec: {
        ...BLANK,
        upstream: "http://127.0.0.1:8081",
        domains: ["shop.example.com", "www.shop.example.com"],
        name: "shop.example.com",
        certPath: "/etc/letsencrypt/live/shop.example.com/fullchain.pem",
        keyPath: "/etc/letsencrypt/live/shop.example.com/privkey.pem",
      },
    })
  })

  test("either half may be missing", () => {
    expect(linkedSite(new URLSearchParams("new=1&upstream=http://127.0.0.1:9000"))?.spec).toEqual({
      ...BLANK,
      upstream: "http://127.0.0.1:9000",
      domains: [],
      name: "",
      certPath: undefined,
      keyPath: undefined,
    })
    const named = linkedSite(new URLSearchParams("new=1&domain=*.example.com"))
    expect(named?.spec).toMatchObject({ upstream: BLANK.upstream, name: "example.com" })
  })

  test("what the link spells wrong goes in as spelled, for the form to say so", () => {
    const odd = linkedSite(new URLSearchParams("new=1&upstream=ftp://x&domain=bad_domain"))
    expect(odd?.spec).toMatchObject({ upstream: "ftp://x", domains: ["bad_domain"] })
  })

  test("nothing without new=1", () => {
    expect(linkedSite(new URLSearchParams("upstream=http://127.0.0.1:3000"))).toBeNull()
    expect(linkedSite(new URLSearchParams("new=true"))).toBeNull()
  })
})
