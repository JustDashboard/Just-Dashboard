import { expect, test, type Page, type Route } from "@playwright/test"

/**
 * The Trash, undo and the index-backed Find, against a mocked API.
 *
 *   a delete moves to the Trash and says so before and after, with Undo on
 *   the notice and on Ctrl+Z, which restores it;
 *
 *   Shift+Delete, and anything the Trash cannot take, is a permanent delete
 *   that says why and offers no Undo;
 *
 *   a rename, a move and a copy are undone by Ctrl+Z, the newest first;
 *
 *   the Trash is a place in the sidebar and a page of its own, listing where
 *   each thing came from, when, by whom and how long it is kept, with
 *   Restore (asking when the place is taken), Delete permanently and Empty
 *   Trash, its rows one height at 1280 and at 390;
 *
 *   Find says how old the index is that answered it.
 */

const now = Date.now()
const home = "/home/operator"
const iso = (ms: number) => new Date(ms).toISOString()
const DAY = 24 * 60 * 60 * 1000

const user = {
  authenticated: true,
  needsTotp: false,
  needsEnrollment: false,
  require2fa: false,
  capabilities: [
    "read",
    "service.control",
    "file.write",
    "terminal",
    "destructive",
    "system.admin",
  ],
  user: {
    id: 1,
    username: "operator",
    role: "admin",
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: iso(now),
    createdAt: iso(now),
  },
}

function entry(name: string, overrides: Record<string, unknown> = {}) {
  return {
    name,
    path: `${home}/${name}`,
    size: 12,
    mode: "-rw-r--r--",
    modeOctal: "0644",
    isDir: false,
    isSymlink: false,
    modified: iso(now),
    owner: "operator",
    group: "operator",
    uid: 1000,
    gid: 1000,
    ...overrides,
  }
}

function trashItem(id: string, name: string, overrides: Record<string, unknown> = {}) {
  return {
    id: id.padEnd(32, "0"),
    name,
    originalPath: `${home}/site/${name}`,
    deletedAt: iso(now - 2 * 60 * 60 * 1000),
    deletedBy: "operator",
    expiresAt: iso(now + 29.5 * DAY),
    isDir: false,
    isSymlink: false,
    size: 2048,
    sizeKnown: true,
    owner: "operator",
    group: "operator",
    mode: "-rw-r--r--",
    modeOctal: "0644",
    folder: "/var/lib/just-dashboard/.jd-trash",
    ...overrides,
  }
}

type Call = { method: string; path: string; query: URLSearchParams; body: unknown }

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

