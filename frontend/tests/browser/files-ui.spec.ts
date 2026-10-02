import { expect, test, type Page, type Route } from "@playwright/test"

test.use({
  video:
    process.env.JD_FILES_SEARCH_VIDEO === "1"
      ? { mode: "on", size: { width: 1280, height: 960 } }
      : "off",
})

/**
 * The file manager as a person meets it, against a mocked API.
 *
 * The claims the redesign rests on, each of which a type check cannot make:
 *
 *   the sidebar is a fixed list of places, starred and recent folders that
 *   stays put while the listing walks into folders;
 *
 *   there is no page header: the page's commands are in the workbench's
 *   strip, and a folder's colour is picked there and in the inspector, drawn
 *   everywhere the folder is, and stored on the server;
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

async function mockPicture(page: Page, width = 80, height = 40) {
  const picture = await page.evaluate(
    ({ width, height }) => {
      const canvas = document.createElement("canvas")
      canvas.width = width
      canvas.height = height
      const ctx = canvas.getContext("2d")!
      ctx.fillStyle = "red"
      ctx.fillRect(0, 0, width, height)
      return canvas.toDataURL().split(",")[1]
    },
    { width, height },
  )
  await page.route("**/api/v1/files/raw**", (route) =>
    route.fulfill({ status: 200, contentType: "image/png", body: Buffer.from(picture, "base64") }),
  )
}

/** The labels the mocked server holds, which a test can change as the real one would. */
async function mockFiles(
  page: Page,
  colours: Record<string, string> = { [`${home}/photos`]: "red" },
  palette: { defaultColour?: string } = {},
) {
  await page.routeWebSocket("**/api/v1/system/stream**", (socket) => socket.close())
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
        colours,
        defaultColour: palette.defaultColour,
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
    if (path === "/files/read")
      return json(route, {
        path: url.searchParams.get("path"),
        content: "# Notes\nhello\n",
        size: 14,
        language: "markdown",
        binary: false,
        modeOctal: "0644",
      })
    if (path === "/files/write") return json(route, { ok: true })
    if (path === "/files/find")
      return json(route, {
        root: home,
        hits: [],
        truncated: false,
        visited: 5,
        elapsedMs: 2,
      })
    if (path === "/files/search")
      return json(route, {
        hits: [],
        truncated: false,
        visited: 5,
        unreadable: 0,
        elapsedMs: 2,
      })
    if (path === "/files/colours/default") {
      palette.defaultColour = route.request().postDataJSON().colour
      for (const target of Object.keys(colours)) delete colours[target]
      return json(route, { defaultColour: palette.defaultColour, colours })
    }
    if (path === "/files/colours") {
      const { path: target, colour } = route.request().postDataJSON()
      if (colour) colours[target] = colour
      else delete colours[target]
      return json(route, { colours })
    }
    return json(route, [])
  })
}

/** The page, with its listing on screen. Not `networkidle`: a video poster's request is left pending on purpose. */
async function openFiles(page: Page) {
  await page.goto("/files")
  await page.locator("tr[data-entry-path]").first().waitFor()
}

test("large listings retain find, selection and keyboard menu focus", async ({ page }) => {
  await mockFiles(page)
  const many = Array.from({ length: 500 }, (_, i) =>
    entry(`large-${String(i).padStart(4, "0")}.txt`),
  )
  await page.route("**/api/v1/files/list**", (route) =>
    json(route, { path: home, parent: "/home", entries: many, roots: ["/"] }),
  )
  await openFiles(page)
  await expect(page.locator("tr[data-entry-path]")).toHaveCount(500)
  const found = await page.evaluate(() =>
    (window as unknown as { find: (text: string) => boolean }).find("large-0499.txt"),
  )
  expect(found).toBe(true)
  const last = page.locator(`tr[data-entry-path="${home}/large-0499.txt"]`)
  await last.scrollIntoViewIfNeeded()
  await expect(last).toBeInViewport()
  await page.getByRole("checkbox", { name: "Select all", exact: true }).click()
  const checkbox = last.getByRole("checkbox", { name: "Select large-0499.txt", exact: true })
  await checkbox.focus()
  await checkbox.press("Space")
  await expect(checkbox).toHaveAttribute("aria-checked", "false")
  await expect(page.getByRole("checkbox", { name: "Select all", exact: true })).toHaveAttribute(
    "aria-checked",
    "mixed",
  )
  const more = last.getByRole("button", { name: "More actions", exact: true })
  await more.focus()
  await more.press("ArrowDown")
  await expect(page.getByRole("menuitem", { name: /Rename/ })).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(more).toBeFocused()
})

