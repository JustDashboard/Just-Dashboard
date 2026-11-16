"use client"

import { createElement, Fragment, useMemo } from "react"
import { usePoll, type PollState } from "@/hooks/use-poll"
import type { RankItem } from "@/components/rank-list"
import { counted, millis, shareWords } from "@/components/database/kit/figures"
import { statementVerb } from "@/components/database/home/kinds"
import { statementHref } from "@/components/database/ops/performance-address"
import { readStatements } from "@/components/database/ops/performance-api"
import { statementKeys } from "@/components/database/ops/performance-figures"
import type {
  DbStatement,
  DbStatements,
  DbStatementsEnable,
} from "@/components/database/ops/performance-types"
import { oneLine } from "@/components/database/ops/queries"

/**
 * The statements a server spent its time on, by their share of all of it, as
 * the rows of a `RankList`.
 *
 * The database's Home (Busiest statements) and the Queries view (Top
 * statements, on the database's Logs and on the host's) each read the list and
 * mapped it on their own, so one statement's share read "4%" on one and "4.2%"
 * on the other, its time "0.00 ms" and "<0.01 ms", and a row opened the
 * Performance list from one and the statement's page from the other. One read
 * and one mapping now: the share and the time in `kit/figures`' words, and
 * every row the way to the statement's own page.
 */
export function useTopStatements(
  id: number,
  { limit, interval = 30_000 }: { limit: number; interval?: number },
): { items: RankItem[]; enable?: DbStatementsEnable; state: PollState<DbStatements> } {
  const state = usePoll(
    (signal) => readStatements(id, { sort: "total", limit }, signal),
    interval,
    [id, limit],
  )
  const list = state.data?.statements
  const items = useMemo(() => topStatementItems(id, list ?? []), [id, list])
  return { items, enable: state.data?.enable, state }
}

/** The rows of a list of statements by total time: pure, so the words are held by a unit test. */
export function topStatementItems(id: number, statements: readonly DbStatement[]): RankItem[] {
  // A row's key is its place in the address: one digest listed twice is two
  // statements, and one with no digest still has a page.
  const keys = statementKeys(statements)
  return statements.map((statement, index) => {
    const line = oneLine(statement.query)
    const share = shareWords(statement.share)
    // Reads that miss the cache are the part of a statement that is trouble:
    // said in amber beside its calls.
    const missed = statement.hitRatio >= 0 && statement.hitRatio < 0.9 ? 1 - statement.hitRatio : 0
    const figures = `${counted(statement.calls, "call")} · ${millis(statement.meanMs)} each`
    return {
      key: keys[index] || `no-id:${index}`,
      label: statementWords(line),
      // Named by its opening and its share: the whole text is four hundred
      // characters a screen reader would read on every row.
      title: `${opening(line)} — ${share} of runtime. Open the statement`,
      // The share of the runtime is the figure the list is ranked by, so it
      // is the row's figure rather than a column beside another one.
      value: share,
      hint:
        missed > 0
          ? createElement(
              Fragment,
              null,
              figures,
              createElement(
                "span",
                { className: "text-warning" },
                ` · ${shareWords(missed)} of reads missed the cache`,
              ),
            )
          : figures,
      href: statementHref(id, keys[index]),
    }
  })
}

/** As much of a statement as names it aloud: its opening, cut at a word. */
export function opening(line: string, length = 60): string {
  if (line.length <= length) return line
  const cut = line.slice(0, length)
  return `${cut.slice(0, Math.max(cut.lastIndexOf(" "), length / 2))}…`
}

/** A statement with its first word in the hue of what it does, as `StatementLine` draws it. */
function statementWords(line: string): React.ReactNode {
  const verb = statementVerb(line)
  if (!verb) return line
  return createElement(
    Fragment,
    null,
    createElement("span", { style: { color: verb.color } }, verb.word),
    verb.rest,
  )
}