/** A mocked server for Files and its Trash; `calls` is every write it was sent. */
async function mockServer(
  page: Page,
  options: {
    trash?: ReturnType<typeof trashItem>[]
    unavailable?: string
    restoreConflict?: boolean
    findIndex?: { ageSeconds: number; complete: boolean }
  } = {},
) {
  const calls: Call[] = []
  const trash = [...(options.trash ?? [])]
  let conflicted = false
  await page.routeWebSocket("**/api/v1/system/stream**", (socket) => socket.close())
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const method = request.method()
    if (method !== "GET") {
      calls.push({ method, path, query: url.searchParams, body: request.postDataJSON?.() ?? null })
    }
    if (path === "/auth/session") return json(route, user)
    if (path === "/updates/self") return json(route, { current: "0.9.1", latest: "0.9.1" })
    if (path === "/files/places")
      return json(route, {
        home,
        roots: ["/"],
        places: [
          { name: "operator", path: home, kind: "home", hint: "" },
          { name: "/", path: "/", kind: "root", hint: "" },
          { name: "Configuration", path: "/etc", kind: "notable", hint: "" },
        ],
        bookmarks: [],
        colours: {},
      })
    if (path === "/files/list")
      return json(route, {
        path: url.searchParams.get("path") ?? home,
        parent: "/home",
        roots: ["/"],
        entries: [
          entry("site", { isDir: true, mode: "drwxr-xr-x", modeOctal: "0755" }),
          entry("notes.md"),
          entry("report.csv"),
        ],
      })
    if (path === "/files/preview") return json(route, { kind: "text", text: "", size: 12 })
    if (path === "/files/trash/check")
      return json(route, {
        path: url.searchParams.get("path"),
        available: !options.unavailable,
        reason: options.unavailable,
        keepDays: 30,
      })
    if (path === "/files/delete") {
      const target = url.searchParams.get("path") ?? ""
      const name = target.split("/").pop() ?? ""
      if (url.searchParams.get("permanent") === "true")
        return json(route, { trashed: false, path: target })
      const item = trashItem(`d${trash.length}`, name, {
        originalPath: target,
        deletedAt: iso(Date.now()),
      })
      trash.unshift(item)
      return json(route, { trashed: true, path: target, item })
    }
    if (path === "/files/trash/restore") {
      const { id, conflict } = request.postDataJSON()
      if (options.restoreConflict && !conflict && !conflicted) {
        conflicted = true
        return json(route, { error: { code: "already_exists", message: "already exists" } }, 409)
      }
      const item = trash.find((t) => t.id === id)
      const index = trash.indexOf(item!)
      if (index >= 0) trash.splice(index, 1)
      const name = conflict === "keep" ? `${item?.name} (2)` : item?.name
      return json(route, { path: `${home}/site/${name}`, name })
    }
    if (path === "/files/trash" && method === "GET")
      return json(route, {
        items: trash,
        bytes: trash.reduce((sum, t) => sum + t.size, 0),
        partial: false,
        keepDays: 30,
      })
    if (path === "/files/trash" && method === "DELETE") {
      const removed = trash.length
      trash.length = 0
      return json(route, { removed, bytes: 0 })
    }
    if (path === "/files/trash/item") {
      const id = url.searchParams.get("id")
      const index = trash.findIndex((t) => t.id === id)
      if (index >= 0) trash.splice(index, 1)
      return route.fulfill({ status: 204 })
    }
    if (path === "/files/move" || path === "/files/copy") return route.fulfill({ status: 204 })
    if (path === "/files/find")
      return json(route, {
        root: url.searchParams.get("path"),
        hits: [
          {
            path: "/srv/app/notes-archive.md",
            name: "notes-archive.md",
            rel: "srv/app/notes-archive.md",
            dir: "srv/app",
            isDir: false,
            size: 10,
            score: 90,
            matches: [0, 1, 2, 3, 4],
          },
        ],
        truncated: false,
        total: 1,
        visited: 600000,
        elapsedMs: 120,
        source: options.findIndex ? "index" : "walk",
        index: options.findIndex
          ? {
              builtAt: iso(now - options.findIndex.ageSeconds * 1000),
              ageSeconds: options.findIndex.ageSeconds,
              entries: 600000,
              complete: options.findIndex.complete,
              building: false,
            }
          : undefined,
      })
    return json(route, [])
  })
  return { calls, trash }
}

const row = (page: Page, name: string) => page.locator(`tr[data-entry-path='${home}/${name}']`)

async function openFiles(page: Page) {
  await page.goto("/files")
  await row(page, "notes.md").waitFor()
}

test("a delete goes to the Trash, says so, and Undo brings it back", async ({ page }) => {
  const { calls } = await mockServer(page)
  await openFiles(page)
  await row(page, "notes.md").click()
  await page.keyboard.press("Delete")
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("heading", { name: "Move to Trash" })).toBeVisible()
  await expect(dialog).toContainText("goes to the Trash")
  await expect(dialog).toContainText("kept 30 days")
  await expect(dialog).not.toContainText("permanently")
  await dialog.getByRole("button", { name: "Move to Trash" }).click()
  await expect(page.getByText("Moved notes.md to the Trash")).toBeVisible()
  const deleted = calls.find((c) => c.path === "/files/delete")!
  expect(deleted.query.get("path")).toBe(`${home}/notes.md`)
  expect(deleted.query.get("permanent")).toBeNull()

  await page.getByRole("button", { name: "Undo" }).click()
  await expect.poll(() => calls.filter((c) => c.path === "/files/trash/restore").length).toBe(1)
  expect(calls.find((c) => c.path === "/files/trash/restore")!.body).toEqual({
    id: "d0".padEnd(32, "0"),
  })
  await expect(page.getByText("Undid deleting notes.md")).toBeVisible()
})

