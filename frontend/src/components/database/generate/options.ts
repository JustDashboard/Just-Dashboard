/**
 * Code generation, as the server describes it and as the page asks for it:
 * the targets and their switches come from `GET /databases/orm/targets`, and
 * nothing here knows one target from another. A target the server adds
 * tomorrow is drawn, configured and asked for with no change to this file.
 */

export type OrmGroup = string

export interface OrmOption {
  id: string
  label: string
  /** One sentence on what the switch does. */
  description: string
  type: "boolean" | "select" | "text"
  default: boolean | string
  choices?: { value: string; label: string }[]
}

export interface OrmTarget {
  id: string
  label: string
  /** The first file's name with the default options. */
  filename: string
  description: string
  language: string
  group: OrmGroup
  /** The drivers the target generates for. */
  engines: string[]
  /** Why not, for every SQL driver it does not. */
  unsupported: Record<string, string>
  options: OrmOption[]
}

export interface OrmFile {
  filename: string
  content: string
}

export interface OrmResult {
  target: string
  language: string
  files: OrmFile[]
  /** What the target could not express, or left out. */
  warnings: string[]
  counts: { tables: number; views: number; enums: number; relations: number }
}

export type OptionValues = Record<string, boolean | string>

/** The groups in the order the page lists them; one the server invents comes after. */
const GROUP_ORDER = ["ORM", "Query builder", "Types & validation", "Schema"]

const isOption = (value: unknown): value is OrmOption =>
  typeof value === "object" &&
  value !== null &&
  typeof (value as OrmOption).id === "string" &&
  ["boolean", "select", "text"].includes((value as OrmOption).type)

/** The catalogue as the page reads it, whatever came back: a server without the route answers with a list. */
export function readTargets(answer: unknown): OrmTarget[] {
  const listed = (answer as { targets?: unknown } | null)?.targets
  if (!Array.isArray(listed)) return []
  return listed
    .filter((entry): entry is OrmTarget => typeof entry?.id === "string")
    .map((entry) => ({
      id: entry.id,
      label: entry.label ?? entry.id,
      filename: entry.filename ?? "",
      description: entry.description ?? "",
      language: entry.language ?? "",
      group: entry.group ?? "Other",
      engines: Array.isArray(entry.engines) ? entry.engines : [],
      unsupported: entry.unsupported ?? {},
      options: Array.isArray(entry.options) ? entry.options.filter(isOption) : [],
    }))
}

export function readResult(answer: unknown): OrmResult {
  const given = (answer ?? {}) as Partial<OrmResult> & { schema?: string; filename?: string }
  const files = Array.isArray(given.files)
    ? given.files.filter((file) => typeof file?.filename === "string")
    : []
  return {
    target: given.target ?? "",
    language: given.language ?? "",
    // A server that predates `files` still answers with the one file it wrote.
    files:
      files.length > 0
        ? files
        : typeof given.schema === "string"
          ? [{ filename: given.filename ?? "schema", content: given.schema }]
          : [],
    warnings: Array.isArray(given.warnings) ? given.warnings : [],
    counts: {
      tables: given.counts?.tables ?? 0,
      views: given.counts?.views ?? 0,
      enums: given.counts?.enums ?? 0,
      relations: given.counts?.relations ?? 0,
    },
  }
}

/** The targets by group, in the page's order. */
export function groupTargets(
  targets: readonly OrmTarget[],
): { group: OrmGroup; targets: OrmTarget[] }[] {
  const groups = new Map<OrmGroup, OrmTarget[]>()
  for (const target of targets) {
    const held = groups.get(target.group)
    if (held) held.push(target)
    else groups.set(target.group, [target])
  }
  const rank = (group: OrmGroup) => {
    const at = GROUP_ORDER.indexOf(group)
    return at < 0 ? GROUP_ORDER.length : at
  }
  return [...groups.entries()]
    .sort(([a], [b]) => rank(a) - rank(b))
    .map(([group, list]) => ({ group, targets: list }))
}

/** Why a target cannot be generated for a driver; nothing when it can. */
export function unsupportedReason(target: OrmTarget, driver: string): string | undefined {
  if (target.engines.includes(driver)) return undefined
  return (
    (Object.hasOwn(target.unsupported, driver) ? target.unsupported[driver] : undefined) ??
    `${target.label} is not generated for this engine.`
  )
}

const PACKAGE = /^[a-z][a-z0-9_]{0,39}$/

/** Why a typed value would be refused; nothing when the server takes it. */
export function optionProblem(option: OrmOption, value: boolean | string): string | undefined {
  if (option.type !== "text" || typeof value !== "string") return undefined
  if (option.id === "package" && !PACKAGE.test(value)) {
    return "A lower-case Go package name: a letter, then letters, digits or underscores."
  }
  return undefined
}

