import { expect, test, type Page, type Request } from "@playwright/test"
import { deepScan } from "./fixtures/proxy/tls-monitor"
import { json, mockProxy, scan, user } from "./proxy-fixtures"

/**
 * The TLS report's deep scan: asked for rather than run on every visit, run
 * after the quick report is on screen, its suites filtered by version and
 * rating, its connection features read as their answers, and its findings
 * not repeating the quick report's.
 */

type Held = {
  request: Request
  url: URL
  answer: (body: object) => Promise<void>
  aborted: boolean
}

/**
 * Answers the quick scan with `quick` and the deep scan with `deep`, keeping
 * each request. The quick scan answers for the target it was asked about, at
 * the moment it was asked. A deep answer of "hold" waits for the test to give
 * it.
 */
async function reports(
  page: Page,
  {
    quick = (url) => ({
      ...scan,
      domain: url.searchParams.get("domain"),
      port: Number(url.searchParams.get("port")),
      checkedAt: new Date().toISOString(),
    }),
    deep = () => deepScan,
  }: { quick?: (url: URL) => object; deep?: () => object | "hold" } = {},
) {
  const seen = { quick: [] as URL[], deep: [] as URL[], held: [] as Held[] }
  page.on("requestfailed", (request) => {
    const entry = seen.held.find((item) => item.request === request)
    if (entry && /abort/i.test(request.failure()?.errorText ?? "")) entry.aborted = true
  })
  await page.route(
    (url) => url.pathname === "/api/v1/certificates/scan",
    (route) => {
      const url = new URL(route.request().url())
      seen.quick.push(url)
      return json(route, quick(url))
    },
  )
  await page.route(
    (url) => url.pathname === "/api/v1/certificates/scan/deep",
    (route) => {
      const url = new URL(route.request().url())
      seen.deep.push(url)
      const body = deep()
      if (body !== "hold") return json(route, body)
      return new Promise<void>((resolve) => {
        seen.held.push({
          request: route.request(),
          url,
          aborted: false,
          answer: async (answer) => {
            await json(route, answer).catch(() => {})
            resolve()
          },
        })
      })
    },
  )
  return seen
}

test("the deep scan is offered, not run, and once asked for it is in the address", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const seen = await reports(page)
  await page.goto("/proxy/tls?domain=app.example.com")
  const ask = page.getByRole("button", { name: "Deep scan", exact: true })
  await expect(ask).toBeVisible()
  await page.waitForLoadState("networkidle")
  expect(seen.quick).toHaveLength(1)
  expect(seen.deep).toHaveLength(0)

  await ask.click()
  await expect(page).toHaveURL(/\/proxy\/tls\?domain=app\.example\.com&deep=1$/)
  await expect(page.getByRole("heading", { name: "Deep scan" })).toBeVisible()
  await expect(page.getByText("41 connections to")).toBeVisible()
  await expect(page.getByText("203.0.113.10:443")).toBeVisible()
  // Asking for it is not a second quick scan.
  expect(seen.quick).toHaveLength(1)
  expect(seen.deep).toHaveLength(1)
  expect(seen.deep[0].searchParams.get("domain")).toBe("app.example.com")
  expect(seen.deep[0].searchParams.get("port")).toBe("443")

  // A reload asks for both again: the address is the question.
  await page.reload()
  await expect(page.getByText("41 connections to")).toBeVisible()
  expect(seen.deep).toHaveLength(2)
})

test("a scan of another name keeps the deep scan asked for", async ({ page }) => {
  await mockProxy(page, { included: true })
  const seen = await reports(page)
  await page.goto("/proxy/tls?domain=app.example.com&deep=1")
  await expect(page.getByText("41 connections to")).toBeVisible()
  await page.getByLabel("Domain to scan").fill("mail.example.com:8443")
  await page.getByRole("button", { name: "Scan", exact: true }).click()
  await expect(page).toHaveURL(/domain=mail\.example\.com%3A8443&deep=1$/)
  await expect.poll(() => seen.deep.length).toBe(2)
  expect(seen.deep[1].searchParams.get("domain")).toBe("mail.example.com")
  expect(seen.deep[1].searchParams.get("port")).toBe("8443")
})

