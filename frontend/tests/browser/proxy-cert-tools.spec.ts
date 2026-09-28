import { expect, test, type Page, type Route } from "@playwright/test"
import { availability, certs, json, mockProxy, mockShowcase, user } from "./proxy-fixtures"
import {
  existingLocalCA,
  issued,
  localCA,
  localLeaf,
  signingRequest,
} from "./fixtures/proxy/cert-tools"

/**
 * The certificates this server makes itself — a signing request for an
 * authority, a self-signed pair, the local CA and what it issues — checked
 * against what the page sends and what it says: where each is trusted, that a
 * key never leaves the server, and that nothing in use is replaced without a
 * second press.
 */

type Sent = { method: string; path: string; body: unknown; confirm?: string }

/** Answers every request under a path prefix with `answer`, recording what was sent. */
async function serve(
  page: Page,
  pattern: string,
  answer: (sent: Sent, route: Route) => Promise<void> | void,
) {
  const sent: Sent[] = []
  await page.route(pattern, async (route) => {
    const request = route.request()
    const entry = {
      method: request.method(),
      path: new URL(request.url()).pathname.replace(/^\/api\/v1/, ""),
      body: request.postData() ? request.postDataJSON() : undefined,
      confirm: request.headers()["x-confirm"],
    }
    sent.push(entry)
    await answer(entry, route)
  })
  return sent
}

function refuse(route: Route, status: number, code: string, message: string) {
  return route.fulfill({
    status,
    contentType: "application/json",
    body: JSON.stringify({ error: { code, message } }),
  })
}

function localCAPanel(page: Page) {
  return page
    .locator("[data-slot='panel']")
    .filter({ has: page.getByRole("heading", { name: "Local CA", exact: true }) })
}

async function openMenu(page: Page) {
  await page.getByRole("button", { name: "Other ways to get a certificate" }).click()
}

test("the Issue command keeps Let's Encrypt on its face and every other way behind it", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy/certificates")
  await page.getByRole("button", { name: "Issue certificate", exact: true }).click()
  await expect(page.getByRole("dialog", { name: "Issue a certificate" })).toBeVisible()
  await page.keyboard.press("Escape")

  await openMenu(page)
  const items = page.getByRole("menuitem")
  await expect(items).toHaveText([
    "Let's Encrypt",
    "From the local CA",
    "Self-signed",
    "Signing request for a CA",
  ])
  await page.getByRole("menuitem", { name: "Let's Encrypt" }).click()
  await expect(page.getByRole("dialog", { name: "Issue a certificate" })).toBeVisible()
})

test("without certbot the private ways remain, under their own command", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/proxy/status", (route) =>
    json(route, { ...availability, certbot: false }),
  )
  await page.route("**/api/v1/certificates/certbot", (route) =>
    refuse(route, 503, "certbot_unavailable", "certbot is not installed on this host"),
  )
  await page.goto("/proxy/certificates")
  await expect(page.getByRole("button", { name: "Issue certificate", exact: true })).toHaveCount(0)
  await page.getByRole("button", { name: "New certificate" }).click()
  await expect(page.getByRole("menuitem")).toHaveText([
    "From the local CA",
    "Self-signed",
    "Signing request for a CA",
  ])
  await page.getByRole("menuitem", { name: "Self-signed" }).click()
  await expect(page.getByRole("dialog", { name: "Make a self-signed certificate" })).toBeVisible()
})