test("the sidebar is a fixed list of places that stays put while browsing", async ({ page }) => {
  await mockFiles(page)
  await openFiles(page)

  // The places, the starred folder and the system directories are all on the page.
  const sidebar = page.getByRole("navigation", { name: "Places" })
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

test("the commands sit in the workbench's strip, not in a page header", async ({ page }) => {
  await mockFiles(page)
  await openFiles(page)

  await expect(page.locator("[data-slot='page-header']")).toHaveCount(0)
  const strip = page.locator("[data-slot='pane-header']").first()
  for (const name of ["Find", "New", "Upload", "Hide the sidebar", "Hide the details"]) {
    await expect(strip.getByRole("button", { name, exact: true })).toBeVisible()
  }
  // The strip starts where the page's frame does: nothing sits above it.
  const frame = await strip.boundingBox()
  const pageBox = await page.locator("[data-slot='page']").boundingBox()
  expect(frame!.y - pageBox!.y).toBeLessThan(20)
})

test("tiles stay compact and toolbar controls share a height", async ({ page }) => {
  await mockFiles(page)
  await openFiles(page)
  await page.getByRole("radio", { name: "Tiles", exact: true }).click()
  const tile = page.locator('[data-entry-path="' + home + '/photos"]').first()
  const box = await tile.boundingBox()
  expect(box!.width).toBeLessThanOrEqual(132)
  expect(box!.height).toBeLessThan(130)
  const strip = page.locator("[data-slot='pane-header']").first()
  const heights = await Promise.all(
    ["Find", "New", "Upload", "Refresh", "Arrange"].map(async (name) => {
      const button = strip.getByRole("button", { name, exact: true })
      return (await button.boundingBox())!.height
    }),
  )
  expect(new Set(heights).size).toBe(1)
  expect(
    await page
      .getByRole("navigation", { name: "Places" })
      .locator('[aria-current="location"] .rounded-full')
      .count(),
  ).toBe(0)
})

test("find responds from the current listing before a disk search and opens from the keyboard", async ({
  page,
}) => {
  await mockFiles(page)
  await page.route("**/api/v1/files/find**", async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 500))
    await json(route, { root: home, hits: [], truncated: false, visited: 5, elapsedMs: 500 })
  })
  await openFiles(page)
  await page.getByRole("button", { name: "Find", exact: true }).click()
  const input = page.getByRole("combobox", { name: "Find by name" })
  await input.fill("ntsmd")
  await expect(page.getByRole("option")).toContainText("notes.md")
  await input.press("Enter")
  await expect(page.getByRole("dialog")).toContainText("notes.md")
  await expect(page.getByRole("button", { name: "Open full editor" })).toBeVisible()
})

test("content search highlights matches, opens at the line and rejects stale results", async ({
  page,
}) => {
  await mockFiles(page)
  await page.route("**/api/v1/files/search**", async (route) => {
    const query = new URL(route.request().url()).searchParams.get("q")
    if (query === "old") await new Promise((resolve) => setTimeout(resolve, 700))
    await json(route, {
      hits: [
        {
          path: home + "/notes.md",
          name: "notes.md",
          isDir: false,
          line: 2,
          snippet: query + " hello",
          ranges: [[0, query!.length]],
        },
      ],
      truncated: false,
      unreadable: 0,
      visited: 4,
      elapsedMs: 4,
    })
  })
  await openFiles(page)
  await page.getByRole("button", { name: "Search inside files", exact: true }).click()
  const input = page.getByRole("combobox", { name: "Search file contents" })
  await input.fill("old")
  await page.waitForRequest(
    (request) => request.url().includes("/files/search") && request.url().includes("q=old"),
  )
  await input.fill("new")
  await expect(page.getByRole("option")).toContainText("new hello")
  await expect(page.getByRole("option").locator("mark")).toHaveText("new")
  await page.waitForTimeout(750)
  await expect(page.getByRole("option")).not.toContainText("old hello")
  await input.press("Enter")
  await expect(page.locator(".monaco-editor")).toBeVisible({ timeout: 20_000 })
  await expect(page.getByRole("dialog")).toContainText("Ln 2")
})

