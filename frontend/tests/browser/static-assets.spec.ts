import { expect, test } from "@playwright/test"
import { readFileSync } from "node:fs"
import { join } from "node:path"

/**
 * How the server answers for the files it copies into `public/`.
 *
 * None of them has a hash in its name, so each was answered with `max-age=0`
 * and a page's policy with a fresh nonce: opening an editor asked the server
 * about dozens of files it already had, one round trip each. The two runtimes
 * are served under their package's version and kept for good, the logos for a
 * day, and none of them is a page.
 */

const installed = (name: string): string =>
  JSON.parse(readFileSync(join(process.cwd(), "node_modules", name, "package.json"), "utf8"))
    .version

const monaco = `/monaco/${installed("monaco-editor")}/vs`
const fonts = `/excalidraw/${installed("@excalidraw/excalidraw")}/fonts`

test("the copied runtimes are kept for good, under their version", async ({ request }) => {
  for (const path of [
    `${monaco}/loader.js`,
    `${monaco}/editor/editor.main.js`,
    `${fonts}/Virgil/Virgil-Regular.woff2`,
  ]) {
    const answer = await request.get(path)
    expect(answer.status(), path).toBe(200)
    expect(answer.headers()["cache-control"], path).toBe("public, max-age=31536000, immutable")
    expect(answer.headers()["content-security-policy"], path).toBeUndefined()
  }
  // The address without a version serves no file any more (the shell answers
  // an unknown address with its own page), so nothing can be reading a copy
  // that an update would leave stale.
  const former = await request.get("/monaco/vs/loader.js")
  expect(former.headers()["content-type"]).toContain("text/html")
  const formerFont = await request.get("/excalidraw/fonts/Virgil/Virgil-Regular.woff2")
  expect(formerFont.headers()["content-type"]).toContain("text/html")
})

test("a logo is kept for a day and is not given a page's policy", async ({ request }) => {
  for (const path of ["/logos/alpine.svg", "/brand-logo/just-dashboard-horizontal.svg"]) {
    const answer = await request.get(path)
    expect(answer.status(), path).toBe(200)
    expect(answer.headers()["cache-control"], path).toBe(
      "public, max-age=86400, stale-while-revalidate=604800",
    )
    expect(answer.headers()["content-security-policy"], path).toBeUndefined()
  }
})

test("a page still carries its own policy and is not kept", async ({ request }) => {
  const answer = await request.get("/login")
  expect(answer.status()).toBe(200)
  expect(answer.headers()["content-security-policy"]).toContain("script-src 'self' 'nonce-")
  expect(answer.headers()["cache-control"] ?? "").not.toContain("immutable")
})

test("the editor loads from the versioned folder", async ({ page }) => {
  const asked: string[] = []
  page.on("request", (r) => {
    const { pathname } = new URL(r.url())
    if (pathname.startsWith("/monaco/")) asked.push(pathname)
  })
  const now = new Date().toISOString()
  await page.routeWebSocket("**/api/v1/**", () => {})
  await page.route("**/api/v1/**", (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace(/^\/api\/v1/, "")
    const json = (body: unknown) =>
      route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
    if (path === "/auth/session")
      return json({
        authenticated: true,
        needsTotp: false,
        needsEnrollment: false,
        require2fa: false,
        capabilities: ["read", "file.write"],
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
      })
    if (path === "/files/places")
      return json({ home: "/srv", roots: ["/"], places: [], bookmarks: [], colours: {} })
    if (path === "/files/list")
      return json({ path: "/srv", parent: "/", entries: [], roots: ["/"] })
    if (path === "/files/read")
      return json({
        path: url.searchParams.get("path"),
        content: '{ "name": "atlas" }\n',
        size: 20,
        language: "json",
        binary: false,
        modeOctal: "0644",
      })
    if (path === "/files/preview")
      return json({
        path: "/srv/app.json",
        name: "app.json",
        modified: now,
        modeOctal: "0644",
        owner: "operator",
        group: "operator",
        kind: "text",
        size: 20,
        editable: true,
        language: "json",
        text: '{ "name": "atlas" }\n',
        lines: 1,
      })
    return json([])
  })
  await page.goto(`/files/editor?path=${encodeURIComponent("/srv/app.json")}`)
  await expect(page.locator(".monaco-editor").first()).toBeVisible({ timeout: 30_000 })
  await expect(page.locator(".monaco-editor .view-lines").first()).toContainText("atlas")
  expect(asked.length).toBeGreaterThan(0)
  expect(asked.filter((path) => !path.startsWith(`${monaco}/`))).toEqual([])
})
