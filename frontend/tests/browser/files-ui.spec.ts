import { expect, test, type Page, type Route } from "@playwright/test"

/**
 * The file manager as a person meets it, against a mocked API.
 *
 * Four claims the redesign rests on, each of which a type check cannot make:
 *
 *   the sidebar is a fixed list of places, starred and recent folders that
 *   stays put while the listing walks into folders;
 *
 *   a picture is visible as itself in the listing, and Space opens it full
 *   screen with the folder's other files an arrow away;
 *
 *   a right-click on a row offers that row's verbs and a right-click on the
 *   space between rows offers the folder's;
 *
 *   an upload whose name is taken asks before anything is written, and "keep
 *   both" sends a numbered name with overwrite off.
 */

const now = new Date().toISOString()
const home = "/home/operator"

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
    lastLoginAt: now,
    createdAt: now,
  },
}

// A 1×1 transparent PNG, which is all a thumbnail needs to prove it loaded.
const png = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==",
  "base64",
)

function entry(name: string, overrides: Record<string, unknown> = {}) {
  return {
    name,
    path: `${home}/${name}`,
    size: 0,
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

const entries = [
  entry("photos", { isDir: true, mode: "drwxr-xr-x", modeOctal: "0755" }),
  entry("site", { isDir: true, mode: "drwxr-xr-x", modeOctal: "0755" }),
  entry("logo.png", { size: png.length }),
  entry("clip.mp4", { size: 4096 }),
  entry("notes.md", { size: 12, mimeHint: "markdown" }),
]

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

async function mockFiles(page: Page) {
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, user)
    if (path === "/updates/self") return json(route, { current: "0.6.7", latest: "0.6.7" })
    if (path === "/files/places") {
      return json(route, {
        home,
        roots: ["/"],
        places: [
          { name: "operator", path: home, kind: "home", hint: "Where this dashboard starts" },
          { name: "/", path: "/", kind: "root", hint: "Permitted root" },
          { name: "/etc", path: "/etc", kind: "notable", hint: "System configuration" },
          { name: "/var/log", path: "/var/log", kind: "notable", hint: "Log files" },
        ],
        bookmarks: [{ path: `${home}/photos`, name: "photos" }],
      })
    }
    if (path === "/files/list") {
      const dir = url.searchParams.get("path") ?? home
      if (dir === home) return json(route, { path: home, parent: "/home", entries, roots: ["/"] })
      return json(route, {
        path: dir,
        parent: dir.split("/").slice(0, -1).join("/") || "/",
        entries: [],
        roots: ["/"],
      })
    }
    if (path === "/files/raw") {
      // A video's bytes are never answered: the poster stays mounted, waiting,
      // which is the state the listing is in for a real recording too until
      // its first frame arrives. Answering with a PNG would make the element
      // error and fall back to the icon, which is also correct but not what
      // this test is about.
      if ((url.searchParams.get("path") ?? "").endsWith(".mp4")) return new Promise(() => undefined)
      return route.fulfill({ status: 200, contentType: "image/png", body: png })
    }
    if (path === "/files/preview") {
      const target = url.searchParams.get("path") ?? ""
      const name = target.split("/").pop() ?? ""
      const base = {
        path: target,
        name,
        modified: now,
        modeOctal: "0644",
        owner: "operator",
        group: "operator",
      }
      if (name === "logo.png") {
        return json(route, {
          ...base,
          kind: "image",
          mime: "image/png",
          size: png.length,
          editable: false,
          width: 1,
          height: 1,
        })
      }
      if (name === "notes.md") {
        return json(route, {
          ...base,
          kind: "text",
          size: 12,
          editable: true,
          language: "markdown",
          text: "# Notes\nhello\n",
          lines: 2,
        })
      }
      if (name === "clip.mp4") {
        return json(route, {
          ...base,
          kind: "video",
          mime: "video/mp4",
          size: 4096,
          editable: false,
        })
      }
      return json(route, { ...base, kind: "dir", size: 0, editable: false, childCount: 0 })
    }
    if (path === "/files/upload") {
      return json(route, { uploaded: ["x"], path: url.searchParams.get("path") }, 201)
    }
    return json(route, [])
  })
}

/** The page, with its listing on screen. Not `networkidle`: a video poster's request is left pending on purpose. */
async function openFiles(page: Page) {
  await page.goto("/files")
  await page.locator("tr[data-entry-path]").first().waitFor()
}

test("the sidebar is a fixed list of places that stays put while browsing", async ({ page }) => {
  await mockFiles(page)
  await openFiles(page)

  // The places, the starred folder and the system directories are all on the page.
  const sidebar = page.locator("div:has(> [data-slot='pane-header'])").first()
  const homeRow = sidebar.locator(`button[title='${home}']`)
  await expect(homeRow).toContainText("Home")
  await expect(homeRow).toHaveAttribute("aria-current", "location")
  await expect(sidebar.locator(`button[title='${home}/photos']`)).toBeVisible()
  await expect(sidebar.locator("button[title='/var/log']")).toBeVisible()
  await expect(sidebar.locator("button[title='/etc']")).toBeVisible()
  // Home's folders are the listing's business, not the sidebar's.
  await expect(sidebar.locator(`button[title='${home}/site']`)).toHaveCount(0)

  // Walking into a folder changes the listing and marks the row, not the list.
  await sidebar.locator("button[title='/etc']").click()
  await expect(page.getByRole("button", { name: "Folders in /etc" })).toBeVisible()
  await expect(sidebar.locator("button[title='/etc']")).toHaveAttribute("aria-current", "location")
  await expect(homeRow).not.toHaveAttribute("aria-current", "location")
  await expect(sidebar.locator(`button[title='${home}/photos']`)).toBeVisible()
  await expect(sidebar.locator("button[title='/var/log']")).toBeVisible()
})

