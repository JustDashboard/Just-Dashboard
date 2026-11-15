import { afterEach, describe, expect, test } from "bun:test"
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"
import { format, mentionRules, rewriteMentions, run } from "./move-files.ts"

const made = []
afterEach(() => {
  for (const dir of made.splice(0)) rmSync(dir, { recursive: true, force: true })
})

/** A frontend/ with the given files, beside a docs/ and a backend/ the way the repository has them. */
function tree(files) {
  const top = mkdtempSync(join(tmpdir(), "move-files-"))
  made.push(top)
  for (const [path, text] of Object.entries(files)) {
    mkdirSync(dirname(join(top, path)), { recursive: true })
    writeFileSync(join(top, path), text)
  }
  const root = join(top, "frontend")
  const go = (moves, extra = {}) =>
    run({ root, moves, docs: [join(top, "docs")], git: false, log: () => {}, ...extra })
  return { top, root, go, read: (path) => readFileSync(join(top, path), "utf8") }
}

const MOVES = {
  files: [
    { from: "components/deploy/wire.tsx", to: "components/wire/wire.tsx" },
    { from: "lib/network-*", to: "lib/network/*" },
  ],
}

const FILES = {
  "backend/testdata/cases.json": "{}",
  "frontend/src/lib/network-ipam.ts": "export const ipam = 1\n",
  "frontend/src/lib/network-ipam.test.js": [
    'import { ipam } from "./network-ipam"',
    'import { cn } from "../lib/utils"',
    'const cases = join(import.meta.dir, "../../../backend/testdata/cases.json")',
    "",
  ].join("\n"),
  "frontend/src/lib/utils.ts": "export const cn = 1\n",
  "frontend/src/lib/network-flows.ts": "export const flows = 1\n",
  "frontend/src/lib/network-flows.test.js":
    'const load = () => import(`./network-flows?copy=${n++}`)\nconst other = import("./network-ipam?raw")\n',
  "frontend/src/components/deploy/wire.tsx": "export function WireMark() {}\n",
  "frontend/src/components/deploy/runtime.tsx": [
    'import { WireMark } from "./wire"',
    'import { ipam } from "@/lib/network-ipam"',
    "// The marks are deploy/wire.tsx's; see `components/deploy/wire.tsx`.",
    'const Lazy = () => import("@/components/deploy/wire")',
    'type T = typeof import("../../lib/network-ipam")',
    "",
  ].join("\n"),
  "frontend/src/app/page.tsx": 'import { WireMark } from "@/components/deploy/wire"\n',
  "frontend/tests/browser/ipam.spec.ts": 'import type { Ipam } from "../../src/lib/network-ipam"\n',
  "docs/internal/notes.md":
    "See `frontend/src/components/deploy/wire.tsx:27`, `@/lib/network-ipam` and `lib/network-*`.\nThe old `lib/network-format.ts` is gone.\n",
  "docs/audits/2026-11-13/map.md": "Built on `components/deploy/wire.tsx`.\n",
}

