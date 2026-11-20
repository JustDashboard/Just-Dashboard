import { expect, test, type Page, type Route, type WebSocketRoute } from "@playwright/test"

/**
 * Files that follow the disk, uploads in pieces and links fetched onto the
 * server, against a mocked API.
 *
 *   a folder on screen takes the rows its socket sends without moving what
 *   the person is doing: the selection, the inspector, a rename being typed
 *   and the scroll all stay where they were;
 *
 *   a file over 32 MiB goes in pieces, carries on by itself after a piece is
 *   cut off, pauses and resumes, and a reloaded page shows what the server
 *   still holds;
 *
 *   New → From a link asks the server to fetch it, and the fetch sits in the
 *   same panel with its progress in words and a Cancel;
 *
 *   a transfer row with pause and cancel is the height of one without.
 */

const now = new Date().toISOString()
const home = "/home/operator"
const MiB = 1024 * 1024

const user = {
  authenticated: true,
  needsTotp: false,
  needsEnrollment: false,
  require2fa: false,
  capabilities: ["read", "file.write", "destructive", "system.admin"],
  user: {
    id: 1,
    username: "operator",
    role: "admin",
    totpEnabled: true,
    disabled: false,
    mustChangePassword: false,
    lastLoginAt: now,
    createdAt: now,
  },
}

function entry(name: string, overrides: Record<string, unknown> = {}) {
  return {
    name,
    path: `${home}/${name}`,
    size: 10,
    mode: "-rw-r--r--",
    modeOctal: "0644",
    isDir: false,
    isSymlink: false,
    modified: now,
    owner: "operator",
    group: "operator",
    uid: 1000,
    gid: 1000,
    ...overrides,
  }
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

type Mock = {
  entries: ReturnType<typeof entry>[]
  sockets: WebSocketRoute[]
  listReads: number
  uploads: unknown[]
  fetches: Record<string, unknown>[]
  print: string
  checks: number
}

async function mockFiles(
  page: Page,
  entries: ReturnType<typeof entry>[],
  { fallback = false } = {},
): Promise<Mock> {
  const mock: Mock = {
    entries,
    sockets: [],
    listReads: 0,
    uploads: [],
    fetches: [],
    print: "one",
    checks: 0,
  }
  await page.routeWebSocket("**/api/v1/system/stream**", (socket) => socket.close())
  await page.routeWebSocket("**/api/v1/files/watch**", (socket) => {
    if (fallback) {
      // A folder on a filesystem the kernel cannot watch.
      socket.send(JSON.stringify({ type: "fallback", data: { reason: "NFS" }, ts: Date.now() }))
      return
    }
    mock.sockets.push(socket)
    const path = new URL(socket.url()).searchParams.get("path")
    socket.send(JSON.stringify({ type: "watching", data: { path }, ts: Date.now() }))
  })
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, user)
    if (path === "/updates/self") return json(route, { current: "0.9.1", latest: "0.9.1" })
    if (path === "/files/places")
      return json(route, {
        home,
        roots: ["/"],
        places: [
          { name: "operator", path: home, kind: "home", hint: "Where this dashboard starts" },
        ],
        bookmarks: [],
        colours: {},
      })
    if (path === "/files/list") {
      mock.listReads++
      return json(route, { path: home, parent: "/home", entries: mock.entries, roots: ["/"] })
    }
    if (path === "/files/preview") {
      const target = url.searchParams.get("path") ?? ""
      return json(route, {
        path: target,
        name: target.split("/").pop(),
        kind: "text",
        size: 10,
        editable: true,
        text: "hello\n",
        lines: 1,
        modified: now,
        modeOctal: "0644",
        owner: "operator",
        group: "operator",
      })
    }
    if (path === "/files/pieces" && route.request().method() === "GET")
      return json(route, mock.uploads)
    if (path === "/files/fetches") return json(route, mock.fetches)
    if (path === "/files/changes") {
      mock.checks++
      return json(route, { path: home, fingerprint: mock.print })
    }
    return json(route, [])
  })
  return mock
}

const item = (page: Page, name: string) => page.locator(`[data-entry-path='${home}/${name}']`)

