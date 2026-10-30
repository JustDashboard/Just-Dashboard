import { generateKeyPairSync, randomBytes } from "node:crypto"
import { expect, test, type Page, type Route } from "@playwright/test"

/**
 * Passkeys in the browser, against a mocked API and Chrome's virtual
 * authenticator: the security page lists them and adds one after the password
 * and code, and the sign-in page signs in with one — and, for an account with
 * an authenticator, goes on to ask for the code. WebAuthn refuses an IP
 * address as a relying party, so the ceremonies run at localhost, and the
 * pages say so when they are opened at 127.0.0.1.
 */

const b64 = (bytes: Buffer | Uint8Array) => Buffer.from(bytes).toString("base64url")
const now = Date.now()
const iso = (ms: number) => new Date(ms).toISOString()

const me = {
  id: 1,
  username: "ion",
  displayName: "Ion Moisei",
  avatarVersion: 0,
  role: "admin",
  totpEnabled: true,
  disabled: false,
  mustChangePassword: false,
  lastLoginAt: iso(now - 3_600_000),
  createdAt: "2026-03-02T09:00:00Z",
}

const signedIn = {
  authenticated: true,
  needsTotp: false,
  needsEnrollment: false,
  require2fa: false,
  capabilities: [
    "read",
    "service.control",
    "file.write",
    "terminal",
    "destructive",
    "system.admin",
  ],
  user: me,
}

const laptop = {
  id: 4,
  name: "Chrome on Linux",
  rpId: "localhost",
  createdAt: iso(now - 5 * 86_400_000),
  lastUsedAt: iso(now - 7_200_000),
  synced: false,
  transports: ["internal"],
}

const phone = {
  ...laptop,
  id: 5,
  name: "iPhone",
  rpId: "dash.example.com",
  lastUsedAt: "0001-01-01T00:00:00Z",
  synced: true,
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

/** The same server, reached by name: what a passkey needs. */
function localhost(page: Page, path: string) {
  const base = new URL(test.info().project.use.baseURL ?? "http://127.0.0.1:43117")
  base.hostname = "localhost"
  return page.goto(new URL(path, base).toString())
}

async function virtualAuthenticator(page: Page) {
  const cdp = await page.context().newCDPSession(page)
  await cdp.send("WebAuthn.enable")
  const { authenticatorId } = await cdp.send("WebAuthn.addVirtualAuthenticator", {
    options: {
      protocol: "ctap2",
      transport: "internal",
      hasResidentKey: true,
      hasUserVerification: true,
      isUserVerified: true,
      automaticPresenceSimulation: true,
    },
  })
  return { cdp, authenticatorId }
}

test("at an IP address the pages say why there is no passkey, rather than offering one", async ({
  page,
}) => {
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, signedIn)
    if (path === "/account/passkeys") return json(route, [laptop, phone])
    return json(route, [])
  })
  await page.goto("/account/security")
  const list = page.getByRole("list", { name: "Passkeys" })
  await expect(list.getByText("Chrome on Linux")).toBeVisible()
  await expect(
    list.getByText(/never used yet · synced · signs in at dash\.example\.com/),
  ).toBeVisible()
  await expect(
    page.getByText(
      /Passkeys need this dashboard's name, and it is open at the address 127\.0\.0\.1/,
    ),
  ).toBeVisible()
  await expect(page.getByRole("button", { name: "Add a passkey" })).toHaveCount(0)
  await expect(
    page.getByText("In place of the password. The code is still asked for."),
  ).toBeVisible()

  await page.unroute("**/api/v1/**")
  await page.route("**/api/v1/**", (route) =>
    json(route, { authenticated: false, require2fa: false }),
  )
  await page.goto("/login")
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Sign in with a passkey" })).toHaveCount(0)
  await expect(page.getByText(/Open it by its host name to use one/)).toBeVisible()
})

