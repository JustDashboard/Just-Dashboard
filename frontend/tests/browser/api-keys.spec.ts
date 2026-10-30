import { expect, test, type Page } from "@playwright/test"
import { keyTokens, mockKeys } from "./api-keys-fixture"

const rows = (page: Page) => page.locator("#keys [data-slot=row]")
async function action(page: Page, name: string, label: string) {
  await page.getByRole("button", { name: `More actions for ${name}` }).click()
  await page.getByRole("menuitem", { name: label, exact: true }).click()
}

test("search and usage filters narrow keys, including owner names and inactive prefixes", async ({
  page,
}) => {
  await mockKeys(page)
  await page.goto("/account/keys")
  await expect(rows(page)).toHaveCount(4)
  await page.getByRole("combobox", { name: "Sort keys" }).click()
  await page.getByRole("option", { name: "Expiring first" }).click()
  await expect(rows(page).first()).toContainText("github-actions")
  await page
    .getByRole("group", { name: "Show", exact: true })
    .getByRole("button", { name: /Unused for 30 days/ })
    .click()
  await expect(rows(page)).toHaveCount(1)
  await expect(rows(page)).toContainText("ansible")
  await page.getByRole("button", { name: "Clear filters" }).click()
  await page.getByRole("textbox", { name: "Search keys" }).fill("Maria readonly")
  await expect(rows(page)).toHaveCount(1)
  await expect(rows(page)).toContainText("grafana")
  await page.getByRole("button", { name: /^Yours/ }).click()
  await expect(rows(page)).toHaveCount(0)
  await page.getByRole("button", { name: "Clear filters" }).click()
  await page.getByRole("textbox", { name: "Search keys" }).fill("0000abcd")
  await expect(page.getByRole("button", { name: "Hide inactive keys" })).toBeVisible()
  await expect(page.locator("[data-slot=row]", { hasText: "old-laptop" })).toBeVisible()
})

test("history folds inactive keys and record deletion is distinct from revocation", async ({
  page,
}) => {
  const { calls } = await mockKeys(page)
  await page.goto("/account/keys")
  await expect(page.locator("[data-slot=row]", { hasText: "old-laptop" })).toHaveCount(0)
  await page.getByRole("button", { name: "Show inactive keys" }).click()
  await action(page, "old-laptop", "Delete record")
  const dialog = page.getByRole("dialog", { name: "Delete key record" })
  await expect(dialog).toContainText("The audit trail is kept")
  await dialog.getByRole("button", { name: "Cancel" }).click()
  expect(calls).toHaveLength(0)
  await action(page, "old-laptop", "Delete record")
  await page.getByRole("dialog").getByRole("button", { name: "Delete record", exact: true }).click()
  await expect(page.locator("[data-slot=row]", { hasText: "old-laptop" })).toHaveCount(0)
  expect(calls).toEqual([{ method: "DELETE", path: "/tokens/4/record", body: undefined }])
  await action(page, "github-actions", "Revoke key")
  await expect(page.getByRole("dialog")).toContainText("Its record stays in history")
  await page.getByRole("dialog").getByRole("button", { name: "Revoke", exact: true }).click()
  await expect(rows(page)).toHaveCount(3)
  await expect(page.locator("[data-slot=row]", { hasText: "github-actions" })).toContainText(
    "revoked",
  )
  expect(calls.at(-1)?.path).toBe("/tokens/1")
})

