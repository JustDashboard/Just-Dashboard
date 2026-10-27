import { expect, test, type Page } from "@playwright/test"
import { json, mockProject, now } from "./deploy-fixture"

/**
 * Secret stores (`/deploy/secret-stores`): the Vault, OpenBao, Infisical and
 * Doppler connections deployment variables read as `${{store.NAME/path#KEY}}`,
 * and the picker an environment's variable reads one through. The shared
 * fixture lists none; each test routes `/deploy/secret-stores` itself.
 */

const minutesAgo = (minutes: number) => new Date(Date.now() - minutes * 60_000).toISOString()

const bao = {
  id: 3,
  name: "bao-prod",
  kind: "openbao",
  address: "https://bao.example.test:8200",
  settings: { mount: "secret", auth: "approle", authMount: "approle" },
  credentialNames: ["roleId", "secretId"],
  allowPreviews: false,
  createdBy: "operator",
  updatedBy: "operator",
  createdAt: now,
  updatedAt: now,
  test: { at: minutesAgo(30), ok: true, message: "Signed in as approle with default, web" },
  lastRead: {
    at: minutesAgo(2),
    ok: false,
    message: "AppRole sign-in refused: invalid role or secret ID (400)",
  },
  usedBy: [
    {
      projectId: 7,
      projectName: "api-production",
      environmentId: 12,
      environmentName: "production",
      environmentKind: "production",
      variable: "DATABASE_URL",
      reference: "bao-prod/apps/web#DATABASE_URL",
    },
  ],
}

const infisical = {
  ...bao,
  id: 4,
  name: "infisical",
  kind: "infisical",
  address: "https://app.infisical.com",
  settings: { project: "4f1c2b7e-0d1a-4c55-9b0e-2f7d4f1a9c11", environment: "prod" },
  credentialNames: ["clientId", "clientSecret"],
  allowPreviews: true,
  test: { at: minutesAgo(1), ok: true, message: "Signed in; 12 secrets at / in prod" },
  lastRead: undefined,
  usedBy: [],
}

async function withStores(page: Page, stores: unknown[]) {
  await mockProject(page)
  const writes: { method: string; path: string; body: unknown }[] = []
  let list = [...stores]
  await page.route("**/api/v1/deploy/secret-stores**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname
    if (request.method() === "GET") return json(route, list)
    const body = request.postDataJSON()
    writes.push({ method: request.method(), path, body })
    if (path.endsWith("/test")) {
      const probe = body as { path?: string; key?: string } | null
      return json(route, {
        ok: true,
        message: probe?.key
          ? `Signed in as approle with default, web; found ${probe.path}#${probe.key}, version 4`
          : "Signed in as approle with default, web",
        version: probe?.key ? "4" : undefined,
      })
    }
    if (request.method() === "POST") {
      const created = {
        ...bao,
        ...(body as object),
        id: 9,
        credentialNames: ["roleId", "secretId"],
        test: undefined,
        lastRead: undefined,
        usedBy: [],
      }
      list = [...list, created]
      return json(route, created)
    }
    return json(route, list[0] ?? {})
  })
  return writes
}

function storeRow(page: Page, name: string) {
  return page
    .getByRole("list", { name: "Secret stores" })
    .locator("[data-slot=choice-row]", { hasText: name })
}

function overflow(page: Page) {
  return page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)
}

test("an empty page offers the four stores, and one is added with sealed AppRole credentials", async ({
  page,
}) => {
  const writes = await withStores(page, [])
  await page.goto("/deploy/secret-stores")
  await expect(page.getByRole("heading", { name: "Secret stores", exact: true })).toBeVisible()
  for (const name of ["HashiCorp Vault", "OpenBao", "Infisical", "Doppler"]) {
    await expect(page.getByRole("button", { name: `Add ${name}` })).toBeVisible()
  }
  await page.getByRole("button", { name: "Add OpenBao" }).click()
  const sheet = page.getByRole("dialog", { name: "Add store" })
  const save = sheet.getByRole("button", { name: "Add store" })
  await expect(save).toBeDisabled()
  await sheet.getByLabel("Name", { exact: true }).fill("Bao-Prod")
  // The name is what a reference says, so it is shown as one while typed.
  await expect(sheet.getByText("${{store.bao-prod/path#KEY}}")).toBeVisible()
  await sheet.getByLabel("Address", { exact: true }).fill("http://bao.lan:8200")
  await expect(sheet.getByText(/this address would send it in the clear/)).toBeVisible()
  await sheet.getByLabel("Address", { exact: true }).fill("https://bao.example.test:8200")
  await sheet.getByRole("radio", { name: "AppRole" }).click()
  await sheet.getByLabel("Role ID", { exact: true }).fill("fixture-role-id")
  await sheet.getByLabel("Secret ID", { exact: true }).fill("fixture-secret-id")
  await save.click()
  await expect.poll(() => writes.length).toBe(1)
  expect(writes[0].body).toEqual({
    name: "bao-prod",
    kind: "openbao",
    address: "https://bao.example.test:8200",
    settings: { auth: "approle", mount: "secret" },
    allowPreviews: false,
    credentials: { roleId: "fixture-role-id", secretId: "fixture-secret-id" },
  })
  // A new store stays open on its edit sheet, where Test is.
  await expect(page.getByRole("dialog", { name: "Edit bao-prod" })).toBeVisible()
  const storage = await page.evaluate(() =>
    JSON.stringify({ local: { ...localStorage }, session: { ...sessionStorage } }),
  )
  expect(storage).not.toContain("fixture-secret-id")
})

