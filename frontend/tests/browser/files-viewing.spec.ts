import { expect, test, type Page } from "@playwright/test"
import { home, mockViewing, openFolder, png, text } from "./files-viewing-fixture"
import { risen } from "./risen"

/**
 * Text that reads as what it is, and media done properly, on the Files page.
 *
 *   Markdown is rendered in the inspector and the viewer, with a way back to
 *   the source; nothing in a file can become markup, run, or fetch from
 *   another site; a link to another file opens it in Files.
 *
 *   A CSV is a table with its numbers to the right, and says when only its
 *   first part is shown. One-line JSON is indented.
 */

const readme = [
  "# Deploying the site",
  "",
  "Read [the setup](docs/setup.md) first, or the [guides](guides/).",
  "",
  "- [x] build",
  "- [ ] ship",
  "",
  "| Host | Port |",
  "| ---- | ---: |",
  "| web  | 443  |",
  "",
  "```sh",
  "# start it",
  'echo "hello" && exit 0',
  "```",
  "",
  "![diagram](./diagram.png)",
  "",
  "![badge](https://img.example.test/badge.svg)",
  "",
].join("\n")

const hostile = [
  "# Hostile",
  "",
  "<script>window.__pwned = 'script'</script>",
  "",
  '<img src="x.png" onerror="window.__pwned = \'onerror\'">',
  "",
  "<a href=\"javascript:window.__pwned = 'link'\">click me</a>",
  "",
  "[also me](javascript:window.__pwned='md')",
  "",
  "[data](data:text/html,<script>window.__pwned='data'</script>)",
  "",
  '<iframe src="https://evil.example.test"></iframe>',
  "",
  "<div onmouseover=\"window.__pwned = 'hover'\">hover me</div>",
  "",
  "<style>body { display: none }</style>",
].join("\n")

const csv = [
  "name,city,visits,share",
  'Smith,"Paris, FR",1200,12.5%',
  "Jones,Leeds,87,0.9%",
  'Brown,"said ""hi""",3,0%',
  "Green,Oslo,9",
].join("\n")

const folders = {
  [home]: [
    { name: "docs", isDir: true },
    { name: "README.md", ...text(readme) },
    { name: "diagram.png", size: png.length, raw: { contentType: "image/png", body: png } },
    { name: "hostile.md", ...text(hostile) },
    { name: "visits.csv", ...text(csv + "\n") },
    { name: "big.csv", ...text(csv, { truncated: true, size: 12 << 20 }) },
    { name: "data.json", ...text('{"name":"site","ports":[80,443],"tls":{"on":true}}\n') },
  ],
  [`${home}/docs`]: [{ name: "setup.md", ...text("# Setup\n\nInstall it.\n") }],
}

const inspector = (page: Page) => page.locator("[data-files-inspector]")
const pick = (page: Page, name: string) =>
  page.locator(`[data-file-listing] [data-entry-path="${home}/${name}"]`).click()

test.beforeEach(async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
})

test("Markdown is rendered in the inspector, with its source a press away", async ({ page }) => {
  const mocked = await mockViewing(page, folders)
  await openFolder(page)
  await pick(page, "README.md")

  const rendered = inspector(page).locator("[data-rendered='markdown']")
  await expect(rendered.getByRole("heading", { name: "Deploying the site" })).toBeVisible()
  await expect(rendered.getByRole("img", { name: "done", exact: true })).toBeVisible()
  await expect(rendered.getByRole("img", { name: "not done" })).toBeVisible()
  await expect(rendered.getByRole("table")).toContainText("443")
  // The fenced block, coloured by the editor's grammar once it has loaded.
  const block = rendered.locator("figure", { hasText: "start it" })
  await expect(block).toContainText("sh")
  await expect(block.locator("[data-highlighted]")).toHaveCount(1, { timeout: 20_000 })

  // A picture beside the file goes through the raw route; one from another
  // site is named and never fetched.
  const diagram = rendered.getByRole("img", { name: "diagram" })
  await expect(diagram).toHaveAttribute(
    "src",
    /\/api\/v1\/files\/raw\?path=%2Fhome%2Foperator%2Fdiagram\.png/,
  )
  await expect(rendered).toContainText("Picture from img.example.test not loaded")
  expect(mocked.requests.some((url) => url.includes("example.test"))).toBe(false)

  // The source, and back; the choice is kept.
  await inspector(page).getByRole("button", { name: "Show the source" }).click()
  await expect(rendered).toHaveCount(0)
  await expect(inspector(page).getByText("# Deploying the site")).toBeVisible()
  await inspector(page).getByRole("button", { name: "Show it rendered" }).click()
  await expect(rendered.getByRole("heading", { name: "Deploying the site" })).toBeVisible()
})

