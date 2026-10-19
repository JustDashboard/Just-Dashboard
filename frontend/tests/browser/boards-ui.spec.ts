import { expect, test, type Page } from "@playwright/test"
import { deployment } from "./deploy-fixture"

const now = new Date().toISOString()
const pixel =
  "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

type Scene = {
  elements: unknown[]
  appState: Record<string, unknown>
  files: Record<string, unknown>
}
type Put = { name: string; revision: number; scene: Scene; merge?: boolean }
type Refusal = { status: number; code: string; message: string }
type BoardPreview = {
  background: string
  count: number
  elements: Record<string, unknown>[]
  resources: { kind: string; id?: number }[]
}

const isObject = (value: unknown) =>
  typeof value === "object" && value !== null && !Array.isArray(value)

/**
 * The boards API as the Go handler behaves: names are trimmed and required, a
 * scene without elements, appState and files is refused, and a stale revision
 * is a conflict. A mock that accepted anything is how a save that could never
 * succeed against the real server passed this spec. Images a scene still
 * carries are moved into the file store on save, as the handler moves them.
 */
async function mockBoards(page: Page, scene: Scene = { elements: [], appState: {}, files: {} }) {
  const board = {
    name: "Untitled board",
    revision: 1,
    scene,
    puts: [] as Put[],
    refuse: null as Refusal | null,
    deletedWith: null as string | null,
    /** What the server derives from the scene on save; the list draws it. */
    preview: undefined as BoardPreview | undefined,
    files: new Map<string, { type: string; body: Buffer }>(),
    fileReads: [] as string[],
    library: { libraryItems: [] as unknown[], revision: 0 },
    fetched: [] as string[],
    prompts: [] as string[],
  }
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const json = (body: unknown, status = 200) =>
      route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
    const refuse = ({ status, code, message }: Refusal) =>
      json({ error: { code, message } }, status)
    const detail = () => ({ id: 1, name: board.name, revision: board.revision, createdAt: now })
    const fileIds = () => [...board.files.keys()].sort()
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
      return json(
        board.deletedWith === null ? [{ ...detail(), updatedAt: now, preview: board.preview }] : [],
      )
    }
    if (path === "/boards/" && request.method() === "POST") {
      return json({ ...detail(), scene: board.scene, updatedAt: now }, 201)
    }
    if (path === "/boards/library" && request.method() === "GET") return json(board.library)
    if (path === "/boards/library" && request.method() === "PUT") {
      const body = request.postDataJSON() as { libraryItems: unknown[]; revision: number }
      if (body.revision !== board.library.revision) {
        return refuse({ status: 409, code: "library_conflict", message: "library changed" })
      }
      board.library = { libraryItems: body.libraryItems, revision: body.revision + 1 }
      return json({ revision: board.library.revision })
    }
    if (path === "/boards/library/fetch") {
      board.fetched.push((request.postDataJSON() as { url: string }).url)
      return json({ type: "excalidrawlib", version: 2, libraryItems: [libraryItem] })
    }
    if (path === "/boards/diagram") {
      board.prompts.push((request.postDataJSON() as { prompt: string }).prompt)
      return json({ mermaid: "flowchart LR\n  web[Storefront] --> db[(Orders)]" })
    }
    const file = path.match(/^\/boards\/1\/files\/(.+)$/)
    if (file && request.method() === "PUT") {
      const known = board.files.has(file[1])
      board.files.set(file[1], {
        type: request.headers()["content-type"],
        body: request.postDataBuffer() ?? Buffer.alloc(0),
      })
      return json({ id: file[1] }, known ? 200 : 201)
    }
    if (file && request.method() === "GET") {
      board.fileReads.push(file[1])
      const stored = board.files.get(file[1])
      if (!stored) return refuse({ status: 404, code: "board_file_not_found", message: "no file" })
      return route.fulfill({ status: 200, contentType: stored.type, body: stored.body })
    }
    if (path === "/system/host") return json({ hostname: "atlas", uptimeSeconds: 7200 })
    if (path === "/system/metrics") {
      return json({ cpu: { totalPercent: 12 }, memory: { usedPercent: 41 }, uptimeSeconds: 7200 })
    }
    if (path === "/boards/1" && request.method() === "GET") {
      return json({ ...detail(), scene: board.scene, fileIds: fileIds(), updatedAt: now })
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
      if (!body.merge && body.revision !== board.revision) {
        return refuse({ status: 409, code: "board_conflict", message: "board changed" })
      }
      const files: Record<string, unknown> = {}
      for (const [id, entry] of Object.entries(body.scene.files)) {
        const { mimeType, dataURL } = entry as { mimeType: string; dataURL: string }
        if (typeof dataURL !== "string" || !dataURL.startsWith(`data:${mimeType};base64,`)) {
          files[id] = entry
          continue
        }
        board.files.set(id, { type: mimeType, body: Buffer.from(dataURL.split(",")[1], "base64") })
      }
      board.name = name
      board.scene = { ...body.scene, files }
      board.revision++
      return json({
        ...detail(),
        fileIds: fileIds(),
        ...(body.merge && { remoteElements: [] }),
        updatedAt: now,
      })
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
    if (path === "/deploy/" && url.searchParams.get("view") === "fleet") {
      return json({ deployments: [{ ...deployment, id: 4, name: "Storefront" }], activeWork: [] })
    }
    if (path === "/deploy/") return json([{ id: 4, name: "Storefront", enabled: true }])
    return json([])
  })
  // A room nobody else is in: the relay never says hello, so the editor saves
  // as it does alone. The live spec below plays the relay itself.
  await page.routeWebSocket(/\/api\/v1\/boards\/\d+\/live/, () => {})
  return board
}

