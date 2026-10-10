import { expect, test, type Page, type Route } from "@playwright/test"
import { json, mockProxy, user } from "./proxy-fixtures"
import {
  dropInPath,
  heldStream,
  includePlan,
  moduleNotInstalled,
  streamEntry,
  streamStatus,
} from "./fixtures/proxy/streams"

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
  await sheet.getByRole("textbox", { name: "Name", exact: true }).fill("replica")
  await sheet.getByRole("textbox", { name: "Listen on", exact: true }).fill("6432")
  await sheet.getByLabel("Forward to").fill("10.0.0.5:5432")
  return sheet
}

test("the stream module comes before the include on a host without it", async ({ page }) => {
  await mockProxy(page, { included: false })
  await listing(page, { included: false, module: moduleNotInstalled })
  await page.goto("/proxy/streams")

  // This host: pasting the stream block first would stop nginx reloading, so
  // the module is the step with a button, and the include waits for it.
  await expect(page.getByText("This nginx cannot forward streams yet")).toBeVisible()
  const steps = page.getByRole("list", { name: "Before a stream can forward" })
  await expect(steps.getByText("not installed")).toBeVisible()
  await expect(page.getByRole("button", { name: "Install libnginx-mod-stream" })).toBeVisible()
  await expect(steps.getByText("after the module")).toBeVisible()
  await expect(page.getByRole("button", { name: "Connect", exact: true })).toHaveCount(0)
  await expect(page.getByText("Or add it by hand")).toHaveCount(0)
  await expect(page.getByText("stream {")).toHaveCount(0)
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
    streams: [
      {
        ...bastion,
        state: "not-read",
        stateReason: "This nginx has no stream module, so it cannot read a stream.",
      },
    ],
  })
  await page.goto("/proxy/streams")
  await expect(page.getByText("nginx.conf has a stream block this nginx cannot read")).toBeVisible()
  await expect(page.getByText(/every reload is refused/)).toBeVisible()
  await expect(page.locator("[data-slot='choice-row']").getByText("not read")).toBeVisible()
  // The steps above say why; the card does not say it again.
  await expect(
    page.getByText("This nginx has no stream module, so it cannot read a stream."),
  ).toHaveCount(0)
  // nginx's test fails on the stream block for every file, so no save can
  // pass: nothing offers to make one.
  await expect(page.getByRole("button", { name: "Prepare a stream" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "New stream" })).toHaveCount(0)
  await page.getByRole("button", { name: "Edit bastion" }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByText("No stream can pass nginx’s test yet")).toBeVisible()
  await expect(sheet.getByText("nginx’s test fails until it has the stream module.")).toBeVisible()
  await expect(sheet.getByRole("textbox", { name: "Listen on", exact: true })).toBeDisabled()
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
  await expect(page.getByText(/Install libnginx-mod-stream/).first()).toBeVisible()
  await expect(
    page.getByText(/includes \/etc\/nginx\/streams inside http instead, where its test refuses/),
  ).toBeVisible()
  // The module can go in from here; the include in http comes out by hand.
  await expect(page.getByRole("button", { name: "Install libnginx-mod-stream" })).toBeVisible()
  await expect(page.getByRole("button", { name: "Connect", exact: true })).toHaveCount(0)
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
  await sheet.getByRole("textbox", { name: "Name", exact: true }).fill("ssh")
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
  await sheet.getByRole("textbox", { name: "Listen on", exact: true }).fill("6433")
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
  await expect(sheet.getByRole("textbox", { name: "Listen on", exact: true })).toBeDisabled()
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
  await expect(sheet.getByRole("combobox", { name: "Listening address" })).toContainText(
    "Every address",
  )
  await expect(
    sheet.getByText("Every address this host has, including any added later."),
  ).toBeVisible()
  await expect(sheet.getByRole("switch", { name: "Listen on IPv6 too" })).toHaveAttribute(
    "aria-checked",
    "false",
  )
  await expect(sheet.getByText(/Only on 0\.0\.0\.0/)).toHaveCount(0)
})

