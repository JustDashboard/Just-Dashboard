import { expect, test, type Page, type Route } from "@playwright/test"
import { json, mockProject, now } from "./deploy-fixture"

/**
 * Shared variables: the server's on `/deploy/variables`, a project's under
 * its Variables settings, and the reference picker an environment's variable
 * reads one through. The shared fixture lists none; every test here routes
 * both scopes itself, statefully, over it.
 */

type Shared = {
  scope: "server" | "project"
  projectId?: number
  name: string
  sensitivity: "plain" | "secret"
  masked: string
  valueDigest: string
  reference: string
  updatedBy: string
  createdAt: string
  updatedAt: string
  usedBy: {
    projectId: number
    projectName: string
    environmentId: number
    environmentName: string
    environmentKind: string
    variable: string
    waiting: boolean
  }[]
}

function shared(
  scope: "server" | "project",
  name: string,
  sensitivity: "plain" | "secret",
  usedBy: Shared["usedBy"] = [],
): Shared {
  return {
    scope,
    ...(scope === "project" ? { projectId: 7 } : {}),
    name,
    sensitivity,
    masked: sensitivity === "secret" ? "••••••••" : "configured",
    valueDigest: `sha256:${"5".repeat(64)}`,
    reference: `\${{${scope}.${name}}}`,
    updatedBy: "operator",
    createdAt: now,
    updatedAt: now,
    usedBy,
  }
}

const reader = (projectId: number, projectName: string, variable: string, waiting = false) => ({
  projectId,
  projectName,
  environmentId: projectId * 10,
  environmentName: "production",
  environmentKind: "production",
  variable,
  waiting,
})

/** Both scopes, held in the test so a write reads back as the server would answer it. */
async function mockShared(page: Page) {
  const lists: Record<string, Shared[]> = {
    server: [
      shared("server", "SENTRY_DSN", "secret", [
        reader(7, "api-production", "SENTRY_DSN"),
        reader(8, "worker", "SENTRY_DSN"),
      ]),
      shared("server", "STRIPE_SECRET_KEY", "secret", [
        reader(7, "api-production", "STRIPE_SECRET_KEY", true),
      ]),
      shared("server", "PUBLIC_API_URL", "plain"),
    ],
    project: [shared("project", "AWS_REGION", "plain", [reader(7, "api-production", "REGION")])],
  }
  const writes: { path: string; method: string; body: unknown }[] = []
  const handle = (scope: "server" | "project") => async (route: Route) => {
    const request = route.request()
    const url = new URL(request.url())
    const rest = url.pathname.split("/shared-variables")[1] ?? ""
    const [, name, verb] = rest.split("/")
    const list = lists[scope]
    if (request.method() !== "GET") {
      writes.push({ path: url.pathname, method: request.method(), body: request.postDataJSON() })
    }
    if (!name) return json(route, list)
    const decoded = decodeURIComponent(name)
    const found = list.find((variable) => variable.name === decoded)
    if (verb === "reveal") {
      return json(route, { name: decoded, value: `revealed-${decoded.toLowerCase()}` })
    }
    if (request.method() === "PUT") {
      const body = request.postDataJSON() as { sensitivity: "plain" | "secret" }
      const next = { ...shared(scope, decoded, body.sensitivity, found?.usedBy ?? []) }
      lists[scope] = [...list.filter((variable) => variable.name !== decoded), next].sort((a, b) =>
        a.name.localeCompare(b.name),
      )
      return json(route, next)
    }
    if (request.method() === "DELETE") {
      if (found && found.usedBy.length > 0) {
        return route.fulfill({
          status: 409,
          contentType: "application/json",
          body: JSON.stringify({
            error: {
              code: "shared_variable_in_use",
              message: `shared variable is in use: ${decoded} is read by api-production (production) as ${found.usedBy[0].variable}; point that variable elsewhere first`,
            },
          }),
        })
      }
      lists[scope] = list.filter((variable) => variable.name !== decoded)
      return route.fulfill({ status: 204 })
    }
    return route.fallback()
  }
  await page.route("**/api/v1/deploy/shared-variables**", handle("server"))
  await page.route("**/api/v1/deploy/7/shared-variables**", handle("project"))
  return { writes }
}

function sharedRow(page: Page, list: string, name: string) {
  return page.getByRole("list", { name: list }).locator("[data-slot=choice-row]", { hasText: name })
}

