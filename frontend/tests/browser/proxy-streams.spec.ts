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
  await expect(page.locator("[data-slot='choice-row']").getByText("not live")).toBeVisible()
  // nginx's test fails on the stream block for every file, so no save can
  // pass: nothing offers to make one.
  await expect(page.getByRole("button", { name: "Prepare a stream" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "New stream" })).toHaveCount(0)
  await page.getByRole("button", { name: "Edit bastion" }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByText("No stream can pass nginx’s test yet")).toBeVisible()
  await expect(sheet.getByText("nginx’s test fails until it has the stream module.")).toBeVisible()
  await expect(sheet.getByLabel("Listen on")).toBeDisabled()
  await expect(sheet.getByRole("button", { name: "Save for later" })).toBeDisabled()
  await expect(sheet.getByText("This will not forward anything yet")).toHaveCount(0)
})

// nginx reads a file included inside http — as http — and its test refuses
// proxy_pass there, so every reload on the host fails. Saying "nginx is not
// reading these" beside a Reload that can only fail was the opposite of it.
test("a stream file included inside http is an outage, said as one on every surface", async ({
  page,
}) => {
  await mockProxy(page, { included: false })
  await listing(page, { included: false, includedIn: "http", streams: [bastion] })
  await page.goto("/proxy/streams")

  await expect(page.getByText("These files stop every nginx reload")).toBeVisible()
  await expect(
    page.getByText(/includes \/etc\/nginx\/streams inside http, where a stream/),
  ).toBeVisible()
  await expect(page.getByText(/every reload is refused — for every site/)).toBeVisible()
  await expect(page.getByText(/Move the include into a stream block of its own/)).toBeVisible()
  await expect(page.getByText("Deleting the files below also ends the refusals.")).toBeVisible()
  await expect(page.getByText("every reload refused")).toBeVisible()
  await expect(page.getByText(/not reading these/)).toHaveCount(0)
  await expect(page.getByText("not read by nginx")).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Prepare a stream" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "New stream" })).toHaveCount(0)

  await page.getByRole("button", { name: "Edit bastion" }).click()
  const sheet = page.getByRole("dialog")
  // nginx reads the directory, as http: it is not "not read yet".
  await expect(
    sheet.getByText("nginx reads this directory inside http, where its test refuses a stream."),
  ).toBeVisible()
  await expect(sheet.getByText(/does not read this directory yet/)).toHaveCount(0)
  await expect(sheet.getByText(/includes this directory inside http/)).toBeVisible()
  await expect(
    sheet.getByText(/with files there, it refuses every reload on this host/),
  ).toBeVisible()
  await expect(sheet.getByText(/Move the include into a top-level stream block/)).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Save for later" })).toBeDisabled()
  await page.keyboard.press("Escape")
  await expect(sheet).toHaveCount(0)

  await page.getByRole("button", { name: "More actions for bastion" }).click()
  await page.getByRole("menuitem", { name: "Delete" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText(
    "nginx reads these inside http, where its test refuses them, so port 2222 was never forwarded.",
  )
  await expect(dialog).not.toContainText("not reading these")
  await expect(dialog.getByRole("button", { name: "Delete and reload" })).toHaveCount(0)
  await dialog.getByRole("button", { name: "Cancel" }).click()

  // The Overview reports it with the module outage's weight, not as streams
  // nginx merely ignores.
  await page.goto("/proxy")
  const finding = page.getByText(
    "nginx refuses every reload: a stream file is included inside http",
  )
  await expect(finding).toBeVisible()
  await expect(page.getByText(/written but nginx is not reading/)).toHaveCount(0)
  await finding.click()
  await expect(
    page.getByText(/every reload is refused — for every site on this host/),
  ).toBeVisible()
  await expect(
    page.getByText(
      "Move the include into a top-level stream block beside the http block, or delete the stream files.",
    ),
  ).toBeVisible()
})

// Ubuntu's default: no stream module. Moving the include into a stream block
// there swaps one outage for another, so the include comes out and the
// module goes in before any stream block is offered.
test("with no stream module, a misplaced include comes out before the module and the block", async ({
  page,
}) => {
  await mockProxy(page, { included: false })
  await listing(page, {
    included: false,
    includedIn: "http",
    module: moduleNotInstalled,
    streams: [bastion],
  })
  await page.goto("/proxy/streams")
  await expect(page.getByText("These files stop every nginx reload")).toBeVisible()
  await expect(
    page.getByText(
      /Take out that include, which ends the refusals\. Install libnginx-mod-stream, the package with nginx's stream module\. Then add this/,
    ),
  ).toBeVisible()
  await expect(page.getByText(/Move the include/)).toHaveCount(0)

  await page.getByRole("button", { name: "Edit bastion" }).click()
  const sheet = page.getByRole("dialog")
  await expect(
    sheet.getByText(
      /Take that include out\. Install libnginx-mod-stream, the package with nginx's stream module\. Then add this stream block/,
    ),
  ).toBeVisible()
  await expect(sheet.getByText(/Move the include/)).toHaveCount(0)
  await page.keyboard.press("Escape")

  await page.goto("/proxy")
  await page.getByText("nginx refuses every reload: a stream file is included inside http").click()
  await expect(
    page.getByText(
      /^Take out the include that puts \/etc\/nginx\/streams there, which ends the refusals\. Install libnginx-mod-stream/,
    ),
  ).toBeVisible()
  await expect(page.getByText(/but this nginx has no stream module/)).toHaveCount(0)
})

test("an empty directory included inside http is named, and nothing offers a save", async ({
  page,
}) => {
  await mockProxy(page, { included: false })
  await listing(page, { included: false, includedIn: "http" })
  await page.goto("/proxy/streams")
  // An empty directory passes nginx's test wherever it is included: no outage.
  await expect(page.getByText("This directory is included in the wrong place")).toBeVisible()
  await expect(
    page.getByText(/includes \/etc\/nginx\/streams inside http, where nginx does not read/),
  ).toBeVisible()
  await expect(page.getByText("These files stop every nginx reload")).toHaveCount(0)
  await expect(page.getByText(/not reading these/)).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Prepare a stream" })).toHaveCount(0)
})

test("the module notice names an include inside http on a host without the module", async ({
  page,
}) => {
  await mockProxy(page, { included: false })
  await listing(page, { included: false, includedIn: "http", module: moduleNotInstalled })
  await page.goto("/proxy/streams")
  await expect(page.getByText("This nginx cannot forward streams yet")).toBeVisible()
  await expect(page.getByText(/Install libnginx-mod-stream/)).toBeVisible()
  await expect(
    page.getByText(/includes \/etc\/nginx\/streams inside http instead, where its test refuses/),
  ).toBeVisible()
  await expect(page.getByText("These files stop every nginx reload")).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Prepare a stream" })).toHaveCount(0)
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
  await sheet.getByRole("radio", { name: "UDP", exact: true }).click()
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

/** Answers the DELETE of one stream and records the path it was sent to. */
async function onDelete(page: Page, answer: Record<string, unknown>) {
  const paths: string[] = []
  await page.route(
    (url) => url.pathname.startsWith("/api/v1/proxy/streams/"),
    (route) => {
      if (route.request().method() !== "DELETE") return route.fallback()
      paths.push(new URL(route.request().url()).pathname)
      return json(route, answer)
    },
  )
  return paths
}

test("an unreadable stream file is deleted without a port or a backup promised", async ({
  page,
}) => {
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
  const paths = await onDelete(page, {
    name: "secret",
    reloaded: true,
    unread: "open /etc/nginx/streams/secret.conf: permission denied",
  })
  await page.goto("/proxy/streams")
  const card = page.locator("[data-slot='choice-row']").first()
  await expect(card.getByText("unreadable")).toBeVisible()
  await expect(card.getByText(/permission denied/)).toBeVisible()
  await expect(card.getByRole("button", { name: "Edit", exact: true })).toBeDisabled()
  await page.getByRole("button", { name: "More actions for secret" }).click()
  await page.getByRole("menuitem", { name: "Delete" }).click()

  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("nginx reloads without it.")
  await expect(dialog).toContainText("It could not be read, so no copy of it is kept.")
  await expect(dialog).not.toContainText("Port 0")
  await expect(dialog).not.toContainText(".bak")
  await dialog.getByRole("button", { name: "Delete and reload" }).click()
  await expect(page.getByText("Delete secret completed")).toBeVisible()
  await expect(page.getByText("No copy was kept")).toHaveCount(0)
  expect(paths).toEqual(["/api/v1/proxy/streams/secret"])
})

test("a link to nothing is deleted as a link, by its encoded name", async ({ page }) => {
  await mockProxy(page, { included: true })
  await listing(page, {
    streams: [
      {
        ...streamEntry({ name: "a+b", listen: 0, protocol: "tcp", upstream: "" }),
        open: false,
        managed: false,
        link: "../streams-available/gone.conf",
        unsupported: ["a symbolic link"],
        error: "it links to ../streams-available/gone.conf, which does not exist",
      },
    ],
  })
  const paths = await onDelete(page, {
    name: "a+b",
    reloaded: true,
    link: "../streams-available/gone.conf",
  })
  await page.goto("/proxy/streams")
  const card = page.locator("[data-slot='choice-row']").first()
  await expect(card.getByText(/which does not exist/)).toBeVisible()
  await page.getByRole("button", { name: "More actions for a+b" }).click()
  await page.getByRole("menuitem", { name: "Delete" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText(
    "This removes the link only, not ../streams-available/gone.conf.",
  )
  await expect(dialog).not.toContainText(".bak")
  await dialog.getByRole("button", { name: "Delete and reload" }).click()
  await expect(page.getByText("Delete a+b completed")).toBeVisible()
  // The server unescapes it; sent raw, + would be read as itself or a space.
  expect(paths).toEqual(["/api/v1/proxy/streams/a%2Bb"])
})

test("a readable file whose delete could not keep a copy says so", async ({ page }) => {
  await mockProxy(page, { included: true })
  await listing(page, { streams: [bastion] })
  await onDelete(page, {
    name: "bastion",
    reloaded: true,
    unread: "open /etc/nginx/streams/bastion.conf: permission denied",
  })
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "More actions for bastion" }).click()
  await page.getByRole("menuitem", { name: "Delete" }).click()
  await expect(page.getByRole("dialog")).toContainText("The file is kept as bastion.conf.bak.")
  await page.getByRole("dialog").getByRole("button", { name: "Delete and reload" }).click()
  await expect(page.getByText("No copy was kept")).toBeVisible()
  await expect(page.getByText(/bastion.conf could not be read when it was deleted/)).toBeVisible()
})

test("a linked stream opens read-only, and its delete keeps what it points to", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const linked = {
    ...streamEntry({
      name: "linked",
      listen: 20003,
      protocol: "tcp",
      upstream: "10.0.0.5:6000",
      allowFrom: ["10.0.0.0/8"],
    }),
    managed: false,
    link: "../streams-available/linked.conf",
    unsupported: ["a symbolic link"],
  }
  await listing(page, { streams: [linked] })
  await page.goto("/proxy/streams")
  const card = page.locator("[data-slot='choice-row']").first()
  await expect(card).toContainText("a symbolic link")

  await page.getByRole("button", { name: "Edit linked" }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByText(/uses a symbolic link, which this form cannot keep/)).toBeVisible()
  await expect(sheet.getByText(/It links to \.\.\/streams-available\/linked\.conf\./)).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Save and reload" })).toBeDisabled()
  await expect(sheet.getByRole("button", { name: "Edit the file" })).toBeVisible()
  await page.keyboard.press("Escape")

  await page.getByRole("button", { name: "More actions for linked" }).click()
  await page.getByRole("menuitem", { name: "Delete" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("Port 20003 stops being forwarded as soon as nginx reloads.")
  await expect(dialog).toContainText(
    "This removes the link only, not ../streams-available/linked.conf.",
  )
})

test("a plain listen port reads as every IPv4 address, not as a restriction", async ({ page }) => {
  await mockProxy(page, { included: true })
  const plain = {
    ...streamEntry({
      name: "plain",
      listen: 20003,
      protocol: "tcp",
      upstream: "192.168.1.5:6000",
      allowFrom: ["192.168.1.0/24"],
      address: "0.0.0.0",
    }),
    managed: false,
  }
  await listing(page, { streams: [plain] })
  await page.goto("/proxy/streams")
  const card = page.locator("[data-slot='choice-row']").first()
  await expect(card.getByText("Listen on every IPv4 address")).toBeVisible()
  await expect(card.getByText("20003", { exact: true })).toBeVisible()
  await expect(card).not.toContainText("0.0.0.0")

  await page.getByRole("button", { name: "Edit plain" }).click()
  const sheet = page.getByRole("dialog")
  await expect(
    sheet.getByText("Every IPv4 address, as the file has it — it has no IPv6 listen."),
  ).toBeVisible()
  await expect(sheet.getByText(/Only on 0\.0\.0\.0/)).toHaveCount(0)
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

test("TCP+UDP is one choice, and asks how its UDP sessions end", async ({ page }) => {
  await mockProxy(page, { included: true })
  const bodies = await onSave(page, (route) => json(route, saved({ name: "replica" })))
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "New stream" }).click()
  const sheet = await fillNew(page)
  await expect(sheet.getByText("UDP sessions")).toHaveCount(0)
  await sheet.getByRole("radio", { name: "TCP+UDP" }).click()
  await expect(sheet.getByText("UDP sessions")).toBeVisible()
  await sheet.getByRole("radio", { name: "One reply" }).click()
  await expect(
    sheet.getByText(
      "Each reply ends the session: one question, one answer, as DNS works. TCP connections are not affected.",
    ),
  ).toBeVisible()
  await sheet.getByRole("button", { name: "Save and reload" }).click()

  await expect(page.getByText("replica saved and reloaded")).toBeVisible()
  const spec = bodies[0].spec as Record<string, unknown>
  expect(spec.protocol).toBe("both")
  expect(spec.udpMode).toBe("request")
})

test("timeouts are typed as 90s, 10m or 1h, and a wrong one is caught before the save", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const previews: { spec: Record<string, unknown> }[] = []
  await page.route("**/api/v1/proxy/streams/preview", (route) => {
    previews.push(route.request().postDataJSON())
    return json(route, { content: "# Managed by Just Dashboard.\n", warnings: [] })
  })
  const bodies = await onSave(page, (route) => json(route, saved({ name: "replica" })))
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "New stream" }).click()
  const sheet = await fillNew(page)
  const save = sheet.getByRole("button", { name: "Save and reload" })

  await sheet.getByLabel("Idle timeout").fill("10x")
  await expect(sheet.getByRole("alert")).toHaveText("Write it as 90s, 10m or 1h30m, up to 24h.")
  await expect(sheet.getByLabel("Idle timeout")).toHaveAttribute("aria-invalid", "true")
  await expect(save).toBeDisabled()
  await expect(sheet.getByText("Correct the timeout, and the nginx appears here.")).toBeVisible()

  await sheet.getByLabel("Idle timeout").fill("1h30m")
  await sheet.getByLabel("Connect timeout").fill("5s")
  await expect(sheet.getByRole("alert")).toHaveCount(0)
  await expect.poll(() => previews.at(-1)?.spec.timeout).toBe(5400)
  expect(previews.at(-1)?.spec.connectTimeout).toBe(5)
  await save.click()

  await expect(page.getByText("replica saved and reloaded")).toBeVisible()
  const spec = bodies[0].spec as Record<string, unknown>
  expect(spec.timeout).toBe(5400)
  expect(spec.connectTimeout).toBe(5)
})

test("a stream's timeouts open as they would be typed, and save unchanged", async ({ page }) => {
  await mockProxy(page, { included: true })
  await listing(page, { streams: [{ ...bastion, timeout: 3600, connectTimeout: 90 }] })
  const bodies = await onSave(page, (route) => json(route, saved()))
  await page.goto("/proxy/streams")
  const card = page.locator("[data-slot='choice-row']").first()
  await expect(card).toContainText("1h idle timeout")
  await expect(card).toContainText("1m 30s connect timeout")

  await page.getByRole("button", { name: "Edit bastion" }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByLabel("Idle timeout")).toHaveValue("1h")
  await expect(sheet.getByLabel("Connect timeout")).toHaveValue("1m30s")
  await sheet.getByRole("button", { name: "Save and reload" }).click()
  await expect(page.getByText("bastion saved and reloaded")).toBeVisible()
  const spec = bodies[0].spec as Record<string, unknown>
  expect([spec.timeout, spec.connectTimeout]).toEqual([3600, 90])
})

test("a stream of both protocols counts as TCP and as UDP", async ({ page }) => {
  await mockProxy(page, { included: true })
  const dns = {
    ...streamEntry({
      name: "dns",
      listen: 53,
      protocol: "both",
      upstream: "10.0.0.53:53",
      allowFrom: ["10.0.0.0/8"],
    }),
    udpMode: "request",
    open: false,
  }
  await listing(page, { streams: [dns, bastion] })
  await page.goto("/proxy/streams")
  const tiles = page.locator("[data-slot='stat-grid']")
  await expect(tiles.getByText("TCP", { exact: true }).locator("..")).toContainText("2")
  await expect(tiles.getByText("UDP", { exact: true }).locator("..")).toContainText("1")
  const card = page.locator("[data-slot='choice-row']").filter({ hasText: "dns" })
  await expect(card).toContainText("TCP+UDP forwarding · one reply per UDP session")

  await page.getByRole("button", { name: "Edit dns" }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByRole("radio", { name: "TCP+UDP" })).toBeChecked()
  await expect(sheet.getByRole("radio", { name: "One reply" })).toBeChecked()
})

/** A stream directory the backend could not read, as it answers. */
function unreadable(route: Route) {
  return route.fulfill({
    status: 500,
    contentType: "application/json",
    body: JSON.stringify({
      error: {
        code: "stream_dir_unreadable",
        message: "open /etc/nginx/streams: permission denied",
        operation: "read",
        resource: "the stream directory",
        retryable: true,
      },
    }),
  })
}

test("an unreadable stream directory is an error to retry, not an empty list", async ({ page }) => {
  await mockProxy(page, { included: true })
  let broken = true
  await page.route("**/api/v1/proxy/streams/", (route) =>
    broken ? unreadable(route) : json(route, streamStatus({ streams: [bastion] })),
  )
  await page.goto("/proxy/streams")
  await expect(page.getByText("Could not read the stream directory")).toBeVisible()
  await expect(page.getByText("open /etc/nginx/streams: permission denied")).toBeVisible()
  await expect(page.getByText("Nothing forwarded")).toHaveCount(0)

  broken = false
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(page.locator("[data-slot='choice-row']")).toHaveCount(1)
})

test("a refresh that fails keeps the list and says it is the last read", async ({ page }) => {
  await mockProxy(page, { included: true })
  let reads = 0
  let broken = false
  await page.route("**/api/v1/proxy/streams/", (route) => {
    if (route.request().method() === "POST") return json(route, saved({ name: "replica" }))
    reads++
    return broken ? unreadable(route) : json(route, streamStatus({ streams: [bastion] }))
  })
  await page.goto("/proxy/streams")
  await expect(page.locator("[data-slot='choice-row']")).toHaveCount(1)

  // The save's refresh finds the directory unreadable.
  broken = true
  await page.getByRole("button", { name: "New stream" }).click()
  await fillNew(page)
  await page.getByRole("dialog").getByRole("button", { name: "Save and reload" }).click()
  await expect(page.getByText("The list below is from the last read")).toBeVisible()
  await expect(page.getByText("open /etc/nginx/streams: permission denied")).toBeVisible()
  await expect(page.locator("[data-slot='choice-row']")).toHaveCount(1)

  broken = false
  const before = reads
  await page.getByRole("button", { name: "Try again" }).click()
  await expect(page.getByText("The list below is from the last read")).toHaveCount(0)
  expect(reads).toBeGreaterThan(before)
})

test("the TCP+UDP form fits a phone, errors and all", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockProxy(page, { included: true })
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "New stream" }).click()
  const sheet = await fillNew(page)
  await sheet.getByRole("radio", { name: "TCP+UDP" }).click()
  await sheet.getByLabel("Idle timeout").fill("forever")
  await sheet.getByLabel("Connect timeout").fill("2d")
  await expect(sheet.getByRole("alert")).toHaveCount(2)
  await expect(sheet).toBeInViewport({ ratio: 1 })
  expect(await sheet.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(
    true,
  )
  // Every protocol is reachable from the keyboard.
  await sheet.getByRole("radio", { name: "TCP+UDP" }).focus()
  await page.keyboard.press("ArrowLeft")
  await expect(sheet.getByRole("radio", { name: "UDP", exact: true })).toBeFocused()
})
