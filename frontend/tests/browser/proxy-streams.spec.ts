import { expect, test, type Page, type Route } from "@playwright/test"
import { json, mockProxy, user } from "./proxy-fixtures"
import { moduleNotInstalled, streamEntry, streamStatus } from "./fixtures/proxy/streams"

/**
 * The Streams page, checked for the ways it used to say something untrue: a
 * snippet that breaks nginx on a host without the stream module, a rename
 * that replaced another stream, "Save for later" that reloaded anyway, a
 * delete or a save whose reload failed reported as done or as not applied,
 * `allow all` counted as a restriction, and hand-written files saved back
 * wider than they were.
 */

const bastion = streamEntry({
  name: "bastion",
  listen: 2222,
  protocol: "tcp",
  upstream: "10.0.0.9:22",
  allowFrom: ["10.0.0.0/8"],
})

/** The listing, answered for GET; every other method falls through to the caller's handler. */
async function listing(page: Page, status: Record<string, unknown>) {
  await page.route("**/api/v1/proxy/streams/", (route) =>
    route.request().method() === "GET" ? json(route, streamStatus(status)) : route.fallback(),
  )
}

/** Captures what the form posts to save, and answers it. */
async function onSave(page: Page, answer: (route: Route) => Promise<void> | void) {
  const bodies: Record<string, unknown>[] = []
  await page.route("**/api/v1/proxy/streams/", (route) => {
    if (route.request().method() !== "POST") return route.fallback()
    bodies.push(route.request().postDataJSON())
    return answer(route)
  })
  return bodies
}

function saved(overrides: Record<string, unknown> = {}) {
  return {
    name: "bastion",
    path: "/etc/nginx/streams/bastion.conf",
    content: "",
    warnings: [],
    reloaded: true,
    ...overrides,
  }
}

async function fillNew(page: Page) {
  const sheet = page.getByRole("dialog")
  await sheet.getByLabel("Name").fill("replica")
  await sheet.getByLabel("Listen on").fill("6432")
  await sheet.getByLabel("Forward to").fill("10.0.0.5:5432")
  return sheet
}

test("the stream module comes before the include on a host without it", async ({ page }) => {
  await mockProxy(page, { included: false })
  await listing(page, { included: false, module: moduleNotInstalled })
  await page.goto("/proxy/streams")

  // This host: pasting the stream block first would stop nginx reloading.
  const notice = page.getByText("This nginx cannot forward streams yet")
  await expect(notice).toBeVisible()
  await expect(page.getByText(/Install libnginx-mod-stream/).first()).toBeVisible()
  await expect(page.getByText("nginx is not reading these yet")).toHaveCount(0)

  await page.getByRole("button", { name: "Prepare a stream" }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByText("This will not forward anything yet")).toBeVisible()
  await expect(sheet.getByText(/nginx has no stream module/)).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Save for later" })).toBeVisible()
})

test("a stream block nginx cannot read is an outage, said as one", async ({ page }) => {
  await mockProxy(page, { included: true })
  await listing(page, {
    included: true,
    module: moduleNotInstalled,
    streams: [bastion],
  })
  await page.goto("/proxy/streams")
  await expect(page.getByText("nginx.conf has a stream block this nginx cannot read")).toBeVisible()
  await expect(page.getByText(/every reload is refused/)).toBeVisible()
  // Nothing is live, so nothing offers to reload for it.
  await expect(page.getByRole("button", { name: "Prepare a stream" })).toBeVisible()
  await expect(page.locator("[data-slot='choice-row']").getByText("not live")).toBeVisible()
})

test("an include inside http is named, not taken for a working one", async ({ page }) => {
  await mockProxy(page, { included: false })
  await listing(page, { included: false, includedIn: "http", streams: [bastion] })
  await page.goto("/proxy/streams")
  await expect(
    page.getByText(/includes .*inside http, where nginx does not read the files/),
  ).toBeVisible()
})

test("save for later does not reload, and says the file was not tested", async ({ page }) => {
  await mockProxy(page, { included: false })
  const bodies = await onSave(page, (route) =>
    json(route, saved({ name: "replica", reloaded: false })),
  )
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "Prepare a stream" }).click()
  const sheet = await fillNew(page)
  await expect(sheet.getByText(/its test cannot check this file/)).toBeVisible()
  await sheet.getByRole("button", { name: "Save for later" }).click()

  await expect(page.getByText("replica saved, not yet live")).toBeVisible()
  expect(bodies).toHaveLength(1)
  expect(bodies[0].reload).toBe(false)
  expect(bodies[0].previous).toBe("")
})