test("a read-only account can inspect streams without edit controls", async ({ page }) => {
  await mockProxy(page, { included: true })
  await listing(page, { streams: [bastion] })
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await page.goto("/proxy/streams")
  await expect(page.locator("[data-slot='choice-row']")).toHaveCount(1)
  await expect(page.getByRole("button", { name: "New stream" })).toHaveCount(0)
  await page.getByRole("button", { name: "More actions for bastion" }).click()
  await expect(page.getByRole("menuitem", { name: "Traffic" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Edit" })).toHaveCount(0)
  await expect(page.getByRole("menuitem", { name: "Delete" })).toHaveCount(0)
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

// A typed 0 was read as empty: nothing was sent, nginx's default was saved,
// and the field went on saying 0. nginx takes 0 and drops every connection.
test("a timeout of 0 is refused, not saved as nginx's default", async ({ page }) => {
  await mockProxy(page, { included: true })
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "New stream" }).click()
  const sheet = await fillNew(page)
  const save = sheet.getByRole("button", { name: "Save and reload" })
  const idle = sheet.getByLabel("Idle timeout")
  const connect = sheet.getByLabel("Connect timeout")

  await idle.fill("0")
  await expect(sheet.getByRole("alert")).toHaveText(
    "0 makes nginx drop every connection at once. Leave it empty for nginx's 10m.",
  )
  await expect(idle).toHaveAttribute("aria-invalid", "true")
  await expect(save).toBeDisabled()
  await expect(sheet.getByText("Correct the timeout, and the nginx appears here.")).toBeVisible()

  await idle.fill("")
  await connect.fill("0s")
  await expect(sheet.getByRole("alert")).toHaveText(
    "0 makes nginx drop every connection at once. Leave it empty for nginx's 60s.",
  )
  await expect(connect).toHaveAttribute("aria-invalid", "true")
  await expect(idle).not.toHaveAttribute("aria-invalid", "true")
  await expect(save).toBeDisabled()

  await connect.fill("")
  await expect(sheet.getByRole("alert")).toHaveCount(0)
  await expect(save).toBeEnabled()
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

// With no finding for a directory it could not read, the overview said
// streams were within limits over open forwards it could not see.
test("the overview does not call streams within limits when it could not read them", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  // Nothing else on this host has anything to say, so the list is the streams' alone.
  for (const path of ["certificates/", "proxy/vhosts", "ports"]) {
    await page.route(`**/api/v1/${path}`, (route) => json(route, []))
  }
  await page.route("**/api/v1/certificates/certbot", (route) =>
    route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "certbot_unavailable", message: "no certbot" } }),
    }),
  )
  let broken = false
  await page.route("**/api/v1/proxy/streams/", (route) =>
    broken ? unreadable(route) : json(route, streamStatus({ streams: [] })),
  )
  await page.goto("/proxy")
  await expect(
    page.getByText(
      "Certificates, renewal, sites, upstreams, streams and exposed ports all within limits",
    ),
  ).toBeVisible()

  broken = true
  await page.reload()
  const finding = page.getByRole("button", { name: /^Could not read the stream directory/ })
  await expect(finding).toBeVisible()
  await expect(page.getByText(/all within limits/)).toHaveCount(0)
  await finding.click()
  await expect(page.getByText("open /etc/nginx/streams: permission denied")).toBeVisible()
  await expect(
    page.getByText(
      "Until it can be read, no stream is checked for an open port or for a reload it would stop.",
    ),
  ).toBeVisible()
  await page.getByRole("button", { name: "Open streams" }).click()
  await expect(page).toHaveURL(/\/proxy\/streams$/)
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

/** A running job as POST /packages/install answers it. */
const installJob = {
  id: "job-1",
  kind: "packages.install",
  title: "Installing libnginx-mod-stream",
  target: "libnginx-mod-stream",
  status: "running",
  exitCode: 0,
  startedAt: new Date().toISOString(),
  lines: 0,
}

test("install posts the module's package, streams the job, and the next step opens", async ({
  page,
}) => {
  await mockProxy(page, { included: false })
  let installed = false
  await page.route("**/api/v1/proxy/streams/", (route) =>
    route.request().method() === "GET"
      ? json(
          route,
          streamStatus({
            included: false,
            module: installed ? { state: "loaded", usable: true } : moduleNotInstalled,
          }),
        )
      : route.fallback(),
  )
  const bodies: unknown[] = []
  await page.route("**/api/v1/packages/install", (route) => {
    bodies.push(route.request().postDataJSON())
    return json(route, installJob)
  })
  await page.routeWebSocket("**/api/v1/jobs/job-1/stream**", (socket) => {
    socket.send(
      JSON.stringify({
        type: "output",
        data: [
          {
            seq: 1,
            stream: "stdout",
            text: "Setting up libnginx-mod-stream (1.26.3-2ubuntu1.2) ...",
            at: "",
          },
        ],
      }),
    )
    installed = true
    socket.send(
      JSON.stringify({
        type: "job",
        data: { ...installJob, status: "succeeded", endedAt: new Date().toISOString(), lines: 1 },
      }),
    )
  })
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "Install libnginx-mod-stream" }).click()

  expect(bodies).toEqual([{ packages: ["libnginx-mod-stream"] }])
  await expect(page.getByText("Installing libnginx-mod-stream").first()).toBeVisible()
  await expect(page.getByText(/Setting up libnginx-mod-stream/)).toBeVisible()
  // The job's end reads the listing again: the module is in, and the
  // include is the step with the button now.
  const steps = page.getByRole("list", { name: "Before a stream can forward" })
  await expect(steps.getByText("loaded", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Connect", exact: true })).toBeVisible()
  await expect(page.getByText("nginx is not reading these yet")).toBeVisible()
})

/** The listing, whose module is in once `installed()` says so. */
async function installListing(page: Page, installed: () => boolean) {
  await page.route("**/api/v1/proxy/streams/", (route) =>
    route.request().method() === "GET"
      ? json(
          route,
          streamStatus({
            included: false,
            module: installed() ? { state: "loaded", usable: true } : moduleNotInstalled,
          }),
        )
      : route.fallback(),
  )
}

/** An install finished earlier on the Packages page. */
const htopJob = {
  ...installJob,
  id: "job-0",
  title: "Installing htop",
  target: "htop",
  status: "succeeded",
  endedAt: new Date().toISOString(),
  lines: 1,
}

test("a page opened while the module installs shows that install and starts no second one", async ({
  page,
}) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockProxy(page, { included: false })
  let installed = false
  await installListing(page, () => installed)
  await page.route("**/api/v1/jobs/", (route) =>
    json(route, [installed ? { ...installJob, status: "succeeded" } : installJob, htopJob]),
  )
  await page.route("**/api/v1/jobs/job-1", (route) =>
    json(route, {
      job: installJob,
      lines: [
        { seq: 1, stream: "stdout", text: "Unpacking libnginx-mod-stream (1.26.3) ...", at: "" },
      ],
    }),
  )
  let finish = () => {}
  await page.routeWebSocket("**/api/v1/jobs/job-1/stream**", (socket) => {
    finish = () => {
      installed = true
      socket.send(
        JSON.stringify({
          type: "job",
          data: { ...installJob, status: "succeeded", endedAt: new Date().toISOString(), lines: 1 },
        }),
      )
    }
  })
  let posted = 0
  await page.route("**/api/v1/packages/install", (route) => {
    posted++
    return json(route, installJob)
  })
  await page.goto("/proxy/streams")

  // The install already running is on screen, and the button waits for it.
  await expect(page.getByText(/Unpacking libnginx-mod-stream/)).toBeVisible()
  await expect(page.getByRole("button", { name: "Installing libnginx-mod-stream…" })).toBeDisabled()
  await expect(
    page.getByText("This keeps running if you close the page — reopen it from the recent list."),
  ).toBeVisible()
  // The recent list the console names is beside the button.
  const steps = page.getByRole("list", { name: "Before a stream can forward" })
  await expect(steps.getByRole("button", { name: "htop", exact: true })).toBeVisible()
  await expect(
    steps.getByRole("button", { name: "libnginx-mod-stream", exact: true }),
  ).toBeVisible()
  const pageFrame = page.locator("[data-slot='page']")
  expect(
    await pageFrame.evaluate((element) => element.scrollWidth <= element.clientWidth + 1),
  ).toBe(true)
  if (process.env.JD_SHOTS) await page.screenshot({ path: process.env.JD_SHOTS, fullPage: true })

  finish()
  await expect(steps.getByText("loaded", { exact: true })).toBeVisible()
  await expect(page.getByRole("button", { name: "Connect", exact: true })).toBeVisible()
  expect(posted).toBe(0)
})

