import type { Plan, PlanNode } from "@/components/database/query/plan"

/**
 * The plans that arrive only as printed text, read into the same tree as the
 * rest: Oracle's DBMS_XPLAN table, MySQL's measured tree from before 8.3 (an
 * older server refuses its JSON form, and the page asks for the text), and
 * ClickHouse's indented steps. Each is recognised by its own unmistakable
 * shape; anything else stays the engine's text.
 */
export function planFromText(text: string): Plan | null {
  const lines = text.split("\n")
  return oraclePlan(lines) ?? mysqlPlan(lines) ?? clickhousePlan(lines)
}

const step = (title: string): PlanNode => ({ title, notes: [], facts: [], children: [] })

/** Each line's node under the last one a level less indented; null unless it is one tree. */
function nest(entries: { depth: number; node: PlanNode }[]): PlanNode | null {
  const open: PlanNode[] = []
  for (const { depth, node } of entries) {
    if (depth > open.length || (depth === 0 && open.length > 0)) return null
    open.length = depth
    open[depth - 1]?.children.push(node)
    open.push(node)
  }
  return open[0] ?? null
}

/* ------------------------------------------------------------- Oracle */

const ORACLE_HEAD = /^\|\s*Id\s*\|\s*Operation\s*\|/

const ORACLE_SCALE = { "": 1, K: 1e3, M: 1e6, G: 1e9, T: 1e12 }

/** "2500", "100K", "3M": the planner's counts, shortened past six figures. */
function oracleCount(cell: string): number | undefined {
  const match = /^(\d+(?:\.\d+)?)\s*([KMGT]?)$/.exec(cell.trim())
  if (!match) return undefined
  return Number(match[1]) * ORACLE_SCALE[match[2] as keyof typeof ORACLE_SCALE]
}

/**
 * DBMS_XPLAN.DISPLAY: a table of steps whose Operation is indented one space
 * a level, the conditions of each step listed under it by its Id, and the
 * optimizer's notes. Its cost is the step's and everything under it, as the
 * other engines state theirs; its time is the optimizer's guess at the
 * elapsed time, so it is a fact and not a measurement.
 */
