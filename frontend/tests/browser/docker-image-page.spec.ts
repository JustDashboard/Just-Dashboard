import { expect, test, type Page, type Route } from "@playwright/test"
import { IMAGES, imageDetail, mockImages } from "./docker-images-fixture"
import { risen } from "./risen"

/**
 * /docker/images/<id>: one image as a place of its own. Its identity line
 * with its verbs, a picture of where it comes from and who runs it, its views
 * in the address, and from 1280px the images as a rail beside it. The fixture
 * is the Images page's: fourteen images, two of them behind their registry.
 */

const hex = (seed: string) => seed.repeat(32)
const NGINX = hex("a1")
const MONGO = hex("fa")
const ACME = hex("b7")
const UNTAGGED = hex("cd")

const identity = (page: Page) => page.locator("[data-slot=image-identity]")
const rail = (page: Page) => page.locator("[data-slot=image-rail]")
const tabs = (page: Page) => page.getByRole("tablist")

async function json(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) })
}

/** An image the fixture does not hold, answered at its id and at any other handle given. */
async function mockOne(page: Page, detail: { id: string }, handles: string[] = []) {
  const asked: string[] = []
  for (const handle of [detail.id, ...handles]) {
    await page.route(`**/api/v1/docker/images/${encodeURIComponent(handle)}`, (route) => {
      if (route.request().method() !== "GET") return route.fallback()
      asked.push(new URL(route.request().url()).pathname)
      return json(route, detail)
    })
  }
  return asked
}

test("an image's page opens on what it is, where it comes from and who runs it", async ({
  page,
}) => {
  await mockImages(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/docker/images/${NGINX}`)

  const line = identity(page)
  await expect(line).toHaveAttribute("data-layout", "project")
  await expect(line.getByText("nginx:1.27-alpine")).toBeVisible()
  await expect(line.locator("img")).toHaveAttribute("src", "/logos/nginx.svg")
  await expect(line.getByText("Update available")).toBeVisible()
  await expect(line).toContainText("48.0 MB")
  await expect(line).toContainText("built 41d")
  await expect(line.getByRole("button", { name: "used by 2 containers" })).toBeVisible()
  await expect(line.getByRole("button", { name: "Pull", exact: true })).toBeVisible()
  // No row of figures opens the page.
  await expect(page.locator('[data-slot="stat-grid"]')).toHaveCount(0)

  const picture = page.locator("[data-slot=wire-flow]")
  const from = picture.getByRole("region", { name: "Comes from" })
  await expect(from).toContainText("Docker Hub")
  await expect(from).toContainText("update available")
  const users = picture.getByRole("region", { name: "Containers" })
  await expect(users.getByRole("link", { name: "Open web" })).toHaveAttribute(
    "href",
    "/docker/containers/1111111111111111",
  )
  await expect(users.getByRole("link", { name: "Open docs" })).toBeVisible()

  await expect(tabs(page).getByRole("tab")).toHaveText([
    /^Layers\s*5$/,
    /^Containers\s*2$/,
    /^History\s*9$/,
    "Configuration",
  ])
  await expect(page.getByRole("tab", { name: /^Layers/ })).toHaveAttribute("aria-selected", "true")
})

test("where the size went is one strip and one line per layer, in build order", async ({
  page,
}) => {
  await mockImages(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/docker/images/${NGINX}`)

  const layers = page.locator("[data-slot=image-layers]")
  await expect(layers).toContainText("5 layers · 48.0 MB")
  await expect(layers.getByRole("img", { name: /^5 layers of 48\.0 MB/ })).toBeVisible()
  const rows = layers.locator("[data-slot=rank-row]")
  await expect(rows).toHaveCount(5)
  // The base layer first, and no bar grows from row to row.
  await expect(rows.first()).toContainText("ADD")
  await expect(rows.first()).toContainText("7.8 MB")
  await expect(rows.first().locator("[data-slot=rank-share]")).toHaveText("16%")
  await expect(layers.locator(".bg-meter-track")).toHaveCount(0)
  const heights = await rows.evaluateAll((all) => all.map((r) => r.getBoundingClientRect().height))
  expect(new Set(heights).size).toBe(1)

  // A long command is cut to its line; the whole of it is a press away.
  const long = rows.nth(2).getByRole("button", { name: /^Step 4: RUN set -x/ })
  await long.click()
  const whole = page.locator("[data-slot=popover-content]")
  await expect(whole).toContainText("Step 4 of 9")
  await expect(whole).toContainText("linux-headers")
  await expect(whole).toContainText("adds 28.9 MB")
})

