import type { Page, Route } from "@playwright/test"

/**
 * A mocked server for the Files page's reading of text and media: folders of
 * named files, each with the preview the server would send and the bytes the
 * raw and thumbnail routes would. Every request the page makes is recorded,
 * so a spec can say what was and was not fetched.
 */

export const home = "/home/operator"
const now = new Date().toISOString()

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

export type Bytes = { contentType: string; body: Buffer }

export type FakeFile = {
  name: string
  isDir?: boolean
  size?: number
  /** What `/files/preview` says beyond the name, path and facts. */
  preview?: Record<string, unknown>
  /** What `/files/raw` answers; nothing leaves the request waiting. */
  raw?: Bytes
}

/** A 1×1 PNG: all a picture needs to prove it loaded. */
export const png = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==",
  "base64",
)

/** A text file's preview: its head, its line count, and whether that is all of it. */
export function text(body: string, options: { truncated?: boolean; size?: number } = {}) {
  const lines = body.replace(/\n$/, "").split("\n").length
  return {
    size: options.size ?? body.length,
    preview: {
      kind: "text",
      text: body,
      lines,
      truncated: options.truncated ?? false,
      editable: !options.truncated,
      language: "plaintext",
    },
  }
}

function entryOf(dir: string, file: FakeFile) {
  return {
    name: file.name,
    path: `${dir === "/" ? "" : dir}/${file.name}`,
    size: file.size ?? 0,
    mode: file.isDir ? "drwxr-xr-x" : "-rw-r--r--",
    modeOctal: file.isDir ? "0755" : "0644",
    isDir: file.isDir ?? false,
    isSymlink: false,
    modified: now,
    owner: "operator",
    group: "operator",
    uid: 1000,
    gid: 1000,
  }
}

export type Mocked = {
  /** Every request the page made, as its full URL. */
  requests: string[]
}

export async function mockViewing(
  page: Page,
  folders: Record<string, FakeFile[]>,
  extra?: (route: Route, url: URL) => Promise<boolean>,
): Promise<Mocked> {
  const mocked: Mocked = { requests: [] }
  page.on("request", (request) => mocked.requests.push(request.url()))
  const find = (path: string) => {
    const dir = path.slice(0, path.lastIndexOf("/")) || "/"
    const name = path.slice(path.lastIndexOf("/") + 1)
    return { dir, file: folders[dir]?.find((f) => f.name === name) }
  }
  await page.routeWebSocket("**/api/v1/system/stream**", (socket) => socket.close())
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    if (extra && (await extra(route, url))) return
    if (path === "/auth/session") return json(route, user)
    if (path === "/updates/self") return json(route, { current: "0.9.1", latest: "0.9.1" })
    if (path === "/files/places") {
      return json(route, {
        home,
        roots: ["/"],
        places: [
          { name: "operator", path: home, kind: "home", hint: "Where this dashboard starts" },
          { name: "/", path: "/", kind: "root", hint: "Permitted root" },
        ],
        bookmarks: [],
        colours: {},
      })
    }
    if (path === "/files/list") {
      const dir = url.searchParams.get("path") ?? home
      const files = folders[dir] ?? []
      return json(route, {
        path: dir,
        parent: dir.split("/").slice(0, -1).join("/") || "/",
        entries: files.map((f) => entryOf(dir, f)),
        roots: ["/"],
      })
    }
    if (path === "/files/preview") {
      const target = url.searchParams.get("path") ?? ""
      const { dir, file } = find(target)
      const entry = entryOf(dir, file ?? { name: target.split("/").pop() ?? "", isDir: true })
      return json(route, {
        path: target,
        name: entry.name,
        modified: now,
        modeOctal: entry.modeOctal,
        owner: "operator",
        group: "operator",
        size: entry.size,
        editable: false,
        ...(file?.isDir || !file ? { kind: "dir", childCount: 0 } : {}),
        ...file?.preview,
      })
    }
    if (path === "/files/raw" || path === "/files/thumbnail") {
      const { file } = find(url.searchParams.get("path") ?? "")
      if (!file?.raw) return new Promise(() => undefined)
      return route.fulfill({
        status: 200,
        contentType: path === "/files/thumbnail" ? "image/jpeg" : file.raw.contentType,
        body: file.raw.body,
      })
    }
    return json(route, [])
  })
  return mocked
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

/** The page, with the listing of `dir` on screen. */
export async function openFolder(page: Page, dir = home) {
  await page.goto(`/files?path=${encodeURIComponent(dir)}`)
  await page.locator("[data-file-listing] [data-entry-path]").first().waitFor()
}
