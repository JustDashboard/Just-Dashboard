// The guardrail ids, each with a line it refuses and a line it lets through.
//
// eslint-rules/design-system.mjs refuses a value typed by hand where a token exists, and
// eslint-rules/retired-apis.mjs a pattern assembled by hand where a shared component owns it. A
// rule that refused nothing would pass `bun run lint` as happily as one that works, so each id is
// run here on a failing and a passing fixture.

import { describe, expect, test } from "bun:test"
import { Linter } from "eslint"
import tsParser from "@typescript-eslint/parser"
import designSystem, { classes } from "../eslint-rules/design-system.mjs"
import retiredApis, { RETIRED_IDS } from "../eslint-rules/retired-apis.mjs"

const linter = new Linter({ configType: "flat" })

/** The ids the two rules report on `code`, as a file at `filename`. */
function reported(code, filename = "src/components/example.tsx") {
  const messages = linter.verify(
    code,
    [
      {
        files: ["**/*.ts", "**/*.tsx"],
        languageOptions: { parser: tsParser, parserOptions: { ecmaFeatures: { jsx: true } } },
        plugins: { "design-system": designSystem, "retired-apis": retiredApis },
        rules: {
          "design-system/tokens": "error",
          "design-system/primitives": "error",
          "retired-apis/retired": "error",
        },
      },
    ],
    { filename },
  )
  for (const m of messages) if (m.fatal) throw new Error(m.message)
  return messages.map((m) => m.message.match(/^\[([\w-]+)\]/)?.[1] ?? m.message)
}

const jsx = (body) => `export function X() { return (${body}) }`

/** [id, refused, allowed, file of both] */
const TOKENS = [
  [
    "ink-opacity",
    jsx(`<span className="text-muted-foreground/60">a</span>`),
    jsx(`<svg className="text-muted-foreground/60" />`),
  ],
  [
    "ink-opacity",
    jsx(`<p className="text-foreground/85">a</p>`),
    jsx(`<p className="text-faint">a</p>`),
  ],
  [
    "colour-var",
    jsx(`<span className="text-[var(--tag-pink)]">a</span>`),
    jsx(`<span className="text-tag-pink [--x:var(--jd-row)]">a</span>`),
  ],
  [
    "colour-var",
    jsx(`<span className="bg-(--git-added)/15" />`),
    jsx(`<span className="bg-git-added/15" />`),
  ],
  ["z-layer", jsx(`<div className="z-50" />`), jsx(`<div className="z-overlay" />`)],
  [
    "radius-arbitrary",
    jsx(`<div className="rounded-[6px]" />`),
    jsx(`<div className="rounded-[50%]" />`),
  ],
  [
    "media-literal",
    `const q = useMediaQuery("(min-width: 1100px)")`,
    `const q = useBreakpoint("lg")`,
  ],
  [
    "media-literal",
    jsx(`<div className="min-[1600px]:grid-cols-2" />`),
    jsx(`<div className="xl:grid-cols-2" />`),
  ],
  [
    "container-arbitrary",
    jsx(`<div className="@max-[46rem]:hidden" />`),
    jsx(`<div className="@max-3xl:hidden" />`),
  ],
  [
    "animate-pulse-status",
    jsx(`<span className="size-1.5 animate-pulse rounded-full" />`),
    jsx(`<span className="animate-breathe" />`),
  ],
  ["vh-unit", jsx(`<div className="max-h-[45vh]" />`), jsx(`<div className="max-h-[45svh]" />`)],
  ["vh-unit", jsx(`<div className="min-h-screen" />`), jsx(`<div className="min-h-svh" />`)],
  [
    "focus-ring",
    jsx(`<div className="focus-within:ring-2" />`),
    jsx(`<div className="focus-ring-within" />`),
  ],
  [
    "focus-ring",
    jsx(`<button className="outline-none">a</button>`),
    jsx(`<div tabIndex={-1} className="outline-none" />`),
  ],
]

const RETIRED = [
  [
    "fact-dot",
    `import { FactDot } from "@/components/identity-header"`,
    `import { Fact, FactLine } from "@/components/fact-line"`,
  ],
  [
    "host-identity",
    `import { HostIdentity } from "@/components/identity-header"`,
    `import { IdentityHeader } from "@/components/identity-header"`,
  ],
  [
    "tab-strip",
    `import { tabClasses } from "@/components/tabs"`,
    `import { ViewTabs } from "@/components/tabs"`,
  ],
  [
    "tab-strip",
    jsx(`<TabsPrimitive.List aria-label="Views" />`),
    jsx(`<TabsPrimitive.Content value="a" />`),
  ],
  [
    "toggle-group",
    jsx(`<ToggleGroup type="single" value="a" />`),
    jsx(`<ToggleGroup type="multiple" value={["a"]} />`),
  ],
  [
    "more-menu",
    jsx(
      `<DropdownMenu><DropdownMenuTrigger><MoreHorizontal /></DropdownMenuTrigger></DropdownMenu>`,
    ),
    jsx(`<DropdownMenu><DropdownMenuTrigger>Sort</DropdownMenuTrigger></DropdownMenu>`),
  ],
  [
    "clipboard",
    `export const copy = (t) => navigator.clipboard.writeText(t)`,
    `export const copy = (t) => copyText(t)`,
  ],
  ["copy-control", `function CopyLine() { return null }`, `function CopiedPart() { return null }`],
  [
    "list-skeleton",
    jsx(`<LoadingRows rows={3} className="[&>*]:h-14 [&>*]:rounded-xl" />`),
    jsx(`<ChoiceListSkeleton rows={3} />`),
  ],
  [
    "list-skeleton",
    jsx(`<Skeleton className="h-14 w-full rounded-xl" />`),
    jsx(`<Skeleton className="h-9 w-full" />`),
  ],
  [
    "list-skeleton",
    `import { GhostRows } from "@/components/network/dns/ghost"`,
    `import { Bar } from "@/components/network/dns/ghost"`,
  ],
  [
    "sweep",
    jsx(`<span className="absolute inset-y-0 w-1/3 animate-sweep bg-brand" />`),
    jsx(`<Sweep className="h-0.5" />`),
  ],
  [
    "status-dot",
    jsx(`<span className="size-1.5 rounded-full bg-warning" />`),
    jsx(`<StatusDot tone="warning" />`),
  ],
  [
    "stat-grid",
    `import { StatGrid } from "@/components/stat-tile"`,
    `import { StatTile } from "@/components/stat-tile"`,
  ],
  [
    "icon-button",
    jsx(`<Button size="icon-sm" aria-label="Close"><Cross /></Button>`),
    jsx(
      `<DropdownMenuTrigger asChild><Button size="icon-sm" aria-label="More"><Cross /></Button></DropdownMenuTrigger>`,
    ),
  ],
  [
    "icon-button",
    jsx(
      `<Tooltip><TooltipTrigger asChild><Button size="icon" aria-label="Zoom"><Cross /></Button></TooltipTrigger></Tooltip>`,
    ),
    jsx(`<VerbMenu trigger={<Button size="icon-xs" aria-label="More"><Cross /></Button>} />`),
  ],
  [
    "back-link",
    `const BACK = <BackLink href="/docker" label="Containers" />`,
    `const BACK = { href: "/docker", label: "Containers" }`,
  ],
  [
    "route-params",
    jsx(`<p>{decodeURIComponent(params.name)}</p>`),
    jsx(`<p>{decodeSegment(params.name)}</p>`),
  ],
]