describe("moving files", () => {
  test("rewrites @/ imports, relative imports and the browser tests' ../../src/lib imports", () => {
    const { go, read, top } = tree(FILES)
    const result = go(MOVES)
    expect(result.moved).toEqual([
      ["src/components/deploy/wire.tsx", "src/components/wire/wire.tsx"],
      ["src/lib/network-flows.test.js", "src/lib/network/flows.test.js"],
      ["src/lib/network-flows.ts", "src/lib/network/flows.ts"],
      ["src/lib/network-ipam.test.js", "src/lib/network/ipam.test.js"],
      ["src/lib/network-ipam.ts", "src/lib/network/ipam.ts"],
    ])
    expect(existsSync(join(top, "frontend/src/components/deploy/wire.tsx"))).toBe(false)
    expect(read("frontend/src/components/wire/wire.tsx")).toBe("export function WireMark() {}\n")
    // A sibling import that now climbs out of its folder is written from the root.
    expect(read("frontend/src/components/deploy/runtime.tsx")).toBe(
      [
        'import { WireMark } from "@/components/wire/wire"',
        'import { ipam } from "@/lib/network/ipam"',
        "// The marks are components/wire/wire.tsx's; see `components/wire/wire.tsx`.",
        'const Lazy = () => import("@/components/wire/wire")',
        'type T = typeof import("../../lib/network/ipam")',
        "",
      ].join("\n"),
    )
    expect(read("frontend/src/app/page.tsx")).toBe(
      'import { WireMark } from "@/components/wire/wire"\n',
    )
    expect(read("frontend/tests/browser/ipam.spec.ts")).toBe(
      'import type { Ipam } from "../../src/lib/network/ipam"\n',
    )
    // A fresh copy of a module, asked for with a query, in a template or not.
    expect(read("frontend/src/lib/network/flows.test.js")).toBe(
      'const load = () => import(`./flows?copy=${n++}`)\nconst other = import("./ipam?raw")\n',
    )
    // The moved test's own imports and the path it reads from disk keep pointing at the same files.
    expect(read("frontend/src/lib/network/ipam.test.js")).toBe(
      [
        'import { ipam } from "./ipam"',
        'import { cn } from "../utils"',
        'const cases = join(import.meta.dir, "../../../../backend/testdata/cases.json")',
        "",
      ].join("\n"),
    )
    expect(read("docs/internal/notes.md")).toBe(
      // A module that is gone keeps the name it had.
      "See `frontend/src/components/wire/wire.tsx:27`, `@/lib/network/ipam` and `lib/network/*`.\nThe old `lib/network-format.ts` is gone.\n",
    )
    // A dated audit is a record of the tree it read.
    expect(read("docs/audits/2026-11-13/map.md")).toBe("Built on `components/deploy/wire.tsx`.\n")
  })

  test("changes nothing the second time, and points an import written later at the new path", () => {
    const { go, read, top } = tree(FILES)
    go(MOVES)
    const again = go(MOVES)
    expect(again.moved).toEqual([])
    expect(again.edited).toEqual([])
    expect(again.docs).toEqual([])

    writeFileSync(
      join(top, "frontend/src/app/late.tsx"),
      'import { WireMark } from "../components/deploy/wire"\nimport { ipam } from "@/lib/network-ipam"\n',
    )
    const late = go(MOVES)
    expect(late.edited).toEqual([{ file: "src/app/late.tsx", edits: 2 }])
    expect(read("frontend/src/app/late.tsx")).toBe(
      'import { WireMark } from "../components/wire/wire"\nimport { ipam } from "@/lib/network/ipam"\n',
    )
  })

  test("a dry run lists the work and touches nothing", () => {
    const { go, read } = tree(FILES)
    const lines = []
    const result = go(MOVES, { dryRun: true, log: (line) => lines.push(line) })
    expect(result.moved.length).toBe(5)
    expect(lines).toContain(
      "would move  src/components/deploy/wire.tsx -> src/components/wire/wire.tsx",
    )
    expect(lines).toContain("would edit  src/app/page.tsx (1)")
    expect(read("frontend/src/app/page.tsx")).toBe(
      'import { WireMark } from "@/components/deploy/wire"\n',
    )
    expect(read("frontend/src/components/deploy/wire.tsx")).toBe("export function WireMark() {}\n")
  })

  test("refuses to move a file onto one that exists", () => {
    const { go } = tree({ ...FILES, "frontend/src/components/wire/wire.tsx": "export {}\n" })
    expect(() => go(MOVES)).toThrow(/would move onto src\/components\/wire\/wire.tsx/)
  })
})