test("a link to another file opens it in Files; a folder link enters the folder", async ({
  page,
}) => {
  await mockViewing(page, folders)
  await openFolder(page)
  await pick(page, "README.md")
  const link = inspector(page).getByRole("link", { name: "the setup" })
  // A middle click has somewhere to go: the page's own address for the file.
  await expect(link).toHaveAttribute(
    "href",
    `/files?path=${encodeURIComponent(`${home}/docs`)}&entry=${encodeURIComponent(`${home}/docs/setup.md`)}`,
  )
  await expect(link).not.toHaveAttribute("target", "_blank")
  await link.click()
  await expect(page).toHaveURL(new RegExp(`path=${encodeURIComponent(`${home}/docs`)}`))
  await expect(inspector(page).getByRole("heading", { name: "Setup", exact: true })).toBeVisible()
  await expect(
    inspector(page).getByRole("heading", { name: "setup.md", exact: true }),
  ).toBeVisible()

  await page.goBack()
  await pick(page, "README.md")
  await inspector(page).getByRole("link", { name: "guides" }).click()
  await expect(page).toHaveURL(new RegExp(`path=${encodeURIComponent(`${home}/guides`)}`))
})

test("a file full of script, handlers and javascript: links renders inert", async ({ page }) => {
  const mocked = await mockViewing(page, folders)
  const dialogs: string[] = []
  page.on("dialog", (dialog) => {
    dialogs.push(dialog.message())
    void dialog.dismiss()
  })
  await openFolder(page)
  await pick(page, "hostile.md")
  const rendered = inspector(page).locator("[data-rendered='markdown']")
  await expect(rendered.getByRole("heading", { name: "Hostile" })).toBeVisible()

  // The tags are words on the page, not elements in it.
  await expect(rendered).toContainText("<script>window.__pwned = 'script'</script>")
  await expect(rendered).toContainText("<style>body { display: none }</style>")
  for (const tag of ["script", "iframe", "style", "[onerror]", "[onmouseover]", "[onclick]"]) {
    await expect(rendered.locator(tag)).toHaveCount(0)
  }
  // No address on the page runs anything.
  const hrefs = await rendered
    .locator("a")
    .evaluateAll((links) => links.map((a) => a.getAttribute("href") ?? ""))
  for (const href of hrefs) expect(href).not.toMatch(/^\s*(javascript|data|vbscript):/i)
  await rendered.getByText("click me").click()
  await rendered.getByText("also me").click()
  await rendered.getByText("hover me").hover()
  await page.waitForTimeout(200)
  expect(await page.evaluate(() => (window as { __pwned?: string }).__pwned)).toBeUndefined()
  expect(dialogs).toEqual([])
  expect(mocked.requests.some((url) => url.includes("evil.example.test"))).toBe(false)
  // The page is still drawn: the <style> did not hide it.
  await expect(page.locator("body")).toBeVisible()
})