test("a store whose last read failed says so in its own words, and Test reaches the server", async ({
  page,
}) => {
  const writes = await withStores(page, [bao, infisical])
  await page.goto("/deploy/secret-stores")
  const row = storeRow(page, "bao-prod")
  await expect(
    row.getByText(
      /A run's read failed .*: AppRole sign-in refused: invalid role or secret ID \(400\)/,
    ),
  ).toBeVisible()
  await expect(row.locator(".text-warning")).toHaveCount(1)
  await expect(row.getByText("Read by api-production (production) as DATABASE_URL")).toBeVisible()
  await expect(row.getByText(/mount secret · AppRole/)).toBeVisible()
  const other = storeRow(page, "infisical")
  await expect(other.getByText(/Tested .*: Signed in; 12 secrets at \/ in prod/)).toBeVisible()
  await expect(other.getByText(/previews too/)).toBeVisible()
  await expect(other.getByText("No variable reads it yet")).toBeVisible()

  await row.getByRole("button", { name: "Test" }).click()
  await expect(page.getByText("bao-prod answered")).toBeVisible()
  await expect
    .poll(() => writes.map((write) => `${write.method} ${write.path}`))
    .toEqual(["POST /api/v1/deploy/secret-stores/3/test"])

  await page.setViewportSize({ width: 390, height: 844 })
  expect(await overflow(page)).toBeLessThanOrEqual(1)
})

test("an edit keeps the sealed credentials unless replaced, and tests a path and key", async ({
  page,
}) => {
  const writes = await withStores(page, [bao])
  await page.goto("/deploy/secret-stores")
  await page.getByRole("button", { name: "Edit bao-prod" }).click()
  const sheet = page.getByRole("dialog", { name: "Edit bao-prod" })
  await expect(sheet.getByText(/Signs in with role id and secret id, sealed\./)).toBeVisible()
  await expect(sheet.getByLabel("Name", { exact: true })).toBeDisabled()

  await sheet.getByLabel("Path", { exact: true }).fill("apps/web")
  await sheet.getByLabel("Key", { exact: true }).fill("DATABASE_URL")
  await sheet.getByRole("button", { name: "Test", exact: true }).click()
  await expect(sheet.getByRole("status")).toHaveText(
    "Signed in as approle with default, web; found apps/web#DATABASE_URL, version 4",
  )

  await sheet.getByRole("switch", { name: "Pull request previews" }).click()
  await sheet.getByRole("button", { name: "Save store" }).click()
  await expect.poll(() => writes.length).toBe(2)
  expect(writes[0].body).toEqual({ path: "apps/web", key: "DATABASE_URL" })
  const body = writes[1].body as Record<string, unknown>
  expect(writes[1].method).toBe("PUT")
  expect(body.credentials).toBeUndefined()
  expect(body.allowPreviews).toBe(true)
})

test("an environment's variable picks a store's key, becomes a secret, and its row names the store", async ({
  page,
}) => {
  await withStores(page, [bao])
  const puts: unknown[] = []
  await page.route("**/api/v1/deploy/7/environments/12/variables/DATABASE_URL", async (route) => {
    if (route.request().method() === "PUT") puts.push(route.request().postDataJSON())
    await route.fallback()
  })
  await page.goto("/deploy/7/settings/variables")
  await page.getByRole("button", { name: "Add variable" }).first().click()
  const editor = page.getByRole("dialog", { name: "Add variable" })
  await editor.getByLabel("Variable name").fill("database_url")
  await editor.getByRole("radio", { name: "Config" }).click()
  await editor.getByRole("button", { name: "Reference a stored value" }).click()
  await editor.getByLabel("Secret store").click()
  await page.getByRole("option", { name: /bao-prod/ }).click()
  await editor.getByLabel("Path", { exact: true }).fill("apps/web")
  await editor.getByLabel("Key", { exact: true }).fill("DATABASE_URL")
  await expect(editor.getByLabel("Typed reference")).toHaveValue(
    "${{store.bao-prod/apps/web#DATABASE_URL}}",
  )
  await editor.getByLabel("Version", { exact: true }).fill("4")
  await expect(editor.getByLabel("Typed reference")).toHaveValue(
    "${{store.bao-prod/apps/web#DATABASE_URL@4}}",
  )
  // A store's value is a secret, which the server requires the variable to be.
  await expect(editor.getByRole("radio", { name: "Secret" })).toHaveAttribute("data-state", "on")
  await editor.getByRole("button", { name: "Add variable" }).click()
  await expect(page.getByText("Variable saved")).toBeVisible()
  expect(puts.at(-1)).toMatchObject({
    reference: "${{store.bao-prod/apps/web#DATABASE_URL@4}}",
    sensitivity: "secret",
  })
  const row = page
    .getByRole("list", { name: "Environment variables" })
    .locator("li", { hasText: "DATABASE_URL" })
    .first()
  await expect(row.getByRole("link", { name: "bao-prod" })).toHaveAttribute(
    "href",
    "/deploy/secret-stores",
  )
  await expect(row.getByText("apps/web#DATABASE_URL@4")).toBeVisible()
})

test("secret stores at 1280 and 390 @shots", async ({ page }) => {
  test.skip(!process.env.JD_SHOTS, "JD_SHOTS names the directory the pictures go in")
  const shot = (name: string) =>
    page.screenshot({ path: `${process.env.JD_SHOTS}/${name}.png`, fullPage: true })
  await page.emulateMedia({ reducedMotion: "reduce" })
  await withStores(page, [bao, infisical])
  await page.setViewportSize({ width: 1280, height: 900 })
  await page.goto("/deploy/secret-stores")
  await expect(storeRow(page, "bao-prod")).toBeVisible()
  await shot("stores-mock-1280")
  await page.setViewportSize({ width: 390, height: 844 })
  await shot("stores-mock-390")
})
