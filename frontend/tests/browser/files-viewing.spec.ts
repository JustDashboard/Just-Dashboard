import { expect, test, type Page } from "@playwright/test"
import { home, mockViewing, openFolder, png, text } from "./files-viewing-fixture"

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
  await expect(rendered.getByRole("img", { name: "done" })).toBeVisible()
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
  await expect(inspector(page).getByRole("heading", { name: "Setup" })).toBeVisible()
  await expect(inspector(page).getByRole("heading", { name: "setup.md" })).toBeVisible()

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
    "Only the first 3 rows of 12 MB are shown.",
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
