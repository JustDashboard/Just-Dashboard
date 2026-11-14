import { bytes } from "@/lib/format"
import type { ChartUnit, ChartView } from "@/components/database/home/charts"
import { compact, perSecond } from "@/components/database/home/readings"
import { chartRows, gauge, type Sample } from "@/components/database/home/samples"

/**
 * A view of a database's activity as one line of words: what its first
 * measurement reads now, said as a sentence, the others after it, and the
 * first one's run over the window for the line's end.
 *
 * The home drew every view as a full chart, the same four Performance draws
 * from the same component. A front page reads its measures as a project's
 * Overview does — "12 open of 100", "10 transactions a second" — and leaves
 * the plots to the page that is about them.
 */
export type LineReading = {
  /** The first measurement's newest reading in words; `undefined` before there is one. */
  words: string | undefined
  /** The view's other measurements that have a reading, in fewer words. */
  rest: string | undefined
  /** The first measurement over the window, oldest first. */
  values: number[]
  /** The scale the run is drawn on, for a unit that has one. */
  max: number | undefined
}

/** A reading in words: the figure in its unit, then what it counts. */
export function said(unit: ChartUnit, value: number, label: string, whole = true): string {
  const what = label.toLowerCase()
  switch (unit) {
    case "count":
      return `${compact(value)} ${what}`
    case "rate":
      return `${perSecond(value)} ${what}${whole ? " a second" : ""}`
    case "bytes":
      return `${bytes(value)} ${what}`
    case "bytesRate":
      return `${bytes(value)} ${what}${whole ? " a second" : ""}`
    case "percent":
      return `${value.toFixed(1)}% ${what}`
  }
}

/** The newest value a line has in the rows, and every value it has, in order. */
function run(rows: readonly Record<string, number>[], key: string) {
  const values: number[] = []
  for (const row of rows) {
    const value = row[key]
    if (typeof value === "number") values.push(value)
  }
  return values
}

export function lineReading(view: ChartView, samples: readonly Sample[]): LineReading {
  const rows = chartRows(samples, view.sources)
  const [first, ...others] = view.series
  const values = first ? run(rows, first.key) : []
  const newest = values.at(-1)
  const ceiling = view.ceiling ? gauge(samples, view.ceiling) : undefined
  const words =
    first && newest !== undefined
      ? `${said(view.unit, newest, first.label)}${ceiling && ceiling > 0 ? ` of ${compact(ceiling)}` : ""}`
      : undefined
  const rest = others
    .flatMap((line) => {
      const value = run(rows, line.key).at(-1)
      return value === undefined ? [] : [said(view.unit, value, line.label, false)]
    })
    .join(" · ")
  return {
    words,
    rest: rest || undefined,
    values,
    max: view.unit === "percent" ? 100 : undefined,
  }
}
