import { expect, test, type Page, type Route } from "@playwright/test"
import { DOCKER_CONTAINERS, DOCKER_DISK } from "./docker-fixture"
import { expectSaved, mockProject, saveSettings, user } from "./deploy-fixture"

/**
 * What the app says when it has nothing to show: an address nothing answers, a
 * render that threw above the shell, and a read that failed, which must not
 * read as "there are none". Also the tab's title, which names the page.
 */

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

const refused = { error: { code: "internal", message: "The read was refused" } }

/** A signed-in shell over an API with nothing in it; later routes win over this one. */
async function mockShell(page: Page, session: unknown = user) {
  await page.route("**/api/v1/**", (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, session)
    if (path === "/docker/ping") return json(route, { available: true, serverVersion: "27.0.0" })
    return json(route, [])
  })
}

/** Anything the content security policy refuses, which is any script without the nonce. */
function policyViolations(page: Page) {
  const violations: string[] = []
  page.on("console", (message) => {
    if (message.text().includes("Content Security Policy")) violations.push(message.text())
  })
  return violations
}

test.describe("an address nothing answers", () => {
  test("keeps the shell and has a way back", async ({ page }) => {
    await mockShell(page)
    await page.goto("/no-such-page")
    await expect(page.getByRole("heading", { level: 1, name: "Page not found" })).toBeAttached()
    await expect(page.locator('[data-slot="empty-state"]')).toContainText("Page not found")
    await expect(
      page.getByRole("complementary").or(page.locator('[data-slot="sidebar"]')).first(),
    ).toBeVisible()
    await page.getByRole("link", { name: "Back to the Overview" }).click()
    await expect(page).toHaveURL(/\/$/)
    await expect(page.getByText("Page not found")).toHaveCount(0)
  })

  test("sends someone signed out to sign in", async ({ page }) => {
    await page.route("**/api/v1/**", (route) =>
      json(route, { error: { code: "unauthenticated", message: "Sign in" } }, 401),
    )
    await page.goto("/no-such-page")
    await expect(page).toHaveURL(/\/login/)
  })

  test("outside the shell, the root page has its own heading and link", async ({ page }) => {
    await mockShell(page)
    const violations = policyViolations(page)
    await page.goto("/_not-found")
    await expect(page.getByRole("heading", { level: 1, name: "Page not found" })).toBeVisible()
    await expect(page.getByRole("link", { name: "Back to the Overview" })).toHaveAttribute(
      "href",
      "/",
    )
    await expect(page).toHaveTitle("Page not found · Just Dashboard")
    expect(violations).toEqual([])
  })
})

test.describe("a render that throws above the shell", () => {
  test("the dashboard's layout falls to the root error page", async ({ page }) => {
    // `can` reads this as a list from inside the rail, in the layout that
    // `(dashboard)/error.tsx` cannot catch.
    await mockShell(page, { ...user, capabilities: 5 })
    const violations = policyViolations(page)
    await page.goto("/")
    await expect(
      page.getByRole("heading", { level: 1, name: "The dashboard stopped rendering" }),
    ).toBeVisible()
    await expect(page.getByRole("button", { name: "Try again" })).toBeVisible()
    await expect(page.getByRole("link", { name: "Back to the Overview" })).toHaveAttribute(
      "href",
      "/",
    )
    expect(violations).toEqual([])
  })

  test("the root layout falls to the global error page, under the policy", async ({ page }) => {
    // The provider in the root layout subscribes to this event in an effect.
    await page.addInitScript(() => {
      const add = window.addEventListener
      window.addEventListener = function (this: Window, ...args: Parameters<typeof add>) {
        if (args[0] === "jd:authentication-lost") throw new Error("Fixture: the layout threw")
        return add.apply(this, args)
      } as typeof add
    })
    await mockShell(page)
    const violations = policyViolations(page)
    await page.goto("/")
    await expect(
      page.getByRole("heading", { level: 1, name: "The dashboard stopped rendering" }),
    ).toBeVisible()
    await expect(page.getByText("Fixture: the layout threw")).toBeVisible()
    await expect(page.locator("html")).toHaveClass(/dark/)
    await expect(page).toHaveTitle("Something went wrong · Just Dashboard")
    // The stylesheet came with it: the page is on the product's dark ground.
    const ground = await page
      .locator(".auth-backdrop")
      .evaluate((node) => getComputedStyle(node).backgroundColor)
    expect(ground).not.toBe("rgba(0, 0, 0, 0)")
    await expect(page.getByRole("button", { name: "Try again" })).toBeEnabled()
    expect(violations).toEqual([])
  })
})

test.describe("the tab's title", () => {
  test("names the page, and follows the reader between pages", async ({ page }) => {
    await mockShell(page)
    await page.goto("/account")
    await expect(page).toHaveTitle("Profile · Just Dashboard")
    await page.goto("/files")
    await expect(page).toHaveTitle("Files · Just Dashboard")
  })

  test("the sign-in page keeps the product's name", async ({ page }) => {
    await page.route("**/api/v1/**", (route) =>
      json(route, { error: { code: "unauthenticated", message: "Sign in" } }, 401),
    )
    await page.goto("/login")
    await expect(page).toHaveTitle("Just Dashboard")
  })

  test("changes on a client navigation and on going back", async ({ page }) => {
    await mockShell(page)
    await page.goto("/account")
    await expect(page).toHaveTitle("Profile · Just Dashboard")
    await page.getByRole("link", { name: "Sessions" }).first().click()
    await expect(page).toHaveURL(/\/account\/sessions$/)
    await expect(page).toHaveTitle("Sessions · Just Dashboard")
    await page.goBack()
    await expect(page).toHaveTitle("Profile · Just Dashboard")
  })

  test("a detail page takes its section, and an unknown address the product", async ({ page }) => {
    await mockProject(page)
    await page.goto("/deploy/7/logs")
    await expect(page).toHaveTitle("Deployments · Just Dashboard")
    await page.goto("/no-such-page")
    await expect(page).toHaveTitle("Just Dashboard")
  })
})