test("issuing from the local CA makes the CA first when there is none, then signs names and addresses", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  let exists = false
  const ca = await serve(page, "**/api/v1/certificates/local-ca", (sent, route) => {
    if (sent.method === "POST") {
      exists = true
      return json(route, existingLocalCA({ leaves: [] }))
    }
    return json(route, exists ? existingLocalCA() : localCA())
  })
  const issues = await serve(page, "**/api/v1/certificates/local-ca/issue", (_, route) =>
    json(route, issued("nas.lan", ["nas.lan", "192.168.1.10"])),
  )
  await page.goto("/proxy/certificates")
  await openMenu(page)
  await page.getByRole("menuitem", { name: "From the local CA" }).click()
  const dialog = page.getByRole("dialog", { name: "Issue from the local CA" })
  await expect(dialog.getByText("This creates the local CA first")).toBeVisible()
  const submit = dialog.getByRole("button", { name: "Create the CA and issue" })
  await expect(submit).toBeDisabled()
  await dialog.getByLabel("Names").fill("nas.lan 192.168.1.10")
  // The name is suggested from the first one, and kept unless changed.
  await expect(dialog.getByLabel("Kept as")).toHaveAttribute("placeholder", "nas.lan")
  await dialog.getByRole("button", { name: /^Key/ }).click()
  await dialog.getByRole("radio", { name: "RSA 2048" }).click()
  await submit.click()

  await expect(dialog.getByText("nas.lan is on disk")).toBeVisible()
  expect(ca.filter((s) => s.method === "POST")).toHaveLength(1)
  expect(issues.map((s) => s.body)).toEqual([
    { name: "nas.lan", names: ["nas.lan", "192.168.1.10"], keyType: "rsa-2048", replace: false },
  ])
  await expect(dialog.getByText("/etc/ssl/just-dashboard/nas.lan/privkey.pem")).toBeVisible()
  await expect(
    dialog.getByText("Trusted only on devices where the local CA's root is installed."),
  ).toBeVisible()
  await expect(dialog.getByRole("link", { name: "Download root" })).toHaveAttribute(
    "href",
    "/api/v1/certificates/local-ca/root.pem",
  )
  await dialog.getByRole("button", { name: "Done" }).click()

  // The panel reads the CA the dialog made, and what it issued.
  const panel = localCAPanel(page)
  await expect(panel.getByText("Just Dashboard local CA (edge-1)")).toBeVisible()
  await expect(panel.getByRole("list", { name: "Certificates the local CA issued" })).toContainText(
    "nas.lan",
  )
})

test("a name in use is replaced only on a second, deliberate press, with the reload offered", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const made = await serve(page, "**/api/v1/certificates/self-signed", (sent, route) => {
    if (!(sent.body as { replace: boolean }).replace) {
      return refuse(
        route,
        409,
        "certificate_exists",
        "printer is already imported: it covers 10.0.0.7 and expires 1 Jan 2027. Replace it to overwrite that pair.",
      )
    }
    return json(
      route,
      issued("printer", ["10.0.0.7"], {
        replaced: true,
        warnings: [
          "It is self-signed: every browser warns before the site until the certificate itself is trusted on that device, and it is not renewed here.",
        ],
      }),
    )
  })
  const reloads = await serve(page, "**/api/v1/proxy/reload", (_, route) =>
    json(route, { reloaded: true }),
  )
  await page.goto("/proxy/certificates")
  await openMenu(page)
  await page.getByRole("menuitem", { name: "Self-signed" }).click()
  const dialog = page.getByRole("dialog", { name: "Make a self-signed certificate" })
  await dialog.getByLabel("Names").fill("10.0.0.7")
  await dialog.getByLabel("Kept as").fill("printer")
  await dialog.getByRole("button", { name: "Make the certificate" }).click()
  await expect(dialog.getByText("That name is taken")).toBeVisible()
  await expect(dialog.getByText(/expires 1 Jan 2027/)).toBeVisible()
  await dialog.getByRole("button", { name: "Replace it" }).click()
  await expect(dialog.getByText("printer was replaced")).toBeVisible()
  expect(made.map((s) => (s.body as { replace: boolean }).replace)).toEqual([false, true])
  expect(made[1].body).toMatchObject({
    name: "printer",
    names: ["10.0.0.7"],
    keyType: "ecdsa-p256",
  })
  await expect(dialog.getByText(/every browser warns before the site until/)).toBeVisible()
  await dialog.getByRole("button", { name: "Reload nginx" }).click()
  await expect(
    dialog.getByText("Sites using this certificate serve the new one now."),
  ).toBeVisible()
  expect(reloads.map((s) => s.body)).toEqual([{ kind: "nginx" }])
})

test("a name the server would refuse is caught while typing", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy/certificates")
  await openMenu(page)
  await page.getByRole("menuitem", { name: "Self-signed" }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByLabel("Names").fill("*.lan.example")
  await expect(dialog.getByLabel("Kept as")).toHaveAttribute("placeholder", "wildcard.lan.example")
  await dialog.getByLabel("Kept as").fill("My Cert")
  await expect(dialog.getByText(/Lowercase letters, digits, dots/)).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Make the certificate" })).toBeDisabled()
})