test("adding a passkey asks for the password and the code, then the browser makes one", async ({
  page,
}) => {
  const { cdp, authenticatorId } = await virtualAuthenticator(page)
  const begins: unknown[] = []
  const finishes: Record<string, unknown>[] = []
  let keys: unknown[] = [laptop]
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, signedIn)
    if (path === "/account/passkeys") return json(route, keys)
    if (path === "/account/passkeys/register/begin") {
      begins.push(request.postDataJSON())
      return json(route, {
        challengeId: "sealed-registration",
        publicKey: {
          challenge: b64(randomBytes(32)),
          rp: { name: "Just Dashboard", id: "localhost" },
          user: { id: b64(Buffer.from("account-handle")), name: "ion", displayName: "Ion Moisei" },
          pubKeyCredParams: [{ type: "public-key", alg: -7 }],
          timeout: 60000,
          excludeCredentials: [],
          authenticatorSelection: { residentKey: "required", userVerification: "required" },
          attestation: "none",
        },
      })
    }
    if (path === "/account/passkeys/register/finish") {
      finishes.push(request.postDataJSON())
      const created = { ...laptop, id: 9, name: "Work laptop", lastUsedAt: "0001-01-01T00:00:00Z" }
      keys = [created, laptop]
      return json(route, created, 201)
    }
    return json(route, [])
  })
  await localhost(page, "/account/security")
  await page.getByRole("button", { name: "Add a passkey" }).click()
  const dialog = page.getByRole("dialog", { name: "Add a passkey" })
  const go = dialog.getByRole("button", { name: "Continue" })
  await dialog.getByLabel("Name", { exact: true }).fill("Work laptop")
  await dialog.getByLabel("Current password").fill("Correct-Horse-Battery-9")
  // The account has an authenticator, so its code is asked for too.
  await expect(go).toBeDisabled()
  await dialog.getByLabel("Code from your authenticator").fill("123456")
  await go.click()
  await expect(page.getByText("Work laptop added")).toBeVisible()
  expect(begins).toEqual([{ password: "Correct-Horse-Battery-9", code: "123456" }])
  expect(finishes[0].challengeId).toBe("sealed-registration")
  expect(finishes[0].name).toBe("Work laptop")
  const response = (finishes[0].credential as { response: Record<string, unknown> }).response
  expect(typeof response.attestationObject).toBe("string")
  expect(typeof response.clientDataJSON).toBe("string")
  const clientData = JSON.parse(
    Buffer.from(response.clientDataJSON as string, "base64url").toString(),
  )
  expect(clientData).toMatchObject({
    type: "webauthn.create",
    origin: expect.stringMatching(/^http:\/\/localhost:\d+$/),
  })
  const { credentials } = await cdp.send("WebAuthn.getCredentials", { authenticatorId })
  expect(credentials).toHaveLength(1)
  expect(credentials[0]).toMatchObject({ isResidentCredential: true, rpId: "localhost" })
  await expect(page.getByRole("list", { name: "Passkeys" }).getByText("Work laptop")).toBeVisible()
})

/**
 * A sign-in page with a passkey for localhost in the virtual authenticator,
 * whose finish answers as the server does for an account with an
 * authenticator: the code is still owed.
 */
async function passkeySignInPage(page: Page) {
  const { cdp, authenticatorId } = await virtualAuthenticator(page)
  const { privateKey } = generateKeyPairSync("ec", { namedCurve: "P-256" })
  await cdp.send("WebAuthn.addCredential", {
    authenticatorId,
    credential: {
      credentialId: randomBytes(32).toString("base64"),
      isResidentCredential: true,
      rpId: "localhost",
      privateKey: privateKey.export({ format: "der", type: "pkcs8" }).toString("base64"),
      userHandle: Buffer.from("account-handle").toString("base64"),
      signCount: 0,
    },
  })
  const finishes: Record<string, unknown>[] = []
  let status: unknown = { authenticated: false, require2fa: false }
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, status)
    if (path === "/auth/passkey/begin") {
      return json(route, {
        challengeId: `sealed-login-${Math.random()}`,
        publicKey: {
          challenge: b64(randomBytes(32)),
          timeout: 60000,
          rpId: "localhost",
          userVerification: "required",
        },
      })
    }
    if (path === "/auth/passkey/finish") {
      finishes.push(request.postDataJSON())
      status = { ...signedIn, authenticated: false, needsTotp: true }
      return json(route, status)
    }
    return json(route, [])
  })
  return finishes
}

async function expectCodeOwed(page: Page, finishes: Record<string, unknown>[]) {
  await expect(page.getByRole("heading", { name: "Two-factor code" })).toBeVisible()
  await expect(page.getByLabel("Verification code")).toBeVisible()
  const sent = finishes.at(-1) as { credential: { response: Record<string, string> } }
  expect(Buffer.from(sent.credential.response.userHandle, "base64url").toString()).toBe(
    "account-handle",
  )
  expect(typeof sent.credential.response.signature).toBe("string")
  const clientData = JSON.parse(
    Buffer.from(sent.credential.response.clientDataJSON, "base64url").toString(),
  )
  expect(clientData.type).toBe("webauthn.get")
}