test.describe("a read that failed is not an empty list", () => {
  test("Docker cleanup says the preview failed, not that nothing is selected", async ({ page }) => {
    await mockShell(page)
    await page.route("**/api/v1/docker/containers/", (route) => json(route, DOCKER_CONTAINERS))
    // The panel is offered only when there is a gigabyte worth reclaiming.
    await page.route("**/api/v1/docker/disk-usage", (route) =>
      json(route, {
        ...DOCKER_DISK,
        buildCache: { ...DOCKER_DISK.buildCache, reclaimable: 2 * 1024 ** 3 },
      }),
    )
    let failing = true
    await page.route("**/api/v1/docker/cleanup/preview", (route) =>
      failing
        ? json(route, refused, 500)
        : json(route, {
            categories: [
              {
                key: "build-cache",
                label: "Build cache",
                items: 48,
                reclaimable: 2 * 1024 ** 3,
                recommended: true,
                destroys: false,
                cost: "The next build recreates its cache",
                examples: [],
              },
            ],
          }),
    )
    await page.goto("/docker")
    const panel = page.locator('[data-slot="panel"]').filter({ hasText: "Docker cleanup" })
    await expect(panel.getByRole("alert")).toContainText("The read was refused")
    await expect(panel.getByText("Nothing selected.")).toHaveCount(0)
    await expect(panel.getByRole("button", { name: "Reclaim" })).toHaveCount(0)

    failing = false
    await panel.getByRole("button", { name: "Try again" }).click()
    await expect(panel.getByRole("alert")).toHaveCount(0)
    await expect(panel.getByRole("button", { name: /Reclaim/ })).toBeEnabled()
  })

  test("Passkeys says the list could not be read, not that there are none", async ({ page }) => {
    await mockShell(page)
    await page.route("**/api/v1/account/passkeys", (route) => json(route, refused, 500))
    await page.goto("/account/security")
    const section = page.locator("section, div").filter({ hasText: "In place of the password" })
    await expect(page.getByText("could not read")).toBeVisible()
    await expect(page.getByRole("alert").filter({ hasText: "The read was refused" })).toBeVisible()
    await expect(section.getByText("none", { exact: true })).toHaveCount(0)
  })

  test("the archive confirmation names what it could not read", async ({ page }) => {
    await mockProject(page)
    for (const part of ["environments/12/triggers", "environments/12/schedules", "previews"]) {
      await page.route(`**/api/v1/deploy/7/${part}`, (route) => json(route, refused, 500))
    }
    await page.goto("/deploy/7/settings/danger")
    await page.getByRole("button", { name: "Archive", exact: true }).click()
    const dialog = page.getByRole("dialog", { name: "Archive api-production" })
    await expect(dialog).toContainText("Could not read")
    await expect(dialog).toContainText("webhooks · schedules · previews")
    await expect(dialog).not.toContainText("nothing is automatic")
  })
})

test.describe("a saved domain's password", () => {
  test("is never written to the tab's storage, and still reaches the server", async ({ page }) => {
    const mock = await mockProject(page)
    mock.configuration().domains[0].protection = { username: "team", hash: "fixture-sealed" }
    const puts: Record<string, unknown>[] = []
    await page.route("**/api/v1/deploy/7/environments/12/configuration", async (route) => {
      if (route.request().method() === "PUT") puts.push(route.request().postDataJSON())
      await route.fallback()
    })
    await page.goto("/deploy/7/settings/domains")

    const secret = "visitor-private-value"
    await page.locator("#domain-password-0").fill(secret)
    await expect(page.locator("#domain-password-0")).toHaveValue(secret)
    await page.locator("#domain-user-0").fill("team-two")
    const stored = () =>
      page.evaluate(
        () => JSON.stringify({ ...sessionStorage }) + JSON.stringify({ ...localStorage }),
      )
    expect(await stored()).toContain("team-two")
    expect(await stored()).not.toContain(secret)

    // A password alone is an edit worth saving, though the draft does not hold it.
    await page.locator("#domain-user-0").fill("team")
    await saveSettings(page)
    await expectSaved(page)
    await expect.poll(() => puts.length).toBe(1)
    expect(puts[0].domains).toEqual([
      expect.objectContaining({
        protection: { username: "team", hash: "fixture-sealed", password: secret },
      }),
    ])
    await expect(page.locator("#domain-password-0")).toHaveValue("")
    expect(await stored()).not.toContain(secret)
  })

  test("a draft from before the move is scrubbed on arrival", async ({ page }) => {
    const mock = await mockProject(page)
    mock.configuration().domains[0].protection = { username: "team", hash: "fixture-sealed" }
    await page.goto("/deploy/7/settings/domains")
    await page.locator("#domain-user-0").fill("team-two")
    const stored = await page.evaluate(() =>
      Object.entries(sessionStorage).find(([name]) => name.includes("settings.domains")),
    )
    expect(stored).toBeTruthy()
    const [name, value] = stored!
    const draft = JSON.parse(value)
    draft[0].protection.password = "left-over-private-value"
    await page.evaluate(([k, v]) => sessionStorage.setItem(k, v), [name, JSON.stringify(draft)])
    await page.reload()
    await expect(page.locator("#domain-user-0")).toHaveValue("team-two")
    await expect
      .poll(() => page.evaluate(() => JSON.stringify({ ...sessionStorage })))
      .not.toContain("left-over-private-value")
  })
})