test("pictures are visible in the listing and Space opens the viewer", async ({ page }) => {
  await mockFiles(page)
  await openFiles(page)

  const row = page.locator(`tr[data-entry-path='${home}/logo.png']`)
  const thumb = row.locator("img")
  await expect(thumb).toHaveAttribute("src", /\/files\/raw\?/)
  await expect(thumb).toHaveJSProperty("complete", true)
  // A video row mounts its poster once it is on screen.
  await expect(page.locator(`tr[data-entry-path='${home}/clip.mp4'] video`)).toHaveCount(1)

  // One click looks: the inspector says what it is.
  await row.click()
  await expect(page.getByRole("heading", { name: "logo.png" })).toBeVisible()

  // Space is the quick look.
  await page.keyboard.press(" ")
  const dialog = page.getByRole("dialog")
  await expect(dialog).toBeVisible()
  // Folders first, then files by name: clip.mp4, logo.png, notes.md.
  await expect(dialog).toContainText("logo.png")
  await expect(dialog).toContainText("2 / 3")
  await page.keyboard.press("ArrowLeft")
  await expect(dialog).toContainText("clip.mp4")
  await expect(dialog).toContainText("1 / 3")
  await page.keyboard.press("Escape")
  await expect(dialog).toBeHidden()
})

test("a right-click offers the row's verbs, or the folder's between rows", async ({ page }) => {
  await mockFiles(page)
  await openFiles(page)

  await page.locator(`tr[data-entry-path='${home}/notes.md']`).click({ button: "right" })
  const menu = page.getByRole("menu")
  await expect(menu.getByRole("menuitem", { name: /Open in editor/ })).toBeVisible()
  await expect(menu.getByRole("menuitem", { name: /Rename/ })).toBeVisible()
  await expect(menu.getByRole("menuitem", { name: /^Delete/ })).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(menu).toBeHidden()

  await page.getByRole("columnheader", { name: /Size/ }).click({ button: "right" })
  await expect(page.getByRole("menuitem", { name: /New folder/ })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: /Select all/ })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: /Upload files/ })).toBeVisible()
  await page.keyboard.press("Escape")
})

test("an upload whose name is taken asks first, and keep-both sends a numbered name", async ({
  page,
}) => {
  await mockFiles(page)
  const sent: { url: string; body: string }[] = []
  await page.route("**/api/v1/files/upload**", async (route) => {
    sent.push({
      url: route.request().url(),
      body: route.request().postDataBuffer()?.toString("latin1") ?? "",
    })
    await json(route, { uploaded: ["notes (2).md"], path: home }, 201)
  })
  await openFiles(page)

  await page
    .locator("input[type='file']")
    .first()
    .setInputFiles({ name: "notes.md", mimeType: "text/markdown", buffer: Buffer.from("hi") })

  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("already exists here")
  await expect(dialog).toContainText("notes.md")
  // Nothing has been sent while the question is open.
  expect(sent).toHaveLength(0)
  await dialog.getByRole("button", { name: "Keep both" }).click()

  await expect.poll(() => sent.length).toBe(1)
  expect(sent[0].url).toContain("overwrite=false")
  expect(sent[0].body).toContain('filename="notes (2).md"')
  await expect(page.getByText("Done")).toBeVisible()
})

test("every icon-only control on /files has an accessible name", async ({ page }) => {
  await mockFiles(page)
  await openFiles(page)

  const unnamed = await page.evaluate(() => {
    const bad: string[] = []
    for (const el of document.querySelectorAll<HTMLElement>("button, [role='button']")) {
      if (el.offsetParent === null && el.getAttribute("aria-hidden") !== "true") continue
      const text = (el.textContent ?? "").trim()
      if (text.length > 0) continue
      const named =
        el.getAttribute("aria-label") ||
        el.getAttribute("aria-labelledby") ||
        el.querySelector(".sr-only")
      if (!named) bad.push(el.outerHTML.slice(0, 160))
    }
    return bad
  })
  expect(unnamed).toEqual([])
})

test.describe("with no hover available", () => {
  test.use({ hasTouch: true, viewport: { width: 390, height: 844 } })

  test("row actions on /files are visible without a pointer", async ({ page }) => {
    await mockFiles(page)
    await openFiles(page)
    await expect(page.locator(`tr[data-entry-path='${home}/notes.md']`)).toBeVisible()

    const hidden = await page.evaluate(() => {
      const bad: string[] = []
      for (const el of document.querySelectorAll<HTMLElement>(
        "[data-slot='table-row'] button, [data-slot='table-row'] a",
      )) {
        if (parseFloat(getComputedStyle(el).opacity) < 0.1) {
          bad.push(el.getAttribute("aria-label") ?? el.outerHTML.slice(0, 120))
        }
      }
      return bad
    })
    expect(hidden).toEqual([])
  })
})
