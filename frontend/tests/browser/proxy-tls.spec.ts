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
  // The port is in the address, and the port field shows it without holding it.
  await expect(page.getByLabel("port", { exact: true })).toHaveValue("")
  await expect(page.getByLabel("port", { exact: true })).toHaveAttribute("placeholder", "993")
})

test("a link may give the port beside the name, and is rewritten the way links spell it", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const seen = await scans(page, (url) => ({
    ...scan,
    domain: url.searchParams.get("domain"),
    port: Number(url.searchParams.get("port")),
  }))
  await page.goto("/proxy/tls?domain=mail.example.com&port=993")
  await expect(page).toHaveURL(/\/proxy\/tls\?domain=mail\.example\.com%3A993$/)
  await expect(page.getByRole("heading", { name: "mail.example.com:993" })).toBeVisible()
  await page.waitForLoadState("networkidle")
  // The same target in another spelling is not a second scan.
  expect(seen).toHaveLength(1)
  expect(seen[0].searchParams.get("domain")).toBe("mail.example.com")
  expect(seen[0].searchParams.get("port")).toBe("993")

  await page.goto(`/proxy/tls?domain=${encodeURIComponent("https://App.Example.com/login")}`)
  await expect(page).toHaveURL(/\/proxy\/tls\?domain=app\.example\.com$/)
  await page.waitForLoadState("networkidle")
  expect(seen).toHaveLength(2)
  expect(seen[1].searchParams.get("domain")).toBe("app.example.com")
})

test("the address follows each scan, so Back and reload ask again", async ({ page }) => {
  await mockProxy(page, { included: true })
  const seen = await scans(page, (url) => ({
    ...scan,
    domain: url.searchParams.get("domain"),
    port: Number(url.searchParams.get("port")),
  }))
  await page.goto("/proxy/tls")
  const field = page.getByLabel("Domain to scan")
  const port = page.getByLabel("port", { exact: true })
  const button = page.getByRole("button", { name: "Scan", exact: true })

  await field.fill("app.example.com")
  await button.click()
  await expect(page).toHaveURL(/\/proxy\/tls\?domain=app\.example\.com$/)
  await expect(page.getByRole("heading", { name: "app.example.com" })).toBeVisible()

  await field.fill("mail.example.com")
  await port.fill("993")
  await button.click()
  await expect(page).toHaveURL(/\/proxy\/tls\?domain=mail\.example\.com%3A993$/)
  await expect(page.getByRole("heading", { name: "mail.example.com:993" })).toBeVisible()
  await expect(field).toHaveValue("mail.example.com:993")
  await expect(port).toHaveValue("")
  expect(
    seen.map((url) => `${url.searchParams.get("domain")}:${url.searchParams.get("port")}`),
  ).toEqual(["app.example.com:443", "mail.example.com:993"])

  // Back is the last report, asked again, with its target in the fields.
  await page.goBack()
  await expect(page).toHaveURL(/\/proxy\/tls\?domain=app\.example\.com$/)
  await expect(page.getByRole("heading", { name: "app.example.com" })).toBeVisible()
  await expect(field).toHaveValue("app.example.com")
  await expect(port).toHaveValue("")
  await expect.poll(() => seen.length).toBe(3)
  expect(seen[2].searchParams.get("domain")).toBe("app.example.com")

  await page.goForward()
  await expect(field).toHaveValue("mail.example.com:993")
  await expect(port).toHaveValue("")
  await expect.poll(() => seen.length).toBe(4)

  await page.reload()
  await expect(page.getByRole("heading", { name: "mail.example.com:993" })).toBeVisible()
  await expect.poll(() => seen.length).toBe(5)
  expect(seen[4].searchParams.get("port")).toBe("993")

  // A bare visit asks nothing.
  await page.goto("/proxy/tls")
  await expect(page.getByText("Nothing scanned yet")).toBeVisible()
  await page.waitForLoadState("networkidle")
  expect(seen).toHaveLength(5)
})