test("replacement keeps the old key until the new secret is copied and revocation confirmed", async ({
  page,
  context,
}) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"])
  const { calls } = await mockKeys(page)
  await page.goto("/account/keys")
  await action(page, "github-actions", "Replace key")
  const editor = page.getByRole("dialog", { name: "Replace API key" })
  await expect(editor.getByLabel("Name", { exact: true })).toHaveValue("github-actions")
  await expect(editor).toContainText("keeps working until you revoke it")
  await expect(editor.getByRole("button", { name: /^admin/ })).toHaveCount(0)
  await editor.getByRole("button", { name: "Create replacement" }).click()
  const secret = page.getByRole("dialog", { name: "Your new key" })
  await expect(secret).toContainText("vpsd_synthetic_key_101")
  await expect(secret).toContainText("previous key still works")
  expect(calls).toHaveLength(1)
  expect(calls[0].body).toEqual({ name: "github-actions", role: "limited", ttlDays: 90 })
  await secret.getByRole("button", { name: "Copy", exact: true }).first().click()
  await expect
    .poll(() => page.evaluate(() => navigator.clipboard.readText()))
    .toBe("vpsd_synthetic_key_101")
  await secret.getByRole("button", { name: "Revoke previous key" }).click()
  const confirmation = page.getByRole("dialog", { name: "Revoke previous key", exact: true })
  await expect(confirmation).toContainText("vpsd_ab12cd34")
  await confirmation.getByRole("button", { name: "Cancel" }).click()
  expect(calls).toHaveLength(1)
  await secret.getByRole("button", { name: "Revoke previous key" }).click()
  await page
    .getByRole("dialog", { name: "Revoke previous key", exact: true })
    .getByRole("button", { name: "Revoke previous key" })
    .click()
  await expect(secret).toContainText("Previous key is inactive")
  await expect(secret).toContainText("vpsd_synthetic_key_101")
  expect(calls.at(-1)?.path).toBe("/tokens/1")
  await secret.getByRole("button", { name: "Done", exact: true }).click()
  await expect(page.getByRole("dialog")).toHaveCount(0)
  await expect(page.getByText("vpsd_synthetic_key_101", { exact: true })).toHaveCount(0)
})

test("replacing a metrics-only key makes another metrics-only key, never a wider one", async ({
  page,
}) => {
  const minutes = (ago: number) => new Date(Date.now() - ago * 60_000).toISOString()
  const { calls } = await mockKeys(page, "admin", [
    {
      id: 7,
      userId: 1,
      name: "prometheus",
      prefix: "vpsd_77aa88bb",
      role: "readonly",
      scope: "metrics.read",
      createdAt: minutes(60 * 24 * 12),
      lastUsedAt: minutes(2),
      revoked: false,
    },
  ])
  await page.goto("/account/keys")
  await expect(rows(page)).toContainText("metrics only")
  await action(page, "prometheus", "Replace key")
  const editor = page.getByRole("dialog", { name: "Replace API key" })
  await expect(editor).toContainText("Reads database metrics only")
  await expect(editor).not.toContainText("Read-only")
  await editor.getByRole("button", { name: "Create replacement" }).click()
  expect(calls[0].body).toEqual({ name: "prometheus", scope: "metrics.read", ttlDays: 90 })
  const secret = page.getByRole("dialog", { name: "Your new key" })
  await expect(secret).toContainText("/api/v1/metrics/databases")
  await expect(secret).not.toContainText("/api/v1/system/host")
})

test("replacing an MCP key makes another MCP key for the same databases, at its role", async ({
  page,
}) => {
  const mcpKey = {
    id: 8,
    userId: 1,
    name: "claude-code",
    prefix: "vpsd_88cc99dd",
    role: "limited" as const,
    scope: "mcp" as const,
    createdAt: new Date(Date.now() - 86_400_000).toISOString(),
    revoked: false,
    connections: [
      { connectionId: 1, access: "write" as const },
      { connectionId: 2, access: "read" as const },
      { connectionId: 7, access: "read" as const },
    ],
  }
  const { calls } = await mockKeys(page, "limited", [mcpKey])
  await page.goto("/account/keys")
  await expect(rows(page)).toContainText("MCP only")
  await action(page, "claude-code", "Replace key")
  const editor = page.getByRole("dialog", { name: "Replace API key" })
  await expect(editor).toContainText("Opens the MCP endpoint only")
  await editor.getByRole("button", { name: "Create replacement" }).click()
  // The replacement first reads which databases a key can still be given.
  await expect
    .poll(() => calls[0])
    .toEqual({
      method: "POST",
      path: "/tokens/mcp",
      // blog, protected since, is left behind: no MCP key reaches it.
      body: {
        name: "claude-code",
        role: "limited",
        ttlDays: 90,
        connections: [
          { connectionId: 1, access: "write" },
          { connectionId: 7, access: "read" },
        ],
        // The deployments as the key had them: none.
        deployments: "",
        deploymentIds: [],
        operations: false,
      },
    })
  const secret = page.getByRole("dialog", { name: "Your new key" })
  await expect(secret).toContainText("claude mcp add --transport http just-dashboard")
  await expect(secret).toContainText("/api/v1/mcp")
})

