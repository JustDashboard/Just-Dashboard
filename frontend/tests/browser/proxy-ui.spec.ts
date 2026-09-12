import { expect, test, type Page, type Route } from "@playwright/test"

/**
 * Two ways the proxy pages told an operator something that was not true.
 *
 * A "+ New stream" button sat above a banner explaining that nginx was not
 * reading these files, and clicking it led to a form that said "Save and
 * reload" and a toast that said the port was forwarding. Nothing in that
 * sequence was accurate until somebody hand-edited nginx.conf.
 *
 * And the Reverse proxy tile read "nginx nginx version: nginx/1.26.3" —
 * `nginx -v` prints a whole sentence, which was stored verbatim and then had
 * the word "nginx" prepended to it.
 */

const now = new Date().toISOString()

const user = {
  authenticated: true,
  needsTotp: false,
  needsEnrollment: false,
  require2fa: false,
  capabilities: ["read", "service.control", "file.write", "terminal", "destructive", "system.admin"],
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

/** What the backend answers once it parses `nginx -v` rather than storing it. */
const availability = {
  nginx: true,
  nginxVersion: "nginx/1.26.3",
  caddy: false,
  caddyVersion: "",
  nginxDir: "/etc/nginx",
  caddyFile: "/etc/caddy/Caddyfile",
  certbot: true,
}

const snippet = "stream {\n    include /etc/nginx/streams/*.conf;\n}"

async function json(route: Route, body: unknown) {
  await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
}

async function mockProxy(page: Page, { included }: { included: boolean }) {
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace(/^\/api\/v1/, "")
    switch (path) {
      case "/auth/session":
        return json(route, user)
      case "/dashboard/update":
        return json(route, { current: "0.6.7", latest: "0.6.7" })
      case "/proxy/status":
        return json(route, availability)
      case "/proxy/streams/":
        return json(route, { included, snippet, dir: "/etc/nginx/streams", streams: [] })
      case "/proxy/vhosts":
        return json(route, [])
      case "/certificates/":
        return json(route, [])
      case "/ports":
        return json(route, [])
      default:
        return route.fulfill({
          status: 503,
          contentType: "application/json",
          body: JSON.stringify({ error: { code: "not_available", message: "Not mocked" } }),
        })
    }
  })
}

test("the reverse proxy tile names the engine once", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy")

  // The tile carries it, once. It used to be in the page description as well,
  // and both copies used to be wrong; the description slot is gone, so the
  // engine is named in exactly one place.
  await expect(page.getByText("nginx nginx/1.26.3")).toHaveCount(1)
  // The shape of the old bug, in either place it appeared.
  await expect(page.getByText(/nginx version:/)).toHaveCount(0)
  await expect(page.getByText(/nginx nginx version/)).toHaveCount(0)
})

test("a stream cannot be created as though nginx were reading it", async ({ page }) => {
  await mockProxy(page, { included: false })
  await page.goto("/proxy/streams")

  // The banner is unchanged — it was already honest.
  await expect(page.getByText("nginx is not reading these yet")).toBeVisible()

  // The button no longer claims to create a working forward.
  await expect(page.getByRole("button", { name: "New stream" })).toHaveCount(0)
  await page.getByRole("button", { name: "Prepare a stream" }).click()

  // And the form repeats it at the point of commit, with the fix to hand.
  await expect(page.getByText("This will not forward anything yet")).toBeVisible()
  await expect(page.getByText(/include/).first()).toBeVisible()
  await expect(page.getByRole("button", { name: "Save for later" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Save and reload" })).toHaveCount(0)
})

test("once nginx is reading them, the stream form promises a live forward again", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy/streams")

  await expect(page.getByText("nginx is not reading these yet")).toHaveCount(0)
  await page.getByRole("button", { name: "New stream" }).click()

  await expect(page.getByText("This will not forward anything yet")).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Save and reload" })).toBeVisible()
})
