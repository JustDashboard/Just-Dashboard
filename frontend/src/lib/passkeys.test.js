import { describe, expect, test } from "bun:test"
import { fromBase64Url, passkeyPlace, suggestedPasskeyName, toBase64Url } from "./passkeys"

describe("base64url", () => {
  test("round-trips bytes without padding", () => {
    const bytes = new Uint8Array([0, 1, 250, 251, 252, 253, 254, 255])
    const text = toBase64Url(bytes)
    expect(text).toBe("AAH6-_z9_v8")
    expect(new Uint8Array(fromBase64Url(text))).toEqual(bytes)
    expect(new Uint8Array(fromBase64Url("AAH6-_z9_v8="))).toEqual(bytes)
  })
})

describe("passkeyPlace", () => {
  const secure = { secure: true, supported: true }
  test("a name over https or localhost can hold a passkey", () => {
    expect(passkeyPlace({ hostname: "dash.example.com", ...secure }).usable).toBe(true)
    expect(passkeyPlace({ hostname: "localhost", ...secure }).usable).toBe(true)
    expect(passkeyPlace({ hostname: "vps.tail0000.ts.net", ...secure }).usable).toBe(true)
  })
  test("an IP address cannot, and says to open the name", () => {
    for (const hostname of ["100.64.0.7", "127.0.0.1", "[fd7a:115c::1]", "::1"]) {
      const place = passkeyPlace({ hostname, ...secure })
      expect(place.usable).toBe(false)
      if (!place.usable) expect(place.reason).toContain("host name")
    }
  })
  test("plain http and a browser without passkeys say so", () => {
    const insecure = passkeyPlace({ hostname: "dash.lan", secure: false, supported: true })
    expect(insecure.usable).toBe(false)
    const none = passkeyPlace({ hostname: "dash.lan", secure: true, supported: false })
    expect(none.usable).toBe(false)
  })
})

describe("suggestedPasskeyName", () => {
  test("names the browser and the system", () => {
    expect(
      suggestedPasskeyName(
        "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36",
      ),
    ).toBe("Chrome on Linux")
    expect(
      suggestedPasskeyName(
        "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1",
      ),
    ).toBe("Safari on iOS")
  })
})