const libraryItem = {
  id: "rack",
  status: "published",
  created: 1,
  name: "Rack",
  elements: [
    {
      type: "rectangle",
      id: "rack-shape",
      x: 0,
      y: 0,
      width: 120,
      height: 200,
      version: 1,
      versionNonce: 1,
      seed: 1,
      isDeleted: false,
      strokeColor: "#1e1e1e",
      backgroundColor: "transparent",
      fillStyle: "solid",
      strokeWidth: 2,
      strokeStyle: "solid",
      roughness: 1,
      opacity: 100,
      angle: 0,
      groupIds: [],
      frameId: null,
      roundness: null,
      boundElements: null,
      updated: 1,
      link: null,
      locked: false,
    },
  ],
}

async function openBoard(page: Page) {
  await page.goto("/excalidraw/1")
  await expect(page.getByLabel("Drawing name")).toHaveValue("Untitled board")
  await expect(page.locator("canvas").first()).toBeVisible()
}

test("a board opens without saving, keeps its images, and saves inserted live server cards", async ({
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

  await page.goto("/excalidraw")
  await page.getByRole("button", { name: "Untitled board", exact: true }).click()
  await expect(page).toHaveURL(/\/excalidraw\/1$/)
  await expect(page.getByLabel("Drawing name")).toHaveValue("Untitled board")
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
  await expect.poll(() => board.scene.elements.length).toBe(4)
  await expect.poll(() => JSON.stringify(board.scene)).toContain('"kind":"server"')
  await expect.poll(() => JSON.stringify(board.scene)).toContain('"kind":"project","resourceId":4')
  await expect.poll(() => JSON.stringify(board.scene)).toContain('"kind":"database","resourceId":7')
  await expect(page.getByRole("status")).toContainText("Saved on this server")
  expect(
    board.scene.elements.filter((e) => (e as { type: string }).type === "embeddable"),
  ).toHaveLength(3)
  // The image left the scene for the file store, sent before the scene that names it.
  expect(board.scene.files).toEqual({})
  expect(board.files.get("pixel")?.type).toBe("image/png")
  expect(board.scene.appState).toMatchObject({ viewBackgroundColor: "#121212" })
  // Each card reads the thing it links now, rather than the words it was placed with.
  const cards = page.locator(".excalidraw__embeddable-container")
  await expect(cards).toHaveCount(3)
  await expect(cards.filter({ hasText: "atlas" })).toContainText("CPU 12%")
  await expect(cards.filter({ hasText: "Storefront" })).toBeVisible()
  await expect(cards.filter({ hasText: "Orders" })).toContainText("Answering")

  const saves = board.puts.length
  await page.reload()
  await expect(page.getByLabel("Drawing name")).toHaveValue("Untitled board")
  await expect(page.locator("canvas").first()).toBeVisible()
  await page.waitForTimeout(1500)
  expect(board.puts).toHaveLength(saves)
  expect(board.fileReads).toContain("pixel")
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
  const name = page.getByLabel("Drawing name")
  const status = page.getByRole("status")

  await name.fill("")
  await expect(status).toContainText("Name the drawing to save it")
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

  await page.getByRole("button", { name: "Delete drawing" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByPlaceholder("Type the phrase above")).toHaveCount(0)
  await dialog.getByRole("button", { name: "Delete drawing" }).click()
  await expect(page).toHaveURL(/\/excalidraw$/)
  expect(board.deletedWith).toBe("Ops map 2")
})

test("a failed save lets the operator leave on purpose, and a board deleted elsewhere stops autosave", async ({
  page,
}) => {
  const board = await mockBoards(page)
  await openBoard(page)
  const name = page.getByLabel("Drawing name")
  const status = page.getByRole("status")

  board.refuse = { status: 500, code: "internal", message: "database is locked" }
  await name.fill("Rack layout")
  await expect(status).toContainText("Save failed")
  await page.getByRole("button", { name: "Back to Excalidraw" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("Leave without saving?")
  await dialog.getByRole("button", { name: "Stay" }).click()
  await expect(page).toHaveURL(/\/excalidraw\/1$/)
  const failure = page.getByText("Drawing was not saved")
  await expect(failure).toBeVisible()

  board.refuse = null
  await page.getByRole("button", { name: "Save", exact: true }).click()
  await expect(status).toContainText("Saved on this server")
  await expect(failure).toBeHidden()
  expect(
    await page.evaluate(() => {
      const event = new Event("beforeunload", { cancelable: true })
      window.dispatchEvent(event)
      return event.defaultPrevented
    }),
  ).toBe(false)

  board.refuse = { status: 404, code: "board_not_found", message: "board not found" }
  await name.fill("Rack layout 2")
  await expect(status).toContainText("This drawing was deleted")
  const attempts = board.puts.length
  await name.fill("Rack layout 3")
  await page.waitForTimeout(1500)
  expect(board.puts).toHaveLength(attempts)
  expect(
    await page.evaluate(() => {
      const event = new Event("beforeunload", { cancelable: true })
      window.dispatchEvent(event)
      return event.defaultPrevented
    }),
  ).toBe(true)

  await page.getByRole("button", { name: "Back to Excalidraw" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Leave without saving" }).click()
  await expect(page).toHaveURL(/\/excalidraw$/)
})

test("the list draws each drawing, names what it links to, and the old address lands on it", async ({
  page,
}) => {
  const board = await mockBoards(page)
  board.name = "Production map"
  board.preview = {
    background: "#ffffff",
    count: 3,
    elements: [
      { type: "rectangle", x: 0, y: 0, w: 240, h: 110, stroke: "#1971c2", fill: "#a5d8ff" },
      { type: "text", x: 20, y: 40, w: 200, h: 30, text: "api", fontSize: 24, fontFamily: 5 },
      {
        type: "arrow",
        x: 240,
        y: 55,
        w: 160,
        h: 0,
        endArrow: true,
        points: [
          [0, 0],
          [160, 0],
        ],
      },
    ],
    resources: [{ kind: "server" }, { kind: "project", id: 4 }, { kind: "database", id: 7 }],
  }
  // Boards became Excalidraw; a bookmark to the old page still arrives.
  await page.goto("/boards")
  await expect(page).toHaveURL(/\/excalidraw$/)
  await expect(page.getByRole("link", { name: "Excalidraw", exact: true })).toBeVisible()

  const identity = page.locator("[data-slot=host-identity]")
  await expect(identity).toContainText("Excalidraw")
  await expect(identity).toContainText("1 drawing")
  await expect(identity).toContainText("3 shapes")
  await expect(identity).toContainText("3 server items linked")
  await expect(page.locator("[data-slot=stat-tile]")).toHaveCount(0)

  const card = page.getByRole("list", { name: "Drawings" }).locator(":scope > li")
  await expect(card.locator("svg text", { hasText: "api" })).toHaveCount(1)
  await expect(card).toContainText("Links this server, Storefront and Orders")

  await card.getByRole("button", { name: "Production map", exact: true }).click()
  await expect(page).toHaveURL(/\/excalidraw\/1$/)
})

test("drawing navigation keeps later edits when a drained save fails", async ({ page }) => {
  await mockBoards(page)
  const writes: Put[] = []
  let release = () => {}
  const pending = new Promise<void>((resolve) => {
    release = resolve
  })
  await page.route("**/api/v1/boards/1", async (route) => {
    if (route.request().method() !== "PUT") return route.fallback()
    writes.push(route.request().postDataJSON() as Put)
    if (writes.length === 1) {
      await pending
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ name: writes[0].name, revision: 2 }),
      })
    }
    return route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "unavailable", message: "Second save failed" } }),
    })
  })
  await page.goto("/excalidraw/1")
  const name = page.getByLabel("Drawing name")
  await expect(name).toHaveValue("Untitled board")
  await name.fill("First revision")
  await page.getByRole("button", { name: "Back to Excalidraw" }).click()
  await expect.poll(() => writes.length).toBe(1)
  await name.fill("Latest revision")
  release()
  await expect(page.getByRole("dialog", { name: "Leave without saving?" })).toBeVisible()
  expect(writes[1].name).toBe("Latest revision")
  await expect(page).toHaveURL(/\/excalidraw\/1$/)
  await page.getByRole("button", { name: "Stay", exact: true }).click()
  await expect(name).toHaveValue("Latest revision")
})