test("Ctrl+Z undoes the last file operation, then the one before", async ({ page }) => {
  const { calls } = await mockServer(page)
  await openFiles(page)
  await row(page, "notes.md").click()
  await page.keyboard.press("F2")
  const input = page.getByRole("dialog").getByRole("textbox")
  await input.fill("ideas.md")
  // A text field keeps its own undo: Ctrl+Z in it touches no file.
  await input.press("Control+z")
  await input.fill("ideas.md")
  await input.press("Enter")
  await expect(page.getByText("Renamed to ideas.md")).toBeVisible()
  expect(calls.filter((c) => c.path === "/files/move")).toHaveLength(1)

  await row(page, "report.csv").click({ button: "right" })
  await page.getByRole("menuitem", { name: "Duplicate", exact: true }).click()
  await expect(page.getByText(/Duplicated as/)).toBeVisible()

  await page
    .locator("[data-files-workspace] table")
    .first()
    .click({ position: { x: 5, y: 5 } })
  await page.keyboard.press("Control+z")
  await expect.poll(() => calls.filter((c) => c.path === "/files/delete").length).toBe(1)
  expect(calls.find((c) => c.path === "/files/delete")!.query.get("path")).toMatch(/report/)
  await page.keyboard.press("Control+z")
  await expect.poll(() => calls.filter((c) => c.path === "/files/move").length).toBe(2)
  expect(calls.filter((c) => c.path === "/files/move")[1].body).toEqual({
    from: `${home}/ideas.md`,
    to: `${home}/notes.md`,
  })
})

test("Shift+Delete deletes for good, and says it cannot be undone", async ({ page }) => {
  const { calls } = await mockServer(page)
  await openFiles(page)
  await row(page, "notes.md").click()
  await page.keyboard.press("Shift+Delete")
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("heading", { name: "Delete permanently" })).toBeVisible()
  await expect(dialog).toContainText("cannot be undone")
  await dialog.getByRole("button", { name: "Delete permanently" }).click()
  await expect(page.getByText("Deleted notes.md permanently")).toBeVisible()
  expect(calls.find((c) => c.path === "/files/delete")!.query.get("permanent")).toBe("true")
  await expect(page.getByRole("button", { name: "Undo" })).toHaveCount(0)

  // And the right-click menu offers the same.
  await row(page, "report.csv").click({ button: "right" })
  await expect(page.getByRole("menuitem", { name: /Delete permanently/ })).toBeVisible()
})

test("what the Trash cannot take is offered as a permanent delete, with the reason", async ({
  page,
}) => {
  const { calls } = await mockServer(page, { unavailable: "its filesystem is read-only" })
  await openFiles(page)
  await row(page, "notes.md").click()
  await page.keyboard.press("Delete")
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("heading", { name: "Delete permanently" })).toBeVisible()
  await expect(dialog).toContainText("cannot go to the Trash: its filesystem is read-only")
  await dialog.getByRole("button", { name: "Delete permanently" }).click()
  await expect(page.getByText("Deleted notes.md permanently")).toBeVisible()
  expect(calls.find((c) => c.path === "/files/delete")!.query.get("permanent")).toBe("true")
})