test("renaming says so, and names the file it came from", async ({ page }) => {
  await mockProxy(page, { included: true })
  await listing(page, { streams: [bastion] })
  const bodies = await onSave(page, (route) =>
    json(route, saved({ name: "ssh", renamed: "bastion" })),
  )
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "Edit bastion" }).click()
  const sheet = page.getByRole("dialog")
  await sheet.getByLabel("Name").fill("ssh")
  await expect(
    sheet.getByText(
      "Saving renames bastion.conf to ssh.conf and keeps the old file as bastion.conf.bak.",
    ),
  ).toBeVisible()
  await sheet.getByRole("button", { name: "Save and reload" }).click()

  await expect(page.getByText("bastion renamed to ssh saved and reloaded")).toBeVisible()
  const body = bodies[0] as { previous: string; reload: boolean; spec: Record<string, unknown> }
  expect(body.previous).toBe("bastion")
  expect(body.reload).toBe(true)
  expect(body.spec.name).toBe("ssh")
  // The listing's own fields would be refused by the save as unknown.
  for (const field of ["path", "managed", "open", "unsupported"]) {
    expect(body.spec).not.toHaveProperty(field)
  }
})

test("a port in use lands on the field, with the way to see who holds it", async ({ page }) => {
  await mockProxy(page, { included: true })
  await onSave(page, (route) =>
    route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "port_in_use",
          message: "port 6432/tcp is already in use by postgres (pid 900) — 6433 is free",
          field: "spec.listen",
        },
      }),
    }),
  )
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "New stream" }).click()
  const sheet = await fillNew(page)
  await sheet.getByRole("button", { name: "Save and reload" }).click()

  const refusal = sheet.getByRole("alert")
  await expect(refusal).toContainText("already in use by postgres (pid 900) — 6433 is free")
  await expect(refusal.getByRole("link", { name: "See who holds it" })).toHaveAttribute(
    "href",
    "/proxy/ports",
  )
  // The sheet stays open on the refused value; changing it clears the refusal.
  await sheet.getByLabel("Listen on").fill("6433")
  await expect(sheet.getByRole("alert")).toHaveCount(0)
})

test("a reload that fails after a clean test is saved, not 'Not applied'", async ({ page }) => {
  await mockProxy(page, { included: true })
  await onSave(page, (route) =>
    json(
      route,
      saved({
        name: "replica",
        reloaded: false,
        reloadError: "nginx: [alert] kill(1234, 1) failed (3: No such process)",
      }),
    ),
  )
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "New stream" }).click()
  await fillNew(page)
  await page.getByRole("dialog").getByRole("button", { name: "Save and reload" }).click()
  await expect(page.getByText("replica saved, reload failed")).toBeVisible()
  await expect(page.getByText(/No such process/)).toBeVisible()
  await expect(page.getByText("Not saved")).toHaveCount(0)
})

test("a delete whose reload failed says the port is still forwarded", async ({ page }) => {
  await mockProxy(page, { included: true })
  await listing(page, { streams: [bastion] })
  let deleted = ""
  await page.route("**/api/v1/proxy/streams/bastion", (route) => {
    deleted = route.request().method()
    return json(route, {
      name: "bastion",
      reloaded: false,
      reloadError: "nginx refused to reload because its configuration test fails: nginx: [emerg] …",
    })
  })
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "More actions for bastion" }).click()
  await page.getByRole("menuitem", { name: "Delete" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Delete and reload" }).click()

  await expect(page.getByText("Delete bastion completed")).toBeVisible()
  await expect(page.getByText("nginx did not reload")).toBeVisible()
  await expect(page.getByText(/Port 2222 is still forwarded until nginx reloads/)).toBeVisible()
  expect(deleted).toBe("DELETE")
})

test("allow all is open to anyone, and counted so", async ({ page }) => {
  await mockProxy(page, { included: true })
  await listing(page, {
    streams: [
      {
        ...streamEntry({
          name: "everyone",
          listen: 7000,
          protocol: "tcp",
          upstream: "10.0.0.5:7000",
        }),
        allowFrom: ["all"],
        open: true,
      },
      bastion,
    ],
  })
  await page.goto("/proxy/streams")
  const tile = page.locator("[data-slot='stat-grid']").getByText("Open to anyone").locator("..")
  await expect(tile).toContainText("1")
  const cards = page.locator("[data-slot='choice-row']")
  await expect(cards.first()).toContainText("everyone")
  await expect(cards.first().getByText("anyone", { exact: true })).toBeVisible()
})

