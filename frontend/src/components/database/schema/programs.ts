import type { DdlRequest } from "@/components/database/schema/changes"
import type { DbCatalog, SchemaObject } from "@/components/database/schema/types"
import type { DbDriver } from "@/lib/types"

/**
 * Functions, procedures and triggers: what their forms ask for on each engine,
 * and the request each one ends in.
 *
 * Nothing here writes SQL. The server renders the statement from these fields
 * and the form shows what it rendered; what is decided here is which fields an
 * engine has a word for, and whether the form is filled in far enough to ask.
 */

export type RoutineKind = "function" | "procedure"
export type ArgMode = "in" | "out" | "inout"

export type ArgDraft = {
  /** Stable across edits and reorders; never sent. */
  key: string
  name: string
  mode: ArgMode
  type: string
  default: string
}

export type RoutineDraft = {
  kind: RoutineKind
  schema: string
  name: string
  args: ArgDraft[]
  returns: string
  language: string
  body: string
  volatility: string
  strict: boolean
  deterministic: boolean
  dataAccess: string
  security: string
}

/** The routine as `GET /program` states it. */
export type ProgramRoutine = {
  kind: RoutineKind
  schema: string
  name: string
  args: { name: string; mode: ArgMode; type: string; default?: string }[]
  returns?: string
  language?: string
  body: string
  volatility?: string
  strict?: boolean
  deterministic?: boolean
  dataAccess?: string
  security?: string
}

export type TriggerTiming = "before" | "after" | "instead of"
export type TriggerEvent = "insert" | "update" | "delete" | "truncate"

export type TriggerDraft = {
  schema: string
  name: string
  table: string
  timing: TriggerTiming
  events: TriggerEvent[]
  updateOf: string[]
  forEach: "row" | "statement"
  condition: string
  /** Postgres: `schema.name` of the function the trigger executes. */
  function: string
  /** Postgres: what is passed to it, one to a line. */
  functionArgs: string
  body: string
}

export type ProgramTrigger = {
  schema: string
  name: string
  table: string
  timing: TriggerTiming
  events: TriggerEvent[]
  updateOf?: string[]
  forEach: "row" | "statement"
  condition?: string
  function?: string
  functionSchema?: string
  functionArgs?: string[]
  body?: string
}

/** `GET /program`: a program as the form writes it, or why it cannot be edited here. */
export type DbProgram = {
  kind: "function" | "procedure" | "trigger"
  editable: boolean
  reason?: string
  routine?: ProgramRoutine
  trigger?: ProgramTrigger
}

/** What a routine form has for an engine; every field it has no word for is absent. */
export type RoutineAbilities = {
  language: boolean
  volatility: boolean
  strict: boolean
  deterministic: boolean
  dataAccess: boolean
  security: boolean
  /** Parameters can be OUT (and INOUT where the engine has it). */
  modes: ArgMode[]
  defaults: boolean
  /** The function names its return type; false where OUT parameters stand in for it. */
  returns: boolean
}

let serial = 0

export function blankArg(over: Partial<ArgDraft> = {}): ArgDraft {
  serial += 1
  return { key: `arg-${serial}`, name: "", mode: "in", type: "", default: "", ...over }
}

export function isBlankArg(arg: ArgDraft): boolean {
  return arg.name.trim() === "" && arg.type.trim() === "" && arg.default.trim() === ""
}

export function routineAbilities(driver: DbDriver | "", kind: RoutineKind): RoutineAbilities {
  const none: RoutineAbilities = {
    language: false,
    volatility: false,
    strict: false,
    deterministic: false,
    dataAccess: false,
    security: false,
    modes: [],
    defaults: false,
    returns: kind === "function",
  }
  switch (driver) {
    case "postgres":
      return {
        ...none,
        language: true,
        volatility: kind === "function",
        strict: kind === "function",
        security: true,
        modes: ["out", "inout"],
        defaults: true,
      }
    case "mysql":
      return {
        ...none,
        deterministic: kind === "function",
        dataAccess: true,
        security: true,
        modes: kind === "procedure" ? ["out", "inout"] : [],
      }
    case "sqlserver":
      return { ...none, security: true, modes: kind === "procedure" ? ["out"] : [], defaults: true }
    case "oracle":
      return {
        ...none,
        deterministic: kind === "function",
        security: true,
        modes: ["out", "inout"],
        defaults: true,
      }
    default:
      return none
  }
}

