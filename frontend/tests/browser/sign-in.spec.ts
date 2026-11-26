import { expect, test, type Page, type Route } from "@playwright/test"

/**
 * Signing in, as an account meets it.
 *
 * The spine names only the steps an account has, so it is not drawn until the
 * server has answered; a forgotten password is recovered on the server, and the
 * screen says how; and an account that owes a new password is not asked again
 * for the one it typed a moment ago.
 */

const signedOut = { authenticated: false, needsTotp: false, needsEnrollment: false }

const fulfill = (route: Route, body: unknown) =>
  route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })

async function mockSignIn(page: Page, answer: Record<string, unknown>) {
  const logins: unknown[] = []
  let status: unknown = signedOut
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.slice(7)
    if (path === "/auth/login") {
      logins.push(route.request().postDataJSON())
      status = answer
    }
    await fulfill(route, status)
  })
  return { logins }
}

async function signIn(page: Page) {
  await page.goto("/login")
  await page.getByLabel("Username", { exact: true }).fill("Ion")
  await page.getByLabel("Password", { exact: true }).fill("Correct-Horse-Battery-9")
  await page.getByRole("button", { name: "Continue", exact: true }).click()
}

const spine = (page: Page) => page.getByRole("list", { name: "Progress" })

test("the spine is drawn once the server says which steps the account has", async ({ page }) => {
  await mockSignIn(page, {
    ...signedOut,
    needsTotp: true,
    user: { username: "ion", totpEnabled: true, mustChangePassword: false },
  })
  await page.goto("/login")
  await expect(page.getByRole("heading", { name: "Sign in", exact: true })).toBeVisible()
  await expect(spine(page)).toHaveCount(0)

  await signIn(page)
  await expect(page.getByRole("heading", { name: "Two-factor code", exact: true })).toBeVisible()
  await expect(page.getByText("Step 2 of 2")).toBeVisible()
  await expect(spine(page).getByRole("listitem")).toHaveText(["Sign in", "Code"])
})

test("a code pasted in its groups arrives as all six digits", async ({ page }) => {
  await mockSignIn(page, {
    ...signedOut,
    needsTotp: true,
    user: { username: "ion", totpEnabled: true, mustChangePassword: false },
  })
  const verified: unknown[] = []
  await page.route("**/api/v1/auth/2fa/verify", async (route) => {
    verified.push(route.request().postDataJSON())
    await fulfill(route, { ...signedOut, needsTotp: true })
  })
  await signIn(page)
  const code = page.getByLabel("Verification code", { exact: true })
  await expect(code).toBeVisible()
  // As a message or an authenticator copies it: the browser's own length
  // limit, applied before the space went, kept "12345".
  await code.fill("123 456")
  await expect(code).toHaveValue("123456")
  // Pasted over what is there, with its dash, and more than a code after it.
  await code.selectText()
  await page.keyboard.insertText("654-321 is your code, valid 10 minutes")
  await expect(code).toHaveValue("654321")
  await page.getByRole("button", { name: "Verify", exact: true }).click()
  await expect.poll(() => verified).toEqual([{ code: "654321" }])
})

test("a forgotten password points at the server's reset script", async ({ page }) => {
  await mockSignIn(page, signedOut)
  await page.goto("/login")
  await page.getByLabel("Username", { exact: true }).fill("Ion")
  await page.getByRole("button", { name: "Forgot it?", exact: true }).click()
  await expect(
    page.getByText("sudo ./scripts/reset-password.sh ion", { exact: true }),
  ).toBeVisible()
})

test("Tab reaches the password before the link for a forgotten one, still drawn on its label's row", async ({
  page,
}) => {
  await mockSignIn(page, signedOut)
  await page.goto("/login")
  const username = page.getByLabel("Username", { exact: true })
  const password = page.getByLabel("Password", { exact: true })
  const forgot = page.getByRole("button", { name: "Forgot it?", exact: true })
  await username.focus()
  await page.keyboard.press("Tab")
  await expect(password).toBeFocused()
  await page.keyboard.press("Tab")
  await expect(page.getByRole("button", { name: "Show password", exact: true })).toBeFocused()
  await page.keyboard.press("Tab")
  await expect(forgot).toBeFocused()

  const label = await page.locator("label[for=password]").boundingBox()
  const link = await forgot.boundingBox()
  const field = await password.boundingBox()
  expect(label && link && field).toBeTruthy()
  if (!label || !link || !field) return
  // On the label's row, at the field's right edge, and above the field.
  expect(Math.abs(link.y + link.height / 2 - (label.y + label.height / 2))).toBeLessThanOrEqual(1)
  expect(Math.abs(link.x + link.width - (field.x + field.width))).toBeLessThanOrEqual(1)
  expect(link.y + link.height).toBeLessThanOrEqual(field.y)
})

test("an account owing a new password is not asked again for the one it typed", async ({
  page,
}) => {
  const { logins } = await mockSignIn(page, {
    ...signedOut,
    needsPasswordChange: true,
    user: { username: "ion", totpEnabled: false, mustChangePassword: true },
  })
  await signIn(page)
  await expect(
    page.getByRole("heading", { name: "Change your password", exact: true }),
  ).toBeVisible()
  await expect(spine(page).getByRole("listitem")).toHaveText(["Sign in", "New password"])
  await expect(page.getByLabel("Current password", { exact: true })).toHaveCount(0)

  // The server's rule is read as the reader types, so the command waits for it.
  const change = page.getByRole("button", { name: "Change password", exact: true })
  await page.getByLabel("New password", { exact: true }).fill("short")
  await page.getByLabel("Repeat new password", { exact: true }).fill("short")
  await expect(change).toBeDisabled()
  await page.getByLabel("New password", { exact: true }).fill("Another-Horse-Battery-7")
  await page.getByLabel("Repeat new password", { exact: true }).fill("Another-Horse-Battery-7")
  await expect(change).toBeEnabled()
  expect(logins).toHaveLength(1)
})