test("an install that ends while another run is on screen still holds the button, then reads the listing", async ({
  page,
}) => {
  await mockProxy(page, { included: false })
  let installed = false
  await installListing(page, () => installed)
  await page.route("**/api/v1/jobs/", (route) =>
    json(route, [installed ? { ...installJob, status: "succeeded" } : installJob, htopJob]),
  )
  await page.route("**/api/v1/jobs/job-1", (route) => json(route, { job: installJob, lines: [] }))
  await page.route("**/api/v1/jobs/job-0", (route) =>
    json(route, {
      job: htopJob,
      lines: [{ seq: 1, stream: "stdout", text: "Setting up htop (3.3.0-4) ...", at: "" }],
    }),
  )
  await page.routeWebSocket("**/api/v1/jobs/job-1/stream**", () => {})
  await page.goto("/proxy/streams")
  const steps = page.getByRole("list", { name: "Before a stream can forward" })
  await expect(page.getByRole("button", { name: "Installing libnginx-mod-stream…" })).toBeDisabled()

  // Reading an earlier run from the list does not free the button for a second install.
  await steps.getByRole("button", { name: "htop", exact: true }).click()
  await expect(page.getByText(/Setting up htop/)).toBeVisible()
  await expect(page.getByRole("button", { name: "Installing libnginx-mod-stream…" })).toBeDisabled()

  // Its end, seen only in the list, reads the listing again: the module is in.
  installed = true
  await expect(steps.getByText("loaded", { exact: true })).toBeVisible({ timeout: 15_000 })
  await expect(page.getByText(/Setting up htop/)).toBeVisible()
})

test("an install that cannot start says why and offers it again", async ({ page }) => {
  await mockProxy(page, { included: false })
  await listing(page, { included: false, module: moduleNotInstalled })
  await page.route("**/api/v1/packages/install", (route) =>
    route.fulfill({
      status: 503,
      contentType: "application/json",
      body: JSON.stringify({
        error: { code: "not_installed", message: "no supported package manager on this host" },
      }),
    }),
  )
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "Install libnginx-mod-stream" }).click()
  await expect(page.getByText("Could not start installing libnginx-mod-stream")).toBeVisible()
  await expect(page.getByRole("button", { name: "Install libnginx-mod-stream" })).toBeEnabled()
})

test("an outage from a stream block without the module offers the install in the notice", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await listing(page, { included: true, module: moduleNotInstalled, streams: [bastion] })
  await page.goto("/proxy/streams")
  await expect(page.getByText("nginx.conf has a stream block this nginx cannot read")).toBeVisible()
  await expect(page.getByRole("button", { name: "Install libnginx-mod-stream" })).toBeVisible()
})

/** Captures what the connect posts, and answers it. */
async function onConnect(page: Page, answer: (route: Route) => Promise<void> | void) {
  const bodies: Record<string, unknown>[] = []
  await page.route("**/api/v1/proxy/streams/include", (route) => {
    bodies.push(route.request().postDataJSON())
    return answer(route)
  })
  return bodies
}

