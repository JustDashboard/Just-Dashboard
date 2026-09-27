import { expect, test, type Page, type Request } from "@playwright/test"
import { certs, json, mockProxy, mockShowcase, now, scan, user } from "./proxy-fixtures"

/**
 * The TLS report and the watch list, checked for the things they once said
 * that were not true: a watched mail server's link scanned the wrong port, a
 * pasted URL was watched as a host called "https", a service that is not a
 * website was told it lacked HSTS, a redirect through www read as no redirect,
 * and an error sat under "Nothing scanned yet".
 */

/** Answers every scan with `report` and keeps each request it answered. */
async function scans(page: Page, report: (url: URL) => object = () => scan) {
  const seen: URL[] = []
  await page.route("**/api/v1/certificates/scan**", (route) => {
    const url = new URL(route.request().url())
    seen.push(url)
    return json(route, report(url))
  })
  return seen
}

const readOnly = { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }

test("a watched endpoint's link scans the port it names", async ({ page }) => {
  await mockShowcase(page)
  const seen = await scans(page, (url) => ({
    ...scan,
    domain: url.searchParams.get("domain"),
    port: Number(url.searchParams.get("port")),
  }))
  await page.goto("/proxy/certificates")
  await page
    .getByRole("list", { name: "Watched domains" })
    .getByRole("link", { name: "Inspect mail.example.com:993" })
    .click()
  await expect(page).toHaveURL(/\/proxy\/tls\?domain=mail\.example\.com%3A993$/)
  await expect(page.getByRole("heading", { name: "mail.example.com:993" })).toBeVisible()
  expect(seen).toHaveLength(1)
  expect(seen[0].searchParams.get("domain")).toBe("mail.example.com")
  expect(seen[0].searchParams.get("port")).toBe("993")
  await expect(page.getByLabel("Domain to scan")).toHaveValue("mail.example.com:993")
})

test("a link may give the port beside the name", async ({ page }) => {
  await mockProxy(page, { included: true })
  const seen = await scans(page)
  await page.goto("/proxy/tls?domain=mail.example.com&port=993")
  await expect.poll(() => seen.length).toBe(1)
  expect(seen[0].searchParams.get("domain")).toBe("mail.example.com")
  expect(seen[0].searchParams.get("port")).toBe("993")
})

test("a pasted URL scans its host, and what is not an address is refused before sending", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const seen = await scans(page)
  await page.goto("/proxy/tls")
  await expect(page.getByText("Nothing scanned yet")).toBeVisible()

  const field = page.getByLabel("Domain to scan")
  await field.fill("https://App.Example.com/login?next=/")
  await page.getByRole("button", { name: "Scan", exact: true }).click()
  await expect.poll(() => seen.length).toBe(1)
  expect(seen[0].searchParams.get("domain")).toBe("app.example.com")
  expect(seen[0].searchParams.get("port")).toBe("443")
  await expect(field).toHaveValue("app.example.com")

  await field.fill("exa mple.com")
  await page.getByRole("button", { name: "Scan", exact: true }).click()
  await expect(page.getByRole("alert").filter({ hasText: "is not a domain name" })).toBeVisible()
  await expect(field).toHaveAttribute("aria-invalid", "true")
  await field.fill("mail.example.com:99999")
  await page.getByRole("button", { name: "Scan", exact: true }).click()
  await expect(page.getByRole("alert").filter({ hasText: "outside 1–65535" })).toBeVisible()
  expect(seen).toHaveLength(1)
  // Typing again clears the complaint.
  await field.fill("mail.example.com")
  await expect(page.getByText("outside 1–65535")).toHaveCount(0)
  await expect(field).not.toHaveAttribute("aria-invalid", "true")
})

test("a link that is not an address is shown with its reason and nothing is scanned", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const seen = await scans(page)
  await page.goto("/proxy/tls?domain=user%40app.example.com")
  await expect(page.getByLabel("Domain to scan")).toHaveValue("user@app.example.com")
  await expect(page.getByRole("alert").filter({ hasText: "user name" })).toBeVisible()
  await page.waitForLoadState("networkidle")
  expect(seen).toHaveLength(0)
})