async function openFiles(page: Page) {
  await page.goto(`/files?path=${encodeURIComponent(home)}`)
  await page.locator("tr[data-entry-path]").first().waitFor()
}

function send(mock: Mock, type: string, data?: unknown) {
  for (const socket of mock.sockets) socket.send(JSON.stringify({ type, data, ts: Date.now() }))
}

test.describe("a folder that follows the disk", () => {
  test("a new file arrives with the selection and the inspector left where they were", async ({
    page,
  }) => {
    const mock = await mockFiles(page, [entry("alpha.txt"), entry("beta.txt"), entry("gamma.log")])
    await openFiles(page)
    await expect.poll(() => mock.sockets.length).toBeGreaterThan(0)
    const reads = mock.listReads

    await item(page, "beta.txt").getByRole("checkbox").click()
    await item(page, "gamma.log").locator("[data-file-name]").click()
    await expect(page.getByRole("heading", { name: "gamma.log" })).toBeVisible()

    send(mock, "changes", {
      entries: [entry("arrived.txt"), entry("gamma.log", { size: 4096 })],
      removed: ["alpha.txt"],
    })
    await expect(item(page, "arrived.txt")).toBeVisible()
    await expect(item(page, "alpha.txt")).toHaveCount(0)
    await expect(item(page, "beta.txt").getByRole("checkbox")).toBeChecked()
    await expect(page.getByRole("heading", { name: "gamma.log" })).toBeVisible()
    await expect(item(page, "gamma.log")).toContainText("4.0 KB")
    // The rows came over the socket: the listing was not read again.
    expect(mock.listReads).toBe(reads)

    // Too many names at once: the page reads the listing once instead.
    mock.entries = [entry("beta.txt"), entry("after-resync.txt")]
    send(mock, "resync")
    await expect(item(page, "after-resync.txt")).toBeVisible()
    await expect(item(page, "beta.txt").getByRole("checkbox")).toBeChecked()
  })

  test("a rename being typed and the scroll survive a change", async ({ page }) => {
    const many = Array.from({ length: 80 }, (_, i) =>
      entry(`file-${String(i).padStart(3, "0")}.txt`),
    )
    const mock = await mockFiles(page, many)
    await openFiles(page)
    await expect.poll(() => mock.sockets.length).toBeGreaterThan(0)

    const scroll = page.locator("[data-file-listing] [data-slot='table-container']")
    await item(page, "file-060.txt").getByRole("checkbox").click()
    expect(await scroll.evaluate((el) => el.scrollTop)).toBeGreaterThan(300)
    const before = (await item(page, "file-060.txt").boundingBox())!.y
    // A row arrives above the ones being read: they stay where they are on
    // screen (the listing scrolls by the new row's height to hold them).
    send(mock, "changes", { entries: [entry("000-new-at-the-top.txt")], removed: [] })
    await expect(item(page, "000-new-at-the-top.txt")).toHaveCount(1)
    expect((await item(page, "file-060.txt").boundingBox())!.y).toBe(before)
    await expect(item(page, "file-060.txt").getByRole("checkbox")).toBeChecked()

    await item(page, "file-061.txt").click({ button: "right" })
    await page.getByRole("menuitem", { name: /^Rename/ }).click()
    const field = page.getByRole("dialog").getByRole("textbox")
    await field.fill("renamed-half")
    send(mock, "changes", { entries: [entry("another.txt")], removed: ["file-001.txt"] })
    await expect(item(page, "another.txt")).toHaveCount(1)
    await expect(page.getByRole("dialog")).toBeVisible()
    await expect(field).toHaveValue("renamed-half")
    await expect(field).toBeFocused()
  })

  test("a folder that cannot be watched is checked by fingerprint, not reread on a timer", async ({
    page,
  }) => {
    const mock = await mockFiles(page, [entry("remote.txt")], { fallback: true })
    await openFiles(page)
    await expect.poll(() => mock.checks, { timeout: 8000 }).toBeGreaterThan(1)
    const reads = mock.listReads
    await page.waitForTimeout(2500)
    expect(mock.listReads).toBe(reads)
    mock.entries = [entry("remote.txt"), entry("from-elsewhere.txt")]
    mock.print = "two"
    await expect(item(page, "from-elsewhere.txt")).toBeVisible({ timeout: 8000 })
  })
})