test("drawing drafts survive failed saves, sidebar and palette exits, and history traversal", async ({
  page,
}) => {
  const board = await mockBoards(page)
  board.refuse = { status: 503, code: "unavailable", message: "Save temporarily unavailable" }
  await openBoard(page)
  await page.getByLabel("Drawing name").fill("Retained drawing draft")
  await expect(page.getByRole("status")).toContainText("Save failed")
  await page.locator('a[href="/files"]').first().click()
  await expect(page).toHaveURL(/\/files$/)
  expect(
    await page.evaluate(() => {
      const event = new Event("beforeunload", { cancelable: true })
      window.dispatchEvent(event)
      return event.defaultPrevented
    }),
  ).toBe(true)
  await page.goBack()
  await expect(page.getByLabel("Drawing name")).toHaveValue("Retained drawing draft")
  expect(board.name).toBe("Untitled board")
  await page
    .locator('[data-slot="sidebar-header"]')
    .getByRole("button", { name: /Search/ })
    .focus()
  await page.keyboard.press("Control+k")
  const palette = page.getByRole("dialog", { name: "Command palette" })
  await palette.getByRole("combobox").fill("page: files")
  await palette.getByRole("option", { name: /Files/i }).first().click()
  await expect(page).toHaveURL(/\/files$/)
  await page.goBack()
  await expect(page.getByLabel("Drawing name")).toHaveValue("Retained drawing draft")
  board.refuse = null
  await page.getByRole("button", { name: "Save", exact: true }).click()
  await expect(page.getByRole("status")).toContainText("Saved on this server")
  expect(board.name).toBe("Retained drawing draft")
})