function overflow(page: Page) {
  return page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
}

test("the server's shared variables name who reads each one and what is not live yet", async ({
  page,
}) => {
  await mockProject(page)
  const { writes } = await mockShared(page)
  await page.goto("/deploy/variables")

  await expect(page.getByRole("heading", { name: "Shared variables", exact: true })).toBeVisible()
  await expect(
    page.locator("[data-slot=panel-header]").filter({ hasText: "Server variables" }),
  ).toContainText("3 variables")
  const sentry = sharedRow(page, "Server variables", "SENTRY_DSN")
  await expect(sentry).toContainText("${{server.SENTRY_DSN}}")
  await expect(sentry).toContainText("Read by 2")
  await expect(sentry.getByRole("link", { name: "worker" })).toHaveAttribute(
    "href",
    "/deploy/8/settings/variables",
  )
  // A reader whose live release started before the change runs the earlier
  // value until it deploys again, in the settings pages' own words.
  const stripe = sharedRow(page, "Server variables", "STRIPE_SECRET_KEY")
  await expect(stripe.getByText("Saved · not live yet")).toBeVisible()
  await expect(stripe).toContainText("(not live yet)")
  await expect(sharedRow(page, "Server variables", "PUBLIC_API_URL")).toContainText("Not read")

  // Revealed in place, through the audited route.
  await sentry.getByRole("button", { name: "Actions for SENTRY_DSN" }).click()
  await page.getByRole("menuitem", { name: "Reveal value" }).click()
  await expect(sentry.getByText("revealed-sentry_dsn")).toBeVisible()

  // A new one: the sheet says how it will be read before it is saved.
  await page.getByRole("button", { name: "Add variable" }).click()
  const editor = page.getByRole("dialog", { name: "Add server variable" })
  await editor.getByLabel("Variable name").fill("mailgun_api_key")
  await expect(editor.getByText("${{server.MAILGUN_API_KEY}}")).toBeVisible()
  await editor.getByLabel("Value", { exact: true }).fill("key-123")
  await editor.getByRole("button", { name: "Add variable" }).click()
  await expect(page.getByText("Shared variable added")).toBeVisible()
  await expect(sharedRow(page, "Server variables", "MAILGUN_API_KEY")).toBeVisible()
  expect(writes.at(-1)).toEqual({
    path: "/api/v1/deploy/shared-variables/MAILGUN_API_KEY",
    method: "PUT",
    body: { sensitivity: "secret", value: "key-123" },
  })

  // Editing without retyping keeps the sealed value: no value is sent.
  await sharedRow(page, "Server variables", "PUBLIC_API_URL")
    .getByRole("button", {
      name: "Edit PUBLIC_API_URL",
    })
    .click()
  const edit = page.getByRole("dialog", { name: "Edit PUBLIC_API_URL" })
  await edit.getByRole("radio", { name: "Secret" }).click()
  await edit.getByRole("button", { name: "Save variable" }).click()
  await expect(page.getByText("Shared variable saved")).toBeVisible()
  expect(writes.at(-1)?.body).toEqual({ sensitivity: "secret" })

  // Removing one that is read is the server's to refuse, and its refusal
  // names the reader.
  await stripe.getByRole("button", { name: "Actions for STRIPE_SECRET_KEY" }).click()
  await page.getByRole("menuitem", { name: "Remove" }).click()
  await expect(page.getByRole("alertdialog").or(page.getByRole("dialog"))).toContainText(
    "The server refuses to remove it until that variable points elsewhere.",
  )
  await page.getByRole("button", { name: "Remove variable" }).click()
  await expect(page.getByText(/api-production \(production\) as STRIPE_SECRET_KEY/)).toBeVisible()
  // The dialog stays open over the refusal, and the variable stays listed.
  await page.getByRole("button", { name: "Cancel" }).click()
  await expect(sharedRow(page, "Server variables", "STRIPE_SECRET_KEY")).toBeVisible()

  await page.setViewportSize({ width: 390, height: 844 })
  expect(await overflow(page)).toBeLessThanOrEqual(1)
})

