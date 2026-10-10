import type { QueryResult } from "@/components/database/query/types"

/**
 * A plan as a tree the page can draw, whichever engine wrote it.
 *
 * Four shapes arrive as JSON — PostgreSQL's `[{ Plan }]`, MySQL's
 * `query_block`, MySQL 8.3's measured `{ operation, inputs }`, MariaDB's
 * `query_block` with its `r_` measurements — and two as rows that name their
 * own parent (SQL Server's SHOWPLAN, SQLite's EXPLAIN QUERY PLAN). Each is
 * read into the same node: what the step is, what it acts on, what it costs,
 * how many rows it expects and — when the statement was run — how many came
 * and how long they took. A shape nobody has described is not guessed at: the
 * page shows the engine's own text.
 */

export interface PlanNode {
  /** The step: "Index Scan", "Nested loop", "Full table scan". */
  title: string
  /** What it acts on: a table and its alias, an index. */
  target?: string
  /** Its conditions and keys, one a line. */
  notes: string[]
  /** Estimated cost of this step and everything under it, in the planner's units. */
  cost?: number
  /** Rows the planner expects out of it. */
  rows?: number
  /** Rows that came out, over all its loops. Only when the statement was run. */
  actualRows?: number
  /** Milliseconds spent in it and everything under it, over all its loops. */
  timeMs?: number
  /** Milliseconds spent in the step alone, where that is what the engine measured. */
  ownTimeMs?: number
  loops?: number
  /** Whatever else the engine said about the step. */
  facts: [string, string][]
  children: PlanNode[]
}

export interface Plan {
  root: PlanNode
  /** The statement was run and the plan carries what happened. */
  analyzed: boolean
  planningMs?: number
  executionMs?: number
}

type Dict = Record<string, unknown>

const isDict = (value: unknown): value is Dict =>
  typeof value === "object" && value !== null && !Array.isArray(value)

const num = (value: unknown): number | undefined => {
  if (typeof value === "number" && Number.isFinite(value)) return value
  if (typeof value === "string" && value.trim() !== "" && Number.isFinite(Number(value))) {
    return Number(value)
  }
  return undefined
}

const text = (value: unknown): string =>
  Array.isArray(value)
    ? value.map(text).join(", ")
    : isDict(value)
      ? JSON.stringify(value)
      : String(value)

/** `rows_examined_per_scan` → "Rows examined per scan". */
function words(key: string): string {
  const spaced = key.replace(/[_-]+/g, " ").trim()
  return spaced.charAt(0).toUpperCase() + spaced.slice(1)
}

/** The scalar entries of a record as facts, the ones already drawn and the empty ones left out. */
function factsOf(record: Dict, used: ReadonlySet<string>, label = (key: string) => key) {
  const facts: [string, string][] = []
  for (const [key, value] of Object.entries(record)) {
    if (used.has(key) || value === null || value === undefined) continue
    if (isDict(value) || (Array.isArray(value) && value.some(isDict))) continue
    if (value === false || value === 0 || value === "" || value === "0") continue
    if (Array.isArray(value) && value.length === 0) continue
    facts.push([label(key), value === true ? "yes" : text(value)])
  }
  return facts
}

/* ------------------------------------------------------------ PostgreSQL */

const PG_USED = new Set([
  "Node Type",
  "Plans",
  "Relation Name",
  "Alias",
  "Schema",
  "Index Name",
  "CTE Name",
  "Function Name",
  "Subplan Name",
  "Total Cost",
  "Plan Rows",
  "Actual Rows",
  "Actual Total Time",
  "Actual Loops",
  "Parallel Aware",
  "Async Capable",
  "Parent Relationship",
  "Actual Startup Time",
])