test("the open view is in the address and goes with the reader to the next image", async ({
  page,
}) => {
  await mockImages(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/docker/images/${NGINX}`)

  await page.getByRole("tab", { name: /^History/ }).click()
  await expect(page).toHaveURL(new RegExp(`/docker/images/${NGINX}\\?tab=history$`))
  const history = page.locator("[data-slot=image-history]")
  await expect(history.locator("[data-slot=table-body] tr")).toHaveCount(9)
  await expect(history.locator("[data-slot=table-body] tr").first()).toContainText("ADD")
  // A step that only set metadata is in the history and adds nothing.
  await expect(history.getByRole("row", { name: /ENV NGINX_VERSION/ })).toContainText("—")

  // The rail is the table's list in the table's order; nginx is second to last.
  const current = rail(page).locator("[aria-current=page]")
  await expect(current).toContainText("nginx")
  await page.keyboard.press("Alt+ArrowDown")
  await expect(page).toHaveURL(new RegExp(`/docker/images/${hex("d4")}\\?tab=history$`))
  await expect(identity(page).getByText("redis:7.2-alpine")).toBeVisible()
  await expect(page.getByRole("tab", { name: /^History/ })).toHaveAttribute("aria-selected", "true")
  await expect(current).toContainText("redis")
  await expect(page.getByRole("button", { name: "Next image" })).toBeDisabled()

  // A link that names a view opens on it.
  await page.goto(`/docker/images/${MONGO}?tab=configuration`)
  await expect(page.locator("[data-slot=image-configuration]")).toBeVisible()
  // The page's own view is not said.
  await page.getByRole("tab", { name: /^Layers/ }).click()
  await expect(page).toHaveURL(new RegExp(`/docker/images/${MONGO}$`))
})

test("the rail is the table's list, with its search and its filters", async ({ page }) => {
  await mockImages(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/docker/images/${NGINX}`)

  const column = rail(page)
  const rows = column.getByRole("listitem")
  await expect(rows).toHaveCount(IMAGES.length)
  await expect(column).toContainText("14 images · largest first")
  await expect(rows.first()).toContainText("n8nio/n8n")
  await expect(rows.first()).toContainText("1.1 GB")
  const heights = await column
    .getByRole("link")
    .evaluateAll((all) => all.map((r) => r.getBoundingClientRect().height))
  expect(new Set(heights)).toEqual(new Set([48]))

  await column.getByLabel("Search the images").fill("postgres")
  await expect(column.getByRole("link")).toHaveCount(3)
  // The image on screen stays first when the search leaves it out.
  await expect(column.getByRole("link").first()).toHaveAttribute("aria-current", "page")
  await expect(column).toContainText("2 of 14 images")
  await column.getByRole("link", { name: "postgres:15" }).click()
  await expect(page).toHaveURL(new RegExp(`/docker/images/${hex("c3")}$`))
  // Nothing runs it: that is its state, said once.
  await expect(identity(page).getByText("Not in use")).toBeVisible()
  await expect(identity(page).getByText("not in use", { exact: true })).toHaveCount(0)
})

