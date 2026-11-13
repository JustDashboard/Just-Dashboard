import { expect, test } from "bun:test"
import { readFileSync, readdirSync } from "node:fs"
import { join, relative } from "node:path"

/**
 * The phone's bar was drawn twice by hand — the Git workspace's `BarTab` and
 * the assistant's `PaneBar` — and every other page with panels stacked them,
 * because there was nothing to reach for. There is one now
 * (`phone-panes.tsx`), and this keeps a third copy out: a strip along the
 * foot that clears the home indicator is the bar, and is drawn there.
 */
const SRC = join(import.meta.dir, "..")
const OWN = "components/phone-panes.tsx"
// A top rule over padding for the home indicator: the foot bar's signature.
const FOOT_BAR = /(?<![\w:-])border-t(?![\w-]).*pb-\[env\(safe-area-inset-bottom\)\]/

function sources(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) return sources(path)
    return /\.tsx$/.test(entry.name) ? [path] : []
  })
}

test("no page draws a bar at the foot of a phone by hand", () => {
  const found = []
  for (const path of sources(SRC)) {
    const name = relative(SRC, path)
    if (name === OWN) continue
    readFileSync(path, "utf8")
      .split("\n")
      .forEach((line, at) => {
        if (FOOT_BAR.test(line)) found.push(`${name}:${at + 1}`)
      })
  }
  expect(found).toEqual([])
})

test("the check reads the bar the way it was written twice", () => {
  expect(
    FOOT_BAR.test(
      'className="shrink-0 border-t border-hairline bg-surface-header pb-[env(safe-area-inset-bottom)] lg:hidden"',
    ),
  ).toBe(true)
  // The map's floating button keeps clear of the indicator and is not a bar.
  expect(
    FOOT_BAR.test('"absolute inset-x-0 bottom-[max(1rem,env(safe-area-inset-bottom))] flex"'),
  ).toBe(false)
  expect(FOOT_BAR.test(readFileSync(join(SRC, OWN), "utf8").replaceAll("\n", " "))).toBe(true)
})

test("the pages that are one pane at a time on a phone are on the shared bar", () => {
  for (const page of [
    "components/git/repo-workspace.tsx",
    "components/assistant/agent/workspace.tsx",
    "components/network/tools-panel.tsx",
    "app/(dashboard)/logs/page.tsx",
  ]) {
    const text = readFileSync(join(SRC, page), "utf8")
    expect(text, page).toContain('from "@/components/phone-panes"')
    expect(text, page).toContain("<PaneBar")
  }
})

test("a pane is hidden only below its break, by a class Tailwind can read", () => {
  // Read from the source: the names must be whole in the file for Tailwind
  // to emit them, which a test of the function's return value would not show.
  const text = readFileSync(join(SRC, OWN), "utf8")
  for (const name of ["max-md:hidden", "max-lg:hidden", "md:hidden", "lg:hidden"]) {
    expect(text).toContain(`"${name}"`)
  }
})