test("connect shows the new file, posts the plan it showed, and the notice goes", async ({
  page,
}) => {
  await mockProxy(page, { included: false })
  let connected = false
  await page.route("**/api/v1/proxy/streams/", (route) =>
    route.request().method() === "GET"
      ? json(
          route,
          streamStatus({
            included: connected,
            connection: connected ? { mode: "dropin", path: dropInPath } : undefined,
            streams: [{ ...bastion, state: connected ? "live" : "not-read" }],
          }),
        )
      : route.fallback(),
  )
  await page.route("**/api/v1/proxy/streams/include/plan", (route) =>
    json(route, includePlan({ streams: ["bastion"] })),
  )
  const bodies = await onConnect(page, (route) => {
    connected = true
    return json(route, {
      mode: "dropin",
      path: dropInPath,
      validation: { valid: true, output: "", command: "nginx -t", diagnostics: [], warnings: 0 },
      streams: 1,
      reloaded: true,
      warnings: [],
    })
  })
  await page.goto("/proxy/streams")
  const steps = page.getByRole("list", { name: "Before a stream can forward" })
  await expect(steps.getByText("built in")).toHaveCount(0)
  await expect(steps.getByText("loaded", { exact: true })).toBeVisible()
  await page.getByRole("button", { name: "Connect", exact: true }).click()

  const sheet = page.getByRole("dialog")
  await expect(sheet.getByText("Connect the stream directory")).toBeVisible()
  await expect(sheet.getByText("a new file")).toBeVisible()
  await expect(sheet.getByText(dropInPath)).toBeVisible()
  await expect(
    sheet.getByText(/^Creates zz-just-dashboard-stream\.conf in \/etc\/nginx\/modules-enabled\./),
  ).toBeVisible()
  await expect(
    sheet.getByText("The stream here starts forwarding once the test passes."),
  ).toBeVisible()
  // A new file has no before to compare with.
  await expect(sheet.getByRole("radio", { name: "Before" })).toHaveCount(0)
  await expect(sheet.locator(".monaco-editor .view-lines")).toContainText("stream {", {
    timeout: 20_000,
  })
  await sheet.getByRole("button", { name: "Connect", exact: true }).click()

  await expect(page.getByText("Stream directory connected", { exact: true })).toBeVisible()
  expect(bodies).toEqual([{ mode: "dropin", path: dropInPath, reload: true }])
  await expect(sheet).toHaveCount(0)
  await expect(page.getByText("nginx is not reading these yet")).toHaveCount(0)
  await expect(page.getByRole("button", { name: "New stream" })).toBeVisible()
  await expect(
    page.locator("[data-slot='choice-row']").getByText("live", { exact: true }),
  ).toBeVisible()
})

test("an edit to nginx.conf shows before and after, and can wait for the next reload", async ({
  page,
}) => {
  await mockProxy(page, { included: false })
  const before = "user nginx;\nevents {}\nhttp {}\n"
  const block =
    "# Just Dashboard: streams begin.\nstream {\n    include /etc/nginx/streams/*.conf;\n}\n# Just Dashboard: streams end."
  await page.route("**/api/v1/proxy/streams/include/plan", (route) =>
    json(
      route,
      includePlan({
        mode: "nginx.conf",
        path: "/etc/nginx/nginx.conf",
        exists: true,
        reason: "no-directory",
        keepsCopy: true,
        before,
        after: `${before}\n${block}\n`,
        added: block,
        line: 5,
      }),
    ),
  )
  const bodies = await onConnect(page, (route) =>
    json(route, {
      mode: "nginx.conf",
      path: "/etc/nginx/nginx.conf",
      backup: "/etc/nginx/nginx.conf.jd-stream-1759000000.bak",
      validation: { valid: true, output: "", command: "nginx -t", diagnostics: [], warnings: 0 },
      streams: 0,
      reloaded: false,
      warnings: [],
    }),
  )
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "Connect", exact: true }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByText("an edit to nginx.conf")).toBeVisible()
  await expect(
    sheet.getByText(
      /it includes no directory at its top level where a file of its own could go\. The file as it is now is kept beside it\./,
    ),
  ).toBeVisible()
  const lines = sheet.locator(".monaco-editor .view-lines")
  await expect(lines).toContainText("streams begin", { timeout: 20_000 })
  await sheet.getByRole("radio", { name: "Before" }).click()
  await expect(lines).not.toContainText("streams begin")
  await expect(lines).toContainText("user nginx;")

  await sheet.getByRole("switch", { name: "Reload nginx after" }).click()
  await expect(
    sheet.getByText("Written and tested now; nginx reads the directory at its next reload."),
  ).toBeVisible()
  await sheet.getByRole("button", { name: "Connect", exact: true }).click()
  await expect(page.getByText("Stream directory connected, not reloaded")).toBeVisible()
  await expect(
    page.getByText(/kept as \/etc\/nginx\/nginx\.conf\.jd-stream-1759000000\.bak/),
  ).toBeVisible()
  expect(bodies[0]).toEqual({ mode: "nginx.conf", path: "/etc/nginx/nginx.conf", reload: false })
})

