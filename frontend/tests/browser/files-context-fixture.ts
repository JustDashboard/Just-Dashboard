import type { Page, Route } from "@playwright/test"

/**
 * A synthetic server for the Files page's server context: a folder whose
 * entries are a Postgres volume, a Compose stack, a site's root under a long
 * name, its stack's file, and two that are nothing in particular. Every name
 * and path is made up.
 */

export const folder = "/srv/fixture"
const now = new Date().toISOString()

export const user = {
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

export const LONG_NAME = "a-folder-whose-name-runs-on-and-on-for-a-while"

function entry(name: string, isDir: boolean) {
  return {
    name,
    path: `${folder}/${name}`,
    size: isDir ? 0 : 120,
    mode: isDir ? "drwxr-xr-x" : "-rw-r--r--",
    modeOctal: isDir ? "0755" : "0644",
    isDir,
    isSymlink: false,
    modified: now,
    owner: "operator",
    group: "operator",
    uid: 1000,
    gid: 1000,
  }
}

export const entries = [
  entry("pgdata", true),
  entry("shop", true),
  entry(LONG_NAME, true),
  entry("plain", true),
  entry("compose.yaml", false),
  entry("notes.md", false),
]

export const context = {
  path: folder,
  facts: [{ kind: "not-backed-up", source: "backups", names: [], href: "/backups" }],
  children: {
    [`${folder}/pgdata`]: [
      {
        kind: "volume",
        source: "docker",
        names: ["shop_pgdata", "shop-db"],
        image: "postgres:16",
        state: "used",
        href: "/docker/volumes/shop_pgdata",
        inherits: true,
      },
      { kind: "not-backed-up", source: "backups", names: [], href: "/backups" },
    ],
    [`${folder}/shop`]: [
      {
        kind: "compose",
        source: "docker",
        names: ["shop"],
        product: "docker-compose",
        state: "running",
        href: "/docker/stacks/shop",
        inherits: true,
      },
    ],
    [`${folder}/${LONG_NAME}`]: [
      {
        kind: "site-root",
        source: "proxy",
        names: ["a-rather-long-subdomain-for-the-fixture.example-shop-store.test"],
        product: "nginx",
        href: "/proxy/sites/fixture-site",
        inherits: true,
      },
    ],
    [`${folder}/compose.yaml`]: [
      {
        kind: "compose-file",
        source: "docker",
        names: ["shop"],
        product: "docker-compose",
        href: "/docker/stacks/shop",
      },
    ],
  },
  partial: ["git"],
  checkedAt: now,
}

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

export type ContextAnswer = "facts" | "fail" | "hang"

/** The Files routes this page reads, and `/files/context` answering as asked. */
export async function mockContextFiles(page: Page, answer: ContextAnswer = "facts") {
  await page.routeWebSocket("**/api/v1/system/stream**", (socket) => socket.close())
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    if (path === "/auth/session") return json(route, user)
    if (path === "/updates/self") return json(route, { current: "0.9.1", latest: "0.9.1" })
    if (path === "/files/places") {
      return json(route, {
        home: folder,
        roots: ["/"],
        places: [{ name: "fixture", path: folder, kind: "home", hint: "Where this starts" }],
        bookmarks: [],
        colours: {},
      })
    }
    if (path === "/files/list") {
      const dir = url.searchParams.get("path") ?? folder
      if (dir === folder)
        return json(route, { path: folder, parent: "/srv", entries, roots: ["/"] })
      return json(route, { path: dir, parent: folder, entries: [], roots: ["/"] })
    }
    if (path === "/files/context") {
      if (answer === "fail") return json(route, { error: "boom", code: "unknown" }, 500)
      if (answer === "hang") return new Promise<void>(() => undefined)
      if (url.searchParams.get("path") !== folder) {
        return json(route, {
          path: url.searchParams.get("path"),
          facts: [],
          children: {},
          partial: [],
          checkedAt: now,
        })
      }
      return json(route, context)
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
      if (name.endsWith(".md") || name.endsWith(".yaml")) {
        return json(route, {
          ...base,
          kind: "text",
          size: 120,
          editable: true,
          language: "yaml",
          text: "x\n",
          lines: 1,
        })
      }
      return json(route, { ...base, kind: "dir", size: 0, editable: false, childCount: 0 })
    }
    return json(route, [])
  })
}
