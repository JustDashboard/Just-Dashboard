import { expect, test, type Page, type Route } from "@playwright/test"
import { mockOnboarding } from "./onboarding-fixture"
import { user } from "./host-fixture"

const signedOut = {
  authenticated: false,
  needsTotp: false,
  needsEnrollment: false,
  require2fa: false,
}

const json = (route: Route, body: unknown, status = 200) =>
  route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })

async function authFixture(
  page: Page,
  onSession?: (route: Route, read: number) => Promise<void>,
  signedIn = true,
) {
  const fixture = await mockOnboarding(page, { signedIn, needsOnboarding: false })
  let reads = 0
  await page.route("**/api/v1/auth/session", async (route) => {
    reads += 1
    if (onSession) return onSession(route, reads)
    await route.fallback()
  })
  return { ...fixture, reads: () => reads }
}

async function rejectKeyCreation(page: Page, onRejected = () => {}) {
  await page.route("**/api/v1/tokens/", async (route) => {
    if (route.request().method() === "GET") return json(route, [])
    onRejected()
    return json(route, { error: { code: "unauthorized", message: "Session expired" } }, 401)
  })
  await page.goto("/account/keys")
  await page.getByRole("button", { name: "New key" }).click()
  await page.getByLabel("Name").fill("Synthetic recovery check")
  await page.getByRole("button", { name: "Create key" }).click()
}

async function signIn(page: Page) {
  await page.getByLabel("Username").fill("ion")
  await page.getByLabel("Password", { exact: true }).fill("Correct-Horse-Battery-9")
  await page.getByRole("button", { name: "Continue", exact: true }).click()
}

async function signOut(page: Page) {
  await page.getByRole("button", { name: "Account menu for Ion", exact: true }).click()
  await page.getByRole("menuitem", { name: "Sign out", exact: true }).click()
}

test("an expired mutation rechecks once and the sign-in explanation survives reload", async ({
  page,
}) => {
  const fixture = await authFixture(page)
  await rejectKeyCreation(page, () => {
    fixture.account.signedIn = false
  })
  await expect(page).toHaveURL(/\/login\?reason=session$/)
  await expect(page.getByText("Your session ended. Sign in again to continue.")).toBeVisible()
  expect(fixture.reads()).toBe(2)
  await page.reload()
  await expect(page.getByText("Your session ended. Sign in again to continue.")).toBeVisible()
  expect(fixture.reads()).toBe(3)
  await signIn(page)
  await expect(page).toHaveURL(/\/$/)
  await expect(page.getByText("Your session ended. Sign in again to continue.")).toHaveCount(0)
})

test("a temporarily unavailable session recheck preserves the current account", async ({
  page,
}) => {
  let rechecked = false
  const fixture = await authFixture(page, async (route, read) => {
    if (read === 1) return route.fallback()
    await json(route, { error: { code: "unavailable", message: "Session read unavailable" } }, 503)
    rechecked = true
  })
  await rejectKeyCreation(page)
  await expect.poll(() => rechecked).toBe(true)
  await expect(page.getByText("Could not create the key")).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(
    page.getByRole("button", { name: "Account menu for Ion", exact: true }),
  ).toBeVisible()
  await expect(page).toHaveURL(/\/account\/keys$/)
  expect(fixture.reads()).toBe(2)
  await expect(page.getByText("Your session ended. Sign in again to continue.")).toHaveCount(0)
})

test("a delayed expired-session recheck cannot replace a later successful sign-in", async ({
  page,
}) => {
  let release = () => {}
  let started = false
  let finished = false
  const pending = new Promise<void>((resolve) => {
    release = resolve
  })
  await authFixture(page, async (route, read) => {
    if (read === 1) return route.fallback()
    started = true
    await pending
    await json(route, signedOut)
    finished = true
  })
  await rejectKeyCreation(page)
  await expect.poll(() => started).toBe(true)
  await page.keyboard.press("Escape")
  await signOut(page)
  await expect(page.getByLabel("Username")).toBeVisible()
  await signIn(page)
  await expect(page).toHaveURL(/\/$/)
  release()
  await expect.poll(() => finished).toBe(true)
  await expect(
    page.getByRole("button", { name: "Account menu for Ion", exact: true }),
  ).toBeVisible()
  await expect(page).toHaveURL(/\/$/)
  await expect(page.getByText("Your session ended. Sign in again to continue.")).toHaveCount(0)
})

test("a rejected current password keeps the account and makes no expiry recheck", async ({
  page,
}) => {
  const fixture = await authFixture(page)
  await page.route("**/api/v1/account/sessions", (route) => json(route, []))
  await page.route("**/api/v1/account/password", (route) =>
    json(
      route,
      { error: { code: "invalid_credentials", message: "Current password is incorrect" } },
      401,
    ),
  )
  await page.goto("/account/security")
  await page.getByLabel("Current password", { exact: true }).fill("Wrong-current-password")
  await page.getByLabel("New password", { exact: true }).fill("A-New-Password-123!")
  await page.getByLabel("Confirm new password", { exact: true }).fill("A-New-Password-123!")
  await page.getByRole("button", { name: "Change password", exact: true }).click()
  await expect(page.getByText("Could not change password", { exact: true })).toBeVisible()
  await expect(
    page.getByRole("button", { name: "Account menu for Ion", exact: true }),
  ).toBeVisible()
  await expect(page).toHaveURL(/\/account\/security$/)
  expect(fixture.reads()).toBe(1)
})

test("a rejected authenticator code stays on verification without an expiry notice", async ({
  page,
}) => {
  const fixture = await authFixture(page, undefined, false)
  await page.route("**/api/v1/auth/login", (route) =>
    json(route, { ...user, authenticated: false, needsTotp: true }),
  )
  await page.route("**/api/v1/auth/2fa/verify", (route) =>
    json(route, { error: { code: "invalid_code", message: "Code was not accepted" } }, 401),
  )
  await page.goto("/login")
  await signIn(page)
  await page.getByLabel("Verification code").fill("000000")
  await page.getByRole("button", { name: "Verify", exact: true }).click()
  await expect(page.getByText("Code rejected", { exact: true })).toBeVisible()
  await expect(page.getByLabel("Verification code")).toHaveValue("")
  await expect(page.getByText("Your session ended. Sign in again to continue.")).toHaveCount(0)
  expect(fixture.reads()).toBe(1)
})

test("repeated sign-out uses one pending request before a fresh login", async ({ page }) => {
  const fixture = await authFixture(page)
  let calls = 0
  let release = () => {}
  const pending = new Promise<void>((resolve) => {
    release = resolve
  })
  await page.route("**/api/v1/auth/logout", async (route) => {
    calls += 1
    await pending
    fixture.account.signedIn = false
    await route.fulfill({ status: 204 })
  })
  await page.goto("/account/sessions")
  await signOut(page)
  await expect.poll(() => calls).toBe(1)
  await signOut(page)
  release()
  await expect(page.getByLabel("Username")).toBeVisible()
  expect(calls).toBe(1)
  await signIn(page)
  await expect(page).toHaveURL(/\/$/)
  await expect(
    page.getByRole("button", { name: "Account menu for Ion", exact: true }),
  ).toBeVisible()
})