test("an edit to nginx.conf gives the plan's own reason, and no copy nginx would read", async ({
  page,
}) => {
  await mockProxy(page, { included: false })
  const before =
    "include /etc/nginx/modules-enabled/*.conf;\nload_module modules/ngx_stream_js_module.so;\nevents {}\n"
  const block =
    "# Just Dashboard: streams begin.\nstream {\n    include /etc/nginx/streams/*.conf;\n}\n# Just Dashboard: streams end."
  const noCopy =
    "No copy of /etc/nginx/nginx.conf is kept beside it: an include in the configuration would read the copy as configuration."
  await page.route("**/api/v1/proxy/streams/include/plan", (route) =>
    json(
      route,
      includePlan({
        mode: "nginx.conf",
        path: "/etc/nginx/nginx.conf",
        exists: true,
        reason: "load-module-after",
        dropIn: dropInPath,
        keepsCopy: false,
        before,
        after: `${before}\n${block}\n`,
        added: block,
        line: 5,
        warnings: [noCopy],
      }),
    ),
  )
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "Connect", exact: true }).click()
  const sheet = page.getByRole("dialog")
  await expect(
    sheet.getByText(
      /after every module it loads: it includes \/etc\/nginx\/modules-enabled at its top level, but a module is loaded after that directory, and nginx refuses a module loaded after a stream block\.$/,
    ),
  ).toBeVisible()
  await expect(sheet.getByText(/includes no directory/)).toHaveCount(0)
  await expect(sheet.getByText(/kept beside it\./)).toHaveCount(0)
  await expect(sheet.getByText(noCopy)).toBeVisible()
})

test("a stream whose port is taken keeps the connect from being made", async ({ page }) => {
  await mockProxy(page, { included: false })
  await page.route("**/api/v1/proxy/streams/include/plan", (route) =>
    json(
      route,
      includePlan({
        streams: ["bastion"],
        conflicts: ["bastion: port 2222/tcp is already in use by sshd (pid 900)"],
      }),
    ),
  )
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "Connect", exact: true }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByText("nginx could not bind every stream")).toBeVisible()
  await expect(
    sheet.getByText("bastion: port 2222/tcp is already in use by sshd (pid 900)"),
  ).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Connect", exact: true })).toBeDisabled()
})

test("a connect nginx's test refuses says so in the sheet and changes nothing", async ({
  page,
}) => {
  await mockProxy(page, { included: false })
  await onConnect(page, (route) =>
    route.fulfill({
      status: 422,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "invalid_config",
          message:
            'nginx: [emerg] "load_module" directive is specified too late in /etc/nginx/nginx.conf:4',
        },
      }),
    }),
  )
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "Connect", exact: true }).click()
  const sheet = page.getByRole("dialog")
  await sheet.getByRole("button", { name: "Connect", exact: true }).click()
  await expect(sheet.getByText("nginx’s test refused it, so nothing changed")).toBeVisible()
  await expect(sheet.getByText(/specified too late/).first()).toBeVisible()
  await expect(page.getByText("Not connected")).toBeVisible()
  await expect(sheet).toBeVisible()
})

test("a plan that cannot be made reads as the reason", async ({ page }) => {
  await mockProxy(page, { included: false })
  await page.route("**/api/v1/proxy/streams/include/plan", (route) =>
    route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({
        error: {
          code: "stream_block_elsewhere",
          message:
            "nginx's stream block is in /opt/streams.conf, which the dashboard does not write; add `include /etc/nginx/streams/*.conf;` inside it by hand",
          operation: "work out",
          resource: "the change",
        },
      }),
    }),
  )
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "Connect", exact: true }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByText("Could not work out the change")).toBeVisible()
  await expect(sheet.getByText(/stream block is in \/opt\/streams\.conf/)).toBeVisible()
  await expect(sheet.getByRole("button", { name: "Connect", exact: true })).toBeDisabled()
})

test("the include can still be added by hand, and copied", async ({ page }) => {
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"])
  await mockProxy(page, { included: false })
  await page.goto("/proxy/streams")
  await page.getByText("Or add it by hand").click()
  await expect(page.getByText(/At the top level of nginx\.conf, beside the/)).toBeVisible()
  await page.getByRole("button", { name: "Copy" }).click()
  await expect(page.getByText("Include copied")).toBeVisible()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
    "stream {\n    include /etc/nginx/streams/*.conf;\n}",
  )
})

test("with a stream block already there, the include goes inside it", async ({ page }) => {
  await mockProxy(page, { included: false })
  await listing(page, {
    included: false,
    streamBlock: "/etc/nginx/nginx.conf",
    snippet: "include /etc/nginx/streams/*.conf;",
  })
  await page.goto("/proxy/streams")
  await page.getByText("Or add it by hand").click()
  await expect(page.getByText(/Inside the stream block in/)).toBeVisible()
  await expect(page.getByText("include /etc/nginx/streams/*.conf;", { exact: true })).toBeVisible()
})

test("a read-only account reads the steps without a button to press", async ({ page }) => {
  await mockProxy(page, { included: false })
  await listing(page, { included: false, module: moduleNotInstalled })
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await page.goto("/proxy/streams")
  await expect(page.getByText("This nginx cannot forward streams yet")).toBeVisible()
  await expect(page.getByText(/Install libnginx-mod-stream, the package/)).toBeVisible()
  await expect(page.getByRole("button", { name: /Install/ })).toHaveCount(0)

  await listing(page, { included: false })
  await page.reload()
  await expect(page.getByText("nginx is not reading these yet")).toBeVisible()
  await expect(page.getByRole("button", { name: "Connect", exact: true })).toHaveCount(0)
  // The manual include is theirs to read, without a fold.
  await expect(page.getByText(/At the top level of nginx\.conf/)).toBeVisible()
})