/** A fake big file: the page only slices it, so its bytes are never read whole. */
async function chooseBigFile(page: Page, name: string, size: number) {
  await page.evaluate(
    ({ name, size }) => {
      const input = document.querySelector<HTMLInputElement>(
        "input[type=file]:not([webkitdirectory])",
      )!
      const transfer = new DataTransfer()
      transfer.items.add(
        new File([new Uint8Array(size)], name, { lastModified: 1_700_000_000_000 }),
      )
      input.files = transfer.files
      input.dispatchEvent(new Event("change", { bubbles: true }))
    },
    { name, size },
  )
}

test.describe("uploads in pieces", () => {
  test("a big file goes in pieces, carries on after a dropped piece, pauses and resumes", async ({
    page,
  }) => {
    // Forty megabytes cross the browser's interception a piece at a time.
    test.setTimeout(150_000)
    const mock = await mockFiles(page, [entry("notes.txt")])
    const size = 40 * MiB
    const pieces: number[] = []
    let next = 0
    let dropOnce = true
    let holdNext: (() => void) | undefined
    let held = false
    let completed = false
    await page.route("**/api/v1/files/pieces**", async (route) => {
      const request = route.request()
      const url = new URL(request.url())
      const session = {
        id: "abc",
        path: `${home}/disk.img`,
        dir: home,
        name: "disk.img",
        size,
        key: "k",
        overwrite: false,
        received: next,
        next,
        created: now,
        updated: now,
      }
      if (request.method() === "POST" && url.pathname.endsWith("/complete")) {
        completed = true
        return json(route, session)
      }
      if (request.method() === "POST") return json(route, session)
      if (request.method() === "GET" && url.pathname.endsWith("/abc")) return json(route, session)
      if (request.method() === "GET") return json(route, [])
      if (request.method() === "PUT") {
        const offset = Number(url.searchParams.get("offset"))
        if (offset === 8 * MiB && dropOnce) {
          dropOnce = false
          return route.abort("connectionreset")
        }
        if (offset === 24 * MiB && !held) {
          held = true
          await new Promise<void>((resolve) => (holdNext = resolve))
          return route.abort("aborted").catch(() => undefined)
        }
        pieces.push(offset)
        next = offset + (request.postDataBuffer()?.length ?? 0)
        return json(route, { ...session, received: next, next })
      }
      return json(route, {})
    })
    await openFiles(page)
    await chooseBigFile(page, "disk.img", size)

    const row = page.locator("[data-transfer='disk.img']")
    await expect(row).toBeVisible()
    // The cut piece is sent again from where the server stood.
    await expect.poll(() => pieces, { timeout: 60_000 }).toEqual([0, 8 * MiB, 16 * MiB])
    await expect.poll(() => held, { timeout: 30_000 }).toBe(true)
    await expect(row).toContainText("of 40.0 MB")

    await row.getByRole("button", { name: "Pause" }).click()
    holdNext?.()
    await expect(row).toHaveAttribute("data-status", "paused")
    await expect(row).toContainText("Paused at 24.0 MB of 40.0 MB")
    await row.getByRole("button", { name: "Resume" }).click()
    await expect.poll(() => completed, { timeout: 60_000 }).toBe(true)
    expect(pieces).toEqual([0, 8 * MiB, 16 * MiB, 24 * MiB, 32 * MiB])
    await expect(row).toHaveAttribute("data-status", "done")
    expect(mock.listReads).toBeGreaterThan(0)
  })

  test("a reloaded page shows what the server still holds, and cancels it", async ({ page }) => {
    const mock = await mockFiles(page, [entry("notes.txt")])
    mock.uploads = [
      {
        id: "left",
        path: `${home}/backup.tar`,
        dir: home,
        name: "backup.tar",
        size: 2 * 1024 * MiB,
        key: "k",
        overwrite: false,
        received: 512 * MiB,
        next: 512 * MiB,
        created: now,
        updated: now,
      },
    ]
    let cancelled = ""
    await page.route("**/api/v1/files/pieces/**", async (route) => {
      if (route.request().method() === "DELETE") {
        cancelled = new URL(route.request().url()).pathname
        return route.fulfill({ status: 204 })
      }
      return route.fallback()
    })
    await openFiles(page)
    const row = page.locator("[data-transfer='backup.tar']")
    await expect(row).toContainText("Stopped at 512.0 MB of 2.0 GB; add the file again to carry on")
    await row.getByRole("button", { name: "Cancel" }).click()
    await expect.poll(() => cancelled).toBe("/api/v1/files/pieces/left")
    await expect(row).toHaveCount(0)
  })
})