test("an MCP key replaced by an owner who can no longer change a database only reads", async ({
  page,
}) => {
  const { calls } = await mockKeys(page, "readonly", [
    {
      id: 8,
      userId: 1,
      name: "cursor",
      prefix: "vpsd_88cc99dd",
      role: "readonly",
      scope: "mcp",
      createdAt: new Date(Date.now() - 86_400_000).toISOString(),
      revoked: false,
      connections: [{ connectionId: 1, access: "write" }],
    },
  ])
  await page.goto("/account/keys")
  await action(page, "cursor", "Replace key")
  await page
    .getByRole("dialog", { name: "Replace API key" })
    .getByRole("button", { name: "Create replacement" })
    .click()
  await expect
    .poll(() => calls[0]?.body)
    .toEqual({
      name: "cursor",
      role: "readonly",
      ttlDays: 90,
      connections: [{ connectionId: 1, access: "read" }],
      deployments: "",
      deploymentIds: [],
      operations: false,
    })
})

test("an MCP key narrowed to chosen deployments is replaced by one narrowed to the same", async ({
  page,
}) => {
  const { calls } = await mockKeys(page, "admin", [
    {
      id: 8,
      userId: 1,
      name: "claude-code",
      prefix: "vpsd_88cc99dd",
      role: "admin",
      scope: "mcp",
      createdAt: new Date(Date.now() - 86_400_000).toISOString(),
      revoked: false,
      connections: [],
      deployments: "deploy",
      deploymentIds: [3, 8],
      operations: true,
    },
  ])
  await page.goto("/account/keys")
  await action(page, "claude-code", "Replace key")
  await page
    .getByRole("dialog", { name: "Replace API key" })
    .getByRole("button", { name: "Create replacement" })
    .click()
  await expect
    .poll(() => calls[0]?.body)
    .toEqual({
      name: "claude-code",
      role: "admin",
      ttlDays: 90,
      connections: [],
      deployments: "deploy",
      deploymentIds: [3, 8],
      operations: true,
    })
})

test("an administrator makes a deploy key for chosen deployments and is shown how jd uses it", async ({
  page,
}) => {
  const { calls } = await mockKeys(page, "admin", [])
  await page.goto("/account/keys")
  await page.getByRole("button", { name: "New key" }).click()
  const dialog = page.getByRole("dialog", { name: "New API key" })
  await dialog.getByLabel("Name", { exact: true }).fill("ci-shop")
  await dialog.getByRole("button", { name: /^Deploy key/ }).click()
  // The role cards give way to what the key reaches, and what it never does.
  await expect(dialog.getByRole("button", { name: /^admin/ })).toHaveCount(0)
  await expect(dialog.getByRole("region", { name: /It can.t/ })).toContainText("never a value")
  await expect(dialog.getByRole("region", { name: /It can.t/ })).toContainText("credentials")
  await expect(dialog).toContainText("change only in the dashboard")
  await dialog.getByRole("radio", { name: "Only these" }).click()
  const create = dialog.getByRole("button", { name: "Create key", exact: true })
  await expect(create).toBeDisabled()
  await dialog.getByRole("checkbox", { name: /status-page/ }).click()
  await dialog.getByRole("checkbox", { name: /^shop/ }).click()
  await dialog.getByRole("radio", { name: "30 days", exact: true }).click()
  await create.click()
  const secret = page.getByRole("dialog", { name: "Your new key" })
  await expect(secret).toContainText(/jd login https?:\/\/\S+ --key vpsd_synthetic_key_101/)
  await expect(secret.getByRole("link", { name: "download jd" })).toHaveAttribute(
    "href",
    "/api/v1/cli/jd",
  )
  const openapi = secret.getByRole("link", { name: "OpenAPI description" })
  await expect(openapi).toHaveAttribute("href", "/api/v1/deploy/openapi.json")
  // Following it must not leave the dialog that shows the secret once.
  await expect(openapi).toHaveAttribute("target", "_blank")
  expect(calls).toEqual([
    {
      method: "POST",
      path: "/tokens/deploy",
      body: { name: "ci-shop", ttlDays: 30, deploymentIds: [3, 8] },
    },
  ])
  await secret.getByRole("button", { name: "Done", exact: true }).click()
  const made = rows(page).filter({ hasText: "ci-shop" })
  await expect(made).toContainText("deploy only")
  await expect(made).toContainText("2 deployments")
})

