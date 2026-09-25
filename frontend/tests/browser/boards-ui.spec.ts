import { expect, test } from "@playwright/test"

const now = new Date().toISOString()

test("a board opens in the dashboard, inserts a server card, and saves its scene", async ({
  page,
}) => {
  let scene: Record<string, unknown> = { elements: [], appState: {}, files: {} }
  let revision = 1
  let name = "Untitled board"
  const externalFonts: string[] = []
  page.on("request", (request) => {
    if (request.url().includes("esm.run/@excalidraw")) externalFonts.push(request.url())
  })

  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    const json = (body: unknown, status = 200) =>
      route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
    if (path === "/auth/session") {
      return json({
        authenticated: true,
        needsTotp: false,
        needsEnrollment: false,
        require2fa: false,
        capabilities: ["read", "service.control", "destructive", "system.admin"],
        user: { id: 1, username: "operator", role: "admin", totpEnabled: true },
      })
    }
    if (path === "/boards/" && request.method() === "GET") return json([])
    if (path === "/boards/" && request.method() === "POST") {
      return json({ id: 1, name, revision, scene, createdAt: now, updatedAt: now }, 201)
    }
    if (path === "/boards/1" && request.method() === "GET") {
      return json({ id: 1, name, revision, scene, createdAt: now, updatedAt: now })
    }
    if (path === "/boards/1" && request.method() === "PUT") {
      const body = request.postDataJSON() as {
        name: string
        revision: number
        scene: Record<string, unknown>
      }
      expect(body.revision).toBe(revision)
      name = body.name
      scene = body.scene
      revision++
      return json({ id: 1, name, revision, createdAt: now, updatedAt: now })
    }
    if (path === "/databases/fleet") {
      return json({
        connections: [{ id: 7, name: "Orders", driver: "postgres", ok: true }],
        unreachable: [],
        needsCredentials: [],
        checkedAt: now,
      })
    }
    if (path === "/deploy/") return json([{ id: 4, name: "Storefront", enabled: true }])
    return json([])
  })

  await page.goto("/boards")
  await page.getByRole("button", { name: "New board" }).first().click()
  await expect(page).toHaveURL(/\/boards\/1$/)
  await expect(page.getByLabel("Board name")).toHaveValue("Untitled board")
  await page.getByRole("button", { name: "Add server item" }).click()
  await page.getByRole("button", { name: /This server/ }).click()
  await page.getByRole("button", { name: "Add server item" }).click()
  await page.getByRole("button", { name: /Storefront/ }).click()
  await page.getByRole("button", { name: "Add server item" }).click()
  await page.getByRole("button", { name: /Orders/ }).click()
  await expect.poll(() => (scene.elements as unknown[]).length).toBeGreaterThanOrEqual(6)
  await expect.poll(() => JSON.stringify(scene)).toContain('"kind":"server"')
  await expect.poll(() => JSON.stringify(scene)).toContain('"kind":"project","resourceId":4')
  await expect.poll(() => JSON.stringify(scene)).toContain('"kind":"database","resourceId":7')
  await expect(page.getByRole("status")).toContainText("Saved on this server")
  await page.reload()
  await expect(page.getByLabel("Board name")).toHaveValue("Untitled board")
  await page.setViewportSize({ width: 375, height: 812 })
  await expect(page.getByRole("button", { name: "Add server item" })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(375)
  expect(externalFonts).toEqual([])
})