test.describe("fixed search palette", () => {
  for (const viewport of [
    { name: "desktop", width: 1280, height: 960 },
    { name: "phone", width: 375, height: 812 },
    { name: "landscape", width: 640, height: 375 },
  ]) {
    for (const mode of ["names", "content"] as const) {
      test(`${mode} keeps its frame still through every result state on ${viewport.name}`, async ({
        page,
      }, testInfo) => {
        test.setTimeout(60_000)
        await page.setViewportSize(viewport)
        await mockFiles(page)
        await page.route(
          `**/api/v1/files/${mode === "names" ? "find" : "search"}**`,
          async (route) => {
            const q = new URL(route.request().url()).searchParams.get("q")!
            await new Promise((resolve) => setTimeout(resolve, 300))
            if (q === "error")
              return json(
                route,
                {
                  error: { code: "internal", message: "Disk search unavailable", retryable: true },
                },
                500,
              )
            const count = q === "many" ? 30 : q === "single" ? 1 : q === "partial" ? 24 : 0
            await json(route, {
              hits: Array.from({ length: count }, (_, i) => ({
                ...entry(`${q}-${String(i).padStart(2, "0")}.md`),
                ...(mode === "content"
                  ? { line: i + 1, snippet: `The ${q} matching line`, ranges: [[4, 4 + q.length]] }
                  : {}),
              })),
              truncated: q === "partial",
              unreadable: q === "partial" ? 37 : 0,
              elapsedMs: 300,
            })
          },
        )
        await openFiles(page)
        const trigger = page.getByRole("button", {
          name: mode === "names" ? "Find" : "Search inside files",
          exact: true,
        })
        await trigger.click()
        const dialog = page.getByRole("dialog", { name: "Find files" })
        const input = dialog.getByRole("combobox")
        const results = dialog.getByRole("listbox", { name: "Search results" })
        await expect(input).toBeFocused()
        await dialog.evaluate(async (el) => {
          await Promise.all(el.getAnimations().map((a) => a.finished))
        })
        await dialog.evaluate((el) => {
          const samples: { bounds: number[]; entering: boolean; exiting: boolean }[] = []
          Object.assign(el, { searchSamples: samples })
          const sample = () => {
            if (!el.isConnected) return
            const rects = [
              el,
              el.querySelector("input")!,
              el.querySelector('[role="listbox"]')!,
              el.querySelector('[data-slot="pane-footer"]')!,
            ]
            const bounds = rects.flatMap((node) => {
              const { x, y, width, height } = node.getBoundingClientRect()
              return [x, y, width, height]
            })
            let entering = false
            let exiting = false
            for (const node of el.querySelectorAll("[data-search-result]")) {
              const opacity = Number(getComputedStyle(node).opacity)
              if (opacity > 0 && opacity < 1) {
                if (node.hasAttribute("inert")) exiting = true
                else entering = true
              }
            }
            samples.push({ bounds, entering, exiting })
            requestAnimationFrame(sample)
          }
          requestAnimationFrame(sample)
        })
        const border = await input.evaluate((el) => {
          const style = getComputedStyle(el)
          return { outline: style.outlineStyle, border: style.borderWidth }
        })
        expect(border).toEqual({ outline: "none", border: "0px" })
        const frame = (await dialog.boundingBox())!
        expect(frame.x).toBeGreaterThanOrEqual(15)
        expect(frame.y).toBeGreaterThanOrEqual(15)
        expect(frame.x + frame.width).toBeLessThanOrEqual(viewport.width - 15)
        expect(frame.y + frame.height).toBeLessThanOrEqual(viewport.height - 15)
        for (const [query, count] of [
          ["many", 30],
          ["single", 1],
          ["missing", 0],
          ["partial", 24],
          ["error", 0],
        ] as const) {
          if (query === "many") await input.pressSequentially(query, { delay: 55 })
          else await input.fill(query)
          await expect(results).toHaveAttribute("aria-busy", "false")
          await expect(results.getByRole("option")).toHaveCount(count)
          if (count) {
            const first = results.getByRole("option").first()
            await expect(first.locator("..")).toHaveCSS("opacity", "1")
            await expect(input).toHaveAttribute(
              "aria-activedescendant",
              (await first.getAttribute("id"))!,
            )
            if (mode === "content") await expect(first.locator("mark")).toHaveText(query)
            if (query === "many") {
              await input.press("ArrowUp")
              await expect(results.getByRole("option").last()).toHaveAttribute(
                "aria-selected",
                "true",
              )
              await expect(results.getByRole("option").last()).toBeInViewport()
              await input.press("ArrowDown")
              await expect(first).toHaveAttribute("aria-selected", "true")
            }
          } else await expect(input).not.toHaveAttribute("aria-activedescendant")
          if (query === "missing") await expect(results).toContainText("Try fewer words")
          if (query === "partial") {
            await expect(dialog.getByRole("status")).toContainText("Partial results")
            await expect(dialog).toContainText("37 entries could not be read")
          }
          if (query === "error") await expect(results).toContainText("Disk search unavailable")
          if (query === "many" || query === "single")
            await page.screenshot({
              path: `test-results/files-search-${mode}-${viewport.name}-${query}.png`,
            })
          await expect(input).toBeFocused()
        }
        await results.getByRole("button", { name: /Try again/ }).click()
        await expect(results).toHaveAttribute("aria-busy", "false")
        await input.fill("partial")
        await expect(results.getByRole("option")).toHaveCount(24)
        if (mode === "content") {
          await dialog.getByRole("button", { name: "Match case", exact: true }).click()
          await expect(results).toHaveAttribute("aria-busy", "false")
          await dialog.getByRole("button", { name: "Regular expression", exact: true }).click()
          await expect(results).toHaveAttribute("aria-busy", "false")
        }
        await dialog.getByText("Hidden files", { exact: true }).click()
        await expect(results).toHaveAttribute("aria-busy", "false")
        await dialog.getByRole("button", { name: "Clear search" }).click()
        await expect(input).toHaveValue("")
        await expect(input).toBeFocused()
        await dialog
          .getByRole("button", { name: mode === "names" ? "Contents" : "Names", exact: true })
          .click()
        await expect(input).toBeFocused()
        const samples = await dialog.evaluate(
          (el) =>
            (
              el as unknown as {
                searchSamples: { bounds: number[]; entering: boolean; exiting: boolean }[]
              }
            ).searchSamples,
        )
        expect(samples.length).toBeGreaterThan(20)
        for (let i = 0; i < samples[0].bounds.length; i++) {
          const values = samples.map((sample) => sample.bounds[i])
          expect(
            Math.max(...values) - Math.min(...values),
            `frame coordinate ${i} moved`,
          ).toBeLessThan(0.6)
        }
        expect(
          samples.some((sample) => sample.entering),
          "results fade in",
        ).toBe(true)
        expect(
          samples.some((sample) => sample.exiting),
          "results fade out",
        ).toBe(true)
        await testInfo.attach("frame-measurements", {
          body: JSON.stringify(
            {
              viewport,
              mode,
              frames: samples.length,
              maximumMovement: Math.max(
                ...samples[0].bounds.map((_, i) => {
                  const values = samples.map((sample) => sample.bounds[i])
                  return Math.max(...values) - Math.min(...values)
                }),
              ),
              fadeIn: samples.some((sample) => sample.entering),
              fadeOut: samples.some((sample) => sample.exiting),
            },
            null,
            2,
          ),
          contentType: "application/json",
        })
        await page.keyboard.press("Escape")
        await expect(dialog).toBeHidden()
      })
    }
  }

  test("reduced motion shows results immediately and scope controls remain reachable", async ({
    page,
  }) => {
    await page.emulateMedia({ reducedMotion: "reduce" })
    await mockFiles(page)
    await openFiles(page)
    await page.getByRole("navigation", { name: "Places" }).locator("button[title='/etc']").click()
    await expect(page.getByRole("button", { name: "Folders in /etc" })).toBeVisible()
    await page.setViewportSize({ width: 375, height: 812 })
    await page.getByRole("button", { name: "Find", exact: true }).click()
    const dialog = page.getByRole("dialog", { name: "Find files" })
    const input = dialog.getByRole("combobox")
    const frame = await dialog.boundingBox()
    await expect(dialog.getByRole("button", { name: "From home" })).toBeInViewport()
    await dialog.getByRole("button", { name: "From home" }).click()
    await expect(dialog.getByRole("button", { name: "This folder" })).toBeInViewport()
    await input.fill("missing")
    await expect(dialog.getByRole("listbox")).toHaveAttribute("aria-busy", "false")
    const result = dialog.locator("[data-search-result]")
    await expect(result).toHaveCSS("opacity", "1")
    await expect(result).toHaveCSS("transform", "none")
    expect(await dialog.boundingBox()).toEqual(frame)
    await page.screenshot({ path: "test-results/files-search-scope-phone.png" })
    await page.keyboard.press("Escape")
    await expect(dialog).toBeHidden()
  })
})

