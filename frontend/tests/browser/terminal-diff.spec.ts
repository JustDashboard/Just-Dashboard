import { expect, test } from "@playwright/test"
import { mockWorkbench } from "./workbench-fixture"

/**
 * The terminal's companion column is Files and Diff: the second tab is the
 * work in the repository the shell is in, read and nothing else, with the Git
 * page one link away. And every session and window says what it is running,
 * as that program's own mark.
 */

test("a browser that had the Git tab open lands on the diff, and the diff is all it is", async ({
  page,
}) => {
  // The old tab's name, as a browser that used it last has it stored.
  await mockWorkbench(page, "git")
  await page.goto("/terminal")
  const diff = page.getByRole("button", { name: /^Diff/ })
  await expect(diff).toHaveAttribute("aria-current", "page")
  await expect(page.getByRole("button", { name: /^Git/ })).toHaveCount(0)

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

  // Nothing here operates on the repository; the Git page does.
  await expect(page.getByRole("button", { name: /Commit/ })).toHaveCount(0)
  await expect(page.getByRole("link", { name: "Open in Git" })).toHaveAttribute(
    "href",
    `/git?repo=${encodeURIComponent("/home/ubuntu/Just-Dashboard")}`,
  )
})

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