/** The body a new routine starts from: the engine's own frame, with nothing in it. */
export function starterBody(
  driver: DbDriver | "",
  kind: RoutineKind,
  language = "plpgsql",
): string {
  switch (driver) {
    case "postgres":
      if (language !== "plpgsql") return kind === "function" ? "SELECT NULL" : "SELECT 1"
      return kind === "function" ? "BEGIN\n  RETURN NULL;\nEND;" : "BEGIN\n  NULL;\nEND;"
    case "mysql":
      return kind === "function" ? "BEGIN\n  RETURN NULL;\nEND" : "BEGIN\n  SELECT 1;\nEND"
    case "sqlserver":
      return kind === "function" ? "BEGIN\n  RETURN NULL\nEND" : "SELECT 1"
    case "oracle":
      return kind === "function" ? "BEGIN\n  RETURN NULL;\nEND;" : "BEGIN\n  NULL;\nEND;"
    default:
      return ""
  }
}

export function blankRoutine(
  driver: DbDriver | "",
  kind: RoutineKind,
  schema: string,
): RoutineDraft {
  const language = driver === "postgres" ? "plpgsql" : ""
  return {
    kind,
    schema,
    name: "",
    args: [],
    returns: "",
    language,
    body: starterBody(driver, kind, language),
    volatility: "",
    strict: false,
    deterministic: false,
    dataAccess: "",
    security: "",
  }
}

export function routineDraftOf(program: ProgramRoutine): RoutineDraft {
  return {
    kind: program.kind,
    schema: program.schema,
    name: program.name,
    args: program.args.map((arg) =>
      blankArg({ name: arg.name, mode: arg.mode, type: arg.type, default: arg.default ?? "" }),
    ),
    returns: program.returns ?? "",
    language: program.language ?? "",
    body: program.body,
    volatility: program.volatility ?? "",
    strict: program.strict ?? false,
    deterministic: program.deterministic ?? false,
    dataAccess: program.dataAccess ?? "",
    security: program.security ?? "",
  }
}

export type RoutineProblems = {
  name?: string
  returns?: string
  body?: string
  /** By argument key. */
  args: Record<string, string>
  summary?: string
}

export function routineProblems(
  draft: RoutineDraft,
  abilities: RoutineAbilities,
  editing: boolean,
): RoutineProblems {
  const problems: RoutineProblems = { args: {} }
  if (!editing && draft.name.trim() === "") problems.name = "Name the routine."
  const outputs = draft.args.some((arg) => !isBlankArg(arg) && arg.mode !== "in")
  // Postgres lets OUT parameters stand in for a return type.
  if (
    draft.kind === "function" &&
    abilities.returns &&
    draft.returns.trim() === "" &&
    !(abilities.language && outputs)
  ) {
    problems.returns = "Say what it returns."
  }
  if (draft.body.trim() === "") problems.body = "Write the body."
  draft.args.forEach((arg, index) => {
    if (isBlankArg(arg)) return
    if (arg.type.trim() === "") problems.args[arg.key] = `Parameter ${index + 1} needs a type.`
    else if (arg.name.trim() === "" && !abilities.language) {
      problems.args[arg.key] = `Parameter ${index + 1} needs a name.`
    }
  })
  const first = [problems.name, problems.returns, ...Object.values(problems.args), problems.body]
  problems.summary = first.find((text) => text !== undefined)
  return problems
}

/**
 * The request that makes a routine, or — given `replaces` — replaces the one
 * with that signature. Null while the form is not filled in far enough to ask
 * the server for a statement.
 */
