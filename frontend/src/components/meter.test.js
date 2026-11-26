import { afterEach, describe, expect, spyOn, test } from "bun:test"
import { createElement as h } from "react"
import { renderToStaticMarkup } from "react-dom/server"
import { Meter } from "./meter"
import { StatTile } from "./stat-tile"

// What a screen reader is given for a bar: a named meter, or nothing. A
// `role="meter"` with a value and no name was read as a bare number.

const bar = (html) => html.match(/<div[^>]*data-slot="meter"[^>]*>/)?.[0] ?? ""

// The unnamed cases say so on the console while developing; held here, and
// kept out of the run's output.
let said
const listen = () => {
  said = spyOn(console, "error").mockImplementation(() => {})
}
afterEach(() => said?.mockRestore())

describe("Meter", () => {
  test("a named bar is a meter with its name and value", () => {
    const tag = bar(renderToStaticMarkup(h(Meter, { value: 34.4, label: "Disk busy" })))
    expect(tag).toContain('role="meter"')
    expect(tag).toContain('aria-label="Disk busy"')
    expect(tag).toContain('aria-valuenow="34"')
  })

  test("a decorative bar is hidden, and so is one that reached it without a name", () => {
    listen()
    for (const props of [{ decorative: true }, { label: "" }, { label: "  " }, {}]) {
      const tag = bar(renderToStaticMarkup(h(Meter, { value: 50, ...props })))
      expect(tag).toContain('aria-hidden="true"')
      expect(tag).not.toContain("role=")
      expect(tag).not.toContain("aria-valuenow")
    }
    // Decorative is a choice and says nothing; the three without a name do.
    expect(said).toHaveBeenCalledTimes(3)
  })
})

describe("StatTile", () => {
  const tile = (props) =>
    bar(renderToStaticMarkup(h(StatTile, { value: "12 MB/s", meter: 34, ...props })))

  test("its bar is named by its label when the label is words", () => {
    expect(tile({ label: "CPU" })).toContain('aria-label="CPU"')
  })

  test("a label drawn with a mark takes its bar's name from meterLabel", () => {
    const label = h("span", null, h("i", { "aria-hidden": true }), "Disk I/O")
    const named = tile({ label, meterLabel: "Disk busy" })
    expect(named).toContain('role="meter"')
    expect(named).toContain('aria-label="Disk busy"')
    // Without one the bar is decoration, never an unnamed meter.
    listen()
    const unnamed = tile({ label })
    expect(unnamed).toContain('aria-hidden="true"')
    expect(unnamed).not.toContain("role=")
    expect(said).toHaveBeenCalledTimes(1)
  })
})