test("a failed scan request shows its error and not the empty state", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/certificates/scan**", (route) =>
    route.fulfill({
      status: 500,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "internal", message: "internal error" } }),
    }),
  )
  await page.goto("/proxy/tls?domain=app.example.com")
  await expect(page.getByText("internal error")).toBeVisible()
  await expect(page.getByText("Nothing scanned yet")).toHaveCount(0)
})

test("a version the server refuses reads as refused, in the server's words", async ({ page }) => {
  await mockProxy(page, { included: true })
  await scans(page, () => ({
    ...scan,
    protocols: [
      {
        name: "TLS 1.0",
        status: "refused",
        detail: "The server answered: protocol version not supported.",
      },
      {
        name: "TLS 1.1",
        status: "refused",
        detail: "The server answered: protocol version not supported.",
      },
      { name: "TLS 1.2", status: "offered" },
      { name: "TLS 1.3", status: "unknown", detail: "The server did not answer in time." },
    ],
  }))
  await page.goto("/proxy/tls?domain=app.example.com")
  const protocols = page.locator("li").filter({ hasText: /^TLS 1\.[0-3]/ })
  await expect(protocols).toHaveCount(4)
  await expect(protocols.filter({ hasText: "TLS 1.0" })).toContainText("refused")
  await expect(protocols.filter({ hasText: "TLS 1.0" })).toContainText(
    "The server answered: protocol version not supported.",
  )
  await expect(protocols.filter({ hasText: "TLS 1.3" })).toContainText(
    "The server did not answer in time.",
  )
  await expect(page.getByText(/“refused” is the server saying no/)).toBeVisible()
})

test("a service that is not a website is not graded on HTTP headers", async ({ page }) => {
  await mockProxy(page, { included: true })
  await scans(page, () => ({
    ...scan,
    domain: "mail.example.com",
    port: 993,
    findings: [
      {
        id: "http.https-error",
        level: "notice",
        title: "HTTPS did not answer an HTTP request",
        detail: 'malformed HTTP response "* OK IMAP4rev1 ready"',
      },
    ],
    http: {
      statusCode: 0,
      httpsError: 'malformed HTTP response "* OK IMAP4rev1 ready"',
      plainRedirects: false,
      redirectChain: [],
      headers: [],
    },
  }))
  await page.goto("/proxy/tls?domain=mail.example.com%3A993")
  await expect(page.getByText("no HTTP answer")).toBeVisible()
  await expect(page.getByText("Only TLS was checked.")).toBeVisible()
  await expect(page.getByText("no Strict-Transport-Security header")).toHaveCount(0)
  await expect(page.getByText("Plain HTTP", { exact: true })).toHaveCount(0)
  await expect(page.getByText("HSTS is not set")).toHaveCount(0)
})

test("a redirect through another host is drawn hop by hop and counts", async ({ page }) => {
  await mockProxy(page, { included: true })
  await scans(page, () => ({
    ...scan,
    http: {
      ...scan.http,
      plainRedirects: true,
      plainStatus: 301,
      plainLocation: "http://www.app.example.com/",
      redirectChain: [
        { url: "http://app.example.com/", status: 301, location: "http://www.app.example.com/" },
        {
          url: "http://www.app.example.com/",
          status: 301,
          location: "https://www.app.example.com/",
        },
      ],
    },
  }))
  await page.goto("/proxy/tls?domain=app.example.com")
  const plain = page.locator("li").filter({ hasText: /^Plain HTTP/ })
  await expect(plain.getByText("redirects to HTTPS in 2 hops")).toBeVisible()
  await expect(
    plain.getByText("http://app.example.com/ 301 → http://www.app.example.com/"),
  ).toBeVisible()
  await expect(
    plain.getByText("http://www.app.example.com/ 301 → https://www.app.example.com/"),
  ).toBeVisible()
  await expect(page.getByText(/without redirecting/)).toHaveCount(0)
})