test("the full editor preserves the sheet draft, saves it and navigates through a collapsible tree", async ({
  page,
}) => {
  await mockFiles(page)
  let written: { content?: string } = {}
  await page.route("**/api/v1/files/write", async (route) => {
    written = route.request().postDataJSON()
    await json(route, { ok: true })
  })
  await openFiles(page)
  await page.locator('tr[data-entry-path="' + home + '/notes.md"]').dblclick()
  await expect(page.locator(".monaco-editor")).toBeVisible({ timeout: 20_000 })
  await page.locator(".monaco-editor").click()
  await page.keyboard.press("Control+End")
  await page.keyboard.insertText("unsaved draft")
  await expect(page.getByRole("dialog")).toContainText("Unsaved changes")
  await page.getByRole("button", { name: "Open full editor" }).click()
  await expect(page).toHaveURL(/\/files\/editor\?/)
  await expect(page.getByRole("complementary", { name: "File explorer" })).toBeVisible()
  await expect(page.locator(".monaco-editor")).toContainText("unsaved draft")
  await page.getByRole("button", { name: "Hide file tree" }).click()
  await expect(page.getByRole("complementary", { name: "File explorer" })).toHaveCount(0)
  await page.getByRole("button", { name: "Show file tree" }).click()
  await page.getByRole("button", { name: "Save", exact: true }).click()
  await expect.poll(() => written.content).toContain("unsaved draft")
  await page
    .getByRole("complementary", { name: "File explorer" })
    .getByRole("button", { name: /logo.png/ })
    .click()
  await expect(page.getByRole("button", { name: "Crop", exact: true })).toBeVisible()
})

