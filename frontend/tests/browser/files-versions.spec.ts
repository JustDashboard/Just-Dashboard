import { expect, test, type Page, type Route } from "@playwright/test"

/**
 * A file's versions, against a mocked API: the details pane's line that leads
 * to the history, the history as a view of the editor page with Monaco's diff
 * beside the list, Restore and its conflict, the phone's one pane at a time,
 * and an unsaved draft that survives a look at the history.
 */

const now = Date.now()
const ago = (minutes: number) => new Date(now - minutes * 60_000).toISOString()
const home = "/home/operator"
const file = `${home}/app.conf`

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
    lastLoginAt: ago(0),
    createdAt: ago(0),
  },
}

const versions = {
  path: file,
  keeping: true,
  perFile: 50,
  maxBytes: 8 << 20,
  tooLarge: false,
  backups: true,
  lastSave: { revision: "r-now", at: ago(5), by: "operator" },
  versions: [
    {
      id: "e3",
      kind: "edit",
      at: ago(5),
      by: "operator",
      reason: "save",
      outside: true,
      size: 31,
      revision: "r3",
    },
    {
      id: "e2",
      kind: "edit",
      at: ago(90),
      by: "operator",
      reason: "displaced",
      size: 24,
      revision: "r2",
    },
    {
      id: "b7",
      kind: "backup",
      at: ago(60 * 26),
      backup: {
        runId: 7,
        jobId: 1,
        job: "nightly",
        at: ago(60 * 26),
        targetKind: "b2",
        destination: "offsite-bucket",
        local: false,
      },
    },
    {
      id: "e1",
      kind: "edit",
      at: ago(60 * 50),
      by: "operator",
      reason: "save",
      size: 20,
      revision: "r1",
    },
  ],
}

const contents: Record<string, string> = {
  e3: "listen 8080\nedited in a shell\n",
  e2: "listen 8080\nworkers 4\n",
  e1: "listen 80\nworkers 2\n",
  b7: "listen 80\nworkers 1\n",
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

type Mock = { current: string; revision: string; restores: Record<string, unknown>[] }

async function mockVersions(page: Page, restore?: (body: Record<string, unknown>) => number) {
  const state: Mock = { current: "listen 8080\nworkers 8\n", revision: "r-now", restores: [] }
  await page.routeWebSocket("**/api/v1/system/stream**", (socket) => socket.close())
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, user)
    if (path === "/updates/self") return json(route, { current: "0.9.1", latest: "0.9.1" })
    if (path === "/files/places")
      return json(route, { home, roots: ["/"], places: [], bookmarks: [], colours: {} })
    if (path === "/files/list")
      return json(route, {
        path: home,
        parent: "/home",
        roots: ["/"],
        entries: [
          {
            name: "app.conf",
            path: file,
            size: state.current.length,
            mode: "-rw-r--r--",
            modeOctal: "0644",
            isDir: false,
            isSymlink: false,
            modified: ago(5),
            owner: "operator",
            group: "operator",
            uid: 1000,
            gid: 1000,
          },
          {
            name: "readme.txt",
            path: `${home}/readme.txt`,
            size: 3,
            mode: "-rw-r--r--",
            modeOctal: "0644",
            isDir: false,
            isSymlink: false,
            modified: ago(5),
            owner: "operator",
            group: "operator",
            uid: 1000,
            gid: 1000,
          },
        ],
      })
    if (path === "/files/preview") {
      const target = url.searchParams.get("path") ?? ""
      return json(route, {
        path: target,
        name: target.split("/").pop(),
        kind: "text",
        size: state.current.length,
        editable: true,
        language: "ini",
        text: state.current,
        lines: 2,
        modified: ago(5),
        modeOctal: "0644",
        owner: "operator",
        group: "operator",
      })
    }
    if (path === "/files/read")
      return json(route, {
        path: file,
        content: state.current,
        revision: state.revision,
        size: state.current.length,
        language: "ini",
        binary: false,
        modeOctal: "0644",
      })
    if (path === "/files/versions") {
      if (url.searchParams.get("path") !== file)
        return json(route, { ...versions, path: url.searchParams.get("path"), versions: [] })
      return json(route, versions)
    }
    if (path === "/files/versions/content") {
      const id = url.searchParams.get("version") ?? ""
      return json(route, { content: contents[id], binary: false, size: 10, revision: id })
    }
    if (path === "/files/versions/restore" || path === "/files/versions/restore-backup") {
      const body = route.request().postDataJSON()
      state.restores.push({ ...body, route: path })
      const status = restore?.(body) ?? 200
      if (status === 409)
        return json(
          route,
          { error: { code: "content_conflict", message: "the content changed" } },
          409,
        )
      state.current = contents[body.version]
      state.revision = `restored-${body.version}`
      return json(route, { path: file, revision: state.revision })
    }
    return json(route, [])
  })
  return state
}