test("a CSV is a table with its numbers to the right, and says when it is cut", async ({
  page,
}) => {
  await mockViewing(page, folders)
  await openFolder(page)
  await pick(page, "visits.csv")
  const table = inspector(page).locator("[data-file-table] table")
  await expect(table.locator("thead th")).toHaveText(["name", "city", "visits", "share"])
  await expect(table.locator("tbody tr")).toHaveCount(4)
  await expect(table.locator("tbody tr").first().locator("td")).toHaveText([
    "Smith",
    "Paris, FR",
    "1200",
    "12.5%",
  ])
  await expect(table.locator("tbody tr").nth(2).locator("td").nth(1)).toHaveText('said "hi"')
  await expect(table.locator("tbody td").nth(2)).toHaveCSS("text-align", "right")
  await expect(table.locator("tbody td").nth(1)).toHaveCSS("text-align", "left")
  await expect(inspector(page)).toContainText("4 rows")
  await expect(inspector(page).locator("[data-truncated]")).toHaveCount(0)

  // Only the head of a large file: the row it stopped in is not offered.
  await pick(page, "big.csv")
  await expect(table.locator("tbody tr")).toHaveCount(3)
  await expect(inspector(page)).toContainText("First 3 rows")
  await expect(inspector(page).locator("[data-truncated]")).toContainText(
    "Only the first 3 rows of 12.0 MB are shown.",
  )
})

test("one-line JSON is indented", async ({ page }) => {
  await mockViewing(page, folders)
  await openFolder(page)
  await pick(page, "data.json")
  const rendered = inspector(page).locator("[data-rendered='json']")
  await expect(rendered).toContainText('"ports": [')
  await expect(rendered).toContainText('"on": true')
})

test("the viewer renders Markdown and draws a CSV as its table", async ({ page }) => {
  await mockViewing(page, folders)
  await openFolder(page)
  await pick(page, "README.md")
  await inspector(page).getByRole("button", { name: "View", exact: true }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("heading", { name: "Deploying the site" })).toBeVisible()
  await dialog.getByRole("button", { name: "Show the source" }).click()
  await expect(dialog.getByText("# Deploying the site")).toBeVisible()
  await dialog.getByRole("button", { name: "Show it rendered" }).click()
  await page.keyboard.press("Escape")

  await pick(page, "visits.csv")
  await inspector(page).getByRole("button", { name: "View", exact: true }).click()
  await expect(dialog.locator("[data-file-table] tbody tr")).toHaveCount(4)
})

/** One second of silence as a WAV: a track the browser really plays. */
function silence(seconds = 1): Buffer {
  const rate = 8000
  const samples = rate * seconds
  const b = Buffer.alloc(44 + samples)
  b.write("RIFF", 0)
  b.writeUInt32LE(36 + samples, 4)
  b.write("WAVEfmt ", 8)
  b.writeUInt32LE(16, 16)
  b.writeUInt16LE(1, 20)
  b.writeUInt16LE(1, 22)
  b.writeUInt32LE(rate, 24)
  b.writeUInt32LE(rate, 28)
  b.writeUInt16LE(1, 32)
  b.writeUInt16LE(8, 34)
  b.write("data", 36)
  b.writeUInt32LE(samples, 40)
  b.fill(128, 44)
  return b
}

const picture = { contentType: "image/png", body: png }
const media = {
  [home]: [
    { name: "music", isDir: true },
    { name: "photos", isDir: true },
    { name: "scan.jpg", size: 12 << 20, raw: picture },
    { name: "logo.webp", size: 2048, raw: picture },
    { name: "notes.txt", ...text("hello\n") },
    {
      name: "film.mkv",
      size: 4096,
      preview: { kind: "video", mime: "video/x-matroska" },
      raw: { contentType: "video/x-matroska", body: Buffer.from("not a video") },
    },
    // The listing's last row has no hairline under it, so it is never the one measured.
    { name: "zz-last.txt", ...text("last\n") },
  ],
  [`${home}/photos`]: ["a.jpg", "b.jpg", "c.png", "d.gif"].map((name) => ({
    name,
    size: 6 << 20,
    raw: picture,
  })),
  [`${home}/music`]: ["01 intro.wav", "02 song.wav", "03 outro.wav"].map((name) => ({
    name,
    size: 8044,
    raw: { contentType: "audio/wav", body: silence(60) },
  })),
}