function postgresNode(raw: Dict): PlanNode {
  const loops = num(raw["Actual Loops"])
  const perLoop = (key: string) => {
    const value = num(raw[key])
    return value === undefined ? undefined : value * (loops ?? 1)
  }
  const relation = raw["Relation Name"] ?? raw["CTE Name"] ?? raw["Function Name"]
  const alias = raw["Alias"]
  const target = [
    relation !== undefined &&
      (alias !== undefined && alias !== relation
        ? `${text(relation)} ${text(alias)}`
        : text(relation)),
    raw["Index Name"] !== undefined && `using ${text(raw["Index Name"])}`,
    raw["Subplan Name"] !== undefined && text(raw["Subplan Name"]),
  ].filter(Boolean)
  const notes: string[] = []
  const used = new Set(PG_USED)
  for (const [key, value] of Object.entries(raw)) {
    // Conditions, filters and keys are the step's own sentence, not facts about it.
    if (/(Cond|Filter|Key)$/.test(key) && !/^Rows Removed/.test(key)) {
      notes.push(`${key}: ${text(value)}`)
      used.add(key)
    }
  }
  const type = text(raw["Node Type"] ?? "Step")
  return {
    title: raw["Parallel Aware"] === true ? `Parallel ${type}` : type,
    target: target.length > 0 ? target.join(" ") : undefined,
    notes,
    cost: num(raw["Total Cost"]),
    rows: num(raw["Plan Rows"]),
    actualRows: perLoop("Actual Rows"),
    timeMs: perLoop("Actual Total Time"),
    loops,
    facts: factsOf(raw, used),
    children: Array.isArray(raw["Plans"]) ? raw["Plans"].filter(isDict).map(postgresNode) : [],
  }
}

/* ------------------------------------------------- MySQL 8.3+ (measured) */

const TREE_USED = new Set([
  "operation",
  "inputs",
  "table_name",
  "alias",
  "index_name",
  "schema_name",
  "estimated_total_cost",
  "estimated_rows",
  "actual_rows",
  "actual_last_row_ms",
  "actual_first_row_ms",
  "actual_loops",
  "condition",
  "hash_condition",
  "ranges",
  "sort_fields",
  "query",
  "query_type",
  "access_type",
])