test("a closed port 80 is named for what happened", async ({ page }) => {
  await mockProxy(page, { included: true })
  await scans(page, () => ({
    ...scan,
    http: {
      ...scan.http,
      plainRedirects: false,
      plainStatus: undefined,
      plainLocation: undefined,
      plainError: "dial tcp 203.0.113.4:80: i/o timeout",
      plainErrorKind: "timeout",
      redirectChain: [],
    },
  }))
  await page.goto("/proxy/tls?domain=app.example.com")
  const plain = page.locator("li").filter({ hasText: /^Plain HTTP/ })
  await expect(plain.getByText("port 80 did not answer in time")).toBeVisible()
  await expect(plain.getByText("dial tcp 203.0.113.4:80: i/o timeout")).toBeVisible()
  await expect(page.getByText("refused connection")).toHaveCount(0)
})

test("the serial is hex and OCSP is only asked of a certificate that names a responder", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const leaf: { ocspServers?: string[] } = {}
  await scans(page, () => ({ ...scan, ...leaf }))
  await page.goto("/proxy/tls?domain=app.example.com")
  await expect(page.locator('dt:has-text("Serial") + dd')).toHaveText("04:D3:51:AA:12:FE:90:81")
  await expect(page.locator('dt:has-text("OCSP stapled") + dd')).toHaveText(
    "not applicable, the certificate names no OCSP responder",
  )

  leaf.ocspServers = ["http://ocsp.example.test"]
  await page.getByRole("button", { name: "Scan", exact: true }).click()
  await expect(page.locator('dt:has-text("OCSP stapled") + dd')).toHaveText("no")
})

test("watching takes what is pasted and keeps each port", async ({ page }) => {
  await mockProxy(page, { included: true })
  const posted: unknown[] = []
  await page.route("**/api/v1/certificates/watched", async (route) => {
    const request: Request = route.request()
    if (request.method() === "POST") {
      posted.push(request.postDataJSON())
      return route.fulfill({
        status: 201,
        contentType: "application/json",
        body: JSON.stringify({ id: posted.length, ...request.postDataJSON(), createdAt: now }),
      })
    }
    return json(route, [])
  })
  await page.goto("/proxy/certificates")
  const field = page.getByLabel("Domain to watch")
  const watch = page.getByRole("button", { name: "Watch", exact: true })

  await field.fill("https://mail.example.com/webmail")
  await watch.click()
  await expect.poll(() => posted.length).toBe(1)
  await expect(field).toHaveValue("")
  await field.fill("mail.example.com:993")
  await watch.click()
  await expect.poll(() => posted.length).toBe(2)
  await field.fill("[2001:db8::1]:8443")
  await watch.click()
  await expect.poll(() => posted.length).toBe(3)
  expect(posted).toEqual([
    { domain: "mail.example.com", port: 443 },
    { domain: "mail.example.com", port: 993 },
    { domain: "2001:db8::1", port: 8443 },
  ])

  await field.fill("exa mple.com")
  await watch.click()
  await expect(page.getByRole("alert").filter({ hasText: "is not a domain name" })).toBeVisible()
  expect(posted).toHaveLength(3)
})

test("a read-only account reads the last check and cannot ask for another", async ({ page }) => {
  await mockShowcase(page)
  await page.route("**/api/v1/auth/session", (route) => json(route, readOnly))
  const lists: URL[] = []
  await page.route("**/api/v1/certificates/watched", (route) => {
    lists.push(new URL(route.request().url()))
    return json(route, [
      {
        id: 1,
        domain: "mail.example.com",
        port: 993,
        checkedAt: new Date(Date.now() - 3 * 60_000).toISOString(),
        certificate: certs[0],
      },
      { id: 2, domain: "new.example.com", port: 443 },
    ])
  })
  await page.goto("/proxy/certificates")
  const watched = page.getByRole("list", { name: "Watched domains" })
  await expect(watched.getByText(/checked 3m( \d+s)? ago/)).toBeVisible()
  await expect(watched.getByText("not checked yet")).toBeVisible()
  await expect(page.getByRole("button", { name: "Re-check now" })).toHaveCount(0)
  await expect(page.getByLabel("Domain to watch")).toHaveCount(0)
  await expect(page.getByText("checked while an administrator has this page open")).toBeVisible()
  expect(lists.length).toBeGreaterThan(0)
})