const row = (page: Page, path: string) =>
  page.locator(`[data-file-listing] [data-entry-path="${path}"]`)

test("every picture lists as the server's small copy, and a row with one is one height", async ({
  page,
}) => {
  await mockViewing(page, media)
  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: 844 })
    await openFolder(page)
    // A 12 MB photograph is drawn, from its copy; the file itself is never fetched for it.
    const scan = row(page, `${home}/scan.jpg`).locator("img")
    await expect(scan).toHaveAttribute(
      "src",
      /\/api\/v1\/files\/thumbnail\?path=%2Fhome%2Foperator%2Fscan\.jpg&v=\d+/,
    )
    await expect(scan).toHaveJSProperty("complete", true)
    // A format the server does not decode is still the file, drawn small.
    await expect(row(page, `${home}/logo.webp`).locator("img")).toHaveAttribute(
      "src",
      /\/files\/raw\?/,
    )
    const withPicture = await row(page, `${home}/scan.jpg`).boundingBox()
    const without = await row(page, `${home}/notes.txt`).boundingBox()
    expect(Math.round(withPicture!.height)).toBe(Math.round(without!.height))
  }
})

test("a folder of pictures is a gallery: words, a strip, a slideshow", async ({ page }) => {
  await page.clock.install()
  await mockViewing(page, media)
  await openFolder(page, `${home}/photos`)
  await row(page, `${home}/photos/a.jpg`).locator("[data-file-name]").dblclick()
  const dialog = page.getByRole("dialog")
  await expect(dialog.locator("[data-gallery-position]")).toHaveText("Picture 1 of 4")
  await page.keyboard.press("ArrowRight")
  await expect(dialog.locator("[data-gallery-position]")).toHaveText("Picture 2 of 4")

  // The strip jumps, and says where the stage is.
  const strip = dialog.getByRole("toolbar", { name: "Files in this folder" })
  await expect(strip.getByRole("button")).toHaveCount(4)
  await strip.getByRole("button", { name: /^d\.gif/ }).click()
  await expect(dialog.locator("[data-gallery-position]")).toHaveText("Picture 4 of 4")
  await expect(strip.getByRole("button", { name: /^d\.gif/ })).toHaveAttribute(
    "aria-current",
    "true",
  )
  // Its pictures are the small copies.
  await expect(strip.locator("img").first()).toHaveAttribute("src", /\/files\/thumbnail\?/)

  // The slideshow walks on by itself and stops at the last picture.
  await strip.getByRole("button", { name: /^b\.jpg/ }).click()
  await dialog.getByRole("button", { name: "Slideshow" }).click()
  await page.clock.fastForward(4100)
  await expect(dialog.locator("[data-gallery-position]")).toHaveText("Picture 3 of 4")
  await page.clock.fastForward(4100)
  await expect(dialog.locator("[data-gallery-position]")).toHaveText("Picture 4 of 4")
  await page.clock.fastForward(4100)
  await expect(dialog.getByRole("button", { name: "Slideshow" })).toHaveAttribute(
    "aria-pressed",
    "false",
  )
})