function treeNode(raw: Dict): PlanNode {
  const operation = text(raw.operation ?? words(text(raw.access_type ?? "step")))
  // "Sort: i.sku, limit…" and "Inner hash join (w.id = …)" open with the step's name.
  const cut = /^(.*?)(:\s| \(| on | using )/.exec(operation)
  const head = cut?.[1] ?? operation
  // What follows "on" or "using" is the target, drawn beside the title; what
  // follows a colon or a parenthesis is the step's own detail.
  const detailed = cut !== null && (cut[2] === ": " || cut[2] === " (")
  const loops = num(raw.actual_loops)
  const scaled = (key: string) => {
    const value = num(raw[key])
    return value === undefined ? undefined : value * (loops ?? 1)
  }
  const table = raw.table_name
  const alias = raw.alias
  const target = [
    table !== undefined &&
      (alias !== undefined && alias !== table ? `${text(table)} ${text(alias)}` : text(table)),
    raw.index_name !== undefined && `using ${text(raw.index_name)}`,
  ].filter(Boolean)
  const notes: string[] = []
  if (detailed) notes.push(operation)
  for (const key of ["condition", "hash_condition", "ranges", "sort_fields"]) {
    const value = raw[key]
    if (value !== undefined && !operation.includes(text(value))) {
      notes.push(`${words(key)}: ${text(value)}`)
    }
  }
  return {
    title: head,
    target: target.length > 0 ? target.join(" ") : undefined,
    notes,
    cost: num(raw.estimated_total_cost),
    rows: num(raw.estimated_rows),
    actualRows: scaled("actual_rows"),
    timeMs: scaled("actual_last_row_ms"),
    loops,
    facts: factsOf(raw, TREE_USED, words),
    children: Array.isArray(raw.inputs) ? raw.inputs.filter(isDict).map(treeNode) : [],
  }
}

/* ------------------------------------- MySQL and MariaDB (`query_block`) */

const ACCESS: Record<string, string> = {
  ALL: "Full table scan",
  index: "Full index scan",
  range: "Index range scan",
  ref: "Index lookup",
  eq_ref: "Unique index lookup",
  const: "Single row by key",
  system: "Single row",
  ref_or_null: "Index lookup, or NULL",
  index_merge: "Index merge",
  fulltext: "Full-text search",
  unique_subquery: "Unique subquery lookup",
  index_subquery: "Subquery index lookup",
}

const BLOCK_TITLES: Record<string, string> = {
  query_block: "Query block",
  ordering_operation: "Sort",
  grouping_operation: "Group",
  duplicates_removal: "Remove duplicates",
  windowing: "Window",
  filesort: "Sort",
  temporary_table: "Temporary table",
  nested_loop: "Nested loop",
  "block-nl-join": "Block nested loop join",
  union_result: "Union",
  query_specifications: "Query specifications",
  materialized_from_subquery: "Materialized subquery",
  materialized: "Materialized",
  attached_subqueries: "Attached subqueries",
  subqueries: "Subqueries",
  read_sorted_file: "Read sorted file",
  buffer_result: "Buffer result",
  having_filter: "Having",
}

const BLOCK_USED = new Set([
  "cost_info",
  "cost",
  "rows",
  "rows_examined_per_scan",
  "rows_produced_per_join",
  "r_rows",
  "r_loops",
  "r_total_time_ms",
  "r_table_time_ms",
  "r_other_time_ms",
  "r_engine_stats",
  "table_name",
  "access_type",
  "key",
  "attached_condition",
  "index_condition",
  "sort_key",
  "select_id",
  "possible_keys",
  "loops",
])

function blockNode(key: string, raw: Dict): PlanNode {
  const info = isDict(raw.cost_info) ? raw.cost_info : {}
  const stats = isDict(raw.r_engine_stats) ? raw.r_engine_stats : {}
  const loops = num(raw.r_loops)
  const isTable = key === "table"
  const access = text(raw.access_type ?? "")
  const title = isTable
    ? (ACCESS[access] ?? (access ? words(access) : "Table"))
    : key === "query_block" && raw.select_id !== undefined
      ? `Query block ${text(raw.select_id)}`
      : (BLOCK_TITLES[key] ?? words(key))
  const target = [
    raw.table_name !== undefined && text(raw.table_name),
    raw.key !== undefined && `using ${text(raw.key)}`,
  ].filter(Boolean)
  const notes: string[] = []
  for (const name of ["attached_condition", "index_condition", "sort_key"]) {
    if (raw[name] !== undefined) notes.push(`${words(name)}: ${text(raw[name])}`)
  }
  // A table's own cost is what reading it and evaluating its rows costs; a
  // block states the whole query's.
  const read = num(info.read_cost)
  const evaluated = num(info.eval_cost)
  const cost =
    num(raw.cost) ??
    num(info.query_cost) ??
    (read !== undefined || evaluated !== undefined ? (read ?? 0) + (evaluated ?? 0) : undefined)
  const actual = num(raw.r_rows)
  const tableTime = num(raw.r_table_time_ms)
  const otherTime = num(raw.r_other_time_ms)
  const children: PlanNode[] = []
  for (const [name, value] of Object.entries(raw)) {
    if (name === "cost_info" || name === "r_engine_stats") continue
    if (isDict(value)) children.push(blockNode(name, value))
    else if (Array.isArray(value) && value.some(isDict)) {
      // A list of steps: each entry is an object with one key naming its step.
      const steps = value.filter(isDict).flatMap((entry) => {
        const keys = Object.keys(entry)
        const inner = keys.length === 1 ? entry[keys[0]] : undefined
        return isDict(inner) ? [blockNode(keys[0], inner)] : [blockNode(name, entry)]
      })
      children.push({
        title: BLOCK_TITLES[name] ?? words(name),
        notes: [],
        facts: [],
        children: steps,
      })
    }
  }
  const facts = factsOf(raw, BLOCK_USED, words)
  for (const [name, value] of Object.entries(stats)) facts.push([words(name), text(value)])
  if (raw.possible_keys !== undefined) facts.push(["Possible keys", text(raw.possible_keys)])
  return {
    title,
    target: target.length > 0 ? target.join(" ") : undefined,
    notes,
    cost,
    rows: num(raw.rows_examined_per_scan) ?? num(raw.rows),
    actualRows: actual === undefined ? undefined : actual * (loops ?? 1),
    // A block's time is everything under it; a sort's and a table's is their own.
    timeMs: key === "query_block" ? num(raw.r_total_time_ms) : undefined,
    ownTimeMs:
      key === "query_block"
        ? undefined
        : (num(raw.r_total_time_ms) ??
          (tableTime !== undefined ? tableTime + (otherTime ?? 0) : undefined)),
    loops: loops ?? num(raw.loops),
    facts,
    children,
  }
}

/* ------------------------------------------------------------- read one */

function measured(node: PlanNode): boolean {
  return (
    node.actualRows !== undefined ||
    node.timeMs !== undefined ||
    node.ownTimeMs !== undefined ||
    node.children.some(measured)
  )
}

/** A JSON plan as a tree; null for a shape this does not know. */
export function planOf(plan: unknown): Plan | null {
  if (Array.isArray(plan) && isDict(plan[0]) && isDict(plan[0].Plan)) {
    const root = postgresNode(plan[0].Plan)
    return {
      root,
      analyzed: measured(root),
      planningMs: num(plan[0]["Planning Time"]),
      executionMs: num(plan[0]["Execution Time"]),
    }
  }
  if (isDict(plan) && isDict(plan.query_block)) {
    const root = blockNode("query_block", plan.query_block)
    const optimization = isDict(plan.query_optimization) ? plan.query_optimization : {}
    return {
      root,
      analyzed: measured(root),
      planningMs: num(optimization.r_total_time_ms),
      executionMs: num(plan.query_block.r_total_time_ms),
    }
  }
  if (isDict(plan) && (typeof plan.operation === "string" || Array.isArray(plan.inputs))) {
    const root = treeNode(plan)
    return { root, analyzed: measured(root), executionMs: root.timeMs }
  }
  return null
}

/**
 * A plan that came as rows naming their own parent: SQL Server's SHOWPLAN
 * (`NodeId`, `Parent`) and SQLite's EXPLAIN QUERY PLAN (`id`, `parent`).
 */
export function planFromRows(result: QueryResult): Plan | null {
  const at = (name: string) => result.columns.findIndex((column) => column.toLowerCase() === name)
  // A step is named by its operator; the row for the statement itself has none and is named by its kind.
  const shapes = [
    { id: at("nodeid"), parent: at("parent"), title: at("physicalop"), fallback: at("type") },
    { id: at("id"), parent: at("parent"), title: at("detail"), fallback: at("detail") },
  ]
  const shape = shapes.find((s) => s.id >= 0 && s.parent >= 0 && s.title >= 0)
  if (!shape || result.rows.length === 0) return null
  const argument = at("argument")
  const rows = at("estimaterows")
  const cost = at("totalsubtreecost")
  const nodes = new Map<string, PlanNode>()
  const order: { id: string; parent: string; node: PlanNode }[] = []
  for (const row of result.rows) {
    const id = String(row[shape.id])
    const step = row[shape.title]
    const node: PlanNode = {
      title: String(step ?? row[shape.fallback] ?? "Step").trim(),
      // The argument of a step is its detail; the statement's own row carries a counter there.
      notes:
        argument >= 0 && step !== null && row[argument] !== null ? [String(row[argument])] : [],
      rows: rows >= 0 ? num(row[rows]) : undefined,
      cost: cost >= 0 ? num(row[cost]) : undefined,
      facts: [],
      children: [],
    }
    result.columns.forEach((column, index) => {
      const lower = column.toLowerCase()
      if (
        [shape.id, shape.parent, shape.title, shape.fallback, argument, rows, cost].includes(index)
      )
        return
      if (["notused", "stmtid", "outputlist", "stmttext", "logicalop"].includes(lower)) return
      const value = row[index]
      if (value === null || value === false || value === "" || value === 0) return
      node.facts.push([column, text(value)])
    })
    nodes.set(id, node)
    order.push({ id, parent: String(row[shape.parent]), node })
  }
  const roots: PlanNode[] = []
  for (const { parent, node } of order) {
    const above = nodes.get(parent)
    if (above && above !== node) above.children.push(node)
    else roots.push(node)
  }
  if (roots.length === 0) return null
  const root =
    roots.length === 1 ? roots[0] : { title: "Query plan", notes: [], facts: [], children: roots }
  return { root, analyzed: false }
}

/** A plan that came as lines of text, as one text. Null when the rows are a table rather than lines. */
export function planText(result: QueryResult): string | null {
  if (result.columns.length !== 1) return null
  return result.rows.map((row) => (row[0] === null ? "" : String(row[0]))).join("\n")
}

/* ------------------------------------------------------- lay out to draw */

export interface PlanRow {
  /** Its place in the tree: "0", "0.1", "0.1.0". */
  id: string
  depth: number
  node: PlanNode
  /** For each level above the step's own, whether a line of the tree runs down through it. */
  rails: boolean[]
  last: boolean
  /** Cost and time with what is under the step filled in where the engine left it out. */
  cost?: number
  timeMs?: number
  /** The step's own share of the whole plan's cost and time, 0–1: what is under it left out. */
  costShare?: number
  timeShare?: number
  /** Its rows against the most any step handles, 0–1. */
  rowsShare?: number
}

type Totals = { cost?: number; timeMs?: number }

const sum = (values: (number | undefined)[]) =>
  values.some((value) => value !== undefined)
    ? values.reduce<number>((total, value) => total + (value ?? 0), 0)
    : undefined

/** The tree as rows, top to bottom, with the bars' proportions worked out. */
export function planRows(plan: Plan): PlanRow[] {
  const rows: PlanRow[] = []
  const inclusive = new Map<PlanNode, Totals>()
  const total = (node: PlanNode): Totals => {
    const below = node.children.map(total)
    const under = sum(below.map((child) => child.timeMs))
    const own: Totals = {
      cost: node.cost ?? sum(below.map((child) => child.cost)),
      timeMs: node.timeMs ?? (node.ownTimeMs !== undefined ? node.ownTimeMs + (under ?? 0) : under),
    }
    inclusive.set(node, own)
    return own
  }
  total(plan.root)
  // A step cannot take longer than the step it runs inside. MariaDB times a
  // sort that drives its own table read with the read included, and times
  // the table as well: added up, the two came to more than the statement
  // took. What a parent states is the most its steps can have taken, and what
  // is left of a step once its own steps are taken out is the step's own.
  const cap = (node: PlanNode, most: number | undefined) => {
    const own = inclusive.get(node) ?? {}
    if (own.timeMs !== undefined && most !== undefined && own.timeMs > most) {
      inclusive.set(node, { ...own, timeMs: most })
    }
    const mine = inclusive.get(node)?.timeMs
    for (const child of node.children) cap(child, mine ?? most)
  }
  cap(plan.root, undefined)
  const whole = inclusive.get(plan.root) ?? {}
  let mostRows = 0
  const walk = (node: PlanNode, id: string, depth: number, rails: boolean[], last: boolean) => {
    const own = inclusive.get(node) ?? {}
    const below = node.children.map((child) => inclusive.get(child) ?? {})
    const self = (mine: number | undefined, theirs: (number | undefined)[]) =>
      mine === undefined ? undefined : Math.max(0, mine - (sum(theirs) ?? 0))
    const selfCost = self(
      own.cost,
      below.map((child) => child.cost),
    )
    const left = self(
      own.timeMs,
      below.map((child) => child.timeMs),
    )
    const selfTime =
      node.ownTimeMs !== undefined && left !== undefined
        ? Math.min(node.ownTimeMs, left)
        : (node.ownTimeMs ?? left)
    const seen = node.actualRows ?? node.rows
    if (seen !== undefined) mostRows = Math.max(mostRows, seen)
    rows.push({
      id,
      depth,
      node,
      rails,
      last,
      cost: own.cost,
      timeMs: own.timeMs,
      costShare:
        selfCost !== undefined && whole.cost ? Math.min(1, selfCost / whole.cost) : undefined,
      timeShare:
        selfTime !== undefined && whole.timeMs ? Math.min(1, selfTime / whole.timeMs) : undefined,
    })
    node.children.forEach((child, index) =>
      walk(
        child,
        `${id}.${index}`,
        depth + 1,
        depth === 0 ? [] : [...rails, !last],
        index === node.children.length - 1,
      ),
    )
  }
  walk(plan.root, "0", 0, [], true)
  for (const row of rows) {
    const seen = row.node.actualRows ?? row.node.rows
    row.rowsShare = seen !== undefined && mostRows > 0 ? seen / mostRows : undefined
  }
  return rows
}
