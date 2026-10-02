import type { CellValue } from "@/components/database/grid"
import type { QueryResult } from "@/components/database/query/types"

/**
 * Whether a result can be drawn, and as what.
 *
 * A result with a column of instants and columns of numbers is a line over
 * time; a result of a name and a number is a ranking. Anything else is a
 * table, and saying so — with what would make it a chart — is better than
 * drawing a picture of the wrong thing.
 */
export type ChartRead =
  | {
      kind: "series"
      /** The column the instants came from. */
      time: string
      rows: ({ ts: number } & Record<string, number>)[]
      /** Each with the largest size it reaches, so series of other magnitudes can be told apart. */
      series: { key: string; label: string; most: number }[]
    }
  | {
      kind: "bars"
      label: string
      value: string
      items: { label: string; value: number }[]
      /** Rows beyond the ones drawn. */
      more: number
    }
  | { kind: "none"; reason: string }

/** The most series one chart draws: the palette has five. */
const MAX_SERIES = 5
/** The most bars a ranking draws. */
const MAX_BARS = 40

const NUMBER_KINDS = new Set(["integer", "decimal", "float"])
const NUMERIC = /^-?(?:\d+\.?\d*|\.\d+)(?:[eE][-+]?\d+)?$/

function asNumber(value: CellValue): number | undefined {
  if (typeof value === "number") return Number.isFinite(value) ? value : undefined
  if (typeof value === "string" && NUMERIC.test(value.trim())) return Number(value)
  return undefined
}

const INSTANT =
  /^\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?:Z|[-+]\d{2}(?::?\d{2})?)?)?$/

function asInstant(value: CellValue): number | undefined {
  if (typeof value !== "string" || !INSTANT.test(value.trim())) return undefined
  const text = value.trim()
  // A date and a zone-less time are read as UTC, as the grid prints them.
  const iso =
    text.length === 10
      ? `${text}T00:00:00Z`
      : /(?:Z|[-+]\d{2}(?::?\d{2})?)$/.test(text)
        ? text.replace(" ", "T")
        : `${text.replace(" ", "T")}Z`
  const at = Date.parse(iso)
  return Number.isNaN(at) ? undefined : at
}

export function chartOf(result: QueryResult | undefined): ChartRead {
  if (!result || result.columns.length === 0) {
    return { kind: "none", reason: "The statement returned no columns to draw." }
  }
  if (result.rows.length === 0) return { kind: "none", reason: "The statement returned no rows." }

  const column = (index: number) => result.rows.map((row) => row[index])
  const every = <T>(cells: CellValue[], read: (cell: CellValue) => T | undefined) =>
    cells.some((cell) => cell !== null) &&
    cells.every((cell) => cell === null || read(cell) !== undefined)
  const kinds = result.kinds ?? []
  const numeric: number[] = []
  const instants: number[] = []
  const names: number[] = []
  result.columns.forEach((_, index) => {
    const cells = column(index)
    const kind = kinds[index]
    // The server's word for the column decides where it has one; a column it
    // has no opinion on is read from its values.
    const loose = kind === undefined || kind === "other"
    if ((kind === "date" || kind === "datetime" || loose) && every(cells, asInstant)) {
      instants.push(index)
    } else if ((NUMBER_KINDS.has(kind ?? "") || loose) && every(cells, asNumber)) {
      numeric.push(index)
    } else names.push(index)
  })

  if (instants.length > 0 && numeric.length > 0) {
    const time = instants[0]
    const drawn = numeric.slice(0, MAX_SERIES)
    const rows = result.rows
      .flatMap((row) => {
        const ts = asInstant(row[time])
        if (ts === undefined) return []
        const point: { ts: number } & Record<string, number> = { ts }
        for (const index of drawn) {
          const value = asNumber(row[index])
          if (value !== undefined) point[`c${index}`] = value
        }
        return [point]
      })
      .sort((a, b) => a.ts - b.ts)
    if (rows.length < 2) {
      return { kind: "none", reason: "A line over time needs at least two rows with an instant." }
    }
    return {
      kind: "series",
      time: result.columns[time],
      rows,
      series: drawn.map((index) => ({
        key: `c${index}`,
        label: result.columns[index],
        most: rows.reduce((most, row) => Math.max(most, Math.abs(row[`c${index}`] ?? 0)), 0),
      })),
    }
  }

  if (names.length > 0 && numeric.length > 0) {
    const label = names[0]
    const value = numeric[0]
    const items = result.rows.flatMap((row) => {
      const number = asNumber(row[value])
      if (number === undefined) return []
      const name = row[label]
      return [
        {
          label: name === null ? "NULL" : typeof name === "string" ? name : JSON.stringify(name),
          value: number,
        },
      ]
    })
    if (items.length > 0) {
      return {
        kind: "bars",
        label: result.columns[label],
        value: result.columns[value],
        items: items.slice(0, MAX_BARS),
        more: Math.max(0, items.length - MAX_BARS),
      }
    }
  }

  return {
    kind: "none",
    reason:
      numeric.length === 0
        ? "Nothing here is a number. A chart needs a column of numbers beside a column of instants or of names."
        : "A chart needs a column of instants or of names beside the numbers.",
  }
}

/** Past this ratio between two series' largest values, the smaller one is a flat line on the larger one's axis. */
const SAME_AXIS = 20

/**
 * Which series are drawn when a result first lands. Series of one magnitude
 * share an axis and are drawn together; where one would flatten another —
 * 131 orders beside 105,162 of revenue reads as "orders is zero" — only the
 * first is, and the reader turns the others on.
 */
export function drawnAtFirst(series: readonly { key: string; most: number }[]): string[] {
  if (series.length === 0) return []
  const sizes = series.map((entry) => entry.most).filter((most) => most > 0)
  const apart = sizes.length > 1 && Math.max(...sizes) / Math.min(...sizes) > SAME_AXIS
  return apart ? [series[0].key] : series.map((entry) => entry.key)
}