test("disconnect takes out only the dashboard's include, and says what stops", async ({ page }) => {
  await mockProxy(page, { included: true })
  await listing(page, {
    included: true,
    connection: { mode: "dropin", path: dropInPath },
    streams: [bastion],
  })
  const bodies: unknown[] = []
  await page.route("**/api/v1/proxy/streams/include/remove", (route) => {
    bodies.push(route.request().postDataJSON())
    return json(route, {
      mode: "dropin",
      path: dropInPath,
      validation: { valid: true, output: "", command: "nginx -t", diagnostics: [], warnings: 0 },
      streams: 1,
      reloaded: false,
      reloadError: "nginx: [alert] kill(1234, 1) failed",
      warnings: [],
    })
  })
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "More stream directory actions" }).click()
  await page.getByRole("menuitem", { name: "Disconnect" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("its stream stops forwarding as soon as it reloads")
  await expect(dialog).toContainText(`It removes ${dropInPath}, the file the dashboard added.`)
  await dialog.getByRole("button", { name: "Disconnect and reload" }).click()
  expect(bodies).toEqual([{ reload: true }])
  await expect(page.getByText("nginx did not reload")).toBeVisible()
})

test("an include written by hand has no disconnect", async ({ page }) => {
  await mockProxy(page, { included: true })
  await listing(page, { included: true, streams: [bastion] })
  await page.goto("/proxy/streams")
  await expect(page.getByRole("button", { name: "New stream" })).toBeVisible()
  await expect(page.getByRole("button", { name: "More stream directory actions" })).toHaveCount(0)
})

test("the steps and the connect sheet fit a phone", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockProxy(page, { included: false })
  await page.route("**/api/v1/proxy/streams/include/plan", (route) =>
    json(
      route,
      includePlan({
        streams: ["bastion"],
        conflicts: ["bastion: port 2222/tcp is already in use by sshd (pid 900)"],
      }),
    ),
  )
  await page.goto("/proxy/streams")
  await page.getByText("Or add it by hand").click()
  const pageFrame = page.locator("[data-slot='page']")
  expect(
    await pageFrame.evaluate((element) => element.scrollWidth <= element.clientWidth + 1),
  ).toBe(true)
  await page.getByRole("button", { name: "Connect", exact: true }).click()
  const sheet = page.getByRole("dialog")
  await expect(sheet.getByText("nginx could not bind every stream")).toBeVisible()
  await expect(sheet).toBeInViewport({ ratio: 1 })
  expect(await sheet.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(
    true,
  )
  await page.keyboard.press("Escape")
  await expect(sheet).toHaveCount(0)
})

// Every card said "configured" while nginx had failed to bind half of them.
// Each now says what nginx does with it, and why where it is not live.
const restricted = { protocol: "tcp" as const, allowFrom: ["10.0.0.0/8"] }
const stateShowcase = [
  streamEntry({ ...restricted, name: "db", listen: 5432, upstream: "10.0.0.5:5432" }),
  heldStream({ ...restricted, name: "cache", listen: 6379, upstream: "10.0.0.9:6379" }),
  streamEntry({
    ...restricted,
    name: "web",
    listen: 8080,
    upstream: "10.0.0.7:80",
    state: "not-listening",
    stateReason:
      "Port 8080/tcp is also where the site app.example.com listens. nginx cannot bind it for both, so every reload fails until one of them moves.",
    blocker: {
      port: 8080,
      proto: "tcp",
      kind: "site",
      name: "app.example.com",
      site: "app.example.com",
    },
  }),
  streamEntry({
    ...restricted,
    name: "zz-copy",
    listen: 5432,
    upstream: "10.0.0.6:5432",
    state: "shadowed",
    stateReason:
      "Port 5432/tcp is taken first by the stream db, so nginx gives that stream every connection there and ignores this one's listen.",
    blocker: { port: 5432, proto: "tcp", kind: "stream", name: "db" },
  }),
  streamEntry({
    ...restricted,
    name: "quiet",
    listen: 7100,
    upstream: "10.0.0.8:7100",
    state: "not-listening",
    stateReason:
      "nginx holds no socket for port 7100/tcp: it last loaded its configuration before this file changed.",
  }),
]