test("the address that opened the sheet lands on the image's page", async ({ page }) => {
  await mockImages(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/docker/images?image=${encodeURIComponent(IMAGES[0].id)}`)
  await expect(page).toHaveURL(new RegExp(`/docker/images/${NGINX}$`))
  await expect(identity(page).getByText("nginx:1.27-alpine")).toBeVisible()
  await expect(page.getByRole("dialog")).toHaveCount(0)
})

test("a reference with a registry host and a slash resolves to the image's own address", async ({
  page,
}) => {
  await mockImages(page)
  const id = `sha256:${hex("9a")}`
  const detail = {
    ...imageDetail(IMAGES[0].id),
    id,
    repoTags: ["ghcr.io/acme/api:main"],
    repoDigests: [`ghcr.io/acme/api@sha256:${hex("9b")}`],
    ref: "ghcr.io/acme/api:main",
    usedBy: [],
    movingTag: false,
  }
  const asked = await mockOne(page, detail, ["ghcr.io/acme/api:main"])
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto("/docker/images/ghcr.io%2Facme%2Fapi%3Amain")

  await expect(identity(page).getByText("ghcr.io/acme/api:main")).toBeVisible()
  // One escaped segment reached the API, not three path segments.
  expect(asked[0]).toBe("/api/v1/docker/images/ghcr.io%2Facme%2Fapi%3Amain")
  await expect(page).toHaveURL(new RegExp(`/docker/images/${hex("9a")}$`))
  const from = page.locator("[data-slot=wire-flow]").getByRole("region", { name: "Comes from" })
  await expect(from).toContainText("ghcr.io")
  // It is not in the table's list, so the rail draws it first, filled.
  await expect(rail(page).getByRole("link").first()).toHaveAttribute("aria-current", "page")
  await expect(rail(page).getByRole("link")).toHaveCount(IMAGES.length + 1)
})

test("the containers are one-line rows of one height, each a way to its page", async ({ page }) => {
  await mockImages(page)
  const detail = {
    ...imageDetail(IMAGES[0].id),
    id: `sha256:${hex("9c")}`,
    repoTags: ["nginx:1.27-alpine"],
    usedBy: [
      { id: "1111111111111111", name: "web", state: "running", stack: "site", service: "web" },
      { id: "5555555555555555", name: "standalone", state: "exited" },
      { id: "6666666666666666", name: "third", state: "running", stack: "site" },
      { id: "7777777777777777", name: "fourth", state: "running" },
    ],
  }
  await mockOne(page, detail)
  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto(`/docker/images/${hex("9c")}?tab=containers`)
    const rows = page.locator("[data-slot=image-containers] li")
    await expect(rows).toHaveCount(4)
    await expect(rows.first().getByRole("link", { name: "Open web" })).toHaveAttribute(
      "href",
      "/docker/containers/1111111111111111",
    )
    await expect(rows.first()).toContainText("site · web")
    await expect(rows.nth(1)).toContainText("exited")
    await risen(page)
    const boxes = await rows.evaluateAll((all) =>
      all.map((r) => {
        const box = r.getBoundingClientRect()
        return { top: box.top, height: box.height }
      }),
    )
    expect(new Set(boxes.map((b) => b.height)).size, `row heights at ${width}`).toBe(1)
    // Each row starts where the one before it ended, plus the list's one gap.
    const gaps = boxes.slice(1).map((b, i) => Math.round(b.top - boxes[i].top))
    expect(new Set(gaps).size, `row steps at ${width}`).toBe(1)
  }
  // Wide, the picture names two and counts the rest, which opens this view.
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/docker/images/${hex("9c")}`)
  const users = page.locator("[data-slot=wire-flow]").getByRole("region", { name: "Containers" })
  await users.getByRole("button", { name: "2 more" }).click()
  await expect(page).toHaveURL(/\?tab=containers$/)
})