test("a deploy key for every deployment is sent with no list", async ({ page }) => {
  const { calls } = await mockKeys(page, "admin", [])
  await page.goto("/account/keys")
  await page.getByRole("button", { name: "New key" }).click()
  const dialog = page.getByRole("dialog", { name: "New API key" })
  await dialog.getByLabel("Name", { exact: true }).fill("github-actions")
  await dialog.getByRole("button", { name: /^Deploy key/ }).click()
  await expect(dialog.getByRole("radio", { name: "Every deployment" })).toBeChecked()
  await dialog.getByRole("button", { name: "Create key", exact: true }).click()
  await expect(page.getByRole("dialog", { name: "Your new key" })).toContainText("jd login")
  expect(calls[0]).toEqual({
    method: "POST",
    path: "/tokens/deploy",
    body: { name: "github-actions", ttlDays: 90, deploymentIds: [] },
  })
  await page.getByRole("button", { name: "Done", exact: true }).click()
  await expect(rows(page).filter({ hasText: "github-actions" })).toContainText("every deployment")
})

test("only an administrator is offered a deploy key", async ({ page }) => {
  await mockKeys(page, "limited", [])
  await page.goto("/account/keys")
  await page.getByRole("button", { name: "New key" }).click()
  const dialog = page.getByRole("dialog", { name: "New API key" })
  await expect(dialog.getByRole("button", { name: /^limited/ })).toBeVisible()
  await expect(dialog.getByRole("button", { name: /^Deploy key/ })).toHaveCount(0)
  await expect(dialog).not.toContainText("jd")
})

test("replacing a deploy key makes another deploy key for the same deployments", async ({
  page,
}) => {
  const { calls } = await mockKeys(page, "admin", [
    {
      id: 9,
      userId: 1,
      name: "ci-shop",
      prefix: "vpsd_99ee00ff",
      role: "admin",
      scope: "deploy",
      deploymentIds: [3],
      createdAt: new Date(Date.now() - 86_400_000).toISOString(),
      revoked: false,
    },
  ])
  await page.goto("/account/keys")
  await expect(rows(page)).toContainText("deploy only")
  await expect(rows(page)).toContainText("1 deployment")
  await action(page, "ci-shop", "Replace key")
  const editor = page.getByRole("dialog", { name: "Replace API key" })
  await expect(editor).toContainText("Reaches the same deployments")
  await expect(editor.getByRole("button", { name: /^Deploy key/ })).toHaveCount(0)
  await expect(editor.getByRole("button", { name: /^admin/ })).toHaveCount(0)
  await editor.getByRole("button", { name: "Create replacement" }).click()
  await expect
    .poll(() => calls[0])
    .toEqual({
      method: "POST",
      path: "/tokens/deploy",
      body: { name: "ci-shop", ttlDays: 90, deploymentIds: [3] },
    })
  await expect(page.getByRole("dialog", { name: "Your new key" })).toContainText("jd login")
})