test.describe("fetching a link", () => {
  for (const width of [1280, 390]) {
    test(`New → From a link fetches on the server, and its row is one height (${width})`, async ({
      page,
    }) => {
      await page.setViewportSize({ width, height: 800 })
      const mock = await mockFiles(page, [entry("notes.txt")])
      let started: Record<string, unknown> | undefined
      let cancelled = false
      await page.route("**/api/v1/files/fetch", async (route) => {
        started = route.request().postDataJSON()
        const running = {
          id: "f1",
          url: "https://downloads.example.test/release.tar.gz",
          dir: home,
          name: "",
          size: -1,
          received: 0,
          status: "running",
          mine: true,
        }
        mock.fetches = [running]
        await json(route, running, 202)
      })
      await page.route("**/api/v1/files/fetches/f1", async (route) => {
        cancelled = true
        mock.fetches = []
        await json(route, {})
      })
      mock.uploads = [
        {
          id: "held",
          path: `${home}/held.img`,
          dir: home,
          name: "held.img",
          size: 100 * MiB,
          key: "k",
          overwrite: false,
          received: 10 * MiB,
          next: 10 * MiB,
          created: now,
          updated: now,
        },
      ]
      await openFiles(page)

      await page.getByRole("button", { name: "New", exact: true }).click()
      await page.getByRole("menuitem", { name: "From a link…" }).click()
      const dialog = page.getByRole("dialog")
      await dialog.getByRole("textbox").fill("ftp://nope")
      await expect(dialog).toContainText("A link starts with http:// or https://.")
      await dialog
        .getByRole("textbox")
        .fill("https://downloads.example.test/release.tar.gz?token=signed")
      await dialog.getByRole("button", { name: "Fetch" }).click()
      await expect
        .poll(() => started?.url)
        .toBe("https://downloads.example.test/release.tar.gz?token=signed")
      expect(started?.path).toBe(home)

      const fetchRow = page.locator("[data-transfer='release.tar.gz']")
      await expect(fetchRow).toContainText("Connecting")
      mock.fetches = [
        {
          ...mock.fetches[0],
          name: "release.tar.gz",
          size: 300 * MiB,
          received: 120 * MiB,
        },
      ]
      await expect(fetchRow).toContainText("120.0 MB of 300.0 MB")
      await expect(page.locator("[data-transfers]")).toContainText("Fetching 1 link")

      // A row with Pause and Cancel and a row with Cancel alone are one height.
      const stopped = page.locator("[data-transfer='held.img']")
      await expect(stopped).toBeVisible()
      const [a, b] = await Promise.all([fetchRow.boundingBox(), stopped.boundingBox()])
      expect(a!.height).toBe(b!.height)

      await fetchRow.getByRole("button", { name: "Cancel" }).click()
      await expect.poll(() => cancelled).toBe(true)
    })
  }

  for (const width of [1280, 390]) {
    test(`a row that arrived over the socket is the height of one that was there (${width})`, async ({
      page,
    }) => {
      await page.setViewportSize({ width, height: 800 })
      // Rows in the middle, so neither is the first or last row of the table.
      const mock = await mockFiles(page, [
        entry("a-first.txt"),
        entry("m-was-there.txt"),
        entry("z-last.txt"),
      ])
      await openFiles(page)
      await expect.poll(() => mock.sockets.length).toBeGreaterThan(0)
      send(mock, "changes", { entries: [entry("n-arrived.txt", { size: 2048 })], removed: [] })
      await expect(item(page, "n-arrived.txt")).toBeVisible()
      const [a, b] = await Promise.all([
        item(page, "m-was-there.txt").boundingBox(),
        item(page, "n-arrived.txt").boundingBox(),
      ])
      expect(a!.height).toBe(b!.height)
    })
  }
})