test("the suites are listed by version and filtered by version and rating", async ({ page }) => {
  await mockProxy(page, { included: true })
  await reports(page)
  await page.goto("/proxy/tls?domain=app.example.com&deep=1")
  const versions = page.getByRole("group", { name: "Version" })
  const ratings = page.getByRole("group", { name: "Rating" })
  const tls13 = page.getByRole("list", { name: "TLS 1.3 suites" })
  const tls12 = page.getByRole("list", { name: "TLS 1.2 suites" })

  await expect(tls13.getByRole("listitem")).toHaveCount(3)
  await expect(tls12.getByRole("listitem")).toHaveCount(4)
  // The server's order, as it chose them.
  await expect(tls12.getByRole("listitem").first()).toContainText("ECDHE-ECDSA-AES128-GCM-SHA256")
  await expect(page.getByRole("region", { name: "TLS 1.2" })).toContainText(
    "4 suites · server's order",
  )
  // Refused versions are part of the whole picture.
  await expect(page.getByRole("region", { name: "SSL 3.0" })).toContainText("refused")
  await expect(tls12.getByRole("listitem").filter({ hasText: "AES128-SHA" }).last()).toContainText(
    "no forward secrecy, CBC mode",
  )

  // A chip counts what it would show, and one that would show nothing is not drawn.
  await expect(versions.getByRole("button", { name: /^All versions/ })).toHaveText("All versions 7")
  await expect(versions.getByRole("button", { name: /^TLS 1\.1/ })).toHaveCount(0)
  await expect(ratings.getByRole("button", { name: /^Insecure/ })).toHaveCount(0)
  await expect(ratings.getByRole("button", { name: /^Weak/ })).toHaveText("Weak 2")

  await versions.getByRole("button", { name: /^TLS 1\.2/ }).click()
  await expect(versions.getByRole("button", { name: /^TLS 1\.2/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await expect(tls13).toHaveCount(0)
  await expect(page.getByRole("region", { name: "SSL 3.0" })).toHaveCount(0)
  await expect(tls12.getByRole("listitem")).toHaveCount(4)

  await ratings.getByRole("button", { name: /^Weak/ }).click()
  await expect(tls12.getByRole("listitem")).toHaveCount(2)
  await expect(tls12).toContainText("ECDHE-ECDSA-AES128-SHA")
  await expect(tls12).not.toContainText("GCM")

  // The filters are kept for the tab.
  await page.reload()
  await expect(tls12.getByRole("listitem")).toHaveCount(2)
  await expect(tls13).toHaveCount(0)

  // Every version, weak only: TLS 1.3 has none, so it is not drawn.
  await versions.getByRole("button", { name: /^All versions/ }).click()
  await expect(tls12.getByRole("listitem")).toHaveCount(2)
  await expect(tls13).toHaveCount(0)
  await ratings.getByRole("button", { name: "Any rating" }).click()
  await expect(tls13.getByRole("listitem")).toHaveCount(3)
})

test("findings the quick report lists are not repeated, and the HTTP/2 one opens its site", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await reports(page, {
    quick: () => ({
      ...scan,
      findings: [
        ...scan.findings,
        {
          id: "tls.cipher.weak",
          level: "notice",
          title: "Weak suites, as the quick report says",
          detail: "",
        },
      ],
    }),
  })
  await page.route("**/api/v1/proxy/sites/app.example.com", (route) =>
    json(route, { spec: { name: "app.example.com" }, managed: true, content: "", warnings: [] }),
  )
  await page.goto("/proxy/tls?domain=app.example.com&deep=1")
  await expect(page.getByText("HTTP/2 is on in the site form and not offered")).toBeVisible()
  await expect(page.getByText("Weak suites, as the quick report says")).toBeVisible()
  await expect(page.getByText("2 weak cipher suites are accepted")).toHaveCount(0)
  await expect(page.getByText("No post-quantum key exchange")).toBeVisible()

  await page.getByText("HTTP/2 is on in the site form and not offered").click()
  await page.getByRole("button", { name: "Open app.example.com" }).click()
  await expect(page).toHaveURL(/\/proxy\/sites\?site=app\.example\.com$/)
})

test("each connection feature reads as its answer", async ({ page }) => {
  await mockProxy(page, { included: true })
  await reports(page)
  await page.goto("/proxy/tls?domain=app.example.com&deep=1")
  const exchange = page.getByRole("list", { name: "Key exchange" })
  const connection = page.getByRole("list", { name: "Connection" })

  await expect(exchange.getByRole("listitem").first()).toContainText("A browser gets")
  await expect(exchange.getByRole("listitem").first()).toContainText("X25519")
  await expect(exchange.getByRole("listitem").first()).toContainText("Not post-quantum")
  const hybrid = exchange
    .getByRole("listitem")
    .filter({ has: page.getByText("X25519MLKEM768", { exact: true }) })
  await expect(hybrid).toContainText("post-quantum")
  await expect(hybrid).toContainText("refused")
  await expect(exchange.getByRole("listitem")).toHaveCount(12)

  const row = (title: string) =>
    connection.getByRole("listitem").filter({ has: page.getByText(title, { exact: true }) })
  await expect(row("HTTP/2")).toContainText("http/1.1")
  await expect(row("HTTP/2")).toContainText("app.example.com's site form has HTTP/2 on.")
  await expect(row("HTTP/3")).toContainText("no QUIC answer")
  await expect(row("HTTP/3")).toContainText("Alt-Svc offers h3 on UDP 443.")
  await expect(row("Resumption, TLS 1.3")).toContainText("resumed")
  await expect(row("Resumption, TLS 1.2")).toContainText("no ticket")
  await expect(row("No name")).toContainText("gets a certificate")
  await expect(row("Unknown name")).toContainText("The scanned name's own certificate")
})

test("Cancel stops a deep scan with a clock until then, and Scan again runs it", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const seen = await reports(page, { deep: () => "hold" })
  await page.goto("/proxy/tls?domain=app.example.com&deep=1")
  // The quick report is on screen while the deep scan works.
  await expect(page.getByText(scan.summary)).toBeVisible()
  const progress = page.getByText(/Asking for every cipher suite/)
  await expect(progress).toBeVisible()
  await expect(progress.locator(".numeric")).toHaveText(/^[1-9]\d*s$/, { timeout: 5000 })
  await page.getByRole("button", { name: "Cancel", exact: true }).click()
  await expect(page.getByText(/^The deep scan was cancelled after \d+s\.$/)).toBeVisible()
  await expect(progress).toHaveCount(0)
  await expect.poll(() => seen.held[0]?.aborted).toBe(true)

  await page.getByRole("button", { name: "Scan again" }).click()
  await expect(progress).toBeVisible()
  await expect.poll(() => seen.held.length).toBe(2)
  await seen.held[1].answer(deepScan)
  await expect(page.getByText("41 connections to")).toBeVisible()
  await expect(progress).toHaveCount(0)
})

test("a failed deep scan shows its error, and an unreachable one says so", async ({ page }) => {
  await mockProxy(page, { included: true })
  let answers = 0
  await reports(page)
  await page.route(
    (url) => url.pathname === "/api/v1/certificates/scan/deep",
    (route) => {
      answers++
      if (answers === 1)
        return route.fulfill({
          status: 500,
          contentType: "application/json",
          body: JSON.stringify({ error: { code: "internal", message: "the deep scan broke" } }),
        })
      return json(route, {
        ...deepScan,
        reachable: false,
        error: "dial tcp 203.0.113.10:443: connect: connection refused",
        versions: [],
        groups: [],
        resumption: [],
        sni: [],
        findings: [],
      })
    },
  )
  await page.goto("/proxy/tls?domain=app.example.com&deep=1")
  await expect(page.getByRole("alert").filter({ hasText: "the deep scan broke" })).toBeVisible()
  // The quick report stands.
  await expect(page.getByText(scan.summary)).toBeVisible()
  await page.getByRole("button", { name: "Scan again" }).click()
  await expect(page.getByText("The deep scan could not connect")).toBeVisible()
  await expect(page.getByText("connect: connection refused")).toBeVisible()
  await expect(page.getByRole("alert").filter({ hasText: "the deep scan broke" })).toHaveCount(0)
  expect(answers).toBe(2)
})

test("turning the deep scan off takes it out of the address and asks nothing more", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const seen = await reports(page)
  await page.goto("/proxy/tls?domain=app.example.com&deep=1")
  await expect(page.getByText("41 connections to")).toBeVisible()
  await page.getByRole("button", { name: "Turn off deep scan" }).click()
  await expect(page).toHaveURL(/\/proxy\/tls\?domain=app\.example\.com$/)
  await expect(page.getByRole("button", { name: "Deep scan", exact: true })).toBeVisible()
  await expect(page.getByText("41 connections to")).toHaveCount(0)
  // The quick report was not asked again either.
  await page.waitForLoadState("networkidle")
  expect(seen.quick).toHaveLength(1)
  expect(seen.deep).toHaveLength(1)
})