test("the first key keeps its one-time secret through refresh, then discards it on Done", async ({
  page,
}) => {
  const { calls } = await mockKeys(page, "admin", [])
  await page.goto("/account/keys")
  await page.getByRole("button", { name: "New key" }).click()
  await page.getByLabel("Name", { exact: true }).fill("first key")
  await page.getByRole("radio", { name: "7 days", exact: true }).click()
  await page.getByRole("button", { name: "Create key", exact: true }).click()
  await expect(rows(page)).toHaveCount(1)
  await expect(page.getByRole("dialog", { name: "Your new key" })).toContainText(
    "vpsd_synthetic_key_101",
  )
  expect(calls[0].body).toEqual({ name: "first key", role: "readonly", ttlDays: 7 })
  await page.getByRole("button", { name: "Done", exact: true }).click()
  await page.getByRole("button", { name: "New key" }).click()
  await expect(page.getByLabel("Name", { exact: true })).toHaveValue("")
  await expect(page.getByRole("dialog")).not.toContainText("vpsd_synthetic_key_101")
})

test("failed replacement keeps the old key and an editable form", async ({ page }) => {
  const { calls } = await mockKeys(page)
  await page.route("**/api/v1/tokens/", async (route) => {
    if (route.request().method() !== "POST") return route.fallback()
    return route.fulfill({
      status: 500,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "internal", message: "Could not create key" } }),
    })
  })
  await page.goto("/account/keys")
  await action(page, "github-actions", "Replace key")
  await page.getByRole("button", { name: "Create replacement" }).click()
  await expect(page.getByText("Could not create the key", { exact: true })).toBeVisible()
  await expect(page.getByRole("dialog", { name: "Replace API key" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Create replacement" })).toBeEnabled()
  await page.keyboard.press("Escape")
  expect(calls).toHaveLength(0)
  await expect(rows(page).filter({ hasText: "github-actions" })).toContainText("expires")
})

test("pending creation cannot close or submit twice", async ({ page }) => {
  const { calls } = await mockKeys(page)
  let release: () => void = () => {}
  const wait = new Promise<void>((resolve) => {
    release = resolve
  })
  await page.route("**/api/v1/tokens/", async (route) => {
    if (route.request().method() === "POST") await wait
    return route.fallback()
  })
  await page.goto("/account/keys")
  await page.getByRole("button", { name: "New key" }).click()
  await page.getByLabel("Name", { exact: true }).fill("pending key")
  await page.getByRole("button", { name: "Create key", exact: true }).click()
  await expect(page.getByRole("button", { name: "Create key", exact: true })).toBeDisabled()
  await expect(page.getByRole("dialog").getByRole("button", { name: /^admin/ })).toBeDisabled()
  await page.keyboard.press("Escape")
  await expect(page.getByRole("dialog", { name: "New API key" })).toBeVisible()
  release()
  await expect(page.getByRole("dialog", { name: "Your new key" })).toContainText(
    "vpsd_synthetic_key_101",
  )
  expect(calls).toHaveLength(1)
})

test("read-only owners can replace keys within their role but cannot delete records", async ({
  page,
}) => {
  await mockKeys(page, "readonly")
  await page.goto("/account/keys")
  await action(page, "github-actions", "Replace key")
  await expect(page.getByRole("dialog")).toContainText("Look at everything, change nothing")
  await expect(page.getByRole("dialog").getByText("Start and stop", { exact: true })).toHaveCount(0)
  await page.keyboard.press("Escape")
  await page.getByRole("button", { name: "Show inactive keys" }).click()
  await page.getByRole("button", { name: "More actions for old-laptop" }).click()
  await expect(page.getByRole("menuitem", { name: "Delete record" })).toHaveCount(0)
  await expect(page.getByRole("menuitem", { name: "Replace key" })).toBeVisible()
})

test("an administrator cannot mint a replacement as a different owner", async ({ page }) => {
  await mockKeys(page)
  await page.goto("/account/keys")
  await page.getByRole("button", { name: "More actions for grafana" }).click()
  await expect(page.getByRole("menuitem", { name: "Replace key" })).toHaveCount(0)
  await expect(page.getByRole("menuitem", { name: "Revoke key" })).toBeVisible()
})

test("search and the key menu work from the keyboard", async ({ page }) => {
  const { calls } = await mockKeys(page)
  await page.goto("/account/keys")
  await page.getByRole("textbox", { name: "Search keys" }).focus()
  await page.keyboard.type("ansible")
  await expect(rows(page)).toHaveCount(1)
  const menu = page.getByRole("button", { name: "More actions for ansible" })
  await menu.focus()
  await page.keyboard.press("Enter")
  await expect(page.getByRole("menuitem", { name: "Replace key" })).toBeFocused()
  await page.keyboard.press("Enter")
  await expect(page.getByRole("dialog", { name: "Replace API key" })).toBeVisible()
  await expect(page.getByLabel("Name", { exact: true })).toBeFocused()
  await page.keyboard.press("Escape")
  await expect(page.getByRole("dialog")).toHaveCount(0)
  expect(calls).toHaveLength(0)
})

test("a failed record deletion leaves the record and confirmation available", async ({ page }) => {
  await mockKeys(page)
  await page.route("**/api/v1/tokens/4/record", (route) =>
    route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "token_active", message: "Revoke this key first." } }),
    }),
  )
  await page.goto("/account/keys")
  await page.getByRole("button", { name: "Show inactive keys" }).click()
  await action(page, "old-laptop", "Delete record")
  await page.getByRole("dialog").getByRole("button", { name: "Delete record", exact: true }).click()
  await expect(page.getByRole("dialog", { name: "Delete key record" })).toBeVisible()
  await expect(page.getByText("Delete key record failed", { exact: true })).toBeVisible()
  await expect(page.locator("[data-slot=row]", { hasText: "old-laptop" })).toBeAttached()
})