test("unsaved full-page edits are guarded when changing files or leaving", async ({ page }) => {
  await mockFiles(page)
  await page.route("**/api/v1/files/find**", (route) =>
    json(route, { hits: [entry("notes.md")], truncated: false }),
  )
  await openFiles(page)
  await page.locator('tr[data-entry-path="' + home + '/notes.md"]').dblclick()
  await page.getByRole("button", { name: "Open full editor" }).click()
  await expect(page).toHaveURL(/\/files\/editor\?/)
  await expect(page.locator(".monaco-editor")).toBeVisible({ timeout: 20_000 })
  await page.locator(".monaco-editor").click()
  await page.keyboard.press("Control+End")
  await page.keyboard.insertText("dirty")
  await expect(page.getByRole("region", { name: "File editor" })).toContainText("Unsaved changes")
  await page.keyboard.press("Control+p")
  const finder = page.getByRole("combobox", { name: "Find by name" })
  await finder.fill("notes")
  await expect(page.getByRole("option")).toContainText("notes.md")
  await finder.press("Enter")
  await expect(page.getByRole("dialog")).toHaveCount(0)
  await expect(page.locator(".monaco-editor")).toContainText("dirty")
  await page.getByRole("button", { name: "Files", exact: true }).click()
  await expect(page.getByRole("dialog")).toContainText("Leave without saving?")
  await page.getByRole("button", { name: "Cancel", exact: true }).click()
  await expect(page.locator(".monaco-editor")).toContainText("dirty")
  await page.evaluate(() => history.back())
  await expect(page.getByRole("dialog")).toContainText("Leave without saving?")
  await expect(page).toHaveURL(/\/files\/editor\?/)
  await page.getByRole("button", { name: "Cancel", exact: true }).click()
  await page.getByRole("button", { name: "Files", exact: true }).click()
  await page.getByRole("button", { name: "Discard and leave", exact: true }).click()
  await expect(page).toHaveURL(/\/files\?path=/)
})