test("returning to a drawing waits for departing saves and keeps the acknowledged revision", async ({
  page,
}) => {
  const board = await mockBoards(page)
  let release = () => {}
  const firstResponse = new Promise<void>((resolve) => {
    release = resolve
  })
  let writes = 0
  await page.route("**/api/v1/boards/1", async (route) => {
    if (route.request().method() !== "PUT") return route.fallback()
    writes++
    if (writes === 1) {
      const body = route.request().postDataJSON() as Put
      await firstResponse
      board.name = body.name
      board.scene = body.scene
      board.revision = 2
      return route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ id: 1, name: body.name, revision: 2 }),
      })
    }
    if (writes === 2)
      return route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({ error: { code: "unavailable", message: "Later save failed" } }),
      })
    return route.fallback()
  })
  await openBoard(page)
  await page.getByLabel("Drawing name").fill("First snapshot")
  await page.getByRole("button", { name: "Save", exact: true }).click()
  await expect.poll(() => writes).toBe(1)
  await page.getByLabel("Drawing name").fill("Newer retained snapshot")
  await page.locator('a[href="/files"]').first().click()
  await expect(page).toHaveURL(/\/files$/)
  await page.goBack()
  await expect(page.getByLabel("Drawing name")).toHaveCount(0)
  release()
  await expect(page.getByLabel("Drawing name")).toHaveValue("Newer retained snapshot")
  await expect(page.getByRole("status")).toContainText("Save failed")
  await page.getByRole("button", { name: "Save", exact: true }).click()
  await expect(page.getByRole("status")).toContainText("Saved on this server")
  expect(board.name).toBe("Newer retained snapshot")
  expect(board.puts.at(-1)?.revision).toBe(2)
})

test("the library is kept on the server, and a pack from the library site is added through it", async ({
  page,
}) => {
  const board = await mockBoards(page)
  const pack = "https://libraries.excalidraw.com/libraries/ops/racks.excalidrawlib"
  const prompts: string[] = []
  page.on("dialog", (dialog) => {
    prompts.push(dialog.message())
    void dialog.accept()
  })
  // The library site sends the pack back in the address; the token is
  // another editor's, so this one asks before adding it.
  await page.goto(`/excalidraw/1#addLibrary=${encodeURIComponent(pack)}&token=another-tab`)
  await expect(page.getByLabel("Drawing name")).toHaveValue("Untitled board")
  await expect.poll(() => board.fetched).toEqual([pack])
  await expect.poll(() => board.library.revision).toBe(1)
  expect(prompts).toHaveLength(1)
  expect(board.library.libraryItems).toEqual([expect.objectContaining({ id: "rack" })])
  // The hand-off leaves no trace, or a reload would add the pack again.
  expect(new URL(page.url()).hash).toBe("")

  await page.reload()
  await expect(page.getByLabel("Drawing name")).toHaveValue("Untitled board")
  await page.waitForTimeout(1500)
  expect(board.fetched).toHaveLength(1)
  expect(board.library.revision).toBe(1)
})