test("each stream says what nginx does with it, worst first, with where to look", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  await listing(page, { streams: stateShowcase })
  await page.goto("/proxy/streams")

  const cards = page.getByRole("list", { name: "Streams" }).locator("[data-slot='choice-row']")
  // Two stop every reload on the host, then one that forwards nothing, one
  // another stream shadows, and the one that is fine.
  await expect(cards).toHaveCount(5)
  const names = await cards.evaluateAll((rows) =>
    rows.map((row) => row.querySelector("button")?.textContent?.trim()),
  )
  expect(names).toEqual(["cache", "web", "quiet", "zz-copy", "db"])

  const tile = page.locator("[data-slot='stat-tile']").filter({ hasText: /^Live/ })
  await expect(tile).toContainText("1")
  await expect(tile).toContainText("2 stop every reload")

  const cache = cards.filter({ hasText: "cache" })
  await expect(cache.getByText("not listening", { exact: true })).toBeVisible()
  await expect(
    cache.getByText(/is held by postgres \(pid 900\), so nginx cannot bind it/),
  ).toBeVisible()
  await expect(
    cache.getByText(
      "2026/09/28 03:29:05 bind() to 0.0.0.0:6379 failed (98: Address already in use)",
    ),
  ).toBeVisible()
  await expect(cache.getByRole("link", { name: "See who holds it" })).toHaveAttribute(
    "href",
    "/proxy/ports",
  )
  const web = cards.filter({ hasText: "web" })
  await expect(web.getByRole("link", { name: "Open the site" })).toHaveAttribute(
    "href",
    "/proxy/sites?site=app.example.com",
  )
  const copy = cards.filter({ hasText: "zz-copy" })
  await expect(copy.getByText("shadowed", { exact: true })).toBeVisible()
  await expect(copy.getByText(/taken first by the stream db/)).toBeVisible()
  await expect(copy.getByRole("link")).toHaveCount(0)
  const db = cards.filter({ hasText: /^db/ })
  await expect(db.getByText("live", { exact: true })).toBeVisible()
  await expect(db.getByRole("button", { name: "Re-check" })).toHaveCount(0)
})

test("the chips filter the list and keep the one chosen", async ({ page }) => {
  await mockProxy(page, { included: true })
  await listing(page, { streams: stateShowcase })
  await page.goto("/proxy/streams")

  // A chip no stream is under is hidden.
  const strip = page.locator("[data-slot='panel-toolbar']")
  await expect(strip.getByRole("button")).toHaveText([
    "All 5",
    "TCP 5",
    "Restricted 5",
    "Not live 4",
  ])
  await strip.getByRole("button", { name: /^Not live/ }).click()
  const cards = page.getByRole("list", { name: "Streams" }).locator("[data-slot='choice-row']")
  await expect(cards).toHaveCount(4)
  await page.reload()
  await expect(cards).toHaveCount(4)

  // Only the live one is left: the chosen chip stays to say why the list is
  // empty, beside All.
  await page.route("**/api/v1/proxy/streams/", (route) =>
    route.request().method() === "GET"
      ? json(route, streamStatus({ streams: stateShowcase.slice(0, 1) }))
      : route.fallback(),
  )
  await page.reload()
  await expect(page.getByText("No stream is under this chip.")).toBeVisible()
  await expect(strip.getByRole("button", { name: /^Not live/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  await strip.getByRole("button", { name: /^All/ }).click()
  await expect(cards).toHaveCount(1)
})

test("re-check reads the list again, for any account", async ({ page }) => {
  await mockProxy(page, { included: true })
  let reads = 0
  let release: () => void = () => {}
  await page.route("**/api/v1/proxy/streams/", async (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    reads++
    if (reads === 2) await new Promise<void>((resolve) => (release = resolve))
    return json(route, streamStatus({ streams: stateShowcase }))
  })
  await page.route("**/api/v1/auth/session", (route) =>
    json(route, { ...user, capabilities: ["read"], user: { ...user.user, role: "viewer" } }),
  )
  await page.goto("/proxy/streams")
  const quiet = page.locator("[data-slot='choice-row']").filter({ hasText: "quiet" })
  // A read-only account edits nothing, and may still ask again.
  await expect(quiet.getByRole("button", { name: "Edit" })).toHaveCount(0)
  await quiet.getByRole("button", { name: "Re-check" }).click()
  await expect(quiet.getByRole("button", { name: "Checking…" })).toBeDisabled()
  await expect.poll(() => reads).toBe(2)
  release()
  await expect(quiet.getByRole("button", { name: "Re-check" })).toBeEnabled()
})

test("the form says a port is taken while it is typed, with the next one free", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const previews: Record<string, unknown>[] = []
  await page.route("**/api/v1/proxy/streams/preview", (route) => {
    const body = route.request().postDataJSON()
    previews.push(body)
    const listen = body.spec.listen
    const conflict =
      listen === 6432
        ? {
            port: 6432,
            proto: "tcp",
            kind: "program",
            name: "postgres",
            pid: 900,
            suggest: 6433,
            message: "port 6432/tcp is already in use by postgres (pid 900) — 6433 is free",
          }
        : listen === 8080
          ? {
              port: 8080,
              proto: "tcp",
              kind: "site",
              name: "app.example.com",
              site: "app.example.com",
              suggest: 8081,
              message: "port 8080/tcp is already in use by the site app.example.com — 8081 is free",
            }
          : undefined
    return json(route, { content: "# Managed by Just Dashboard.\n", warnings: [], conflict })
  })
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "New stream" }).click()
  const sheet = await fillNew(page)
  await sheet.getByRole("textbox", { name: "Listen on", exact: true }).fill("6432")

  const refusal = sheet.getByRole("alert")
  await expect(refusal).toContainText("already in use by postgres (pid 900) — 6433 is free")
  await expect(refusal.getByRole("link", { name: "See who holds it" })).toHaveAttribute(
    "href",
    "/proxy/ports",
  )
  expect(previews.at(-1)?.previous).toBe("")
  await refusal.getByRole("button", { name: "Use 6433" }).click()
  await expect(sheet.getByRole("textbox", { name: "Listen on", exact: true })).toHaveValue("6433")
  await expect(sheet.getByRole("alert")).toHaveCount(0)

  await sheet.getByRole("textbox", { name: "Listen on", exact: true }).fill("8080")
  await expect(
    sheet.getByRole("alert").getByRole("link", { name: "Open the site" }),
  ).toHaveAttribute("href", "/proxy/sites?site=app.example.com")
})