test("local name results remain usable when the disk search fails", async ({ page }) => {
  await mockFiles(page)
  await page.route("**/api/v1/files/find**", (route) =>
    json(
      route,
      { error: { code: "internal", message: "Disk search unavailable", retryable: true } },
      500,
    ),
  )
  await openFiles(page)
  await page.getByRole("button", { name: "Find", exact: true }).click()
  const input = page.getByRole("combobox", { name: "Find by name" })
  await input.fill("ntsmd")
  await expect(page.getByText("Disk search unavailable")).toBeVisible()
  await expect(page.getByRole("option")).toContainText("notes.md")
  await input.press("Enter")
  await expect(page.getByRole("button", { name: "Open full editor" })).toBeVisible()
})

test("a deep-linked file unfolds its ancestor folders in the full editor", async ({ page }) => {
  await mockFiles(page)
  const path = home + "/site/src/app.ts"
  await page.route("**/api/v1/files/list**", (route) => {
    const dir = new URL(route.request().url()).searchParams.get("path")
    const listing =
      dir === home
        ? [entry("site", { isDir: true })]
        : dir === home + "/site"
          ? [entry("src", { path: home + "/site/src", isDir: true })]
          : [entry("app.ts", { path })]
    return json(route, { path: dir, entries: listing })
  })
  await page.goto(`/files/editor?path=${encodeURIComponent(path)}&root=${encodeURIComponent(home)}`)
  await expect(
    page
      .getByRole("complementary", { name: "File explorer" })
      .getByRole("button", { name: /app.ts/ }),
  ).toBeVisible()
})

test("image edits have crop handles, redo and a full editor that keeps the edit", async ({
  page,
}) => {
  await mockFiles(page)
  await mockPicture(page)
  await openFiles(page)
  await page.locator('tr[data-entry-path="' + home + '/logo.png"]').click()
  await page.getByRole("button", { name: "Crop, rotate, resize" }).click()
  await page.getByRole("button", { name: "Right", exact: true }).click()
  await expect(page.getByLabel("Image editing canvas")).toHaveAttribute("width", "40")
  await page.getByRole("button", { name: "Undo", exact: true }).click()
  await expect(page.getByLabel("Image editing canvas")).toHaveAttribute("width", "80")
  await page.getByRole("button", { name: "Redo", exact: true }).click()
  await page.getByRole("button", { name: "Open full editor" }).click()
  await expect(page).toHaveURL(/\/files\/editor\?/)
  await expect(page.getByLabel("Image editing canvas")).toHaveAttribute("width", "40")
  await page.getByRole("button", { name: "Crop", exact: true }).click()
  await expect(page.locator(".ReactCrop__drag-handle").first()).toBeVisible()
  await expect(page.getByRole("combobox", { name: "Crop aspect ratio" })).toBeVisible()
  await page.getByRole("button", { name: /^Apply \d/ }).click()
  await expect(page.getByLabel("Image editing canvas")).toHaveAttribute("width", "32")
})