test("editors on one drawing see each other's changes and save by merging them", async ({
  page,
}) => {
  const board = await mockBoards(page)
  const frames: Record<string, unknown>[] = []
  let relay: { send: (message: string) => void } | undefined
  await page.routeWebSocket(/\/api\/v1\/boards\/1\/live/, (socket) => {
    relay = socket
    socket.onMessage((message) => frames.push(JSON.parse(String(message))))
    socket.send(
      JSON.stringify({
        type: "hello",
        you: "me",
        peers: [{ id: "ana", user: "ana", name: "Ana", canWrite: true }],
      }),
    )
  })
  await openBoard(page)
  await expect(page.getByText("Live with Ana")).toBeVisible()

  // Ana's rectangle is drawn here and left to Ana to save.
  relay!.send(
    JSON.stringify({
      type: "scene",
      from: "ana",
      elements: [
        {
          type: "rectangle",
          id: "ana-box",
          x: 80,
          y: 80,
          width: 120,
          height: 80,
          version: 3,
          versionNonce: 9,
        },
      ],
    }),
  )
  await page.waitForTimeout(1500)
  expect(board.puts).toHaveLength(0)

  // A change made here goes to the room as it is made, and the save merges.
  await page.getByLabel("Drawing name").fill("Shared map")
  await page.getByRole("button", { name: "Add server item" }).click()
  await page.getByRole("button", { name: /This server/ }).click()
  await expect
    .poll(() =>
      frames.some(
        (frame) =>
          frame.type === "scene" &&
          (frame.elements as { type: string }[]).some((element) => element.type === "embeddable"),
      ),
    )
    .toBe(true)
  await expect.poll(() => board.puts.at(-1)?.merge).toBe(true)
  expect(board.puts.at(-1)!.scene.elements).toContainEqual(
    expect.objectContaining({ id: "ana-box" }),
  )
  expect(board.name).toBe("Shared map")

  // Somebody who joins is sent the scene, since the relay keeps none.
  relay!.send(
    JSON.stringify({
      type: "joined",
      peer: { id: "ion", user: "ion", name: "Ion", canWrite: false },
    }),
  )
  await expect
    .poll(() => frames.some((frame) => frame.type === "scene" && frame.to === "ion"))
    .toBe(true)
  await expect(page.getByText("Live with Ana and Ion")).toBeVisible()

  relay!.send(JSON.stringify({ type: "deleted" }))
  await expect(page.getByRole("status")).toContainText("This drawing was deleted")
})

test("an empty drawing offers what to draw, the menu stays on this server, and a description becomes a diagram", async ({
  page,
}) => {
  const board = await mockBoards(page)
  await openBoard(page)
  await expect(
    page.getByText("Draw this server: what runs on it, and how it connects."),
  ).toBeVisible()

  await page.getByRole("button", { name: /Add a project, database or this server/ }).click()
  await expect(page.getByRole("complementary", { name: "Server items" })).toBeVisible()
  await page.getByRole("button", { name: "Close" }).click()

  await page.locator(".main-menu-trigger").click()
  const menu = page.locator(".dropdown-menu")
  await expect(menu).toContainText("Save to this server")
  await expect(menu).not.toContainText(/GitHub|Discord|Follow us/)
  await page.keyboard.press("Escape")

  await page.getByRole("button", { name: /Describe a diagram/ }).click()
  await page.locator(".ttd-dialog textarea").fill("Storefront talks to the Orders database")
  await page.getByRole("button", { name: /Generate/ }).click()
  await expect.poll(() => board.prompts).toEqual(["Storefront talks to the Orders database"])
  // Excalidraw draws the Mermaid preview itself, under this page's CSP.
  await expect(
    page
      .locator(
        ".ttd-dialog-output-canvas-container svg, .ttd-dialog-output-canvas-container canvas",
      )
      .first(),
  ).toBeVisible()
  await page.getByRole("button", { name: /Insert/ }).click()
  await expect
    .poll(() => (board.scene.elements as { type: string }[]).map((element) => element.type).sort())
    .toEqual(expect.arrayContaining(["arrow", "rectangle", "text"]))
})