test("the Trash is a place, and lists what was deleted with the way back", async ({ page }) => {
  const { calls } = await mockServer(page, {
    trash: [
      trashItem("a1", "nginx.conf"),
      trashItem("b2", "old-release", {
        isDir: true,
        mode: "drwxr-xr-x",
        modeOctal: "0755",
        replaced: true,
        size: 5 * 1024 * 1024,
      }),
    ],
    restoreConflict: true,
  })
  await openFiles(page)
  const places = page.getByRole("navigation", { name: "Places" })
  await places.getByRole("link", { name: "Trash" }).click()
  await expect(page).toHaveURL(/\/files\/trash$/)
  await expect(page.getByRole("heading", { level: 1 })).toHaveText("Trash")
  await expect(page.getByTestId("trash-summary")).toHaveText("2 items, 5.0 MB, each kept 30 days")
  const trashRow = (id: string) => page.locator(`tr[data-trash-item='${id.padEnd(32, "0")}']`)
  await expect(trashRow("a1")).toContainText("nginx.conf")
  await expect(trashRow("a1")).toContainText(`${home}/site`)
  await expect(trashRow("a1")).toContainText("2 hours ago by operator")
  await expect(trashRow("a1")).toContainText("29 days")
  await expect(places.getByRole("link", { name: "Trash" })).toHaveAttribute("aria-current", "page")

  // The details say it in sentences, and the address carries the choice.
  await trashRow("a1").getByRole("button", { name: "nginx.conf", exact: true }).click()
  await expect(page).toHaveURL(/item=a1/)
  await expect(page.getByText("Deleted 2 hours ago by operator, kept 29 more days")).toBeVisible()

  // Restoring onto a taken name asks, and Keep both restores beside it.
  await trashRow("a1").hover()
  await trashRow("a1").getByRole("button", { name: "Restore nginx.conf" }).click()
  const conflict = page.getByRole("dialog")
  await expect(conflict).toContainText("puts what is there in the Trash")
  await conflict.getByRole("button", { name: "Keep both" }).click()
  await expect(page.getByText("Restored nginx.conf (2)")).toBeVisible()
  const restores = calls.filter((c) => c.path === "/files/trash/restore")
  expect(restores.map((c) => c.body)).toEqual([
    { id: "a1".padEnd(32, "0") },
    { id: "a1".padEnd(32, "0"), conflict: "keep" },
  ])
  await expect(trashRow("a1")).toHaveCount(0)

  // Empty Trash is final, and says how much goes.
  await page.getByRole("button", { name: "Empty Trash" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("1 item, 5.0 MB")
  await expect(dialog).toContainText("cannot be undone")
  await dialog.getByRole("button", { name: "Empty Trash" }).click()
  await expect(page.getByText("The Trash is empty")).toBeVisible()
  expect(calls.some((c) => c.path === "/files/trash" && c.method === "DELETE")).toBe(true)
})

for (const width of [1280, 390]) {
  test(`a trashed row is one height with or without its extras at ${width}px`, async ({ page }) => {
    await page.setViewportSize({ width, height: 860 })
    await mockServer(page, {
      trash: [
        trashItem("a1", "a.txt"),
        trashItem(
          "b2",
          "a-very-long-name-that-has-to-truncate-rather-than-wrap-onto-two-lines.tar.gz",
          {
            replaced: true,
            expiresAt: undefined,
            sizeKnown: false,
            deletedBy: undefined,
          },
        ),
      ],
    })
    await page.goto("/files/trash")
    const rows = page.locator("tr[data-trash-item]")
    await expect(rows).toHaveCount(2)
    const heights = await rows.evaluateAll((all) =>
      all.map((r) => r.getBoundingClientRect().height),
    )
    // One line each: the long name truncates, the missing facts leave no gap.
    expect(heights).toEqual([44, 44])
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
      width,
    )
    if (width < 1024) {
      // One pane on a phone: the list, with the places and details on the bar.
      const bar = page.getByRole("navigation", { name: "Trash" })
      await bar.getByRole("button", { name: "Places" }).click()
      const trashPlace = page
        .getByRole("navigation", { name: "Places" })
        .getByRole("link", { name: "Trash" })
      await expect(trashPlace).toBeVisible()
      const place = await page
        .getByRole("navigation", { name: "Places" })
        .locator("li")
        .first()
        .boundingBox()
      expect((await trashPlace.boundingBox())!.height).toBe(place!.height)
      await bar.getByRole("button", { name: "Trash" }).click()
      await rows.first().getByRole("button", { name: "a.txt", exact: true }).click()
      await expect(page.getByText(/Deleted 2 hours ago/)).toBeVisible()
    }
  })
}
test("Find says how old the index is that answered it", async ({ page }) => {
  await mockServer(page, { findIndex: { ageSeconds: 2 * 60 * 60, complete: true } })
  await openFiles(page)
  await page.keyboard.press("Control+p")
  const palette = page.getByRole("dialog")
  await palette.getByRole("combobox").fill("notes")
  await expect(palette.getByText("notes-archive.md")).toBeVisible()
  await expect(palette.getByRole("status")).toContainText("index from 2 hours ago")
})
