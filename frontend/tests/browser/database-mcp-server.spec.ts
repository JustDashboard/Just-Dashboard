import { expect, test, type Locator, type Page } from "@playwright/test"
import { mockFleet, type FleetMock } from "./database-fleet-fixture"

/**
 * The MCP server, opened from the control center: the endpoint an AI client is
 * pointed at, the keys made for it, and the databases each key reaches. The
 * fleet's five connections are one of each engine the dialog treats
 * differently: shop (PostgreSQL) and notes (SQLite) can be given to a key;
 * blog (MariaDB) is protected here; cache (Redis) and app (MongoDB) speak no
 * SQL.
 */

const SECRET = "vpsd_Hc4pX7mQe2LwZ9tRb3NvK8sYa6JdUf1G"

function mcpKey(over: Record<string, unknown> = {}) {
  return {
    id: 9,
    userId: 1,
    name: "claude-code",
    prefix: "vpsd_Hc4pX7mQ",
    role: "admin",
    scope: "mcp",
    createdAt: "2026-10-01T09:00:00Z",
    revoked: false,
    connections: [{ connectionId: 1, access: "read" }],
    ...over,
  }
}

/** The fleet, with blog protected and the token routes answered from `tokens`. */
async function mockMcp(
  page: Page,
  tokens: Record<string, unknown>[] = [],
  options: FleetMock = {},
) {
  const server = await mockFleet(page, { rows: { 2: { readOnly: true } }, ...options })
  let listed = tokens
  server.answer("GET /tokens/", () => ({ body: listed }))
  server.answer("POST /tokens/mcp", ({ body }) => {
    const token = mcpKey({ id: 12, name: body.name, connections: body.connections })
    listed = [...listed, token]
    return { status: 201, body: { token, secret: SECRET } }
  })
  server.answer("PUT /tokens/9/connections", ({ body }) => {
    const token = mcpKey({ connections: body.connections })
    listed = listed.map((one) => (one.id === 9 ? token : one))
    return { body: token }
  })
  server.answer("DELETE /tokens/9", () => {
    listed = listed.map((one) => (one.id === 9 ? { ...one, revoked: true } : one))
    return { status: 204 }
  })
  return server
}

const dialogOf = (page: Page) => page.getByRole("dialog")
const databases = (dialog: Locator) => dialog.getByRole("list", { name: "Databases" })
const choice = (dialog: Locator, name: string) =>
  databases(dialog).getByRole("listitem").filter({ hasText: name })

async function open(page: Page) {
  await page.goto("/databases")
  await page.getByRole("button", { name: "MCP server" }).click()
  const dialog = dialogOf(page)
  await expect(dialog.getByText("Server URL")).toBeVisible()
  return dialog
}

test.describe("MCP server", () => {
  test("makes a key for the databases chosen, and writes each client's configuration", async ({
    page,
  }) => {
    const server = await mockMcp(page)
    const dialog = await open(page)
    const origin = new URL(page.url()).origin

    await expect(dialog).toContainText(`${origin}/api/v1/mcp`)
    await expect(dialog.getByText("No MCP keys yet.")).toBeVisible()

    await dialog.getByRole("button", { name: "New key" }).click()
    await expect(dialog.getByRole("heading", { name: "New MCP key" })).toBeVisible()
    await expect(choice(dialog, "blog")).toContainText("protected, so no MCP key reaches it")
    await expect(choice(dialog, "blog").getByRole("checkbox")).toBeDisabled()
    await expect(choice(dialog, "cache").getByRole("checkbox")).toBeDisabled()
    await expect(choice(dialog, "app").getByRole("checkbox")).toBeDisabled()
    await expect(choice(dialog, "cache")).toContainText("is not reachable over MCP")

    const create = dialog.getByRole("button", { name: "Create key" })
    await expect(create).toBeDisabled()
    await dialog.getByLabel("Name").fill("cursor")
    await choice(dialog, "shop").getByRole("checkbox").click()
    await choice(dialog, "shop").getByRole("radio", { name: "Change" }).click()
    await choice(dialog, "notes").getByRole("checkbox").click()
    await create.click()

    expect(server.bodies("POST /tokens/mcp")).toEqual([
      {
        name: "cursor",
        ttlDays: 90,
        connections: [
          { connectionId: 1, access: "write" },
          { connectionId: 7, access: "read" },
        ],
      },
    ])
    await expect(dialog.getByRole("heading", { name: "Your new MCP key" })).toBeVisible()
    await expect(dialog.getByText("stored hashed, so it cannot be shown again")).toBeVisible()
    const config = dialog.locator("[data-slot=well]").nth(1)
    await expect(config).toHaveText(
      `claude mcp add --transport http just-dashboard ${origin}/api/v1/mcp --header "Authorization: Bearer ${SECRET}"`,
    )
    await dialog.getByRole("radio", { name: "Cursor" }).click()
    await expect(config).toContainText('"mcpServers"')
    await expect(config).toContainText(`"Authorization": "Bearer ${SECRET}"`)
    await expect(dialog.getByText(".cursor/mcp.json")).toBeVisible()

    await dialog.getByRole("button", { name: "Done" }).click()
    const keys = dialog.getByRole("list", { name: "MCP keys" })
    await expect(keys.getByRole("listitem")).toHaveCount(1)
    await expect(keys).toContainText("cursor")
    await expect(keys).toContainText("reads notes, changes shop")
  })

  test("re-points a key without a new one, and revokes it", async ({ page }) => {
    const server = await mockMcp(page, [
      mcpKey(),
      // Neither is one of this account's MCP keys, so neither is listed here.
      mcpKey({ id: 10, name: "prometheus", scope: "metrics.read", connections: undefined }),
      mcpKey({ id: 11, userId: 2, name: "maria-cursor" }),
    ])
    const dialog = await open(page)
    const keys = dialog.getByRole("list", { name: "MCP keys" })
    await expect(keys.getByRole("listitem")).toHaveCount(1)
    await expect(keys).toContainText("reads shop")

    await keys.getByRole("button", { name: "Edit databases" }).click()
    await expect(dialog.getByRole("heading", { name: "Databases for claude-code" })).toBeVisible()
    await expect(choice(dialog, "shop").getByRole("checkbox")).toBeChecked()
    const save = dialog.getByRole("button", { name: "Save databases" })
    await expect(save).toBeDisabled()
    await choice(dialog, "notes").getByRole("checkbox").click()
    await save.click()
    expect(server.bodies("PUT /tokens/9/connections")).toEqual([
      {
        connections: [
          { connectionId: 1, access: "read" },
          { connectionId: 7, access: "read" },
        ],
      },
    ])
    await expect(keys).toContainText("reads notes, shop")

    await keys.getByRole("button", { name: "More actions for claude-code" }).click()
    await page.getByRole("menuitem", { name: "Revoke key" }).click()
    await page
      .getByRole("dialog", { name: "Revoke MCP key" })
      .getByRole("button", { name: "Revoke" })
      .click()
    await expect(dialog.getByText("No MCP keys yet.")).toBeVisible()
    expect(server.sent.map((one) => one.call)).toContain("DELETE /tokens/9")
  })

  test("a role that runs no SQL makes keys that read", async ({ page }) => {
    await mockMcp(page, [], { viewer: true })
    const dialog = await open(page)
    await expect(dialog.getByText("Your role runs no SQL")).toBeVisible()
    await dialog.getByRole("button", { name: "New key" }).click()
    await choice(dialog, "shop").getByRole("checkbox").click()
    await expect(choice(dialog, "shop").getByRole("radio", { name: "Change" })).toBeDisabled()
  })
})