test("the field says what it will scan, and a port that disagrees is refused before sending", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockProxy(page, { included: true })
  const seen = await scans(page)
  await page.goto("/proxy/tls")
  const field = page.getByLabel("Domain to scan")
  const port = page.getByLabel("port", { exact: true })
  const button = page.getByRole("button", { name: "Scan", exact: true })
  await expect(page.getByText("A name, host:port or URL")).toBeVisible()

  await field.fill("imaps://Mail.Example.com/")
  await expect(page.getByText("Scans mail.example.com, port 993")).toBeVisible()
  // An empty port field shows the port the name implies.
  await expect(port).toHaveAttribute("placeholder", "993")
  await port.fill("995")
  await expect(page.getByText("Scans mail.example.com, port 995")).toBeVisible()
  await field.fill("[2001:db8::1]")
  await expect(page.getByText("Scans 2001:db8::1, port 995")).toBeVisible()

  await field.fill("mail.example.com:993")
  await expect(page.getByText("A name, host:port or URL")).toBeVisible()
  await button.click()
  await expect(
    page
      .getByRole("alert")
      .filter({ hasText: "the address says port 993 and the port field says 995" }),
  ).toBeVisible()
  await expect(port).toHaveAttribute("aria-invalid", "true")
  await port.fill("0")
  await button.click()
  await expect(
    page.getByRole("alert").filter({ hasText: "port 0 is outside 1–65535" }),
  ).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  await page.waitForLoadState("networkidle")
  expect(seen).toHaveLength(0)

  // Mended, it scans what the hint said.
  await port.fill("")
  await button.click()
  await expect.poll(() => seen.length).toBe(1)
  expect(seen[0].searchParams.get("domain")).toBe("mail.example.com")
  expect(seen[0].searchParams.get("port")).toBe("993")
  await expect(field).toHaveValue("mail.example.com:993")
  await expect(port).toHaveValue("")

  // So the next address typed is not outvoted by the port just scanned.
  await field.fill("imaps://mail.example.com")
  await expect(page.getByText("Scans mail.example.com, port 993")).toBeVisible()
})

/** Scans that wait for the test to answer them; each request is kept with how it ended. */
async function heldScans(page: Page) {
  const held: {
    request: Request
    url: URL
    answer: (report: object) => Promise<void>
    aborted: boolean
  }[] = []
  page.on("requestfailed", (request) => {
    const entry = held.find((item) => item.request === request)
    if (entry && /abort/i.test(request.failure()?.errorText ?? "")) entry.aborted = true
  })
  await page.route("**/api/v1/certificates/scan**", (route) => {
    return new Promise<void>((resolve) => {
      held.push({
        request: route.request(),
        url: new URL(route.request().url()),
        aborted: false,
        answer: async (report) => {
          await json(route, report).catch(() => {})
          resolve()
        },
      })
    })
  })
  return held
}

test("Cancel stops a scan in flight, with a clock until then", async ({ page }) => {
  await mockProxy(page, { included: true })
  const held = await heldScans(page)
  await page.goto("/proxy/tls?domain=slow.example.com")
  const progress = page.getByText(/Handshaking, probing each TLS version/)
  await expect(progress).toBeVisible()
  await expect(page.getByRole("heading", { name: "slow.example.com" })).toBeVisible()
  // The clock moves.
  await expect(progress.locator(".numeric")).toHaveText(/^[1-9]\d*s$/, { timeout: 5000 })
  await page.getByRole("button", { name: "Cancel", exact: true }).click()
  await expect(progress).toHaveCount(0)
  await expect(
    page.getByText(/^The scan was cancelled after \d+s\. Scan to run it again\.$/),
  ).toBeVisible()
  await expect(page.getByText("Nothing scanned yet")).toHaveCount(0)
  await expect.poll(() => held[0]?.aborted).toBe(true)
  const button = page.getByRole("button", { name: "Scan", exact: true })
  await expect(button).toBeEnabled()

  // Scan asks again, and its answer is the report.
  await button.click()
  await expect(progress).toBeVisible()
  await expect.poll(() => held.length).toBe(2)
  await expect(page.getByText(/cancelled after/)).toHaveCount(0)
  await held[1].answer({ ...scan, domain: "slow.example.com", summary: "The answer after all" })
  await expect(page.getByText("The answer after all")).toBeVisible()
  await expect(progress).toHaveCount(0)
})

test("Cancelling a scan asked for again keeps the report it was asked over", async ({ page }) => {
  await mockProxy(page, { included: true })
  const held = await heldScans(page)
  await page.goto("/proxy/tls?domain=app.example.com")
  await expect.poll(() => held.length).toBe(1)
  await held[0].answer({ ...scan, summary: "The first answer" })
  await expect(page.getByText("The first answer")).toBeVisible()

  await page.getByRole("button", { name: "Scan", exact: true }).click()
  await expect.poll(() => held.length).toBe(2)
  await page.getByRole("button", { name: "Cancel", exact: true }).click()
  await expect(
    page.getByText(
      /^The new scan was cancelled after \d+s\. The report below is the one from before\.$/,
    ),
  ).toBeVisible()
  await expect(page.getByText("The first answer")).toBeVisible()
  await expect(page.getByText(/Handshaking, probing each TLS version/)).toHaveCount(0)
  await expect.poll(() => held[1].aborted).toBe(true)
  await expect(page.getByRole("button", { name: "Scan", exact: true })).toBeEnabled()
})