test("a new quick scan of the same target runs the deep scan again", async ({ page }) => {
  await mockProxy(page, { included: true })
  const seen = await reports(page)
  await page.goto("/proxy/tls?domain=app.example.com&deep=1")
  await expect(page.getByText("41 connections to")).toBeVisible()
  expect(seen.deep).toHaveLength(1)
  await page.getByRole("button", { name: "Scan", exact: true }).click()
  await expect.poll(() => seen.quick.length).toBe(2)
  await expect.poll(() => seen.deep.length).toBe(2)
  await expect(page.getByText("41 connections to")).toBeVisible()
})

test("a read-only account is offered no deep scan and sends none", async ({ page }) => {
  await mockProxy(page, { included: true })
  const seen = await reports(page)
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await page.goto("/proxy/tls?domain=app.example.com&deep=1")
  await expect(page.getByText("Scanning needs an administrator")).toBeVisible()
  await page.waitForLoadState("networkidle")
  expect(seen.deep).toHaveLength(0)
  await expect(page.getByRole("button", { name: "Deep scan", exact: true })).toHaveCount(0)
})

test("on a phone the deep scan fits the screen and every button is named", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockProxy(page, { included: true })
  await reports(page)
  await page.goto("/proxy/tls?domain=app.example.com&deep=1")
  await expect(page.getByRole("list", { name: "TLS 1.2 suites" })).toBeVisible()
  await expect(page.getByRole("list", { name: "Connection" })).toBeVisible()
  const overflow = await page
    .locator("[data-slot='page']")
    .evaluate((el) => el.scrollWidth - el.clientWidth)
  expect(overflow).toBeLessThanOrEqual(1)
  const unnamed = await page
    .locator("button:visible")
    .evaluateAll(
      (buttons) =>
        buttons.filter(
          (b) =>
            !b.textContent?.trim() &&
            !b.getAttribute("aria-label") &&
            !b.getAttribute("aria-labelledby"),
        ).length,
    )
  expect(unnamed).toBe(0)
  // The version chips scroll sideways inside their strip rather than widening the page.
  await page
    .getByRole("group", { name: "Version" })
    .getByRole("button", { name: /^TLS 1\.2/ })
    .click()
  await expect(page.getByRole("list", { name: "TLS 1.3 suites" })).toHaveCount(0)
})