test("a signing request keeps its key on the server and hands over only the request", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const made = await serve(page, "**/api/v1/certificates/csr", (sent, route) =>
    sent.method === "POST"
      ? json(
          route,
          signingRequest({ keyType: "rsa-3072", subject: { organization: "Example Ltd" } }),
        )
      : json(route, []),
  )
  await page.goto("/proxy/certificates")
  await openMenu(page)
  await page.getByRole("menuitem", { name: "Signing request for a CA" }).click()
  const dialog = page.getByRole("dialog", { name: "Signing request for a certificate authority" })
  await dialog.getByLabel("Names").fill("shop.example.com www.shop.example.com")
  await dialog.getByRole("radio", { name: "RSA 3072" }).click()
  await dialog.getByRole("button", { name: /^Organisation details/ }).click()
  await dialog.getByLabel("Organisation").fill("Example Ltd")
  await dialog.getByLabel("Country").fill("M1")
  await expect(dialog.getByText("Its two-letter code, such as DE or US.")).toBeVisible()
  const submit = dialog.getByRole("button", { name: "Make the key and request" })
  await expect(submit).toBeDisabled()
  await dialog.getByLabel("Country").fill("md")
  await submit.click()

  await expect(dialog.getByText("The key is on this server")).toBeVisible()
  expect(made.filter((s) => s.method === "POST").map((s) => s.body)).toEqual([
    {
      name: "shop.example.com",
      names: ["shop.example.com", "www.shop.example.com"],
      keyType: "rsa-3072",
      subject: { organization: "Example Ltd", locality: "", province: "", country: "md" },
    },
  ])
  await expect(
    dialog.getByText("/etc/ssl/just-dashboard-private/requests/shop.example.com/privkey.pem"),
  ).toBeVisible()
  await expect(dialog.getByLabel("Signing request for shop.example.com")).toContainText(
    "BEGIN CERTIFICATE REQUEST",
  )
  await expect(dialog.getByText(/BEGIN [A-Z ]*PRIVATE KEY/)).toHaveCount(0)
  const download = page.waitForEvent("download")
  await dialog.getByRole("button", { name: "Download shop.example.com.csr" }).click()
  expect((await download).suggestedFilename()).toBe("shop.example.com.csr")
})

test("a waiting request is completed with the certificate alone, and says first when it replaces one", async ({
  page,
}) => {
  await mockShowcase(page)
  let waiting = true
  await page.route("**/api/v1/certificates/csr", (route) =>
    json(
      route,
      waiting
        ? [
            signingRequest({
              name: "portal",
              domains: ["portal.example.com"],
              replaces: { ...certs[1], name: "portal", domains: ["portal.example.com"] },
            }),
          ]
        : [],
    ),
  )
  const completions = await serve(page, "**/api/v1/certificates/csr/*/complete", (sent, route) => {
    waiting = false
    return json(route, issued("portal", ["portal.example.com"], { replaced: true }))
  })
  await page.goto("/proxy/certificates")
  const list = page.getByRole("list", { name: "Signing requests" })
  await expect(list).toContainText("portal.example.com")
  await expect(list).toContainText("replaces the certificate kept now")
  await list.getByRole("button", { name: "Add certificate" }).click()
  const dialog = page.getByRole("dialog", { name: "Add the certificate for portal" })
  await expect(dialog.getByText("This replaces the certificate kept now")).toBeVisible()
  const replace = dialog.getByRole("button", { name: "Replace it" })
  await expect(replace).toBeDisabled()
  await dialog
    .getByLabel("Certificate")
    .fill("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----")
  await replace.click()

  // The list empties behind the dialog, and the outcome stays in front.
  await expect(dialog.getByText("portal was replaced")).toBeVisible()
  await expect(dialog.getByText("Sites pick it up on a reload")).toBeVisible()
  await expect(page.getByRole("list", { name: "Signing requests" })).toHaveCount(0)
  expect(completions).toEqual([
    {
      method: "POST",
      path: "/certificates/csr/portal/complete",
      body: {
        certificate: "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----",
        replace: true,
      },
      confirm: undefined,
    },
  ])
})