test("the field offers recent scans and the names this server knows", async ({ page }) => {
  await mockShowcase(page)
  await scans(page)
  const lists: URL[] = []
  page.on("request", (request) => {
    const url = new URL(request.url())
    if (/\/api\/v1\/(proxy\/vhosts|certificates\/|certificates\/watched)$/.test(url.pathname))
      lists.push(url)
  })
  await page.goto("/proxy/tls?domain=app.example.com")
  await expect(page.getByRole("heading", { name: "app.example.com" })).toBeVisible()
  await page.waitForLoadState("networkidle")
  // Nothing is fetched for the suggestions until the field is used.
  expect(lists).toHaveLength(0)

  const field = page.getByLabel("Domain to scan")
  await expect(field).toHaveAttribute("list", "tls-targets")
  await field.focus()
  const options = page.locator("#tls-targets option")
  await expect.poll(() => options.count()).toBeGreaterThan(3)
  const offered = await options.evaluateAll((items) =>
    items.map(
      (item) => `${(item as HTMLOptionElement).value} | ${(item as HTMLOptionElement).label}`,
    ),
  )
  expect(offered).toEqual([
    "app.example.com | Scanned recently",
    "legacy.example.com | Site legacy.example.com",
    "shop.example.com | Site just-dashboard-shop",
    "mail.example.com:993 | Watched",
    "old.example.com | Certificate",
  ])
  // The watch list is read as stored; offering a name sends no handshake.
  const watched = lists.find((url) => url.pathname.endsWith("/certificates/watched"))
  expect(watched?.searchParams.get("check")).toBe("false")
})