describe("moving exports", () => {
  const SPLIT = {
    files: [],
    exports: [
      { from: "components/page.tsx", to: "components/section.tsx", names: ["Section"] },
      { from: "components/page.tsx", to: "components/toolbar.tsx", names: ["Toolbar"] },
    ],
  }
  const BASE = {
    "frontend/src/components/page.tsx": "export function Page() {}\n",
    "frontend/src/components/section.tsx":
      "export function Section() {}\nexport function Other() {}\n",
    "frontend/src/components/toolbar.tsx": "export function Toolbar() {}\n",
  }

  test("splits a named import across the new files and joins an import the file already has", () => {
    const { go, read } = tree({
      ...BASE,
      "frontend/src/app/page.tsx": [
        '"use client"',
        "",
        'import { Page, Section, Toolbar as Bar, type Shape } from "@/components/page"',
        'import { Other } from "@/components/section"',
        "",
        "export default Page",
        "",
      ].join("\n"),
    })
    go(SPLIT)
    expect(read("frontend/src/app/page.tsx")).toBe(
      [
        '"use client"',
        "",
        'import { Page, type Shape } from "@/components/page"',
        'import { Toolbar as Bar } from "@/components/toolbar"',
        'import { Other, Section } from "@/components/section"',
        "",
        "export default Page",
        "",
      ].join("\n"),
    )
  })

  test("drops an import whose names all left, keeps a relative one relative, and splits a re-export", () => {
    const { go, read } = tree({
      ...BASE,
      "frontend/src/components/panel.tsx":
        'import { Section } from "./page"\nimport { Page } from "./page"\n',
      "frontend/src/components/kit/index.ts": 'export { Page, Section } from "@/components/page"\n',
    })
    go(SPLIT)
    expect(read("frontend/src/components/panel.tsx")).toBe(
      'import { Section } from "./section"\nimport { Page } from "./page"\n',
    )
    expect(read("frontend/src/components/kit/index.ts")).toBe(
      'export { Page } from "@/components/page"\nexport { Section } from "@/components/section"\n',
    )
  })

  test("breaks a split import that would run past the print width over lines", () => {
    const names = ["AlphaBravoCharlie", "DeltaEchoFoxtrot", "GolfHotelIndia", "JulietKiloLima"]
    const { go, read } = tree({
      ...BASE,
      "frontend/src/components/page.tsx": names.map((n) => `export const ${n} = 1\n`).join(""),
      "frontend/src/app/page.tsx": `import { Section, ${names.join(", ")}, Toolbar } from "@/components/page"\n`,
    })
    go({
      files: [],
      exports: [
        { from: "components/page.tsx", to: "components/section.tsx", names: ["Section", ...names] },
      ],
    })
    expect(read("frontend/src/app/page.tsx")).toBe(
      [
        'import { Toolbar } from "@/components/page"',
        "import {",
        "  Section,",
        ...names.map((n) => `  ${n},`),
        '} from "@/components/section"',
        "",
      ].join("\n"),
    )
  })
})

describe("path mentions", () => {
  const rules = mentionRules({
    files: [
      { from: "components/nav.ts", to: "components/shell/nav.ts" },
      { from: "components/deploy/settings/segments.tsx", to: "components/segments.tsx" },
    ],
  })
  const rewrite = (text) => rewriteMentions(text, rules).text

  test("rewrites full paths, @/ paths and paths written from components/", () => {
    expect(rewrite("`components/nav.ts` and @/components/nav.")).toBe(
      "`components/shell/nav.ts` and @/components/shell/nav.",
    )
    expect(rewrite("`components/nav.test.js`")).toBe("`components/shell/nav.test.js`")
    expect(rewrite("(deploy/settings/segments.tsx:20)")).toBe("(components/segments.tsx:20)")
  })

  test("leaves basenames, longer names and other folders' files alone", () => {
    for (const text of [
      "nav.ts",
      "components/nav.tsx",
      "components/nav-scope.tsx",
      "src/components/navigation.ts",
      "lib/components/nav.ts",
      "settings/segments.tsx",
      "/deploy/settings/segments",
    ])
      expect(rewrite(text)).toBe(text)
  })
})

describe("printing", () => {
  test("an import that got shorter comes back to one line under the repository's Prettier", async () => {
    const { root, read, go } = tree({
      "frontend/.prettierrc": JSON.stringify({
        semi: false,
        printWidth: 100,
        trailingComma: "all",
      }),
      "frontend/src/components/deploy/settings/save-bar-words.ts": "export const appliesLine = 1\n",
      "frontend/src/app/page.tsx": [
        "import {",
        "  appliesLine,",
        "  pendingLine,",
        "  type SettingApplies,",
        '} from "@/components/deploy/settings/save-bar-words"',
        "",
      ].join("\n"),
    })
    const result = go({
      files: [
        {
          from: "components/deploy/settings/save-bar-words.ts",
          to: "components/settings/save-bar-words.ts",
        },
      ],
    })
    expect(
      await format(
        root,
        result.edited.map(({ file }) => file),
      ),
    ).toBe(1)
    expect(read("frontend/src/app/page.tsx")).toBe(
      'import { appliesLine, pendingLine, type SettingApplies } from "@/components/settings/save-bar-words"\n',
    )
  })
})