test("configuration says how it runs, how it is named and what it carries", async ({ page }) => {
  await mockImages(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/docker/images/${hex("b2")}?tab=configuration`)

  const view = page.locator("[data-slot=image-configuration]")
  await expect(view.getByRole("heading", { name: "How it runs" })).toBeVisible()
  await expect(view).toContainText("/docker-entrypoint.sh nginx -g daemon off;")
  await expect(view).toContainText("80/tcp")
  await expect(view).toContainText("/var/lib/postgresql/data")
  await expect(view).toContainText("linux/amd64")
  await expect(view.getByRole("heading", { name: "Names" })).toBeVisible()
  await expect(view).toContainText(`sha256:${hex("b2")}`)
  await expect(view).toContainText("postgres:16")
  await expect(view).toContainText(`postgres@sha256:${hex("e2")}`)
  await expect(view).toContainText("moving tag")
  await view.getByRole("button", { name: "What a moving tag means" }).hover()
  await expect(page.getByRole("tooltip")).toContainText("may resolve to another image")
  await expect(view.getByRole("heading", { name: "Environment" })).toBeVisible()
  await expect(view).toContainText("NGINX_VERSION=1.27.2")
})

test("every verb the sheet had works from the page", async ({ page }) => {
  const mocks = await mockImages(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/docker/images/${NGINX}`)

  // Pull is the one command, seeded with the image's own name.
  await identity(page).getByRole("button", { name: "Pull", exact: true }).click()
  const pull = page.getByRole("dialog", { name: "Pull an image" })
  await expect(pull.getByRole("textbox", { name: "Image reference" })).toHaveValue(
    "nginx:1.27-alpine",
  )
  await pull.getByRole("button", { name: "Close" }).first().click()

  // In use: Remove is drawn and Docker would refuse it.
  const more = identity(page).getByRole("button", { name: "More actions for nginx:1.27-alpine" })
  await more.click()
  await expect(page.getByRole("menuitem", { name: "Remove" })).toHaveAttribute("data-disabled", "")
  await expect(page.getByRole("menuitem", { name: "Copy name" })).toBeVisible()
  await expect(page.getByRole("menuitem", { name: "Copy ID" })).toBeVisible()
  await page.getByRole("menuitem", { name: "Tag…" }).click()
  const tag = page.getByRole("dialog", { name: "Tag nginx:1.27-alpine" })
  const field = tag.getByRole("textbox", { name: "New tag" })
  await expect(field).toHaveValue("nginx:1.27-alpine-previous")
  // Enter twice before the first answer names it once.
  await page.route("**/api/v1/docker/images/*/tag", async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 400))
    return route.fallback()
  })
  await field.press("Enter")
  await field.press("Enter")
  await expect(tag).toHaveCount(0)
  expect(mocks.calls.filter((call) => call.includes("/tag "))).toEqual([
    `POST /docker/images/sha256%3A${NGINX}/tag {"tag":"nginx:1.27-alpine-previous"}`,
  ])

  // Nothing runs mongo:7: it can be removed, from the menu or from the empty view.
  await page.goto(`/docker/images/${MONGO}?tab=containers`)
  await expect(page.getByText("No container uses this image")).toBeVisible()
  await page.getByRole("button", { name: "Remove" }).click()
  await page
    .getByRole("dialog", { name: "Delete image" })
    .getByRole("button", { name: "Delete" })
    .click()
  await expect
    .poll(() => mocks.calls.join("\n"))
    .toContain(`DELETE /docker/images/sha256%3A${MONGO}`)
  // The image is gone, so the page is too.
  await expect(page).toHaveURL(/\/docker\/images$/)
  await expect(page.getByRole("row", { name: /mongo:7/ })).toHaveCount(0)
})

test("an image with two names is removed whole, and only after reading that nothing uses it", async ({
  page,
}) => {
  const mocks = await mockImages(page)
  const detail = {
    ...imageDetail(IMAGES[0].id),
    id: `sha256:${hex("9d")}`,
    repoTags: ["tools/backup:2", "registry.example.com/tools/backup:2"],
    repoDigests: [],
    usedBy: [] as { id: string; name: string; state: string }[],
    localBuild: true,
  }
  await mockOne(page, detail)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/docker/images/${hex("9d")}`)

  // Built here: no registry to pull from, so the command is a second name.
  await expect(identity(page).getByRole("button", { name: "Tag…" })).toBeVisible()
  await expect(identity(page).getByRole("button", { name: "Pull", exact: true })).toHaveCount(0)
  await identity(page).getByRole("button", { name: "More actions for tools/backup:2" }).click()
  await page.getByRole("menuitem", { name: "Remove" }).click()
  const dialog = page.getByRole("dialog", { name: "Delete image" })
  await expect(dialog).toContainText("and its other name, registry.example.com/tools/backup:2")
  await dialog.getByRole("button", { name: "Delete" }).click()
  // Docker refuses an id two names point at unless it is forced.
  await expect
    .poll(() => mocks.calls.join("\n"))
    .toContain(`DELETE /docker/images/sha256%3A${hex("9d")}?force=true`)
})

test("a forced removal stops when a container has started using the image", async ({ page }) => {
  const mocks = await mockImages(page)
  const detail = {
    ...imageDetail(IMAGES[0].id),
    id: `sha256:${hex("9e")}`,
    repoTags: ["tools/backup:2", "tools/backup:previous"],
    repoDigests: [],
    usedBy: [] as { id: string; name: string; state: string }[],
    localBuild: true,
  }
  await mockOne(page, detail)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/docker/images/${hex("9e")}`)
  await expect(identity(page).getByText("tools/backup:2")).toBeVisible()
  await identity(page).getByRole("button", { name: "More actions for tools/backup:2" }).click()
  await page.getByRole("menuitem", { name: "Remove" }).click()

  // Between the page's reading and the press, somebody ran it.
  detail.usedBy = [{ id: "8888888888888888", name: "nightly", state: "exited" }]
  await page
    .getByRole("dialog", { name: "Delete image" })
    .getByRole("button", { name: "Delete" })
    .click()
  await expect(page.getByText("1 container uses this image now.")).toBeVisible()
  expect(mocks.calls.some((call) => call.startsWith("DELETE"))).toBe(false)
  await expect(identity(page).getByRole("button", { name: "used by 1 container" })).toBeVisible()
})

