import { describe, expect, test } from "bun:test"
import { changedFields, changedViews } from "./site-sections"

const spec = (overrides) => ({
  name: "app.example.com",
  domains: ["app.example.com"],
  kind: "proxy",
  upstream: "http://127.0.0.1:3000",
  tls: true,
  certPath: "/etc/ssl/app.pem",
  keyPath: "/etc/ssl/app.key",
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

describe("the parts of a site's Settings that hold an unsaved change", () => {
  test("a draft that is what it started from holds none", () => {
    expect(changedViews(spec(), spec())).toEqual([])
    expect(changedFields(spec(), spec())).toEqual([])
  })

  test("a switch set to false where the file never mentioned it is not a change", () => {
    expect(changedViews(spec({ buffering: false, custom: "", indexFiles: [] }), spec())).toEqual([])
  })

  test("each field is counted in the part it is edited in", () => {
    expect(changedViews(spec({ upstream: "http://127.0.0.1:4000" }), spec())).toEqual(["general"])
    expect(changedViews(spec({ http2: false }), spec())).toEqual(["https"])
    expect(changedViews(spec({ allowFrom: ["10.0.0.0/8"] }), spec())).toEqual(["access"])
    expect(changedViews(spec({ accessLog: false }), spec())).toEqual(["hardening"])
    expect(changedViews(spec({ headers: { frameOptions: "deny" } }), spec())).toEqual(["headers"])
    expect(changedViews(spec({ custom: "client_body_timeout 30s;" }), spec())).toEqual(["paths"])
  })

  test("several parts come back in the order they are read, each once", () => {
    const draft = spec({
      custom: "etag off;",
      domains: ["app.example.com", "www.app.example.com"],
      tls: false,
      gzip: false,
      upstream: "http://127.0.0.1:4000",
    })
    expect(changedViews(draft, spec())).toEqual(["general", "https", "hardening", "paths"])
  })

  test("a field drawn only under another setting is not a change while that setting is off", () => {
    // HSTS and its options are sent only with TLS, so on a plain-HTTP site
    // they are not something a save would write.
    const plain = spec({ tls: false, hsts: false })
    expect(changedViews({ ...plain, hstsMaxAge: 300, hstsPreload: true }, plain)).toEqual([])
  })

  test("a field with no part of its own is General's", () => {
    expect(changedViews({ ...spec(), somethingNew: "x" }, spec())).toEqual(["general"])
  })
})
