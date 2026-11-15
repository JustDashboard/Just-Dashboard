import { describe, expect, test } from "bun:test"
import { readFileSync } from "node:fs"
import { join } from "node:path"
import { imports, table } from "./imports.ts"
import { expectCommandLine, expectFixture, lines } from "./test-kit.js"

const MOVES_FILE = join(import.meta.dir, "fixtures/imports/moves.json")
const moves = JSON.parse(readFileSync(MOVES_FILE, "utf8"))
const options = { moves, srcRoot: "/virtual/src" }

describe("imports", () => {
  test("moved files are re-pointed and split exports go to their new files", () => {
    const findings = expectFixture(imports, options, "/virtual/src/components/demo.tsx")
    expect(lines(findings, "review")).toEqual([
      '5 review import * as page from "@/components/page"',
    ])
    expect(findings.filter((f) => f.kind === "rewrite")).toHaveLength(7)
  })

  test("the table follows globs and index files, and leaves what did not move", () => {
    const t = table({
      files: [
        { from: "lib/tls-*", to: "lib/tls/*" },
        { from: "components/a/index.tsx", to: "components/b/index.tsx" },
      ],
    })
    expect(t.settle("lib/tls-chain")).toEqual({ path: "lib/tls/chain" })
    expect(t.settle("lib/tls-chain/deeper")).toBeUndefined()
    expect(t.settle("components/a")).toEqual({ path: "components/b", ext: ".tsx" })
    expect(t.settle("lib/utils")).toBeUndefined()
  })

  test("a relative import from a test outside src keeps its relative spelling", () => {
    const text = 'import { parse } from "../../src/lib/log-filter.ts"\n'
    const out = imports.apply(text, "/virtual/tests/browser/a.spec.ts", options)
    expect(out.text).toBe('import { parse } from "../../src/lib/log/filter.ts"\n')
  })

  test("--check fails on the old spelling, --dry-run writes nothing, a write fixes it", () => {
    expectCommandLine("imports", ["--moves", MOVES_FILE])
  })
})