test("a read-only account is offered nothing and scans nothing", async ({ page }) => {
  await mockShowcase(page)
  await page.route("**/api/v1/auth/session", (route) => json(route, readOnly))
  const seen = await scans(page)
  await page.goto("/proxy/tls?domain=app.example.com")
  await expect(page.getByText("Scanning needs an administrator")).toBeVisible()
  const field = page.getByLabel("Domain to scan")
  await expect(field).not.toHaveAttribute("list", /.*/)
  await expect(page.locator("#tls-targets")).toHaveCount(0)
  await page.waitForLoadState("networkidle")
  expect(seen).toHaveLength(0)
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

test("a new watch shows its row being checked until its first check arrives", async ({ page }) => {
  await mockShowcase(page)
  const rows: object[] = []
  const posted: unknown[] = []
  let lists = 0
  let hold = false
  let release: () => void = () => {}
  await page.route("**/api/v1/certificates/watched", async (route) => {
    const request = route.request()
    if (request.method() === "POST") {
      posted.push(request.postDataJSON())
      const existing = rows.length > 0
      const row = { id: 7, ...request.postDataJSON(), createdAt: now }
      if (!existing) {
        rows.push({ ...row, checkedAt: new Date().toISOString(), certificate: certs[0] })
        hold = true
      }
      return route.fulfill({
        status: existing ? 200 : 201,
        contentType: "application/json",
        body: JSON.stringify(row),
      })
    }
    lists++
    if (hold) {
      hold = false
      await new Promise<void>((resolve) => (release = resolve))
    }
    return json(route, rows)
  })
  await page.goto("/proxy/certificates")
  await expect(page.getByText("Nothing watched yet.", { exact: false })).toBeVisible()
  const field = page.getByLabel("Domain to watch")
  const watch = page.getByRole("button", { name: "Watch", exact: true })
  const list = page.getByRole("list", { name: "Watched domains" })

  await field.fill("new.example.com")
  await watch.click()
  await expect(
    page.locator("[data-sonner-toast]").filter({ hasText: "Watching new.example.com" }),
  ).toBeVisible()
  // The row is there at once, and everything says the check is running.
  const row = list.getByRole("listitem").filter({ hasText: "new.example.com" })
  await expect(row).toContainText("checking…")
  await expect(page.getByText("1 watched, checked while")).toBeVisible()
  await expect(page.getByRole("button", { name: "Re-checking…" })).toHaveAttribute(
    "aria-busy",
    "true",
  )
  await expect(watch).toHaveAttribute("aria-busy", "true")
  await field.fill("other.example.com")
  await field.press("Enter")
  await expect.poll(() => lists).toBe(2)
  expect(posted).toHaveLength(1)

  release()
  await expect(row).toContainText("checked just now")
  await expect(list.getByRole("listitem")).toHaveCount(1)
  await expect(page.getByRole("button", { name: "Re-check now" })).toBeEnabled()
  await expect(watch).not.toHaveAttribute("aria-busy", "true")

  // Watching it again says so and does not check the whole list for nothing.
  await field.fill("new.example.com")
  await watch.click()
  await expect(
    page.locator("[data-sonner-toast]").filter({ hasText: "new.example.com is already watched" }),
  ).toBeVisible()
  await expect(field).toHaveValue("")
  expect(posted).toHaveLength(2)
  expect(lists).toBe(2)
})

test("a server that refused the handshake is not said to be silent", async ({ page }) => {
  await mockProxy(page, { included: true })
  const refused = {
    ...scan,
    reachable: false,
    grade: "F",
    summary: "The server on app.example.com:443 refused the handshake.",
    error: "remote error: tls: unrecognized name",
    certificate: undefined,
    protocols: [],
    chain: [],
    http: undefined,
    findings: [
      {
        id: "tls.refused",
        level: "critical",
        title: "The server refused the handshake",
        detail:
          "It answered unrecognized name, both to the handshake a current client makes and to one offering every version and cipher suite this check has.",
        advice:
          "Something on port 443 speaks TLS and will not finish a handshake for app.example.com.",
      },
    ],
  }
  const silent = {
    ...refused,
    summary: "Nothing answered a TLS handshake on app.example.com:443.",
    error: "dial tcp 203.0.113.4:443: connect: connection refused",
    findings: [
      {
        id: "tls.unreachable",
        level: "critical",
        title: "Nothing answered a TLS handshake",
        detail: "dial tcp 203.0.113.4:443: connect: connection refused",
        advice: "Check the domain resolves to this server and that the proxy is listening on 443.",
      },
    ],
  }
  let answer: object = refused
  await scans(page, () => answer)
  await page.goto("/proxy/tls?domain=app.example.com")
  await expect(page.getByText("The server refused the handshake")).toBeVisible()
  await expect(page.getByText("It answered unrecognized name", { exact: false })).toBeVisible()
  await expect(page.getByText("will not finish a handshake for app.example.com")).toBeVisible()
  await expect(page.getByText(/Nothing answered/)).toHaveCount(0)
  await expect(page.getByText(/resolves to this server/)).toHaveCount(0)

  answer = silent
  await page.getByRole("button", { name: "Scan", exact: true }).click()
  await expect(page.getByText("Nothing answered a TLS handshake", { exact: true })).toBeVisible()
  await expect(page.getByText(/resolves to this server/)).toBeVisible()
  await expect(page.getByText("The server refused the handshake")).toHaveCount(0)
})

test("a server that takes only an older offer is reported with what it took", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockProxy(page, { included: true })
  await scans(page, () => ({
    ...scan,
    grade: "F",
    summary: "The handshake a current client makes is refused; only an older offer is taken.",
    negotiated: "TLS 1.2",
    cipherSuite: "TLS_RSA_WITH_AES_128_GCM_SHA256",
    legacyOnly: true,
    findings: [
      {
        id: "tls.legacy-only",
        level: "critical",
        title: "The handshake a current client makes is refused",
        detail:
          "The server refused the versions and cipher suites a current client offers, and took TLS 1.2 with TLS_RSA_WITH_AES_128_GCM_SHA256 when older ones were offered as well.",
      },
    ],
  }))
  await page.goto("/proxy/tls?domain=app.example.com")
  await expect(
    page.getByText(
      "The handshake a current client makes is refused; only an older offer is taken.",
    ),
  ).toBeVisible()
  await expect(
    page.getByText("The handshake a current client makes is refused", { exact: true }),
  ).toBeVisible()
  await expect(page.getByText("TLS_RSA_WITH_AES_128_GCM_SHA256").first()).toBeVisible()
  await expect(page.getByText(/Nothing answered/)).toHaveCount(0)
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
})

test("a full-length serial wraps inside its panel", async ({ page }) => {
  // Let's Encrypt serials are 18 bytes and the RFC allows 20: one unbroken
  // token of up to 59 characters.
  const serial = Array.from({ length: 20 }, (_, i) => (0x60 + i).toString(16).toUpperCase()).join(
    ":",
  )
  await mockProxy(page, { included: true })
  await scans(page, () => ({ ...scan, serial }))
  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: 844 })
    await page.goto("/proxy/tls?domain=app.example.com")
    const value = page.locator('dt:has-text("Serial") + dd')
    await expect(value).toHaveText(serial)
    expect(await value.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
      width,
    )
  }
})
