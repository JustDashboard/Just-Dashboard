import { describe, expect, test } from "bun:test"
import { createElement } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { sharePercent } from "@/lib/format"
import { RankList, SplitBar, percent, splitWidths } from "./rank-list"

const PARTS = [
  { key: "string", count: 600, color: "var(--chart-1)", label: "String", figure: "600" },
  { key: "hash", count: 399, color: "var(--chart-2)", label: "Hash", figure: "399" },
  { key: "stream", count: 1, color: "var(--chart-3)", label: "Stream", figure: "1" },
  { key: "set", count: 0, color: "var(--chart-4)", label: "Set", figure: "0" },
]

const draw = (props) => renderToStaticMarkup(createElement(SplitBar, { label: "Keys", ...props }))

describe("splitWidths", () => {
  test("the parts fill the bar in the order given, and an empty part is left out", () => {
    const widths = splitWidths(PARTS, { minShare: 0 })
    expect(widths.map((part) => part.key)).toEqual(["string", "hash", "stream"])
    expect(widths.reduce((n, part) => n + part.width, 0)).toBeCloseTo(100, 6)
    expect(widths[0].width).toBeCloseTo(60, 6)
  })

  test("a sliver is kept to a percent of the bar, taken back out of the others", () => {
    const widths = splitWidths([
      { key: "a", count: 9999, color: "x" },
      { key: "b", count: 1, color: "y" },
    ])
    expect(widths[1].width).toBeGreaterThan(0.98)
    expect(widths.reduce((n, part) => n + part.width, 0)).toBeCloseTo(100, 6)
  })

  test("against a larger total the parts leave the rest of the track", () => {
    const widths = splitWidths(
      [
        { key: "in", count: 20, color: "x" },
        { key: "out", count: 5, color: "y" },
      ],
      { total: 100 },
    )
    expect(widths.map((part) => part.width)).toEqual([20, 5])
  })

  test("nothing at all is no parts", () => {
    expect(splitWidths([{ key: "a", count: 0, color: "x" }])).toEqual([])
  })
})

describe("SplitBar", () => {
  test("without a legend it is the bar alone, named for a screen reader", () => {
    const html = draw({ parts: PARTS })
    expect(html).toContain('data-slot="split-bar"')
    expect(html).toContain('role="img"')
    expect(html).toContain('aria-label="Keys"')
    expect(html).not.toContain("split-legend")
    // One gap, the shared one.
    expect(html).toContain("gap-0.5")
  })

  test("a static legend names each part with its figure, and presses nothing", () => {
    const html = draw({ parts: PARTS, legend: "static" })
    expect(html).toContain('data-slot="split-legend"')
    for (const name of ["String", "Hash", "Stream"]) expect(html).toContain(`>${name}</span>`)
    // A part with nothing in it is not named under a bar it has no width in.
    expect(html).not.toContain(">Set</span>")
    expect(html).not.toContain("<button")
  })

  test("a pick legend is a button per part, pressed for the one picked", () => {
    const html = draw({ parts: PARTS, legend: "pick", picked: "hash", onPick: () => {} })
    expect(html.match(/<button/g)).toHaveLength(3)
    expect(html.match(/aria-pressed="true"/g)).toHaveLength(1)
    expect(html).toMatch(/aria-pressed="true"[^>]*>(?:(?!<\/button>).)*>Hash</)
  })

  test("a small bar is four pixels, and the gap can be left off", () => {
    const html = draw({ parts: PARTS, size: "sm", gap: false })
    expect(html).toContain("h-1")
    expect(html).not.toContain("gap-0.5")
  })

  test("an empty whole is the track", () => {
    const html = draw({ parts: [] })
    expect(html).toContain("bg-meter-track")
  })
})

describe("RankList", () => {
  test("a row's share is said the way lib/format says one, and the old name still says it", () => {
    const html = renderToStaticMarkup(
      createElement(RankList, {
        items: [
          { key: "a", label: "/", value: "996", share: 0.996 },
          { key: "b", label: "/health", value: "4", share: 0.004 },
          { key: "c", label: "/login", value: "0" },
        ],
      }),
    )
    const shares = [...html.matchAll(/data-slot="rank-share"[^>]*>([^<]*)</g)].map((m) => m[1])
    expect(shares).toEqual(["&gt;99%", "&lt;1%", ""])
    expect(percent).toBe(sharePercent)
  })
})
