import { describe, expect, test } from "bun:test"
import { readFileSync } from "node:fs"
import {
  fromBase64Url,
  PASSKEY_POLICY_REASON,
  passkeyPlace,
  policyAllowsPasskeys,
  refusedByPolicy,
  suggestedPasskeyName,
  toBase64Url,
} from "./passkeys"

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

describe("the page's Permissions-Policy", () => {
  const reader = (allowed, features = ["publickey-credentials-get"]) => ({
    features: () => features,
    allowsFeature: () => allowed,
  })
  test("a policy that forbids passkeys is read as forbidding them, from either name", () => {
    expect(policyAllowsPasskeys({ permissionsPolicy: reader(false) })).toBe(false)
    expect(policyAllowsPasskeys({ featurePolicy: reader(false) })).toBe(false)
    expect(policyAllowsPasskeys({ permissionsPolicy: reader(true) })).toBe(true)
  })
  test("a browser that cannot say, or does not know the feature, is not told no", () => {
    expect(policyAllowsPasskeys({})).toBe(true)
    expect(policyAllowsPasskeys({ featurePolicy: reader(false, ["camera"]) })).toBe(true)
  })
  test("the place says so, after the reasons a passkey could not work anyway", () => {
    const where = { hostname: "dash.example.com", secure: true, supported: true }
    expect(passkeyPlace({ ...where, allowedByPolicy: true }).usable).toBe(true)
    expect(passkeyPlace({ ...where, allowedByPolicy: false })).toEqual({
      usable: false,
      reason: PASSKEY_POLICY_REASON,
    })
    const atAddress = passkeyPlace({ ...where, hostname: "100.64.0.7", allowedByPolicy: false })
    expect(atAddress.usable || atAddress.reason).toContain("host name")
  })
  test("only the policy's refusal is told apart from closing the prompt", () => {
    const refusal = (name, message) => new DOMException(message, name)
    expect(
      refusedByPolicy(
        refusal(
          "NotAllowedError",
          "The 'publickey-credentials-get' feature is not enabled in this document. Permissions Policy may be used to delegate Web Authentication capabilities to cross-origin child frames.",
        ),
      ),
    ).toBe(true)
    expect(
      refusedByPolicy(
        refusal(
          "NotAllowedError",
          "The operation either timed out or was not allowed. See: https://www.w3.org/TR/webauthn-2/#sctn-privacy-considerations-client.",
        ),
      ),
    ).toBe(false)
    expect(refusedByPolicy(refusal("AbortError", "Permissions Policy"))).toBe(false)
    expect(refusedByPolicy("Permissions Policy")).toBe(false)
  })
  // Browser specs talk to the frontend directly, so nothing else would notice
  // the production proxy's header taking passkeys away again.
  test("the proxy this project ships allows passkeys on its own origin", () => {
    const caddyfile = readFileSync(new URL("../../../deploy/Caddyfile", import.meta.url), "utf8")
    const policy = caddyfile.match(/Permissions-Policy "([^"]*)"/)?.[1]
    expect(policy).toContain("publickey-credentials-get=(self)")
    expect(policy).not.toContain("publickey-credentials-create=()")
  })
})
