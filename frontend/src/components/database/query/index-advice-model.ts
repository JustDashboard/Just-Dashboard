import { plural } from "@/lib/format"
import { planOf, planRows } from "@/components/database/query/plan"

/**
 * The Explain view's two index tools, as the server answers them.
 *
 * Both are asked for, never shown unasked: "What if?" proposes indexes for a
 * statement's plan and plans it again with one of them made hypothetical, and
 * "Missing indexes" lists what SQL Server's own optimizer wished for.
 */

export type IndexUse = "equality" | "range" | "join" | "sort"

export interface IndexCandidate {
  schema: string
  table: string
  columns: string[]
  /** What each column does in the plan, in the order of `columns`. */
  uses: IndexUse[]
}

export interface HypoPG {
  state: "ready" | "not_created" | "not_installed"
  version?: string
}

export interface IndexAdvice {
  candidates: IndexCandidate[]
  /** Candidates left out because an index already leads with their columns. */
  covered: number
  /** Candidates left out because their table could not be proposed for. */
  skipped: number
  hypopg: HypoPG
}

export interface WhatIf {
  /** The CREATE INDEX the extension was given. */
  statement: string
  /** What the extension calls the index, which is how the plan names it. */
  indexName: string
  before: unknown
  after: unknown
  beforeCost: number
  afterCost: number
  /** The planner chose the index. */
  used: boolean
}

export interface MissingIndex {
  schema: string
  table: string
  equality: string[]
  inequality: string[]
  include: string[]
  seeks: number
  scans: number
  avgCost: number
  /** What the optimizer expects the index to save, in percent. */
  avgImpact: number
  lastSeek?: string
  statement: string
}

export interface MissingIndexes {
  supported: boolean
  reason?: string
  indexes: MissingIndex[]
  since?: string
}

/** Whether HypoPG can be used, said as a fact about the server; nothing when it can. */
export function hypopgFact(hypopg: HypoPG): string | undefined {
  switch (hypopg.state) {
    case "not_installed":
      return "HypoPG (the hypopg extension) is not installed on this server."
    case "not_created":
      return "HypoPG (the hypopg extension) is installed on this server but not created in this database."
    default:
      return undefined
  }
}

/**
 * Why a list of candidates is empty, in what the server counted and nothing
 * more: a condition it could not read proposes nothing and is in neither count.
 */
export function noCandidates(advice: Pick<IndexAdvice, "covered" | "skipped">): string {
  const said: string[] = []
  if (advice.covered > 0) {
    said.push(
      `${plural(advice.covered, "candidate")} already ${advice.covered === 1 ? "has" : "have"} an index that leads with the same columns.`,
    )
  }
  if (advice.skipped > 0) {
    said.push(
      `${plural(advice.skipped, "candidate")} ${advice.skipped === 1 ? "is" : "are"} on a table that cannot be proposed for: a partition, a view, a foreign table, or one past the first twelve.`,
    )
  }
  return said.length > 0
    ? said.join(" ")
    : "Nothing this plan's conditions test is a plain column compared with a value or another column."
}

const USE_WORD: Record<IndexUse, string> = {
  equality: "equals",
  range: "range",
  join: "join",
  sort: "sort",
}

/** What a candidate's columns do in the plan: "status equals · total range". */
export function usesText(candidate: IndexCandidate): string {
  return candidate.columns
    .map((column, i) => `${column} ${USE_WORD[candidate.uses[i]] ?? ""}`.trim())
    .join(" · ")
}

export const candidateKey = (candidate: IndexCandidate) =>
  `${candidate.schema}.${candidate.table}(${candidate.columns.join(",")})`

const PLAIN = /^[a-z_][a-z0-9_$]*$/

/** Column names as the editable field holds them: plain ones bare, the rest double-quoted. */
export function columnsText(columns: readonly string[]): string {
  return columns
    .map((name) => (PLAIN.test(name) ? name : `"${name.replace(/"/g, '""')}"`))
    .join(", ")
}

/** The field's text back to column names: commas separate, a double-quoted name may hold one. */
export function parseColumns(text: string): string[] {
  const names: string[] = []
  let name = ""
  let quoted = false
  let inside = false
  const end = () => {
    const value = quoted ? name : name.trim()
    if (value !== "") names.push(value)
    name = ""
    quoted = false
  }
  for (let i = 0; i < text.length; i++) {
    const c = text[i]
    if (inside) {
      if (c !== '"') name += c
      else if (text[i + 1] === '"') {
        name += '"'
        i++
      } else inside = false
    } else if (c === '"' && name.trim() === "") {
      name = ""
      quoted = true
      inside = true
    } else if (c === ",") end()
    else if (!(quoted && c === " ")) name += c
  }
  end()
  return names
}

export interface CostChange {
  /** The share of the cost the index takes away, negative when it adds. */
  saved: number
  /** "falls by 47%", "does not change", "rises by 12%". */
  words: string
}

/** How the planner's total cost moved between the two plans. */
export function costChange(before: number, after: number): CostChange {
  if (!(before > 0) || !Number.isFinite(after)) return { saved: 0, words: "does not change" }
  const saved = (before - after) / before
  const percent = Math.round(Math.abs(saved) * 100)
  if (percent === 0) return { saved: 0, words: "does not change" }
  return saved > 0
    ? { saved, words: `falls by ${percent}%` }
    : { saved, words: `rises by ${percent}%` }
}

export function costText(cost: number): string {
  return cost.toLocaleString("en-US", { maximumFractionDigits: Math.abs(cost) >= 100 ? 0 : 2 })
}

export interface StepLine {
  depth: number
  text: string
  /** The step reads the hypothetical index. */
  hypothetical: boolean
}

/** A plan's steps, top down, as the lines the before and after columns print. */
export function stepLines(plan: unknown, indexName?: string): StepLine[] {
  const parsed = planOf(plan)
  if (!parsed) return []
  return planRows(parsed).map((row) => ({
    depth: row.depth,
    text: row.node.target ? `${row.node.title} ${row.node.target}` : row.node.title,
    hypothetical: indexName !== undefined && (row.node.target ?? "").includes(indexName),
  }))
}

/** A missing index's columns as one line: keys first, then what it would include. */
export function missingColumnsText(index: MissingIndex): string {
  const keys = [...index.equality, ...index.inequality]
  return index.include.length > 0
    ? `${columnsText(keys)} · includes ${columnsText(index.include)}`
    : columnsText(keys)
}