const historyUrl = (version?: string) =>
  `/files/editor?path=${encodeURIComponent(file)}&root=${encodeURIComponent(home)}&view=history` +
  (version ? `&version=${version}` : "")

const versionList = (page: Page) => page.getByRole("list", { name: "Earlier versions" })

test("the details pane says how many versions a file has and leads to its history", async ({
  page,
}) => {
  await mockVersions(page)
  await page.goto("/files")
  await page.locator(`tr[data-entry-path="${file}"]`).click()
  const line = page.locator("[data-versions-line]")
  await expect(line).toHaveText(/^4, last changed 5m ago$/)
  await line.click()
  await expect(page).toHaveURL(/\/files\/editor\?.*view=history/)
  await expect(versionList(page).getByRole("link")).toHaveCount(4)
})

test("the history lists versions beside Monaco's diff, and the address carries the pick", async ({
  page,
}) => {
  await mockVersions(page)
  await page.goto(historyUrl())
  const rows = versionList(page).getByRole("link")
  await expect(rows).toHaveCount(4)
  await expect(rows.nth(0)).toContainText("Changed outside the dashboard")
  await expect(rows.nth(1)).toContainText("Saved by operator")
  await expect(rows.nth(1)).toContainText("then changed outside")
  await expect(rows.nth(2)).toContainText("nightly")
  await expect(rows.nth(2)).toContainText("in Backblaze B2")

  // The newest is compared with the file as it is now.
  const diff = page.locator("[data-version-diff] .monaco-diff-editor")
  await expect(diff).toBeVisible({ timeout: 20_000 })
  await expect(diff).toContainText("edited in a shell")
  await expect(diff).toContainText("workers 8")

  await rows.nth(3).click()
  await expect(page).toHaveURL(/version=e1/)
  await expect(rows.nth(3)).toHaveAttribute("aria-current", "page")
  await expect(diff).toContainText("workers 2")

  // Against the version before it.
  await rows.nth(0).click()
  await page.getByRole("radio", { name: "Before it" }).click()
  await expect(page).toHaveURL(/against=previous/)
  await expect(diff).toContainText("workers 4")
  await expect(diff).toContainText("edited in a shell")

  // A backup's copy on another machine is fetched only on a press.
  await page.getByRole("radio", { name: "Now" }).click()
  await rows.nth(2).click()
  await expect(page.getByText("Held in offsite-bucket on another machine")).toBeVisible()
  await expect(page.getByRole("button", { name: "Restore this version" })).toBeDisabled()
  await page.getByRole("button", { name: "Fetch this copy" }).click()
  await expect(diff).toContainText("workers 1")
})

