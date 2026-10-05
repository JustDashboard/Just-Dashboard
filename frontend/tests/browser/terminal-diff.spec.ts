import { mkdir } from "node:fs/promises"
import { join } from "node:path"
import { expect, test, type Page } from "@playwright/test"
import { mockWorkbench } from "./workbench-fixture"

/**
 * The terminal's companion column is Files and Git: the second tab is the
 * repository the shell is in — its diff first, then switching branch and
 * checking out a pull request — with the Git page one link away. And every
 * session and window says what it is running, as that program's own mark.
 */

test("a browser that had the Diff tab open lands on Git, which opens on the diff", async ({
  page,
}) => {
  // The old tab's name, as a browser that used it last has it stored.
  await mockWorkbench(page, "diff")
  await page.goto("/terminal")
  const git = page.getByRole("button", { name: /^Git/ })
  await expect(git).toHaveAttribute("aria-current", "page")
  await expect(page.getByRole("button", { name: /^Diff/ })).toHaveCount(0)
  const views = page.getByRole("group", { name: "Git view" })
  await expect(views.getByRole("button", { name: /^Changes/ })).toHaveAttribute(
    "aria-pressed",
    "true",
  )

  const files = page.getByRole("list", { name: "Changed files" })
  await expect(files.getByRole("listitem")).toHaveCount(4)
  await expect(page.getByText("4 files", { exact: true })).toBeVisible()
  await expect(page.getByText("+7", { exact: true })).toBeVisible()
  // Staged, unstaged, untracked and deleted all read as the work.
  await expect(files.getByText("+export function JobCard() {")).toBeVisible()
  await expect(files.getByText('+  git: "git.svg",')).toBeVisible()
  await expect(files.getByText("D", { exact: true })).toHaveClass(/text-destructive/)

  // A file folds away and back.
  const header = files.getByRole("button", { name: /product-logo\.tsx/ })
  await header.click()
  await expect(header).toHaveAttribute("aria-expanded", "false")
  await expect(files.getByText('+  git: "git.svg",')).toHaveCount(0)

  // Staging and committing stay on the Git page.
  await expect(page.getByRole("button", { name: /Commit/ })).toHaveCount(0)
  await expect(page.getByRole("link", { name: "Open in Git" })).toHaveAttribute(
    "href",
    `/git?repo=${encodeURIComponent("/home/ubuntu/Just-Dashboard")}`,
  )
})

test("the Files and Git tabs split the column's strip between them", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockWorkbench(page, "files")
  await page.goto("/terminal")
  const files = page.getByRole("button", { name: "Files", exact: true })
  const git = page.getByRole("button", { name: /^Git/ })
  await expect(files).toHaveAttribute("aria-current", "page")
  const strip = await files.locator("xpath=..").boundingBox()
  const left = await files.boundingBox()
  const right = await git.boundingBox()
  expect(strip && left && right).toBeTruthy()
  expect(Math.abs(left!.width - right!.width)).toBeLessThanOrEqual(1)
  expect(left!.width + right!.width).toBeGreaterThanOrEqual(strip!.width - 2)
})

test("window tabs that do not fit scroll behind chevrons and edge fades, with no scrollbar", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockWorkbench(page, "files")
  await page.route("**/api/v1/terminal/s1/windows", (route) =>
    route.fulfill({
      json: Array.from({ length: 9 }, (_, index) => ({
        id: `many-${index}`,
        name: `window ${index}`,
        title: `window ${index}`,
        index,
        cwd: "/home/ubuntu/Just-Dashboard",
      })),
    }),
  )
  await page.goto("/terminal")
  const strip = page.getByLabel("Terminal windows")
  await expect(strip.locator("[data-window]")).toHaveCount(9)
  expect(await strip.evaluate((el) => getComputedStyle(el).scrollbarWidth)).toBe("none")
  // Tabs shrink to a readable width before the strip scrolls.
  const tab = await strip.locator("[data-window]").first().boundingBox()
  expect(tab!.width).toBeGreaterThanOrEqual(127)

  const left = page.getByRole("button", { name: "Scroll tabs left" })
  const right = page.getByRole("button", { name: "Scroll tabs right" })
  await expect(strip).toHaveAttribute("data-overflow-end", "true")
  await expect(left).toBeDisabled()
  await expect(right).toBeEnabled()
  // New window stays in reach rather than scrolling away with the tabs.
  await expect(page.getByRole("button", { name: "New window", exact: true })).toBeInViewport()

  await right.click()
  await expect(strip).toHaveAttribute("data-overflow-start", "true")
  await expect(left).toBeEnabled()
  const scrolled = await strip.evaluate((el) => el.scrollLeft)
  expect(scrolled).toBeGreaterThan(0)

  // A vertical wheel scrolls the strip sideways.
  await strip.hover()
  await page.mouse.wheel(0, -2000)
  await expect.poll(() => strip.evaluate((el) => el.scrollLeft)).toBe(0)
  await expect(left).toBeDisabled()
})