test("a scan asked for again says it is scanning until the answer replaces the report", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  let answered = 0
  let release: () => void = () => {}
  await page.route("**/api/v1/certificates/scan**", async (route) => {
    if (answered > 0) await new Promise<void>((resolve) => (release = resolve))
    answered++
    return json(route, {
      ...scan,
      summary: answered === 1 ? "The first answer" : "The second answer",
    })
  })
  await page.goto("/proxy/tls?domain=app.example.com")
  const button = page.getByRole("button", { name: "Scan", exact: true })
  await expect(page.getByText("The first answer")).toBeVisible()
  await expect(button).not.toHaveAttribute("aria-busy", "true")

  // The field already holds the target, so this is the same report again.
  await button.click()
  await expect(button).toHaveAttribute("aria-busy", "true")
  await expect(button).toBeDisabled()
  await expect(page.getByText(/Handshaking, probing each TLS version/)).toBeVisible()
  release()
  await expect(button).not.toHaveAttribute("aria-busy", "true")
  await expect(page.getByText(/Handshaking, probing each TLS version/)).toHaveCount(0)
  await expect(page.getByText("The second answer")).toBeVisible()
  expect(answered).toBe(2)
})

test("a scan asked for again that fails shows the error, not a spinner", async ({ page }) => {
  await mockProxy(page, { included: true })
  let calls = 0
  await page.route("**/api/v1/certificates/scan**", (route) => {
    calls++
    if (calls === 1) return json(route, scan)
    return route.fulfill({
      status: 502,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "upstream", message: "the scan could not run" } }),
    })
  })
  await page.goto("/proxy/tls?domain=app.example.com")
  await expect(page.getByRole("heading", { name: "app.example.com" })).toBeVisible()
  await page.getByRole("button", { name: "Scan", exact: true }).click()
  await expect(page.getByText("the scan could not run")).toBeVisible()
  await expect(page.getByText(/Handshaking, probing each TLS version/)).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Scan", exact: true })).toBeEnabled()
})

test("a redirect into this machine reads as not followed, not as broken", async ({ page }) => {
  await mockProxy(page, { included: true })
  await scans(page, () => ({
    ...scan,
    http: {
      ...scan.http,
      plainRedirects: false,
      plainStatus: 301,
      plainLocation: "http://127.0.0.1:8080/admin",
      redirectChain: [
        { url: "http://app.example.com/", status: 301, location: "http://127.0.0.1:8080/admin" },
        { url: "http://127.0.0.1:8080/admin", internal: true },
      ],
    },
  }))
  await page.goto("/proxy/tls?domain=app.example.com")
  const plain = page.locator("li").filter({ hasText: /^Plain HTTP/ })
  await expect(plain.getByText("not followed to an internal address")).toBeVisible()
  await expect(
    plain.getByText(
      "http://127.0.0.1:8080/admin not requested, on this machine or its private network",
    ),
  ).toBeVisible()
  await expect(page.getByText("a redirect leads nowhere")).toHaveCount(0)
  await expect(page.getByText("did not answer")).toHaveCount(0)
})

/** A watch list whose GETs are counted, and whose second answer can be held. */
async function watchList(page: Page, rows: object[]) {
  const lists: number[] = []
  const hold: { release?: () => void; next: boolean } = { next: false }
  await page.route("**/api/v1/certificates/watched", async (route) => {
    lists.push(Date.now())
    if (hold.next) {
      hold.next = false
      await new Promise<void>((resolve) => (hold.release = resolve))
    }
    return json(route, rows)
  })
  return { lists, hold }
}

const mailRow = {
  id: 1,
  domain: "mail.example.com",
  port: 993,
  checkedAt: new Date(Date.now() - 3 * 60_000).toISOString(),
  certificate: certs[0],
}