test("restore sends the revision it compared with, and a conflict restores nothing", async ({
  page,
}) => {
  let conflict = true
  const state = await mockVersions(page, () => (conflict ? 409 : 200))
  await page.goto(historyUrl("e1"))
  await expect(page.locator("[data-version-diff] .monaco-diff-editor")).toBeVisible({
    timeout: 20_000,
  })
  await page.getByRole("button", { name: "Restore this version" }).click()
  await expect(
    page.getByRole("alert").filter({ hasText: "changed since this was opened" }),
  ).toBeVisible()
  expect(state.restores[0]).toMatchObject({
    path: file,
    version: "e1",
    expectedRevision: "r-now",
    route: "/files/versions/restore",
  })
  conflict = false
  await page.getByRole("button", { name: "Restore this version" }).click()
  await expect(page.getByText("Version restored")).toBeVisible()
  expect(state.restores).toHaveLength(2)
  await expect(
    page.getByRole("alert").filter({ hasText: "changed since this was opened" }),
  ).toHaveCount(0)
})

test("an unsaved draft is still there after a look at the history", async ({ page }) => {
  await mockVersions(page)
  await page.goto(`/files/editor?path=${encodeURIComponent(file)}&root=${encodeURIComponent(home)}`)
  const editor = page.locator(".monaco-editor").first()
  await expect(editor).toBeVisible({ timeout: 20_000 })
  await editor.click()
  await page.keyboard.press("Control+End")
  await page.keyboard.insertText("MY DRAFT")
  await expect(page.getByText("unsaved", { exact: true }).first()).toBeVisible()

  await page.getByRole("button", { name: "History" }).click()
  await expect(page).toHaveURL(/view=history/)
  // Moving into the history is not leaving: nothing asks.
  await expect(page.getByRole("alertdialog")).toHaveCount(0)
  await versionList(page).getByRole("link").nth(1).click()
  await expect(page).toHaveURL(/version=e2/)
  await expect(page.getByRole("alertdialog")).toHaveCount(0)

  await page.getByRole("button", { name: "History" }).click()
  await expect(page).not.toHaveURL(/view=history/)
  await expect(page.locator(".monaco-editor").first()).toContainText("MY DRAFT")
  await expect(page.getByText("unsaved", { exact: true }).first()).toBeVisible()
})

test.describe("on a phone", () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true })

  test("the history is one pane at a time, the list then the comparison", async ({ page }) => {
    await mockVersions(page)
    await page.goto(historyUrl())
    const bar = page.getByRole("navigation", { name: "File editor" })
    await expect(bar.getByRole("button", { name: "Versions" })).toHaveAttribute(
      "aria-current",
      "page",
    )
    const list = versionList(page)
    await expect(list).toBeVisible()
    await expect(page.locator("[data-version-diff]")).toBeHidden()
    await list.getByRole("link").nth(3).click()
    await expect(page).toHaveURL(/version=e1/)
    await expect(list).toBeHidden()
    await expect(page.locator("[data-version-diff] .monaco-diff-editor")).toBeVisible({
      timeout: 20_000,
    })
    await expect(bar.getByRole("button", { name: "Compare" })).toHaveAttribute(
      "aria-current",
      "page",
    )
    await page.getByRole("button", { name: "Back to the versions" }).click()
    await expect(list).toBeVisible()
    const width = await page.evaluate(() => document.documentElement.scrollWidth)
    expect(width).toBeLessThanOrEqual(390)
  })
})

for (const viewport of [
  { width: 1280, height: 900 },
  { width: 390, height: 844 },
]) {
  test(`a version row is one height whatever it says, at ${viewport.width}`, async ({ page }) => {
    await page.setViewportSize(viewport)
    await mockVersions(page)
    await page.goto(historyUrl())
    const rows = versionList(page).getByRole("link")
    await expect(rows).toHaveCount(4)
    // An outside change, a displaced save, a backup on another machine, a
    // plain save: four kinds of line, one height.
    const heights = await rows.evaluateAll((links) =>
      links.map((link) => Math.round(link.getBoundingClientRect().height)),
    )
    expect(new Set(heights).size).toBe(1)
    expect(heights[0]).toBe(48)
  })
}