/**
 * The value of each of a target's switches: what the reader last chose where
 * that is still a value the switch takes, the server's default otherwise. A
 * choice remembered from an older catalogue — a select value that is gone, a
 * boolean where there is now a list — is dropped rather than sent.
 */
export function resolveOptions(target: OrmTarget, chosen: OptionValues | undefined): OptionValues {
  const values: OptionValues = {}
  for (const option of target.options) {
    const kept = chosen && Object.hasOwn(chosen, option.id) ? chosen[option.id] : undefined
    const usable =
      option.type === "boolean"
        ? typeof kept === "boolean"
        : option.type === "select"
          ? typeof kept === "string" && (option.choices ?? []).some((c) => c.value === kept)
          : typeof kept === "string"
    values[option.id] = usable ? (kept as boolean | string) : option.default
  }
  return values
}

export interface Scope {
  /** The schemas read; none means the connection's own default. */
  schemas: string[]
  /** The tables kept, as `name` or `schema.name`; none means every one. */
  tables: string[]
}

/**
 * The request for a target with these switches. Only the switches the target
 * lists are sent — the server refuses any other — and only where they differ
 * from its default, so the request says what the reader changed.
 */
export function requestBody(
  target: OrmTarget,
  values: OptionValues,
  scope: Scope,
): Record<string, unknown> {
  const body: Record<string, unknown> = { target: target.id }
  if (scope.schemas.length === 1) body.schema = scope.schemas[0]
  else if (scope.schemas.length > 1) body.schemas = scope.schemas
  if (scope.tables.length > 0) body.tables = scope.tables
  for (const option of target.options) {
    const value = values[option.id]
    if (value === undefined || value === option.default) continue
    if (optionProblem(option, value)) continue
    body[option.id] = value
  }
  return body
}

/** The first problem among a target's typed switches, if any: the request is not sent with one. */
export function firstProblem(target: OrmTarget, values: OptionValues): string | undefined {
  for (const option of target.options) {
    const problem = optionProblem(option, values[option.id] ?? option.default)
    if (problem) return `${option.label}: ${problem}`
  }
  return undefined
}

/** The editor's grammar for a generated file. The editor has none for a Prisma schema: it is read as plain text. */
export function editorLanguage(language: string): string {
  return language === "prisma" ? "plaintext" : language || "plaintext"
}

/** How a table is named in the request: bare while one schema is read, with its schema once several are. */
export function tableName(schema: string, name: string, several: boolean): string {
  return several && schema ? `${schema}.${name}` : name
}

const count = (n: number, one: string, many = `${one}s`) =>
  `${n.toLocaleString("en-US")} ${n === 1 ? one : many}`

/** What the files hold, in a line: "7 tables · 1 view · 2 enums · 6 relations". */
export function countsLine(counts: OrmResult["counts"]): string {
  return [
    count(counts.tables, "table"),
    counts.views > 0 ? count(counts.views, "view") : "",
    counts.enums > 0 ? count(counts.enums, "enum") : "",
    counts.relations > 0 ? count(counts.relations, "relation") : "",
  ]
    .filter(Boolean)
    .join(" · ")
}

/**
 * The generator a visit opens on. The one the reader last used, where this
 * engine has it. One remembered from another connection that this engine has
 * no connector for is passed over for the first it does have — the page opens
 * on something that can be written, not on a refusal — unless the reader
 * picked it on this very visit, when the server's reason is what they asked
 * to see.
 */
export function startingTarget(
  targets: readonly OrmTarget[],
  stored: string,
  driver: string,
  pickedHere: boolean,
): OrmTarget | undefined {
  if (targets.length === 0) return undefined
  const kept = targets.find((entry) => entry.id === stored)
  if (kept && (pickedHere || kept.engines.includes(driver))) return kept
  return targets.find((entry) => entry.engines.includes(driver)) ?? kept ?? targets[0]
}

/**
 * What a target's switches are set to, in a line: only what differs from the
 * server's defaults, since the defaults are what nobody needs telling.
 * "Naming camelCase · Views on · Relations off".
 */
export function optionsLine(target: OrmTarget, values: OptionValues): string {
  const changed: string[] = []
  for (const option of target.options) {
    const value = values[option.id] ?? option.default
    if (value === option.default) continue
    if (option.type === "boolean") changed.push(`${option.label} ${value ? "on" : "off"}`)
    else if (option.type === "select") {
      const choice = (option.choices ?? []).find((entry) => entry.value === value)
      changed.push(`${option.label} ${choice?.label ?? String(value)}`)
    } else changed.push(`${option.label} ${String(value)}`)
  }
  return changed.length > 0 ? changed.join(" · ") : "As the generator sets them"
}