function oraclePlan(lines: string[]): Plan | null {
  const head = lines.findIndex((line) => ORACLE_HEAD.test(line))
  if (head < 0) return null
  const cells = (line: string) => line.split("|").slice(1, -1)
  const columns = cells(lines[head]).map((cell) => cell.trim())
  const byId = new Map<string, PlanNode>()
  const entries: { depth: number; node: PlanNode }[] = []
  let at = head + 1
  for (; at < lines.length; at++) {
    const line = lines[at]
    if (/^-+$/.test(line.trim())) {
      if (entries.length > 0) break
      continue
    }
    if (!line.startsWith("|")) break
    const row = cells(line)
    const value = (name: string) => row[columns.indexOf(name)] ?? ""
    const id = value("Id").replace("*", "").trim()
    const operation = value("Operation").slice(1)
    const node = step(operation.trim())
    const name = value("Name").trim()
    if (name) node.target = name
    node.rows = oracleCount(value("Rows"))
    const cost = /^\s*(\d+(?:\.\d+)?)/.exec(value("Cost (%CPU)"))
    if (cost) node.cost = Number(cost[1])
    columns.forEach((column, index) => {
      if (["Id", "Operation", "Name", "Rows", "Cost (%CPU)"].includes(column)) return
      const cell = (row[index] ?? "").trim()
      if (cell) node.facts.push([column === "Time" ? "Estimated time" : column, cell])
    })
    byId.set(id, node)
    entries.push({ depth: operation.length - operation.trimStart().length, node })
  }
  const root = nest(entries)
  if (!root) return null
  // "   2 - access(…)" opens a step's conditions; a line indented under it is
  // its next condition, or the rest of a long one.
  let section = ""
  let open: { node: PlanNode; index: number } | null = null
  for (; at < lines.length; at++) {
    const line = lines[at]
    const content = line.trim()
    if (!content || /^-+$/.test(content)) continue
    if (/^\S/.test(line)) {
      section = content
      open = null
      continue
    }
    if (section.startsWith("Predicate Information")) {
      const opens = /^(\d+) - (.*)$/.exec(content)
      if (opens) {
        const node = byId.get(opens[1])
        open = node ? { node, index: node.notes.push(opens[2]) - 1 } : null
      } else if (open && /^(access|filter)\(/.test(content)) {
        open.index = open.node.notes.push(content) - 1
      } else if (open) {
        open.node.notes[open.index] += ` ${content}`
      }
    } else if (section === "Note") {
      root.notes.push(content.replace(/^- /, ""))
    }
  }
  return { root, analyzed: false }
}

/* ---------------------------------------------- MySQL's measured tree */

const MYSQL_LINE = /^( *)-> (.*)$/
const MYSQL_COST = /\s*\(cost=(?:[\d.e+-]+\.\.)?([\d.e+-]+) rows=([\d.e+-]+)\)/
const MYSQL_ACTUAL =
  /\s*\(actual (?:time=[\d.e+-]+\.\.([\d.e+-]+) )?rows=([\d.e+-]+) loops=(\d+)\)|\s*\(never executed\)/

/**
 * EXPLAIN ANALYZE as MySQL prints it: a step a line, four spaces a level,
 * its estimate in one parenthesis and what happened in the next. Rows and
 * time are what one loop averaged, so the whole run is that times the loops.
 */
function mysqlPlan(lines: string[]): Plan | null {
  const steps = lines.filter((line) => line.trim() !== "")
  if (steps.length === 0 || !steps.every((line) => MYSQL_LINE.test(line))) return null
  const entries = steps.map((line) => {
    const [, indent, rest] = MYSQL_LINE.exec(line) ?? []
    const estimate = MYSQL_COST.exec(rest)
    const actual = MYSQL_ACTUAL.exec(rest)
    const operation = rest.replace(MYSQL_COST, "").replace(MYSQL_ACTUAL, "").trim()
    const node = mysqlStep(operation)
    if (estimate) {
      node.cost = Number(estimate[1])
      node.rows = Number(estimate[2])
    }
    if (actual?.[0].includes("never executed")) {
      node.loops = 0
      node.actualRows = 0
    } else if (actual) {
      node.loops = Number(actual[3])
      node.actualRows = Number(actual[2]) * node.loops
      if (actual[1] !== undefined) node.timeMs = Number(actual[1]) * node.loops
    }
    return { depth: indent.length / 4, node }
  })
  if (entries.some((entry) => !Number.isInteger(entry.depth))) return null
  const root = nest(entries)
  if (!root) return null
  const analyzed = entries.some((entry) => entry.node.actualRows !== undefined)
  return { root, analyzed, executionMs: root.timeMs }
}

/**
 * "Index scan on o using PRIMARY" is a step on a table; "Filter: (…)" and
 * "Inner hash join (…)" are a step and its detail, drawn under it as the
 * measured JSON's are.
 */
function mysqlStep(operation: string): PlanNode {
  const cut = /^(.*?)(:\s| \(| on )/.exec(operation)
  if (!cut) return withStops(step(operation))
  const node = step(cut[1])
  if (cut[2] === " on ") {
    const rest = operation.slice(cut[0].length)
    const detail = rest.indexOf(" (")
    node.target = detail < 0 ? rest : rest.slice(0, detail)
    if (detail >= 0) node.notes.push(operation)
  } else {
    node.notes.push(operation)
  }
  return withStops(node)
}

function withStops(node: PlanNode): PlanNode {
  if (/^Limit/.test(node.title)) node.stops = "inputs"
  else if (/^Nested loop (semi|anti)join/.test(node.title)) node.stops = "inner"
  return node
}

/* --------------------------------------------------------- ClickHouse */

const CLICKHOUSE_LINE = /^( *)([A-Z][A-Za-z0-9]*)(?: \((.*)\))?$/

/**
 * ClickHouse's EXPLAIN: a step a line, two spaces a level, each named by one
 * word with what it is for in a parenthesis — the table, for a step that
 * reads one. It states no cost and no rows, so the tree is the steps alone.
 */
function clickhousePlan(lines: string[]): Plan | null {
  const steps = lines.filter((line) => line.trim() !== "")
  if (steps.length === 0 || !steps.every((line) => CLICKHOUSE_LINE.test(line))) return null
  const entries = steps.map((line) => {
    const [, indent, name, about] = CLICKHOUSE_LINE.exec(line) ?? []
    const node = step(name)
    // "((A + B))" is one parenthesis around a description that has its own.
    const said =
      about && /^\(.*\)$/.test(about) && balanced(about.slice(1, -1)) ? about.slice(1, -1) : about
    if (said && name.startsWith("ReadFrom")) node.target = said
    else if (said) node.notes.push(said)
    return { depth: indent.length / 2, node }
  })
  if (entries.some((entry) => !Number.isInteger(entry.depth))) return null
  const root = nest(entries)
  return root ? { root, analyzed: false } : null
}

function balanced(text: string): boolean {
  let depth = 0
  for (const char of text) {
    if (char === "(") depth++
    else if (char === ")" && --depth < 0) return false
  }
  return depth === 0
}
