/**
 * What an engine has counted of one table, said as sentences.
 *
 * These were a grid of captioned figures — ROWS, DEAD ROWS, INSERTED ·
 * UPDATED · DELETED — which the reader had to put together to learn the one
 * thing the tab is opened for: is this table being looked after. A sentence
 * says it ("412 dead rows, 4% of the table"), and puts the figure that needs
 * the other beside it.
 *
 * Every count is the engine's own and is said only where it keeps one: a
 * count it does not keep is left out, never said as zero.
 */

/** A sentence in parts: plain words, and the figures among them. */
export type Sentence = { key: string; words: (string | { figure: string })[] }

/** What of a table's statistics the sentences are made from. */
export type StatFigures = {
  rows?: number
  deadRows?: number
  inserts?: number
  updates?: number
  deletes?: number
  modsSinceAnalyze?: number
  engine?: string
  parts?: number
}

const kept = (value: number | undefined): value is number => value !== undefined && value >= 0
const grouped = (value: number) => value.toLocaleString("en-US")
const figure = (value: number) => ({ figure: grouped(value) })

/** A share as a reader says it: none under half a percent is "under 1%", never "0%". */
function share(part: number, whole: number): string {
  const percent = (part / whole) * 100
  if (percent > 0 && percent < 1) return "under 1%"
  return `${Math.round(percent)}%`
}

export function statSentences(stat: StatFigures): Sentence[] {
  const sentences: Sentence[] = []

  if (kept(stat.rows)) {
    const live: Sentence = {
      key: "rows",
      words: ["About ", figure(stat.rows), stat.rows === 1 ? " row" : " rows"],
    }
    if (kept(stat.deadRows)) {
      const whole = stat.rows + stat.deadRows
      live.words.push(
        ", and ",
        figure(stat.deadRows),
        stat.deadRows === 1 ? " dead row" : " dead rows",
        // A share of nothing is no share.
        ...(stat.deadRows > 0 && whole > 0
          ? [`, ${share(stat.deadRows, whole)} of the table`]
          : []),
      )
    }
    sentences.push(live)
  } else {
    sentences.push({
      key: "rows",
      words: kept(stat.deadRows)
        ? [
            "Rows not counted; ",
            figure(stat.deadRows),
            stat.deadRows === 1 ? " dead row" : " dead rows",
          ]
        : ["Rows not counted"],
    })
  }

  if ([stat.inserts, stat.updates, stat.deletes].every(kept)) {
    sentences.push({
      key: "writes",
      words: [
        figure(stat.inserts!),
        " inserted, ",
        figure(stat.updates!),
        " updated and ",
        figure(stat.deletes!),
        " deleted since the counters were reset",
      ],
    })
  } else if (kept(stat.updates)) {
    sentences.push({
      key: "writes",
      words: [figure(stat.updates), stat.updates === 1 ? " write" : " writes"],
    })
  }

  if (kept(stat.modsSinceAnalyze)) {
    sentences.push({
      key: "changed",
      words: [
        figure(stat.modsSinceAnalyze),
        stat.modsSinceAnalyze === 1 ? " row" : " rows",
        " changed since it was last analyzed",
      ],
    })
  }

  if (stat.engine || stat.parts !== undefined) {
    sentences.push({
      key: "stored",
      words: [
        "Stored",
        ...(stat.engine ? [" by ", { figure: stat.engine }] : []),
        ...(stat.parts !== undefined
          ? [" in ", figure(stat.parts), stat.parts === 1 ? " part" : " parts"]
          : []),
      ],
    })
  }

  return sentences
}