test("pulling a tag that moved follows the name to the new image", async ({ page }) => {
  await mockImages(page, { pull: "finish" })
  const moved = { ...imageDetail(IMAGES[0].id), id: `sha256:${hex("9f")}`, usedBy: [] }
  await mockOne(page, moved)
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/docker/images/${NGINX}`)
  await identity(page).getByRole("button", { name: "Pull", exact: true }).click()
  // From here the name answers with the new copy.
  await page.route("**/api/v1/docker/images/nginx%3A1.27-alpine", (route) => json(route, moved))
  const dialog = page.getByRole("dialog", { name: "Pull an image" })
  await dialog.getByRole("button", { name: "Pull", exact: true }).click()
  // The dialog keeps what Docker said until it is closed; then the page moves.
  await expect(dialog.getByRole("region", { name: "Pull progress" })).toContainText("already here")
  await expect(page).toHaveURL(new RegExp(`/docker/images/${NGINX}$`))
  await dialog.getByRole("button", { name: "Done" }).click()
  await expect(page).toHaveURL(new RegExp(`/docker/images/${hex("9f")}$`))
})

test("a reader without control sees the image and no verb it cannot use", async ({ page }) => {
  await mockImages(page, { capabilities: ["read"] })
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/docker/images/${MONGO}?tab=containers`)

  await expect(identity(page).getByText("mongo:7")).toBeVisible()
  await expect(page.getByRole("button", { name: "Pull", exact: true })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Tag…" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Remove" })).toHaveCount(0)
  await identity(page).getByRole("button", { name: "More actions for mongo:7" }).click()
  await expect(page.getByRole("menuitem")).toHaveText(["Copy name", "Copy ID"])
})

for (const width of [1280, 390]) {
  test(`the page has its shape before the image lands, and holds it, at ${width}px`, async ({
    page,
  }) => {
    await mockImages(page)
    const gate = () => {
      let release = () => {}
      const held = new Promise<void>((resolve) => (release = resolve))
      const hold = async (route: Route) => {
        await held
        return route.fallback()
      }
      return { hold, release: () => release() }
    }
    const list = gate()
    const image = gate()
    await page.route("**/api/v1/docker/images/", list.hold)
    await page.route(`**/api/v1/docker/images/sha256%3A${NGINX}`, image.hold)
    await page.setViewportSize({ width, height: 900 })
    await page.goto(`/docker/images/${NGINX}`)

    const line = identity(page)
    const strip = tabs(page)
    const picture = page.locator("[data-slot=wire-flow]")
    const boxes = async () => {
      await risen(page)
      return {
        line: await line.boundingBox(),
        picture: await picture.boundingBox(),
        strip: await strip.boundingBox(),
      }
    }
    await expect(line).toContainText("Reading the image…")
    await expect(picture.locator("[data-slot=skeleton]").first()).toBeVisible()
    await expect(strip.getByRole("tab")).toHaveCount(4)
    const before = await boxes()

    // The table's list knows the image's name, size and users: the line is
    // drawn from it while the picture and the view still wait.
    list.release()
    await expect(line.getByText("nginx:1.27-alpine")).toBeVisible()
    await expect(line.getByText("Update available")).toBeVisible()
    await expect(line.getByRole("button", { name: "used by 2 containers" })).toBeVisible()
    await expect(picture.locator("[data-slot=skeleton]").first()).toBeVisible()
    expect(await boxes()).toEqual(before)

    image.release()
    await expect(picture.locator("[data-slot=skeleton]")).toHaveCount(0)
    await expect(page.locator("[data-slot=image-layers]")).toBeVisible()
    expect(await boxes()).toEqual(before)
  })
}