test("re-checking the watch list says so until the new checks arrive", async ({ page }) => {
  await mockShowcase(page)
  const { lists, hold } = await watchList(page, [mailRow])
  await page.goto("/proxy/certificates")
  const recheck = page.getByRole("button", { name: "Re-check now" })
  await expect(recheck).toBeVisible()
  hold.next = true
  await recheck.click()
  const busy = page.getByRole("button", { name: "Re-checking…" })
  await expect(busy).toHaveAttribute("aria-busy", "true")
  await expect(busy).toBeDisabled()
  await expect.poll(() => lists.length).toBe(2)
  hold.release?.()
  await expect(page.getByRole("button", { name: "Re-check now" })).toBeEnabled()
  await expect(busy).toHaveCount(0)
})

test("stopping a watch says what happened, and a failure keeps the row", async ({ page }) => {
  await mockShowcase(page)
  const { lists } = await watchList(page, [
    mailRow,
    { ...mailRow, id: 2, domain: "gone.example.com", port: 443 },
    { ...mailRow, id: 3, domain: "stuck.example.com", port: 443 },
  ])
  const deleted: string[] = []
  await page.route("**/api/v1/certificates/watched/*", (route) => {
    const id = route.request().url().split("/").pop() ?? ""
    deleted.push(id)
    if (id === "1") return route.fulfill({ status: 204 })
    if (id === "2")
      return route.fulfill({
        status: 404,
        contentType: "application/json",
        body: JSON.stringify({ error: { code: "not_found", message: "not found" } }),
      })
    return route.fulfill({
      status: 500,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "internal", message: "database is locked" } }),
    })
  })
  const errors: string[] = []
  page.on("pageerror", (error) => errors.push(error.message))
  await page.goto("/proxy/certificates")
  const list = page.getByRole("list", { name: "Watched domains" })
  await expect(list.getByRole("listitem")).toHaveCount(3)
  const loaded = lists.length

  const stop = async (label: string) => {
    await expect(page.getByRole("menu")).toHaveCount(0)
    await page.getByRole("button", { name: `More actions for ${label}` }).click()
    await page.getByRole("menuitem", { name: "Stop watching" }).click()
  }

  await stop("mail.example.com:993")
  await expect(
    page
      .locator("[data-sonner-toast]")
      .filter({ hasText: "mail.example.com:993 is no longer watched" }),
  ).toBeVisible()
  await expect(list.getByRole("listitem")).toHaveCount(2)

  await stop("gone.example.com")
  await expect(
    page.locator("[data-sonner-toast]").filter({ hasText: "gone.example.com was already removed" }),
  ).toBeVisible()
  await expect(list.getByRole("listitem")).toHaveCount(1)

  await stop("stuck.example.com")
  await expect(
    page
      .locator("[data-sonner-toast]")
      .filter({ hasText: "Could not stop watching stuck.example.com" }),
  ).toBeVisible()
  await expect(list.getByRole("listitem")).toHaveCount(1)
  await expect(list.getByText("stuck.example.com")).toBeVisible()

  expect(deleted).toEqual(["1", "2", "3"])
  // A removal does not fetch the list again: for an administrator that is a
  // handshake with every other endpoint.
  expect(lists.length).toBe(loaded)
  expect(errors).toEqual([])
})

test("on a phone every watched row keeps how old its check is in full", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockShowcase(page)
  await page.route("**/api/v1/auth/session", (route) => json(route, readOnly))
  await watchList(page, [
    {
      ...mailRow,
      certificate: { ...certs[0], issuer: "A Rather Long Issuing Authority Name R11" },
    },
  ])
  await page.goto("/proxy/certificates")
  const age = page
    .getByRole("list", { name: "Watched domains" })
    .getByText(/^checked 3m( \d+s)? ago$/)
  await expect(age).toBeVisible()
  expect(await age.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
  expect(await age.evaluate((el) => getComputedStyle(el).textOverflow)).not.toBe("ellipsis")
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
})