test("full editors and the search palette fit desktop and phone widths", async ({ page }) => {
  await mockFiles(page)
  await mockPicture(page, 320, 180)
  await openFiles(page)
  for (const width of [1280, 1720]) {
    await page.setViewportSize({ width, height: 960 })
    await page.getByRole("radio", { name: "Tiles", exact: true }).click()
    await page.screenshot({ path: `test-results/files-tiles-${width}.png` })
  }
  await page.getByRole("button", { name: "Find", exact: true }).click()
  await page.getByRole("combobox", { name: "Find by name" }).fill("ntsmd")
  await expect(page.getByRole("option")).toContainText("notes.md")
  const resultBounds = await page.getByRole("listbox", { name: "Search results" }).boundingBox()
  expect(resultBounds!.height).toBeGreaterThan(300)
  await page.screenshot({ path: "test-results/files-find-1720.png" })
  await page.keyboard.press("Enter")
  await page.getByRole("button", { name: "Open full editor" }).click()
  await expect(page.locator(".monaco-editor")).toBeVisible({ timeout: 20_000 })
  for (const width of [1280, 1720]) {
    await page.setViewportSize({ width, height: 960 })
    await page.screenshot({ path: `test-results/files-editor-${width}.png` })
  }
  await page.setViewportSize({ width: 375, height: 812 })
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBe(375)
  await page.screenshot({ path: "test-results/files-editor-phone.png" })
  await expect(page.getByRole("complementary", { name: "File explorer" })).toBeHidden()
  await page.getByRole("button", { name: "Show file tree", exact: true }).click()
  const tree = page.getByRole("complementary", { name: "File explorer" })
  await expect(tree).toBeVisible()
  await tree.getByRole("button", { name: /logo.png/ }).click()
  await expect(tree).toBeHidden()
  await expect(page.getByRole("button", { name: "Crop", exact: true })).toBeVisible()
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth)).toBe(375)
  await page.screenshot({ path: "test-results/files-image-editor-phone.png" })
  await page.setViewportSize({ width: 640, height: 375 })
  await expect(
    page.getByRole("button", { name: "Save over original", exact: true }),
  ).toBeInViewport()
  await page.getByRole("slider", { name: "saturate", exact: true }).scrollIntoViewIfNeeded()
  await expect(page.getByRole("slider", { name: "saturate", exact: true })).toBeInViewport()
  await page.setViewportSize({ width: 1720, height: 960 })
  await page.screenshot({ path: "test-results/files-image-editor-1720.png" })
})