test("the branch is switched from the Git tab", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  const { mutations } = await mockWorkbench(page, "git")
  await page.goto("/terminal")
  await page.getByRole("button", { name: "On patch/0.7.0, switch branch" }).click()
  const views = page.getByRole("group", { name: "Git view" })
  await expect(views.getByRole("button", { name: "Branches" })).toHaveAttribute(
    "aria-pressed",
    "true",
  )
  const local = page.getByRole("list", { name: "Local branches" })
  await expect(local.locator("[data-current]")).toHaveAttribute("data-branch", "patch/0.7.0")
  // The current branch, and one another worktree holds, offer no switch.
  await expect(
    local.locator('[data-branch="patch/0.7.0"]').getByRole("button", { name: "Switch" }),
  ).toHaveCount(0)
  await expect(local.locator('[data-branch="feat/backup-cards"]')).toContainText(
    "checked out in /home/ubuntu/Just-Dashboard-backups",
  )
  await expect(
    local.locator('[data-branch="feat/backup-cards"]').getByRole("button", { name: "Switch" }),
  ).toHaveCount(0)
  await toolsEvidence(page, "terminal-git-branches.png")

  await local.locator('[data-branch="main"]').getByRole("button", { name: "Switch" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path === "/git/checkout")?.body)
    .toEqual({
      ref: "main",
    })
  await expect(page.getByText("Switched to main")).toBeVisible()

  // A remote branch with no local one is checked out as a tracking branch;
  // one that already has a local branch is reached through it.
  const remote = page.getByRole("list", { name: "Remote branches" })
  await expect(remote.locator("[data-branch]")).toHaveCount(1)
  await remote.getByRole("button", { name: "Check out" }).click()
  await expect
    .poll(() => mutations.filter((m) => m.path === "/git/checkout").at(-1)?.body)
    .toEqual({ ref: "origin/fix/terminal-paste", local: "fix/terminal-paste" })
})

test("an open pull request is checked out from the Git tab to try it", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  const { mutations } = await mockWorkbench(page, "git")
  await page.goto("/terminal")
  const chip = page.getByRole("group", { name: "Git view" }).getByRole("button", {
    name: /^Pull requests/,
  })
  await expect(chip).toContainText("3")
  await chip.click()
  const list = page.getByRole("list", { name: "Open pull requests" })
  await expect(list.locator("[data-pull]")).toHaveCount(3)
  // The pull request whose branch the shell is on says so instead of offering itself.
  const current = list.locator('[data-pull="146"]')
  await expect(current).toContainText("checked out")
  await expect(current.getByRole("button", { name: "Check out" })).toHaveCount(0)
  await expect(list.locator('[data-pull="141"]')).toContainText("draft")
  await expect(
    list.locator('[data-pull="141"]').getByRole("img", { name: "Checks failed" }),
  ).toBeVisible()
  await toolsEvidence(page, "terminal-git-pull-requests.png")

  await list.locator('[data-pull="144"]').getByRole("button", { name: "Check out" }).click()
  await expect
    .poll(() => mutations.find((m) => m.path.endsWith("/checkout"))?.path)
    .toBe("/git/github/pulls/144/checkout")
  expect(mutations.find((m) => m.path.endsWith("/checkout"))?.query).toBe(
    `path=${encodeURIComponent("/home/ubuntu/Just-Dashboard")}`,
  )
  await expect(page.getByText("Checked out #144")).toBeVisible()
})

