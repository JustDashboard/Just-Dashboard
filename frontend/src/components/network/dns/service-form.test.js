import { describe, expect, test } from "bun:test"
import {
  dnsConnectionDraft,
  dnsDraftLines,
  dnsStageProblem,
  prepareDNSConnection,
} from "./service-form"

const connection = {
  id: "native-fixture",
  name: "Private DNS",
  engine: "adguard",
  endpoint: "https://192.0.2.10:8443",
  management: false,
  customCA: true,
  hasCredential: true,
  generation: 2,
  ownership: "connected",
  serverName: "dns.example.test",
  createdAt: "2026-10-09T00:00:00Z",
  updatedAt: "2026-10-09T00:00:00Z",
}
const completeDraft = () => ({
  ...dnsConnectionDraft(),
  name: "Private DNS",
  endpoint: "http://127.0.0.1:8080",
  username: "native-user",
  password: "request-only-secret",
})

describe("native DNS connection draft", () => {
  test("new connections default read-only and sealed fields are never prefilled", () => {
    expect(dnsConnectionDraft().management).toBe(false)
    const draft = dnsConnectionDraft(connection)
    expect(draft).toMatchObject({
      customCA: true,
      ca: "",
      username: "",
      password: "",
      token: "",
      serverName: "dns.example.test",
    })
    expect(prepareDNSConnection(draft, connection).request).toBeUndefined()
  })
  test.each(["adguard", "pihole", "technitium"])(
    "%s sends only its native credential",
    (engine) => {
      const draft = { ...completeDraft(), engine, token: "scoped-native-token" }
      const result = prepareDNSConnection(draft)
      expect(result.errors).toEqual({})
      expect(result.request?.credential).toEqual(
        engine === "adguard"
          ? { username: "native-user", password: "request-only-secret" }
          : engine === "pihole"
            ? { password: "request-only-secret" }
            : { token: "scoped-native-token" },
      )
      expect(result.request?.management).toBe(false)
    },
  )
  test.each([
    "http://192.0.2.1:8080",
    "https://dns.example.test:8443",
    "https://192.0.2.1",
    "https://192.0.2.1:8443/private",
    "https://192.0.2.1:8443/?token=secret",
    "https://native-user:secret@192.0.2.1:8443",
    "https://0.0.0.0:8443",
    "https://224.0.0.1:8443",
    "https://[::]:8443",
    "https://[ff02::1]:8443",
    "http://[2001:db8::1]:8080",
    "https://999.2.3.4:8443",
    "https://127.1:8443",
  ])("unsupported management origin is refused: %s", (endpoint) => {
    expect(prepareDNSConnection({ ...completeDraft(), endpoint }).errors.endpoint).toBeTruthy()
  })
  test.each(["http://[::1]:8080", "http://127.0.0.1:80", "https://[2001:db8::1]:443"])(
    "explicit literal origin remains valid: %s",
    (endpoint) => {
      expect(prepareDNSConnection({ ...completeDraft(), endpoint }).request?.endpoint).toBe(
        endpoint,
      )
    },
  )
  test("replacement keeps the same engine/origin and requires custom CA re-entry", () => {
    const draft = {
      ...dnsConnectionDraft(connection),
      username: "native-user",
      password: "new-native-secret",
    }
    expect(prepareDNSConnection(draft, connection).errors.ca).toBeTruthy()
    expect(prepareDNSConnection({ ...draft, customCA: false }, connection).request).toBeDefined()
    expect(
      prepareDNSConnection(
        { ...draft, customCA: false, endpoint: "https://192.0.2.11:8443" },
        connection,
      ).request,
    ).toBeUndefined()
    expect(
      prepareDNSConnection({ ...draft, customCA: false, engine: "pihole" }, connection).request,
    ).toBeUndefined()
  })
  test("HTTPS identity/trust cannot silently accompany a cleartext connection", () => {
    expect(
      prepareDNSConnection({ ...completeDraft(), serverName: "dns.example.test" }).request,
    ).toBeUndefined()
    expect(
      prepareDNSConnection({ ...completeDraft(), customCA: true, ca: "certificate" }).request,
    ).toBeUndefined()
  })
  test("credentials are re-entered verbatim, and invalid credential bounds block requests", () => {
    expect(
      prepareDNSConnection({ ...completeDraft(), password: " leading and trailing " }).request
        ?.credential.password,
    ).toBe(" leading and trailing ")
    for (const password of ["", "a\nsecret", "x".repeat(4097)])
      expect(prepareDNSConnection({ ...completeDraft(), password }).request).toBeUndefined()
    expect(
      prepareDNSConnection({ ...completeDraft(), engine: "technitium", token: "space token" })
        .request,
    ).toBeUndefined()
  })
})

describe("native DNS retained review eligibility", () => {
  const view = {
    connection: { ...connection, management: true },
    state: "available",
    snapshot: {},
  }
  test("only an administrator's fresh, available, manageable view can stage", () => {
    expect(dnsStageProblem(view, false, true)).toBeUndefined()
    expect(dnsStageProblem(view, false, false)).toBeTruthy()
    expect(dnsStageProblem(view, true, true)).toBeTruthy()
    expect(dnsStageProblem({ ...view, state: "needs_review" }, false, true)).toBeTruthy()
    expect(dnsStageProblem({ ...view, snapshot: undefined }, false, true)).toBeTruthy()
    expect(dnsStageProblem({ ...view, connection }, false, true)).toBeTruthy()
  })
  test("bounded draft entries retain their deliberate order and cannot duplicate scope", () => {
    expect(dnsDraftLines("192.0.2.1:53\n\n[2001:db8::1]:53", 16, true)).toEqual([
      "192.0.2.1:53",
      "[2001:db8::1]:53",
    ])
    expect(() => dnsDraftLines("", 16, true)).toThrow()
    expect(() => dnsDraftLines("same\nsame", 16)).toThrow()
    expect(() => dnsDraftLines("one\ntwo\nthree", 2)).toThrow()
    expect(dnsDraftLines("", 128)).toEqual([])
  })
})