test("audio plays as a queue that carries on across folders, on one line", async ({ page }) => {
  await mockViewing(page, media)
  await openFolder(page, `${home}/music`)
  // Read where the listing rests, not where its rise is taking it.
  await risen(page)
  const footer = page.locator("[data-slot='pane-footer']").first()
  const footerBefore = await footer.boundingBox()
  const firstRow = row(page, `${home}/music/01 intro.wav`)
  const rowBefore = await firstRow.boundingBox()

  await row(page, `${home}/music/02 song.wav`).locator("[data-file-name]").dblclick()
  const dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("button", { name: "Pause" })).toBeVisible()
  await page.keyboard.press("Escape")

  const player = page.locator("[data-files-player]")
  await expect(player).toContainText("02 song.wav")
  await expect(player).toContainText("2 of 3")
  // One line in the foot the listing already had: nothing moved.
  expect(Math.round((await footer.boundingBox())!.height)).toBe(Math.round(footerBefore!.height))
  expect((await firstRow.boundingBox())!.y).toBe(rowBefore!.y)

  // Another folder, walked to within the page: the music goes on.
  await firstRow.locator("[data-file-name]").focus()
  await page.keyboard.press("Alt+ArrowUp")
  await row(page, `${home}/photos`).locator("[data-file-name]").dblclick()
  await expect(row(page, `${home}/photos/a.jpg`)).toBeVisible()
  await expect(player).toContainText("02 song.wav")
  await player.getByRole("button", { name: "Previous track" }).click()
  await expect(player).toContainText("01 intro.wav")
  await player.getByRole("button", { name: "Next track" }).click()
  await expect(player).toContainText("02 song.wav")
  await player.getByRole("button", { name: "Stop playing" }).click()
  await expect(player).toHaveCount(0)
})

test("a video the browser cannot play says so, with Download", async ({ page }) => {
  await mockViewing(page, media)
  await openFolder(page)
  await row(page, `${home}/film.mkv`).locator("[data-file-name]").click()
  const pane = page.locator("[data-files-inspector] [data-cannot-play]")
  await expect(pane).toContainText("This browser cannot play this video (.mkv).")
  await expect(pane.getByRole("link", { name: "Download" })).toHaveAttribute(
    "href",
    /\/files\/download\?path=%2Fhome%2Foperator%2Ffilm\.mkv/,
  )
  await row(page, `${home}/film.mkv`).locator("[data-file-name]").dblclick()
  const stage = page.getByRole("dialog").locator("[data-cannot-play]")
  await expect(stage).toContainText("This browser cannot play this video (.mkv).")
  await expect(stage.getByRole("link", { name: "Download" })).toBeVisible()
})

test.describe("on a phone", () => {
  test.use({ hasTouch: true, viewport: { width: 390, height: 844 } })

  test("the viewer and its strip fit, a swipe steps, and the player is the foot", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await mockViewing(page, media)
    await openFolder(page, `${home}/photos`)
    await row(page, `${home}/photos/a.jpg`).locator("[data-file-name]").dblclick()
    const dialog = page.getByRole("dialog")
    const strip = dialog.locator("[data-gallery-strip]")
    await expect(strip).toBeVisible()
    const box = (await strip.boundingBox())!
    expect(box.x).toBeGreaterThanOrEqual(0)
    expect(box.x + box.width).toBeLessThanOrEqual(390)
    // A finger moving leftwards across the stage is the next picture.
    const stage = (await dialog.locator("img[alt='a.jpg']").boundingBox())!
    const y = stage.y + stage.height / 2
    await dialog.locator("img[alt='a.jpg']").dispatchEvent("pointerdown", {
      pointerType: "touch",
      clientX: 300,
      clientY: y,
      bubbles: true,
    })
    await dialog.locator("img[alt='a.jpg']").dispatchEvent("pointerup", {
      pointerType: "touch",
      clientX: 120,
      clientY: y + 5,
      bubbles: true,
    })
    await expect(dialog.locator("[data-gallery-position]")).toHaveText("Picture 2 of 4")
    await page.keyboard.press("Escape")

    await openFolder(page, `${home}/music`)
    const footer = page.locator("[data-slot='pane-footer']").first()
    const before = (await footer.boundingBox())!.height
    await row(page, `${home}/music/01 intro.wav`).locator("[data-file-name]").dblclick()
    await page.keyboard.press("Escape")
    const player = page.locator("[data-files-player]")
    await expect(player).toContainText("01 intro.wav")
    expect(Math.round((await footer.boundingBox())!.height)).toBe(Math.round(before))
    const line = (await player.boundingBox())!
    expect(line.x + line.width).toBeLessThanOrEqual(390)
  })
})