test("a save nginx could not bind is refused on the field and changes nothing", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  const message =
    "nginx could not bind port 6432/tcp when it reloaded: bind() to 0.0.0.0:6432 failed (98: Address in use) — it is held by postgres (pid 900); the stream was put back as it was — 6433 is free"
  await onSave(page, (route) =>
    route.fulfill({
      status: 409,
      contentType: "application/json",
      body: JSON.stringify({ error: { code: "port_in_use", message, field: "spec.listen" } }),
    }),
  )
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "New stream" }).click()
  const sheet = await fillNew(page)
  await sheet.getByRole("button", { name: "Save and reload" }).click()
  await expect(sheet.getByRole("alert")).toContainText("the stream was put back as it was")
  await expect(page.getByText("Not saved", { exact: true })).toBeVisible()
  await expect(sheet).toBeVisible()
})

test("a save says listening only when nginx held the port, and offers to ask again when not", async ({
  page,
}) => {
  await mockProxy(page, { included: true })
  let answer = saved({ name: "replica", listening: true })
  await onSave(page, (route) => json(route, answer))
  let reads = 0
  await page.route("**/api/v1/proxy/streams/", (route) => {
    if (route.request().method() !== "GET") return route.fallback()
    reads++
    return json(route, streamStatus())
  })
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "New stream" }).click()
  await (await fillNew(page)).getByRole("button", { name: "Save and reload" }).click()
  await expect(page.getByText("replica saved and listening")).toBeVisible()

  answer = saved({
    name: "replica",
    listening: false,
    listenNote: "nginx had not taken the reload up 3s after it was sent.",
  })
  await page.getByRole("button", { name: "New stream" }).click()
  await (await fillNew(page)).getByRole("button", { name: "Save and reload" }).click()
  await expect(page.getByText("replica saved, not listening yet")).toBeVisible()
  await expect(
    page.getByText("nginx had not taken the reload up 3s after it was sent."),
  ).toBeVisible()
  const before = reads
  await page.getByRole("button", { name: "Re-check" }).click()
  await expect.poll(() => reads).toBeGreaterThan(before)

  // Nobody could watch: saved and reloaded, and why it could not be told.
  answer = saved({
    name: "replica",
    listenNote:
      "Whether nginx took it up could not be checked: no running nginx reads /etc/nginx/nginx.conf.",
  })
  await page.getByRole("button", { name: "New stream" }).click()
  await (await fillNew(page)).getByRole("button", { name: "Save and reload" }).click()
  await expect(page.getByText("replica saved and reloaded")).toBeVisible()
  await expect(page.getByText(/could not be checked: no running nginx reads/)).toBeVisible()
})

test("the overview names a stream that stops every reload", async ({ page }) => {
  await mockProxy(page, { included: true })
  await listing(page, { streams: [stateShowcase[1]] })
  await page.goto("/proxy")
  const finding = page.getByRole("button", {
    name: /^nginx refuses every reload: stream cache asks for port 6379\/tcp, which postgres \(pid 900\) holds/,
  })
  await expect(finding).toBeVisible()
})

test("the states fit a phone, bind errors and all", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mockProxy(page, { included: true })
  await listing(page, { streams: stateShowcase })
  await page.goto("/proxy/streams")
  await expect(page.getByText(/bind\(\) to 0\.0\.0\.0:6379 failed/)).toBeVisible()
  expect(
    await page
      .locator("[data-slot='page']")
      .evaluate((element) => element.scrollWidth <= element.clientWidth + 1),
  ).toBe(true)
})

test("a TCP stream traces its connection path on the Network page", async ({ page }) => {
  await mockProxy(page, { included: true })
  const udp = streamEntry({ name: "dns", listen: 5353, protocol: "udp", upstream: "10.0.0.53:53" })
  await listing(page, { streams: [bastion, udp] })
  await page.route("**/api/v1/network/investigate/sources", (route) =>
    json(route, { containers: [] }),
  )
  await page.goto("/proxy/streams")
  await page.getByRole("button", { name: "More actions for dns" }).click()
  await expect(page.getByRole("menuitem", { name: "Trace in Network" })).toHaveCount(0)
  await page.keyboard.press("Escape")

  await page.getByRole("button", { name: "More actions for bastion" }).click()
  await page.getByRole("menuitem", { name: "Trace in Network" }).click()
  await expect(page).toHaveURL(
    /\/network\/investigate\?target=127\.0\.0\.1&port=2222&protocol=tcp&family=inet&measure=1$/,
  )
  await expect(page.getByLabel("Destination", { exact: true })).toHaveValue("127.0.0.1")
  await expect(page.getByLabel("Port", { exact: true })).toHaveValue("2222")
})