export function routineRequest(
  draft: RoutineDraft,
  abilities: RoutineAbilities,
  replaces?: { signature: string },
): DdlRequest | null {
  if (routineProblems(draft, abilities, Boolean(replaces)).summary) return null
  const body: Record<string, unknown> = {
    kind: draft.kind,
    schema: draft.schema,
    name: draft.name.trim(),
    args: draft.args
      .filter((arg) => !isBlankArg(arg))
      .map((arg) => {
        const sent: Record<string, string> = {
          name: arg.name.trim(),
          mode: abilities.modes.includes(arg.mode) ? arg.mode : "in",
          type: arg.type.trim(),
        }
        if (abilities.defaults && arg.default.trim() !== "") sent.default = arg.default.trim()
        return sent
      }),
    body: draft.body.trim(),
  }
  if (draft.kind === "function" && draft.returns.trim() !== "") body.returns = draft.returns.trim()
  if (abilities.language) body.language = draft.language.trim()
  if (abilities.volatility && draft.volatility !== "") body.volatility = draft.volatility
  if (abilities.strict && draft.strict) body.strict = true
  if (abilities.deterministic && draft.deterministic) body.deterministic = true
  if (abilities.dataAccess && draft.dataAccess !== "") body.dataAccess = draft.dataAccess
  if (abilities.security && draft.security !== "") body.security = draft.security
  if (replaces) body.replaces = { signature: replaces.signature }
  return { method: "POST", path: "/ddl/routine", body }
}

export function dropRoutineRequest(
  kind: RoutineKind,
  schema: string,
  name: string,
  signature: string,
): DdlRequest {
  const body: Record<string, unknown> = { kind, schema, name }
  if (signature) body.signature = signature
  return { method: "DELETE", path: "/ddl/routine", body }
}

/* --------------------------------------------------------------- triggers */

export type TriggerAbilities = {
  timings: TriggerTiming[]
  events: TriggerEvent[]
  /** Fires on one event, chosen from a list; every other engine takes several. */
  singleEvent: boolean
  /** Which of row and statement it can be; one means the form says it and does not ask. */
  forEach: ("row" | "statement")[]
  condition: boolean
  /** UPDATE OF columns (SQLite). */
  updateOf: boolean
  /** Executes a function that already exists (Postgres) rather than a body of its own. */
  function: boolean
}

export function triggerAbilities(driver: DbDriver | ""): TriggerAbilities {
  switch (driver) {
    case "postgres":
      return {
        timings: ["before", "after", "instead of"],
        events: ["insert", "update", "delete", "truncate"],
        singleEvent: false,
        forEach: ["row", "statement"],
        condition: true,
        updateOf: false,
        function: true,
      }
    case "mysql":
      return {
        timings: ["before", "after"],
        events: ["insert", "update", "delete"],
        singleEvent: true,
        forEach: ["row"],
        condition: false,
        updateOf: false,
        function: false,
      }
    case "sqlserver":
      return {
        timings: ["after", "instead of"],
        events: ["insert", "update", "delete"],
        singleEvent: false,
        forEach: ["statement"],
        condition: false,
        updateOf: false,
        function: false,
      }
    case "oracle":
      return {
        timings: ["before", "after", "instead of"],
        events: ["insert", "update", "delete"],
        singleEvent: false,
        forEach: ["row", "statement"],
        condition: true,
        updateOf: false,
        function: false,
      }
    default:
      return {
        timings: ["before", "after", "instead of"],
        events: ["insert", "update", "delete"],
        singleEvent: true,
        forEach: ["row"],
        condition: true,
        updateOf: driver === "sqlite",
        function: false,
      }
  }
}

export function starterTriggerBody(driver: DbDriver | ""): string {
  switch (driver) {
    case "mysql":
      return "BEGIN\n  SET @touched = 1;\nEND"
    case "sqlserver":
      return "SELECT 1"
    case "oracle":
      return "BEGIN\n  NULL;\nEND;"
    default:
      return ""
  }
}

export function blankTrigger(driver: DbDriver | "", schema: string, table = ""): TriggerDraft {
  const abilities = triggerAbilities(driver)
  return {
    schema,
    name: "",
    table,
    timing: abilities.timings.includes("after") ? "after" : abilities.timings[0],
    events: ["insert"],
    updateOf: [],
    forEach: abilities.forEach[0],
    condition: "",
    function: "",
    functionArgs: "",
    body: starterTriggerBody(driver),
  }
}