test("a project's variables page holds its shared ones and picks a shared reference by name", async ({
  page,
}) => {
  await mockProject(page)
  await mockShared(page)
  const puts: unknown[] = []
  await page.route("**/api/v1/deploy/7/environments/12/variables/STRIPE_KEY", async (route) => {
    if (route.request().method() === "PUT") puts.push(route.request().postDataJSON())
    await route.fallback()
  })
  await page.goto("/deploy/7/settings/variables")

  await expect(page.getByRole("heading", { name: "Project variables" })).toBeVisible()
  await expect(sharedRow(page, "Project variables", "AWS_REGION")).toContainText(
    "${{project.AWS_REGION}}",
  )

  await page.getByRole("button", { name: "Add variable" }).first().click()
  const editor = page.getByRole("dialog", { name: "Add variable" })
  await editor.getByLabel("Variable name").fill("stripe_key")
  await editor.getByRole("button", { name: "Reference a stored value" }).click()
  await editor.getByLabel("Shared variable").click()
  // The project's own come first, then the server's.
  await expect(page.getByRole("option").first()).toContainText("AWS_REGION")
  await page.getByRole("option", { name: /STRIPE_SECRET_KEY/ }).click()
  await expect(editor.getByLabel("Typed reference")).toHaveValue("${{server.STRIPE_SECRET_KEY}}")
  // Reading a secret makes the variable one, which the server requires.
  await expect(editor.getByRole("radio", { name: "Secret" })).toHaveAttribute("data-state", "on")
  await editor.getByRole("button", { name: "Add variable" }).click()
  await expect(page.getByText("Variable saved")).toBeVisible()
  expect(puts.at(-1)).toMatchObject({
    reference: "${{server.STRIPE_SECRET_KEY}}",
    sensitivity: "secret",
  })

  const row = page
    .getByRole("list", { name: "Environment variables" })
    .locator("li", { hasText: "STRIPE_KEY" })
  await expect(row.getByRole("link", { name: "server.STRIPE_SECRET_KEY" })).toHaveAttribute(
    "href",
    "/deploy/variables",
  )
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await overflow(page)).toBeLessThanOrEqual(1)
})

test("shared variables at 1280 and 390 @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "JD_SHOTS names the directory the pictures go in")
  const shot = (name: string) =>
    page.screenshot({ path: `${process.env.JD_SHOTS}/${name}.png`, fullPage: true })
  // Pictures of the settled page, not of rows still rising into place.
  await page.emulateMedia({ reducedMotion: "reduce" })
  await mockProject(page, { showcase: true })
  await mockShared(page)

  await page.setViewportSize({ width: 1280, height: 900 })
  await page.goto("/deploy/variables")
  const sentry = sharedRow(page, "Server variables", "SENTRY_DSN")
  await sentry.getByRole("button", { name: "Actions for SENTRY_DSN" }).click()
  await page.getByRole("menuitem", { name: "Reveal value" }).click()
  await expect(sentry.getByText("revealed-sentry_dsn")).toBeVisible()
  await shot("shared-server-1280")

  await page.getByRole("button", { name: "Add variable" }).click()
  const editor = page.getByRole("dialog", { name: "Add server variable" })
  await editor.getByLabel("Variable name").fill("mailgun_api_key")
  await editor.getByLabel("Value", { exact: true }).fill("key-123")
  await shot("shared-add-1280")
  await editor.getByRole("button", { name: "Cancel" }).click()

  await page.goto("/deploy/7/settings/variables")
  await page.getByRole("heading", { name: "Project variables" }).scrollIntoViewIfNeeded()
  await shot("shared-project-1280")

  await page.getByRole("button", { name: "Add variable" }).first().click()
  const variable = page.getByRole("dialog", { name: "Add variable" })
  await variable.getByLabel("Variable name").fill("stripe_key")
  await variable.getByRole("button", { name: "Reference a stored value" }).click()
  await variable.getByLabel("Shared variable").click()
  await page.getByRole("option", { name: /STRIPE_SECRET_KEY/ }).click()
  await expect(page.getByRole("listbox")).toHaveCount(0)
  await shot("shared-picker-1280")
  await variable.getByRole("button", { name: "Add variable" }).click()
  await expect(page.getByText("Variable saved")).toBeVisible()
  await page.getByRole("list", { name: "Environment variables" }).scrollIntoViewIfNeeded()
  await shot("shared-reference-row-1280")

  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto("/deploy/variables")
  await expect(sharedRow(page, "Server variables", "SENTRY_DSN")).toBeVisible()
  await shot("shared-server-390")
})
