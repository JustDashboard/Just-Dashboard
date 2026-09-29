import { expect, test, type Page } from "@playwright/test"

const now = new Date().toISOString()
const pixel =
  "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

type Scene = {
  elements: unknown[]
  appState: Record<string, unknown>
  files: Record<string, unknown>
}
type Put = { name: string; revision: number; scene: Scene }
type Refusal = { status: number; code: string; message: string }

const isObject = (value: unknown) =>
  typeof value === "object" && value !== null && !Array.isArray(value)

/**
 * The boards API as the Go handler behaves: names are trimmed and required, a
 * scene without elements, appState and files is refused, and a stale revision
 * is a conflict. A mock that accepted anything is how a save that could never
 * succeed against the real server passed this spec.
 */
async function mockBoards(page: Page, scene: Scene = { elements: [], appState: {}, files: {} }) {
  const board = {
    name: "Untitled board",
    revision: 1,
    scene,
    puts: [] as Put[],
    refuse: null as Refusal | null,
    deletedWith: null as string | null,
  }
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const path = new URL(request.url()).pathname.replace(/^\/api\/v1/, "")
    const json = (body: unknown, status = 200) =>
      route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
    const refuse = ({ status, code, message }: Refusal) =>
      json({ error: { code, message } }, status)
    const detail = () => ({ id: 1, name: board.name, revision: board.revision, createdAt: now })
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
    if (path === "/boards/" && request.method() === "GET") {
      return json(board.deletedWith === null ? [{ ...detail(), updatedAt: now }] : [])
    }
    if (path === "/boards/" && request.method() === "POST") {
      return json({ ...detail(), scene: board.scene, updatedAt: now }, 201)
    }
    if (path === "/boards/1" && request.method() === "GET") {
      return json({ ...detail(), scene: board.scene, updatedAt: now })
    }
    if (path === "/boards/1" && request.method() === "PUT") {
      const body = request.postDataJSON() as Put
      board.puts.push(body)
      if (board.refuse) return refuse(board.refuse)
      const name = body.name.trim()
      if (!name) {
        return refuse({
          status: 400,
          code: "bad_request",
          message: "board name must be between 1 and 100 characters",
        })
      }
      const s = body.scene as unknown as Record<string, unknown>
      if (
        !isObject(s) ||
        !Array.isArray(s.elements) ||
        !isObject(s.appState) ||
        !isObject(s.files)
      ) {
        return refuse({
          status: 400,
          code: "bad_request",
          message:
            "board revision and scene are required; scene must contain elements, appState, and files",
        })
      }
      if (body.revision !== board.revision) {
        return refuse({ status: 409, code: "board_conflict", message: "board changed" })
      }
      board.name = name
      board.scene = body.scene
      board.revision++
      return json({ ...detail(), updatedAt: now })
    }
    if (path === "/boards/1" && request.method() === "DELETE") {
      board.deletedWith = board.name
      return route.fulfill({ status: 204 })
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
  return board
}

async function openBoard(page: Page) {
  await page.goto("/boards/1")
  await expect(page.getByLabel("Board name")).toHaveValue("Untitled board")
  await expect(page.locator("canvas").first()).toBeVisible()
}

test("a board opens without saving, keeps its images, and saves inserted server cards", async ({
  page,
}) => {
  const board = await mockBoards(page, {
    elements: [
      {
        type: "image",
        id: "photo",
        fileId: "pixel",
        status: "saved",
        scale: [1, 1],
        x: 40,
        y: 40,
        width: 64,
        height: 64,
      },
    ],
    appState: { viewBackgroundColor: "#121212" },
    files: { pixel: { id: "pixel", mimeType: "image/png", dataURL: pixel, created: 1 } },
  })
  const externalFonts: string[] = []
  page.on("request", (request) => {
    if (request.url().includes("esm.run/@excalidraw")) externalFonts.push(request.url())
  })

  await page.goto("/boards")
  await page.getByRole("link", { name: /Untitled board/ }).click()
  await expect(page).toHaveURL(/\/boards\/1$/)
  await expect(page.getByLabel("Board name")).toHaveValue("Untitled board")
  await expect(page.locator("canvas").first()).toBeVisible()
  // Opening is not an edit. A save here bumped the revision on every visit, so
  // two people merely looking at one board put the second into a conflict.
  await page.mouse.move(600, 500)
  await page.mouse.wheel(0, 200)
  await page.waitForTimeout(1500)
  expect(board.puts).toHaveLength(0)
  await expect(page.getByRole("status")).toContainText("Saved on this server")

  await page.getByRole("button", { name: "Add server item" }).click()
  await page.getByRole("button", { name: /This server/ }).click()
  await page.getByRole("button", { name: "Add server item" }).click()
  await page.getByRole("button", { name: /Storefront/ }).click()
  await page.getByRole("button", { name: "Add server item" }).click()
  await page.getByRole("button", { name: /Orders/ }).click()
  await expect.poll(() => board.scene.elements.length).toBeGreaterThanOrEqual(7)
  await expect.poll(() => JSON.stringify(board.scene)).toContain('"kind":"server"')
  await expect.poll(() => JSON.stringify(board.scene)).toContain('"kind":"project","resourceId":4')
  await expect.poll(() => JSON.stringify(board.scene)).toContain('"kind":"database","resourceId":7')
  await expect(page.getByRole("status")).toContainText("Saved on this server")
  expect(board.scene.files).toMatchObject({ pixel: { dataURL: pixel } })
  expect(board.scene.appState).toMatchObject({ viewBackgroundColor: "#121212" })

  const saves = board.puts.length
  await page.reload()
  await expect(page.getByLabel("Board name")).toHaveValue("Untitled board")
  await expect(page.locator("canvas").first()).toBeVisible()
  await page.waitForTimeout(1500)
  expect(board.puts).toHaveLength(saves)
  await page.setViewportSize({ width: 375, height: 812 })
  await expect(page.getByRole("button", { name: "Add server item" })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(375)
  expect(externalFonts).toEqual([])
})

test("the name is saved trimmed, never blank, Ctrl+S saves now, and delete uses ordinary confirmation", async ({
  page,
}) => {
  const board = await mockBoards(page)
  const downloads: string[] = []
  page.on("download", (download) => downloads.push(download.suggestedFilename()))
  await openBoard(page)
  const name = page.getByLabel("Board name")
  const status = page.getByRole("status")

  await name.fill("")
  await expect(status).toContainText("Name the board to save it")
  await page.waitForTimeout(1500)
  expect(board.puts).toHaveLength(0)
  await name.blur()
  await expect(name).toHaveValue("Untitled board")
  await expect(status).toContainText("Saved on this server")

  await name.fill("  Ops map  ")
  const saved = page.waitForRequest((request) => request.method() === "PUT", { timeout: 600 })
  await page.keyboard.press("ControlOrMeta+s")
  await saved
  await expect(status).toContainText("Saved on this server")
  expect(board.name).toBe("Ops map")
  expect(downloads).toEqual([])
  await name.blur()
  await expect(name).toHaveValue("Ops map")

  // On a Russian layout the S key reports "ы"; the browser still saves the
  // page on Ctrl+S, so the board has to answer to the physical key.
  await name.fill("Ops map 2")
  const russian = page.waitForRequest((request) => request.method() === "PUT", { timeout: 600 })
  await name.dispatchEvent("keydown", { key: "ы", code: "KeyS", ctrlKey: true, bubbles: true })
  await russian
  await expect.poll(() => board.name).toBe("Ops map 2")

  await page.getByRole("button", { name: "Delete board" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByPlaceholder("Type the phrase above")).toHaveCount(0)
  await dialog.getByRole("button", { name: "Delete board" }).click()
  await expect(page).toHaveURL(/\/boards$/)
  expect(board.deletedWith).toBe("Ops map 2")
})

test("a failed save lets the operator leave on purpose, and a board deleted elsewhere stops autosave", async ({
  page,
}) => {
  const board = await mockBoards(page)
  await openBoard(page)
  const name = page.getByLabel("Board name")
  const status = page.getByRole("status")

  board.refuse = { status: 500, code: "internal", message: "database is locked" }
  await name.fill("Rack layout")
  await expect(status).toContainText("Save failed")
  await page.getByRole("button", { name: "Back to boards" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("Leave without saving?")
  await dialog.getByRole("button", { name: "Stay" }).click()
  await expect(page).toHaveURL(/\/boards\/1$/)
  const failure = page.getByText("Board was not saved")
  await expect(failure).toBeVisible()

  board.refuse = null
  await page.getByRole("button", { name: "Save", exact: true }).click()
  await expect(status).toContainText("Saved on this server")
  await expect(failure).toBeHidden()

  board.refuse = { status: 404, code: "board_not_found", message: "board not found" }
  await name.fill("Rack layout 2")
  await expect(status).toContainText("This board was deleted")
  const attempts = board.puts.length
  await name.fill("Rack layout 3")
  await page.waitForTimeout(1500)
  expect(board.puts).toHaveLength(attempts)

  await page.getByRole("button", { name: "Back to boards" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Leave without saving" }).click()
  await expect(page).toHaveURL(/\/boards$/)
})