for (const width of [390, 1280, 1720]) {
  test(`key management fits at ${width} with reachable actions and dialogs`, async ({ page }) => {
    await mockKeys(
      page,
      "admin",
      keyTokens.map((token) =>
        token.id === 2
          ? { ...token, name: "backup-cron-with-a-long-recognisable-name-for-this-server" }
          : token,
      ),
    )
    await page.setViewportSize({ width, height: 1000 })
    await page.goto("/account/keys")
    await page.getByRole("button", { name: "Show inactive keys" }).click()
    await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth > document.documentElement.clientWidth,
      ),
    ).toBe(false)
    await action(page, "github-actions", "Replace key")
    const modal = page.getByRole("dialog", { name: "Replace API key" })
    await expect(modal.getByRole("button", { name: "Create replacement" })).toBeInViewport()
    const bounds = await modal.boundingBox()
    expect(bounds!.x).toBeGreaterThanOrEqual(0)
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width)
    await page.keyboard.press("Escape")
    // The deploy key's form — two kinds, a checklist and two lists — fits too.
    await page.getByRole("button", { name: "New key" }).click()
    const creating = page.getByRole("dialog", { name: "New API key" })
    await creating.getByRole("button", { name: /^Deploy key/ }).click()
    await creating.getByRole("radio", { name: "Only these" }).click()
    await expect(creating.getByRole("checkbox", { name: /status-page/ })).toBeVisible()
    const deployBounds = await creating.boundingBox()
    expect(deployBounds!.x).toBeGreaterThanOrEqual(0)
    expect(deployBounds!.x + deployBounds!.width).toBeLessThanOrEqual(width)
    expect(
      await creating.evaluate((node) =>
        [...node.querySelectorAll("*")].some(
          (child) =>
            child.scrollWidth > child.clientWidth + 1 &&
            getComputedStyle(child).overflowX === "auto",
        ),
      ),
    ).toBe(false)
  })
}