test("the button signs in with a passkey, and an account with an authenticator is asked for its code", async ({
  page,
}) => {
  // Without autofill for passkeys, the button is the way in.
  await page.addInitScript(() => {
    Object.defineProperty(window.PublicKeyCredential, "isConditionalMediationAvailable", {
      value: () => Promise.resolve(false),
    })
  })
  const finishes = await passkeySignInPage(page)
  await localhost(page, "/login")
  await page.getByRole("button", { name: "Sign in with a passkey" }).click()
  await expectCodeOwed(page, finishes)
})

test("the username field's autofill offers the passkey, and picking it signs in to the code step", async ({
  page,
}) => {
  // The virtual authenticator answers the conditional request as a person
  // picking the passkey from the autofill would, with no button pressed.
  const finishes = await passkeySignInPage(page)
  await localhost(page, "/login")
  await expectCodeOwed(page, finishes)
  expect(finishes).toHaveLength(1)
})

test("a page whose policy forbids passkeys says so and asks nothing of the browser", async ({
  page,
}) => {
  let begins = 0
  await page.route("**/api/v1/**", async (route) => {
    if (route.request().url().endsWith("/auth/passkey/begin")) begins++
    await json(route, { authenticated: false, require2fa: false })
  })
  await page.route("**/login*", async (route) => {
    if (route.request().resourceType() !== "document") return route.fallback()
    const response = await route.fetch()
    await route.fulfill({
      response,
      headers: { ...response.headers(), "permissions-policy": "publickey-credentials-get=()" },
    })
  })
  await localhost(page, "/login")
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible()
  await expect(page.getByText(/This page's Permissions-Policy forbids passkeys/)).toBeVisible()
  await expect(page.getByRole("button", { name: "Sign in with a passkey" })).toHaveCount(0)
  await expect(page.getByText("Passkey sign-in failed")).toHaveCount(0)
  expect(begins).toBe(0)
})

test("closing the browser's prompt is not reported, and the conditional offer stays quiet", async ({
  page,
}) => {
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/passkey/begin") {
      return json(route, {
        challengeId: "sealed",
        publicKey: { challenge: b64(randomBytes(32)), rpId: "localhost", timeout: 60000 },
      })
    }
    return json(route, { authenticated: false, require2fa: false })
  })
  await page.addInitScript(() => {
    navigator.credentials.get = () =>
      Promise.reject(
        new DOMException("The operation either timed out or was not allowed.", "NotAllowedError"),
      )
  })
  await localhost(page, "/login")
  await page.getByRole("button", { name: "Sign in with a passkey" }).click()
  await expect(page.getByRole("button", { name: "Sign in with a passkey" })).toBeEnabled()
  await expect(page.getByText("Passkey sign-in failed")).toHaveCount(0)
  await expect(page.getByText("Passkeys are blocked on this page")).toHaveCount(0)
})

test("a refusal only the browser can name is explained when the button is pressed", async ({
  page,
}) => {
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/passkey/begin") {
      return json(route, {
        challengeId: "sealed",
        publicKey: { challenge: b64(randomBytes(32)), rpId: "localhost", timeout: 60000 },
      })
    }
    return json(route, { authenticated: false, require2fa: false })
  })
  await page.addInitScript(() => {
    navigator.credentials.get = () =>
      Promise.reject(
        new DOMException(
          "The 'publickey-credentials-get' feature is not enabled in this document.",
          "NotAllowedError",
        ),
      )
  })
  await localhost(page, "/login")
  await page.getByRole("button", { name: "Sign in with a passkey" }).click()
  await expect(page.getByText("Passkeys are blocked on this page")).toBeVisible()
})

test("regenerating recovery codes asks for the password, and sends it", async ({ page }) => {
  const sent: unknown[] = []
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, signedIn)
    if (path === "/account/recovery-codes") {
      sent.push(request.postDataJSON())
      if (sent.length === 1) {
        return json(
          route,
          { error: { code: "invalid_credentials", message: "that password is incorrect" } },
          401,
        )
      }
      return json(route, { recoveryCodes: ["aaaa-bbbb", "cccc-dddd"] })
    }
    return json(route, [])
  })
  await page.goto("/account/security")
  await page.getByRole("button", { name: "Regenerate recovery codes" }).click()
  const go = page.getByRole("button", { name: "Regenerate recovery codes" })
  await expect(go).toBeDisabled()
  await page.getByLabel("Current password").first().fill("Not-The-Password-1")
  await go.click()
  await expect(page.getByText("that password is incorrect")).toBeVisible()
  await page.getByLabel("Current password").first().fill("Correct-Horse-Battery-9")
  await go.click()
  await expect(page.getByText("aaaa-bbbb")).toBeVisible()
  expect(sent).toEqual([
    { password: "Not-The-Password-1" },
    { password: "Correct-Horse-Battery-9" },
  ])
})