export function triggerDraftOf(program: ProgramTrigger): TriggerDraft {
  return {
    schema: program.schema,
    name: program.name,
    table: program.table,
    timing: program.timing,
    events: program.events,
    updateOf: program.updateOf ?? [],
    forEach: program.forEach,
    condition: program.condition ?? "",
    function: program.function
      ? program.functionSchema
        ? `${program.functionSchema}.${program.function}`
        : program.function
      : "",
    functionArgs: (program.functionArgs ?? []).join("\n"),
    body: program.body ?? "",
  }
}

export type TriggerProblems = {
  name?: string
  table?: string
  events?: string
  function?: string
  body?: string
  summary?: string
}

export function triggerProblems(
  draft: TriggerDraft,
  abilities: TriggerAbilities,
  editing: boolean,
): TriggerProblems {
  const problems: TriggerProblems = {}
  if (!editing && draft.name.trim() === "") problems.name = "Name the trigger."
  if (draft.table === "") problems.table = "Choose the table it fires on."
  if (draft.events.length === 0) problems.events = "Choose at least one event."
  if (abilities.function) {
    if (draft.function === "") problems.function = "Choose the function it executes."
  } else if (draft.body.trim() === "") problems.body = "Write the body."
  problems.summary = [
    problems.name,
    problems.table,
    problems.events,
    problems.function,
    problems.body,
  ].find((text) => text !== undefined)
  return problems
}

/** `schema.name` split at the dot that separates them; a bare name has no schema. */
export function splitQualified(text: string): { schema: string; name: string } {
  const dot = text.indexOf(".")
  return dot < 0
    ? { schema: "", name: text }
    : { schema: text.slice(0, dot), name: text.slice(dot + 1) }
}

export function triggerRequest(
  draft: TriggerDraft,
  abilities: TriggerAbilities,
  editing: boolean,
): DdlRequest | null {
  if (triggerProblems(draft, abilities, editing).summary) return null
  const body: Record<string, unknown> = {
    schema: draft.schema,
    name: draft.name.trim(),
    table: draft.table,
    timing: draft.timing,
    events: draft.events,
    forEach: abilities.forEach.includes(draft.forEach) ? draft.forEach : abilities.forEach[0],
  }
  if (abilities.condition && draft.condition.trim() !== "") body.condition = draft.condition.trim()
  if (abilities.updateOf && draft.events.includes("update") && draft.updateOf.length > 0) {
    body.updateOf = draft.updateOf
  }
  if (abilities.function) {
    const target = splitQualified(draft.function)
    body.function = target.name
    if (target.schema) body.functionSchema = target.schema
    const args = draft.functionArgs
      .split("\n")
      .map((line) => line.trim())
      .filter((line) => line !== "")
    if (args.length > 0) body.functionArgs = args
  } else {
    body.body = draft.body.trim()
  }
  if (editing) body.replace = true
  return { method: "POST", path: "/ddl/trigger", body }
}

export function dropTriggerRequest(schema: string, name: string, table: string): DdlRequest {
  return { method: "DELETE", path: "/ddl/trigger", body: { schema, name, table } }
}

/** The names a trigger can be put on: the tables and views of the schema. */
export function triggerTables(catalog: DbCatalog | undefined): string[] {
  return [...(catalog?.objects.tables ?? []), ...(catalog?.objects.views ?? [])].map(
    (object) => object.name,
  )
}

/** Postgres: the functions that return `trigger`, as `schema.name`. */
export function triggerFunctions(catalog: DbCatalog | undefined): string[] {
  return ((catalog?.objects.functions ?? []) as SchemaObject[])
    .filter((object) => object.returns === "trigger")
    .map((object) => `${object.schema}.${object.name}`)
}

/** What the schema browser's address and verbs call each kind, and the operation that offers it. */
export const PROGRAM_OPERATION = {
  function: "functions",
  procedure: "procedures",
  trigger: "triggers",
} as const