describe("design-system tokens", () => {
  for (const [id, refused, allowed] of TOKENS) {
    test(`${id}: refuses one and lets the other through`, () => {
      expect(reported(refused)).toContain(id)
      expect(reported(allowed)).not.toContain(id)
    })
  }

  test("the component that owns a pattern may use it", () => {
    const pulse = jsx(`<span className="animate-pulse rounded-md bg-muted" />`)
    expect(reported(pulse, "src/components/bone.tsx")).not.toContain("animate-pulse-status")
    const media = `export const Q = "(min-width: 1024px)"`
    expect(reported(media, "src/lib/breakpoints.ts")).not.toContain("media-literal")
  })

  test("a control inside a group that draws the ring keeps outline-none", () => {
    const code = jsx(`<div className="focus-ring-within"><input className="outline-none" /></div>`)
    expect(reported(code)).not.toContain("focus-ring")
  })

  test("reads a class list's variants apart from its utility", () => {
    expect(classes("hover:[&_svg]:text-faint/50 md:z-10")).toEqual([
      { variants: ["hover", "[&_svg]"], utility: "text-faint/50" },
      { variants: ["md"], utility: "z-10" },
    ])
  })
})

describe("retired APIs", () => {
  for (const [id, refused, allowed] of RETIRED) {
    test(`${id}: refuses one and lets the other through`, () => {
      expect(reported(refused)).toContain(id)
      expect(reported(allowed)).not.toContain(id)
    })
  }

  test("every id has a fixture", () => {
    const tested = new Set(RETIRED.map(([id]) => id))
    expect(RETIRED_IDS.filter((id) => !tested.has(id))).toEqual([])
  })

  test("a component's own file may use its parts", () => {
    const strip = `import { tabClasses } from "@/components/tabs"`
    expect(reported(strip, "src/components/database/query/tab-strip.tsx")).not.toContain(
      "tab-strip",
    )
    const group = jsx(`<ToggleGroup type="single" value="a" />`)
    expect(reported(group, "src/components/segments.tsx")).not.toContain("toggle-group")
  })

  test("a model decodes what is not an address", () => {
    const model = `export const dsn = (s) => decodeURIComponent(s)`
    expect(reported(model, "src/lib/dsn.ts")).not.toContain("route-params")
  })
})

describe("a field of several controls", () => {
  test("field-group: two controls under one Field's id are refused; a group or htmlFor is not", () => {
    const pair = `<Input aria-label="Setting 1 name" /><Input aria-label="Setting 1 value" />`
    expect(reported(jsx(`<Field label="Settings">${pair}</Field>`))).toContain("field-group")
    expect(reported(jsx(`<Field label="Settings" group>${pair}</Field>`))).not.toContain(
      "field-group",
    )
    expect(
      reported(jsx(`<Field label="Name" htmlFor="name"><Input id="name" /><Input /></Field>`)),
    ).not.toContain("field-group")
  })

  test("field-group: a control drawn per item is many", () => {
    const rows = jsx(`<Field label="Columns">
      {pairs.map((pair) => <SelectTrigger key={pair} aria-label={pair} />)}
    </Field>`)
    expect(reported(rows)).toContain("field-group")
  })

  test("field-group: one control at a time, its own id, or a field inside is not", () => {
    const either = jsx(`<Field label="Which">
      <Segments label="Which" value={v} options={[]} onChange={set} />
      {typed ? <Textarea aria-label="Names" /> : <SearchInput aria-label="Filter" />}
    </Field>`)
    expect(reported(either)).not.toContain("field-group")
    const own = jsx(`<Field label="Name"><Input /><Input id="other" /></Field>`)
    expect(reported(own)).not.toContain("field-group")
    const named = jsx(`<Field label="Mode"><Input /><Switch aria-label="On" /></Field>`)
    expect(reported(named)).not.toContain("field-group")
    const nested = jsx(`<Field label="Pair" group><Field label="A"><Input /></Field></Field>`)
    expect(reported(nested)).not.toContain("field-group")
  })
})
