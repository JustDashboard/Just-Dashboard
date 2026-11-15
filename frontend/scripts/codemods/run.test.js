import { describe, expect, test } from "bun:test"
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"
import { changedLines, TRANSFORMS, unmet } from "./run.ts"
import { cli } from "./test-kit.js"

describe("run.ts", () => {
  test("every transform has a fixture pair and a test beside it", () => {
    for (const t of TRANSFORMS) {
      const here = import.meta.dir
      expect(readFileSync(join(here, "fixtures", t.name, "before.tsx.fixture"), "utf8")).not.toBe(
        "",
      )
      expect(readFileSync(join(here, "fixtures", t.name, "after.tsx.fixture"), "utf8")).not.toBe("")
      expect(readFileSync(join(here, `${t.name}.test.js`), "utf8")).toContain(
        `describe("${t.name}"`,
      )
    }
  })

  test("the README lists every transform in the order all runs them", () => {
    const readme = readFileSync(join(import.meta.dir, "README.md"), "utf8")
    const order = TRANSFORMS.map((t) => readme.indexOf(`| \`${t.name}\``))
    expect(order.every((at) => at >= 0)).toBe(true)
    expect([...order].sort((a, b) => a - b)).toEqual(order)
  })

  test("a dry run shows each changed line, not the stretch between the first and the last", () => {
    const before = ["a", "b z-10", "c", "d", "e", "f z-20", "g"].join("\n")
    const after = ["a", "b z-raised", "c", "d", "e", "f z-sticky", "new", "g"].join("\n")
    expect(changedLines(before, after).split("\n")).toEqual([
      "@@ line 2",
      "- b z-10",
      "+ b z-raised",
      "@@ line 6",
      "- f z-20",
      "+ f z-sticky",
      "+ new",
    ])
  })

  test("paths are required, and an unknown transform or flag is refused", () => {
    expect(cli(["radius"]).status).toBe(2)
    expect(cli(["nonesuch", "src"]).status).toBe(2)
    expect(cli(["radius", "src", "--nonesuch"]).status).toBe(2)
  })

  test("a guard names what the transform waits for", () => {
    const why = unmet({
      name: "x",
      requires: [{ file: "package.json", pattern: /^nothing like this$/, why: "a thing lands" }],
    })
    expect(why).toBe("x waits until a thing lands: package.json does not have it yet")
    expect(
      unmet({ name: "y", requires: [{ file: "package.json", pattern: /"name"/, why: "" }] }),
    ).toBeUndefined()
  })

  test("all chains the transforms on one file, and never reads its own folder", () => {
    const top = mkdtempSync(join(tmpdir(), "codemod-all-"))
    try {
      const path = join(top, "src/a.tsx")
      mkdirSync(dirname(path), { recursive: true })
      writeFileSync(
        path,
        'export const a = <div className="z-10 rounded-[4px] text-muted-foreground/60" />\n',
      )
      const out = cli(
        [
          "all",
          "src",
          "--no-guard",
          "--no-format",
          "--moves",
          join(import.meta.dir, "fixtures/imports/moves.json"),
        ],
        top,
      )
      expect(out.status).toBe(0)
      expect(readFileSync(path, "utf8")).toBe(
        'export const a = <div className="z-raised rounded-sm text-faint" />\n',
      )
      const self = cli(
        [
          "all",
          import.meta.dir,
          "--check",
          "--no-guard",
          "--moves",
          join(import.meta.dir, "fixtures/imports/moves.json"),
        ],
        top,
      )
      expect(self.status).toBe(0)
    } finally {
      rmSync(top, { recursive: true, force: true })
    }
  })

  test("--strict fails a check on findings left for review", () => {
    const top = mkdtempSync(join(tmpdir(), "codemod-strict-"))
    try {
      writeFileSync(join(top, "a.tsx"), 'export const a = <div className="z-[3]" />\n')
      expect(cli(["z-layers", "a.tsx", "--check", "--no-guard"], top).status).toBe(0)
      expect(cli(["z-layers", "a.tsx", "--check", "--strict", "--no-guard"], top).status).toBe(1)
    } finally {
      rmSync(top, { recursive: true, force: true })
    }
  })
})