test("a hand-written file opens read-only, with the file itself to hand", async ({ page }) => {
  await mockProxy(page, { included: true })
  const manual = {
    ...streamEntry({
      name: "legacy",
      listen: 6000,
      protocol: "tcp",
      upstream: "10.0.0.5:6000",
      address: "127.0.0.1",
    }),
    managed: false,
    unsupported: ["deny rules", "2 upstream servers"],
  }
  await listing(page, { streams: [manual] })
  let read = ""
  await page.route("**/api/v1/proxy/config?**", (route) => {
    read = new URL(route.request().url()).searchParams.get("path") ?? ""
    return json(route, { content: "server { listen 127.0.0.1:6000; }\n" })
  })
  await page.goto("/proxy/streams")

  const card = page.locator("[data-slot='choice-row']").first()
  // A loopback bind is shown as one, and the card says who wrote the file.
  await expect(card.getByText("127.0.0.1:6000")).toBeVisible()
  await expect(card).toContainText("written by hand")

  await page.getByRole("button", { name: "Edit legacy" }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByText("Written by hand")).toBeVisible()
  await expect(sheet.getByText(/uses deny rules, 2 upstream servers/)).toBeVisible()
  await expect(sheet.getByLabel("Listen on")).toBeDisabled()
  await expect(sheet.getByRole("button", { name: "Save and reload" })).toBeDisabled()

  await sheet.getByRole("button", { name: "Edit the file" }).click()
  const editor = page.getByRole("dialog", { name: "legacy.conf" })
  await expect(editor.getByText("/etc/nginx/streams/legacy.conf").first()).toBeVisible()
  expect(read).toBe("/etc/nginx/streams/legacy.conf")
})

test("UDP asks how a session ends, and the preview warns in full", async ({ page }) => {
  await mockProxy(page, { included: true })
  const previews: { spec: Record<string, unknown> }[] = []
  await page.route("**/api/v1/proxy/streams/preview", (route) => {
    previews.push(route.request().postDataJSON())
    return json(route, {
      content: "# Managed by Just Dashboard.\n",
      warnings: [
        "A stream has no authentication of any kind — anything that can reach this port is through to the backend.",
        "On UDP, nginx puts the PROXY header in front of the first datagram of every session.",
      ],
    })
  })
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "New stream" }).click()
  const sheet = await fillNew(page)
  await sheet.getByRole("radio", { name: "UDP" }).click()
  await expect(sheet.getByText("UDP sessions")).toBeVisible()
  await sheet.getByRole("radio", { name: "One reply" }).click()
  await expect(sheet.getByText(/Each reply ends the session/)).toBeVisible()

  // Every warning, not only the first.
  await expect(sheet.getByText(/no authentication of any kind — anything that can/)).toBeVisible()
  await expect(sheet.getByText(/in front of the first datagram/)).toBeVisible()
  await expect.poll(() => previews.at(-1)?.spec.udpMode).toBe("request")
})

test("a preview refusal reads as a sentence", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.route("**/api/v1/proxy/streams/preview", (route) =>
    route.fulfill({
      status: 400,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "bad_request",
          message: "the upstream must look like 10.0.0.5:5432 or unix:/run/app.sock",
        },
      }),
    }),
  )
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "New stream" }).click()
  const sheet = await fillNew(page)
  await expect(
    sheet.getByText("the upstream must look like 10.0.0.5:5432 or unix:/run/app.sock"),
  ).toBeVisible()
  await expect(sheet.getByText(/ApiError/)).toHaveCount(0)
})

test("an unreadable stream file is shown as one, and can still be deleted", async ({ page }) => {
  await mockProxy(page, { included: true })
  await listing(page, {
    streams: [
      {
        ...streamEntry({ name: "secret", listen: 0, protocol: "tcp", upstream: "" }),
        open: false,
        managed: false,
        error: "open /etc/nginx/streams/secret.conf: permission denied",
      },
    ],
  })
  await page.goto("/proxy/streams")
  const card = page.locator("[data-slot='choice-row']").first()
  await expect(card.getByText("unreadable")).toBeVisible()
  await expect(card.getByText(/permission denied/)).toBeVisible()
  await expect(card.getByRole("button", { name: "Edit", exact: true })).toBeDisabled()
  await page.getByRole("button", { name: "More actions for secret" }).click()
  await expect(page.getByRole("menuitem", { name: "Delete" })).toBeEnabled()
})

test("a read-only account reads streams with no controls", async ({ page }) => {
  await mockProxy(page, { included: true })
  await listing(page, { streams: [bastion] })
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await page.goto("/proxy/streams")
  await expect(page.locator("[data-slot='choice-row']")).toHaveCount(1)
  await expect(page.getByRole("button", { name: "New stream" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "More actions for bastion" })).toHaveCount(0)
})

test("without nginx the page says so and offers nothing to save", async ({ page }) => {
  await mockProxy(page, { included: false })
  await page.route("**/api/v1/proxy/status", (route) =>
    json(route, {
      nginx: false,
      caddy: false,
      nginxDir: "/etc/nginx",
      caddyFile: "/etc/caddy/Caddyfile",
      certbot: false,
    }),
  )
  await listing(page, {
    included: false,
    module: { state: "unknown", usable: false, detail: "nginx was not found on this host" },
  })
  await page.goto("/proxy/streams")
  await expect(page.getByText("nginx is not installed on this host")).toBeVisible()
  await expect(page.getByRole("button", { name: "Prepare a stream" })).toHaveCount(0)
  await expect(page.getByText("nginx is not reading these yet")).toHaveCount(0)
})