test("a certificate that is not the request's is refused with the server's reason", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/certificates/csr", (route) => json(route, [signingRequest()]))
  await page.route("**/api/v1/certificates/csr/*/complete", (route) =>
    refuse(
      route,
      400,
      "bad_request",
      "this certificate was not signed for the request shop.example.com: its public key is not the one made for it.",
    ),
  )
  await page.goto("/proxy/certificates")
  await page.getByRole("button", { name: "Add certificate" }).click()
  const dialog = page.getByRole("dialog", { name: "Add the certificate for shop.example.com" })
  await dialog
    .getByLabel("Certificate")
    .fill("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----")
  await dialog.getByRole("button", { name: "Add certificate" }).click()
  await expect(page.getByText("Not added")).toBeVisible()
  await expect(page.getByText(/its public key is not the one made for it/)).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Add certificate" })).toBeEnabled()
})

test("discarding a request asks once, with no phrase, and deletes it", async ({ page }) => {
  await mockProxy(page, { included: true })
  let waiting = true
  await page.route("**/api/v1/certificates/csr", (route) =>
    json(route, waiting ? [signingRequest()] : []),
  )
  const deletes = await serve(page, "**/api/v1/certificates/csr/shop.example.com", (_, route) => {
    waiting = false
    return route.fulfill({ status: 204 })
  })
  await page.goto("/proxy/certificates")
  await page.getByRole("button", { name: "More actions for the request shop.example.com" }).click()
  await page.getByRole("menuitem", { name: "Discard" }).click()
  const confirm = page.getByRole("alertdialog").or(page.getByRole("dialog"))
  await expect(confirm.getByText("Its key is deleted from this server.")).toBeVisible()
  await confirm.getByRole("button", { name: "Discard", exact: true }).click()
  await expect(page.getByRole("list", { name: "Signing requests" })).toHaveCount(0)
  expect(deletes.map((s) => [s.method, s.confirm])).toEqual([["DELETE", undefined]])
})