test("the picture is one height whatever the image has", async ({ page }) => {
  test.setTimeout(90_000)
  await mockImages(page)
  await page.setViewportSize({ width: 1440, height: 1000 })
  const heights: number[] = []
  // Two containers and a registry; none and a registry; built here; no name at all.
  for (const id of [NGINX, MONGO, ACME, UNTAGGED]) {
    await page.goto(`/docker/images/${id}`)
    const picture = page.locator("[data-slot=wire-flow]")
    await expect(picture.locator("[data-slot=skeleton]")).toHaveCount(0)
    await risen(page)
    heights.push((await picture.boundingBox())!.height)
  }
  expect(new Set(heights).size).toBe(1)
  // Built here and untagged say so where the registry would be.
  await page.goto(`/docker/images/${ACME}`)
  await expect(page.getByRole("region", { name: "Comes from" })).toContainText("This server")
  await expect(identity(page).getByText("Built here")).toBeVisible()
  // A layer with no name is called by its id, and says what it is as its state.
  await page.goto(`/docker/images/${UNTAGGED}`)
  await expect(identity(page).getByText("cdcdcdcdcdcd", { exact: true })).toBeVisible()
  await expect(identity(page).getByText("Untagged", { exact: true })).toBeVisible()
  await expect(page.getByText("No container uses it")).toBeVisible()
})

test("an image that is not there says so, with the way back", async ({ page }) => {
  await mockImages(page)
  await page.route(`**/api/v1/docker/images/sha256%3A${hex("00")}`, (route) =>
    json(
      route,
      { error: { code: "not_found", message: "Docker does not have that object." } },
      404,
    ),
  )
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.goto(`/docker/images/${hex("00")}`)
  await expect(page.getByText("That image is not on this server")).toBeVisible()
  await expect(page.getByText("Docker does not have that object")).toHaveCount(0)
  await page.getByRole("link", { name: "Back to the images" }).click()
  await expect(page).toHaveURL(/\/docker\/images$/)
})

test("on a phone the image is one pane: no rail, nothing cut off or scrolled sideways", async ({
  page,
}) => {
  test.setTimeout(90_000)
  await mockImages(page)
  await page.setViewportSize({ width: 390, height: 844 })
  for (const tab of ["layers", "containers", "history", "configuration"]) {
    await page.goto(`/docker/images/${NGINX}?tab=${tab}`)
    await expect(identity(page).getByText("nginx:1.27-alpine")).toBeVisible()
    await expect(rail(page)).toBeHidden()
    await expect(page.getByRole("tab", { selected: true })).toBeVisible()
    await page.waitForLoadState("networkidle")
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    )
    expect(overflow, `${tab} overflows`).toBeLessThanOrEqual(1)
  }
  // The way back and the walk are on the page.
  await expect(page.getByRole("link", { name: "Images" }).first()).toBeVisible()
  await expect(page.getByRole("button", { name: "Next image" })).toBeVisible()
  // The history keeps its step, its instruction and what it added.
  await page.goto(`/docker/images/${NGINX}?tab=history`)
  await expect(page.locator("[data-slot=image-history]").getByRole("columnheader")).toHaveCount(3)
})

for (const width of [768, 1024, 1280, 1720]) {
  test(`an image's page does not scroll sideways at ${width}px`, async ({ page }) => {
    await mockImages(page)
    await page.setViewportSize({ width, height: 900 })
    await page.goto(`/docker/images/${hex("bc")}?tab=history`)
    await expect(identity(page).getByText("n8nio/n8n:1.64.3")).toBeVisible()
    await page.waitForLoadState("networkidle")
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    )
    expect(overflow).toBeLessThanOrEqual(1)
  })
}