test("image export saves live adjustments and a copy keeps the source dirty", async ({ page }) => {
  await mockFiles(page)
  await mockPicture(page)
  let upload: { url: string; body: Buffer } | undefined
  await page.route("**/api/v1/files/upload**", async (route) => {
    upload = { url: route.request().url(), body: route.request().postDataBuffer()! }
    await json(route, { ok: true })
  })
  await page.goto(
    `/files/editor?path=${encodeURIComponent(home + "/logo.png")}&root=${encodeURIComponent(home)}`,
  )
  await expect(page.getByLabel("Image editing canvas")).toBeVisible()
  const brightness = page.getByRole("slider", { name: "brightness", exact: true })
  await brightness.focus()
  await brightness.press("ArrowLeft")
  await expect(page.getByText("Unsaved edits", { exact: true })).toBeVisible()
  await page.getByRole("textbox", { name: "Save image as" }).fill("copy.png")
  await page.getByRole("button", { name: "Save as", exact: true }).click()
  await expect.poll(() => upload?.url).toContain("overwrite=false")
  expect(upload!.body.toString("latin1")).toContain('filename="copy.png"')
  const start = upload!.body.indexOf(Buffer.from("89504e470d0a1a0a", "hex"))
  const end = upload!.body.indexOf(Buffer.from("0000000049454e44", "hex"), start) + 12
  expect(start).toBeGreaterThan(0)
  const encoded = upload!.body.subarray(start, end).toString("base64")
  const red = await page.evaluate(async (encoded) => {
    const data = Uint8Array.from(atob(encoded), (char) => char.charCodeAt(0))
    const blob = new Blob([data], { type: "image/png" })
    const bitmap = await createImageBitmap(blob)
    const canvas = document.createElement("canvas")
    canvas.width = bitmap.width
    canvas.height = bitmap.height
    const ctx = canvas.getContext("2d")!
    ctx.drawImage(bitmap, 0, 0)
    const value = ctx.getImageData(0, 0, 1, 1).data[0]
    bitmap.close()
    return value
  }, encoded)
  expect(red).toBeGreaterThan(0)
  expect(red).toBeLessThan(255)
  await expect(page.getByText("Unsaved edits", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Files", exact: true }).click()
  await expect(page.getByRole("dialog")).toContainText("Leave without saving?")
  await page.getByRole("button", { name: "Cancel", exact: true }).click()
  await page.getByRole("button", { name: "Save over original", exact: true }).click()
  await expect.poll(() => upload?.url).toContain("overwrite=true")
  await expect(page.getByText("Unsaved edits", { exact: true })).toBeHidden()
})

test("a folder's colour is drawn everywhere it is and saved on the server", async ({ page }) => {
  const colours: Record<string, string> = { [`${home}/photos`]: "red" }
  await mockFiles(page, colours)
  const sent: { path: string; colour: string }[] = []
  page.on("request", (request) => {
    if (request.url().includes("/api/v1/files/colours")) sent.push(request.postDataJSON())
  })
  await openFiles(page)

  // The label the server keeps is the one drawn, in the listing and the sidebar.
  const photos = `${home}/photos`
  const row = page.locator(`tr[data-entry-path='${photos}'] [data-folder]`)
  await expect(row).toHaveAttribute("style", /--folder-red/)
  await expect(
    page
      .getByRole("navigation", { name: "Places" })
      .locator(`button[title='${photos}'] [data-folder]`),
  ).toHaveAttribute("style", /--folder-red/)

  // Picked in the inspector, it is drawn at once and sent for that folder.
  await page.locator(`tr[data-entry-path='${home}/site']`).click()
  await page.getByRole("radio", { name: "Green" }).click()
  await expect(page.locator(`tr[data-entry-path='${home}/site'] [data-folder]`)).toHaveAttribute(
    "style",
    /--folder-green/,
  )
  await expect.poll(() => sent).toEqual([{ path: `${home}/site`, colour: "green" }])

  // Choosing a folder's own default clears its label rather than storing it.
  await page.getByRole("radio", { name: "Blue" }).click()
  await expect.poll(() => sent.at(-1)).toEqual({ path: `${home}/site`, colour: "" })
  await expect(page.locator(`tr[data-entry-path='${home}/site'] [data-folder]`)).toHaveAttribute(
    "style",
    /--folder-blue/,
  )
  expect(colours).toEqual({ [`${home}/photos`]: "red" })
})

test("the compact toolbar control colours all folders and individual colours still work", async ({
  page,
}) => {
  const colours: Record<string, string> = { [`${home}/photos`]: "red" }
  const palette: { defaultColour?: string } = {}
  await mockFiles(page, colours, palette)
  const sent: { url: string; body: { path?: string; colour: string } }[] = []
  page.on("request", (request) => {
    if (request.url().includes("/api/v1/files/colours")) {
      sent.push({ url: request.url(), body: request.postDataJSON() })
    }
  })
  await openFiles(page)

  const button = page.getByRole("button", { name: "Colour all folders" })
  const icon = button.locator("[data-folder]")
  const buttonBox = await button.boundingBox()
  const iconBox = await icon.boundingBox()
  expect(iconBox!.width).toBeLessThanOrEqual(18)
  expect(buttonBox!.width).toBeLessThanOrEqual(32)

  await button.click()
  await page.getByRole("menuitem", { name: "Yellow" }).click()
  await expect(page.locator(`tr[data-entry-path='${home}/photos'] [data-folder]`)).toHaveAttribute(
    "style",
    /--folder-yellow/,
  )
  await expect(page.locator(`tr[data-entry-path='${home}/site'] [data-folder]`)).toHaveAttribute(
    "style",
    /--folder-yellow/,
  )
  await expect.poll(() => sent[0]?.body).toEqual({ colour: "yellow" })
  expect(sent[0].url).toContain("/files/colours/default")
  await expect.poll(() => palette.defaultColour).toBe("yellow")
  expect(colours).toEqual({})

  await page.locator(`tr[data-entry-path='${home}/site']`).click()
  await page.getByRole("radio", { name: "Green" }).click()
  await expect(page.locator(`tr[data-entry-path='${home}/site'] [data-folder]`)).toHaveAttribute(
    "style",
    /--folder-green/,
  )
  await expect(page.locator(`tr[data-entry-path='${home}/photos'] [data-folder]`)).toHaveAttribute(
    "style",
    /--folder-yellow/,
  )
  await expect.poll(() => sent[1]?.body).toEqual({ path: `${home}/site`, colour: "green" })

  await page.reload()
  await expect(page.locator(`tr[data-entry-path='${home}/site'] [data-folder]`)).toHaveAttribute(
    "style",
    /--folder-green/,
  )
  await expect(page.locator(`tr[data-entry-path='${home}/photos'] [data-folder]`)).toHaveAttribute(
    "style",
    /--folder-yellow/,
  )
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