test("the local CA panel reads its root, what it issued and its renewal", async ({ page }) => {
  await mockShowcase(page)
  await page.goto("/proxy/certificates")
  const panel = localCAPanel(page)
  await expect(panel.getByText("Just Dashboard local CA (edge-1)")).toBeVisible()
  await expect(panel.getByText(/^root valid until/)).toBeVisible()
  // Wrapped only between bytes.
  await expect(panel.getByText(/3A:\u200b9F:\u200b12:\u200bC4/)).toBeVisible()
  await expect(panel.getByText(/Trusted only on devices where the local CA's root/)).toBeVisible()
  const leaves = panel.getByRole("list", { name: "Certificates the local CA issued" })
  await expect(leaves.getByText(/^renews /)).toBeVisible()
  await expect(leaves).toContainText("nas.lan, 192.168.1.10")
  await expect(leaves).toContainText("Used by nas.lan")
  await expect(panel.getByText(/Checked just now: nothing was due\. Next check/)).toBeVisible()
  await expect(panel.getByRole("link", { name: "Download root" })).toHaveAttribute(
    "href",
    "/api/v1/certificates/local-ca/root.pem",
  )
  await panel.getByRole("button", { name: "Issue", exact: true }).click()
  await expect(page.getByRole("dialog", { name: "Issue from the local CA" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Create the CA and issue" })).toHaveCount(0)
})

test("a renewal check that failed says why, and a leaf that cannot renew says so", async ({
  page,
}) => {
  await mockShowcase(page)
  await page.route("**/api/v1/certificates/local-ca", (route) =>
    json(
      route,
      existingLocalCA({
        leaves: [
          localLeaf({ error: "its key could not be read, so it cannot be renewed" }),
          localLeaf({ name: "grafana.lan", path: "/x/grafana.lan", daysLeft: 20 }),
        ],
        lastCheck: {
          at: new Date().toISOString(),
          renewed: ["wiki.lan"],
          reloaded: [],
          failed: [],
          error:
            "the certificate was renewed, but nginx was not reloaded: its configuration test failed. wiki.lan keeps serving the previous certificate until nginx reloads",
        },
      }),
    ),
  )
  await page.goto("/proxy/certificates")
  const panel = localCAPanel(page)
  await expect(panel.getByText("The last renewal check did not finish")).toBeVisible()
  await expect(panel.getByText(/wiki.lan keeps serving the previous certificate/)).toBeVisible()
  await expect(panel.getByText("cannot be renewed", { exact: true })).toBeVisible()
  await expect(panel.getByText("its key could not be read, so it cannot be renewed")).toBeVisible()
  await expect(panel.getByText("20d left, due")).toBeVisible()
})

test("an administrator makes the local CA from its panel; a reader takes the root and nothing else", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  let exists = false
  const ca = await serve(page, "**/api/v1/certificates/local-ca", (sent, route) => {
    if (sent.method === "POST") exists = true
    return json(route, exists ? existingLocalCA({ leaves: [] }) : localCA())
  })
  await page.goto("/proxy/certificates")
  const panel = localCAPanel(page)
  await panel.getByRole("button", { name: "Create the local CA" }).click()
  await expect(page.getByText("Local CA created")).toBeVisible()
  await expect(panel.getByText("Nothing issued yet.")).toBeVisible()
  expect(ca.filter((s) => s.method === "POST")).toHaveLength(1)

  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await page.reload()
  await expect(panel.getByRole("link", { name: "Download root" })).toBeVisible()
  await expect(panel.getByRole("button", { name: "Issue", exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Other ways to get a certificate" })).toHaveCount(0)

  exists = false
  await page.reload()
  await expect(page.locator("[data-slot='stat-grid']")).toBeVisible()
  await expect(page.getByRole("heading", { name: "Local CA", exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Create the local CA" })).toHaveCount(0)
})

test("a certificate nothing trusts by default says where it is trusted", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/certificates/", (route) =>
    json(route, [
      { ...issued("nas.lan", ["nas.lan", "192.168.1.10"]).certificate, usedBy: ["nas.lan"] },
      {
        ...issued("nas-copy", ["nas.lan"]).certificate,
        path: "/etc/nginx/ssl/nas-copy.pem",
        source: "nginx:nas-copy",
        usedBy: ["nas-copy"],
      },
      {
        ...certs[0],
        name: "printer",
        path: "/etc/ssl/just-dashboard/printer/fullchain.pem",
        issuer: "10.0.0.7",
        domains: ["10.0.0.7"],
        selfSigned: true,
        source: "imported",
        expiring: false,
        daysLeft: 300,
      },
    ]),
  )
  await page.goto("/proxy/certificates")
  const cards = page.getByRole("list", { name: "Installed certificates" })
  // The certificate kept with the imports, and a copy a site names elsewhere.
  await expect(cards.getByText("local CA", { exact: true })).toHaveCount(2)
  await cards.getByRole("button", { name: "Inspect nas.lan" }).click()
  const sheet = page.getByRole("dialog")
  await expect(
    sheet.getByText(
      /^Trusted only on devices where the local CA's root is installed\. Renewed here/,
    ),
  ).toBeVisible()
  await page.keyboard.press("Escape")
  await cards.getByRole("button", { name: "Inspect nas-copy" }).click()
  await expect(
    page.getByRole("dialog").getByText(/so the daily check does not renew it\.$/),
  ).toBeVisible()
  await page.keyboard.press("Escape")
  await cards.getByRole("button", { name: "Inspect printer" }).click()
  await expect(
    page
      .getByRole("dialog")
      .getByText(/^Trusted only on devices told to trust this certificate itself/),
  ).toBeVisible()
})

test("the private forms and panels fit a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockShowcase(page)
  await page.goto("/proxy/certificates")
  for (const item of ["From the local CA", "Self-signed", "Signing request for a CA"]) {
    await openMenu(page)
    await page.getByRole("menuitem", { name: item }).click()
    const dialog = page.getByRole("dialog")
    await dialog.getByLabel("Names").fill("nas.lan 192.168.1.10 fe80::1")
    await expect(dialog).toBeInViewport({ ratio: 1 })
    expect(await dialog.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(
      true,
    )
    await page.keyboard.press("Escape")
    await expect(dialog).toBeHidden()
  }
  await page.getByRole("button", { name: "Show request" }).first().click()
  const shown = page.getByRole("dialog")
  await expect(shown).toBeInViewport({ ratio: 1 })
  expect(await shown.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(
    true,
  )
  await page.keyboard.press("Escape")
  const main = page.locator("[data-slot='page']")
  expect(await main.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(
    true,
  )
})