test("an open file offers Save only once it has been changed, floating at its foot", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  const { mutations } = await mockWorkbench(page, "files")
  await page.goto("/terminal")
  await page
    .getByRole("button", { name: /README\.md/ })
    .first()
    .click()
  const editor = page.locator(".monaco-editor").last()
  await expect(editor).toBeVisible({ timeout: 15_000 })
  await expect(page.getByRole("button", { name: "Save" })).toHaveCount(0)

  await editor.click()
  await page.keyboard.press("Control+End")
  // One edit rather than a keystroke at a time: per-key typing races the
  // controlled editor's value on a loaded machine, which is not this test.
  await page.keyboard.insertText("Edited from the terminal.")
  const save = page.getByRole("button", { name: "Save" })
  await expect(save).toBeVisible()
  await expect(page.getByText("Unsaved changes")).toBeVisible()
  const bar = await save.boundingBox()
  const body = await editor.boundingBox()
  // At the foot of the text, not in the header above it.
  expect(bar!.y).toBeGreaterThan(body!.y + body!.height / 2)
  await toolsEvidence(page, "terminal-file-floating-save.png")

  await save.click()
  await expect
    .poll(
      () =>
        (mutations.find((m) => m.path === "/files/write")?.body as { content?: string })?.content,
    )
    .toContain("Edited from the terminal.")
  await expect(page.getByRole("button", { name: "Save" })).toHaveCount(0)
})

async function toolsEvidence(page: Page, name: string) {
  const dir = process.env.JD_TERMINAL_EVIDENCE
  if (!dir) return
  await mkdir(dir, { recursive: true })
  await page.screenshot({ path: join(dir, name) })
}

test("each session and window is drawn as the program it is running", async ({ page }) => {
  await mockWorkbench(page, "files")
  await page.goto("/terminal")
  const rail = page.locator("[data-session]")
  await expect(
    rail.filter({ hasText: "claude" }).locator('img[src="/logos/claude.svg"]'),
  ).toBeVisible()
  await expect(
    rail.filter({ hasText: "node" }).locator('img[src="/logos/nodejs.svg"]'),
  ).toBeVisible()
  await expect(
    rail.filter({ hasText: "psql" }).locator('img[src="/logos/postgresql.svg"]'),
  ).toBeVisible()
  // A shell at its prompt is drawn as a terminal, as is a program with no mark of its own.
  await expect(
    rail.filter({ hasText: "bash" }).locator('img[src="/logos/terminal.svg"]'),
  ).toBeVisible()
  const tabs = page.getByLabel("Terminal windows")
  await expect(tabs.locator('img[src="/logos/neovim.svg"]')).toBeVisible()
  await expect(tabs.locator('img[src="/logos/terminal.svg"]')).toHaveCount(1)
})

test("the rail, emulator and companion strips end on one rule, and only the emulator's strip toggles the panels", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1440, height: 900 })
  await mockWorkbench(page, "files")
  await page.goto("/terminal")
  const hideRail = page.getByRole("button", { name: "Hide the sessions rail" })
  const hideTools = page.getByRole("button", { name: "Hide files & git" })
  await expect(hideRail).toBeVisible()
  await expect(hideTools).toBeVisible()

  const bottoms = await page.evaluate(() => {
    const bottom = (el: Element | null | undefined) => el?.getBoundingClientRect().bottom
    const rail = document.querySelector('[aria-label="Terminal sessions"] [data-slot=pane-header]')
    const emulator = document
      .querySelector('[aria-label="Hide the sessions rail"]')
      ?.closest(".border-b")
    const tools = [...document.querySelectorAll("[data-slot=pane-header]")].find((el) =>
      el.textContent?.startsWith("Files"),
    )
    return [bottom(rail), bottom(emulator), bottom(tools)]
  })
  expect(bottoms[0]).toBeDefined()
  expect(new Set(bottoms).size).toBe(1)

  // The companion column carries no second copy of its own toggle.
  await expect(page.getByRole("button", { name: "Hide this panel" })).toHaveCount(0)
  await hideTools.click()
  await expect(page.getByRole("button", { name: "Show files & git" })).toBeVisible()
})
