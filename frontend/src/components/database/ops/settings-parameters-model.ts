import { bytes, duration } from "@/lib/format"

/**
 * A server's parameters, as one list a page can search, group and edit —
 * whichever route they came from. A SQL engine answers `GET /settings` with a
 * typed row per parameter; a key–value server answers `GET /redis/config`
 * with its own groups and no types. Both become a `Parameter` here, and the
 * page below never asks which it was. No React.
 */

/** One parameter of `GET /databases/{id}/settings`. */
export type DbSetting = {
  name: string
  value: string
  unit?: string
  category?: string
  description?: string
  /** Where the value came from, in the engine's own word. */
  source?: string
  /** A change only applies after a restart. */
  restartRequired?: boolean
  type?: "bool" | "integer" | "real" | "string" | "enum"
  default?: string
  min?: string
  max?: string
  enum?: string[]
  context?: string
  /** The configured value differs from the running one. */
  pendingRestart?: boolean
  /** Not the default. */
  changed?: boolean
  /** `PUT /settings` would be accepted for this name. */
  editable: boolean
  /** The value is withheld from this viewer. */
  redacted?: boolean
}

export type DbSettings = {
  settings: DbSetting[]
  supported: boolean
  all?: boolean
  /** The engine can persist a change. */
  writable?: boolean
  reason?: string
}

/** What `PUT /databases/{id}/settings` did. */
export type DbSettingChange = {
  name: string
  /** What the engine reports afterwards. */
  value: string
  /** What ran. */
  statements: string[]
  /** It survives a restart. */
  persisted: boolean
  /** Stored, and not yet in effect. */
  restartRequired: boolean
  note?: string
}

/** `GET /databases/{id}/redis/config`. */
export type RedisConfig = {
  supported: boolean
  reason?: string
  groups: {
    name: string
    params: { name: string; value: string; secret?: true; set?: boolean }[]
  }[]
  configFile?: string
  /** False: a change cannot be written to a config file, and lasts until the restart. */
  rewritable: boolean
}

/** What `PUT /databases/{id}/redis/config` did. */
export type RedisConfigChange = {
  name: string
  secret: boolean
  rewritten: boolean
  rewriteError?: string
  notice?: string
}

export type Parameter = {
  name: string
  value: string
  unit?: string
  /** The heading it is listed under. */
  group: string
  /** The rest of the engine's own category, under that heading. */
  subgroup?: string
  description?: string
  type?: DbSetting["type"]
  options?: string[]
  min?: string
  max?: string
  default?: string
  context?: string
  source?: string
  /** A change is stored and takes effect only after a restart. */
  restart: boolean
  /** A stored value is waiting for a restart now. */
  pending: boolean
  /** Not the default. */
  changed: boolean
  editable: boolean
  /** A value that is never sent back: only whether one is set. */
  secret?: { set: boolean }
  /** The value is withheld from this viewer. */
  redacted: boolean
}

const OTHER = "Other"

function titled(word: string): string {
  return word ? word[0].toUpperCase() + word.slice(1) : word
}

/** A SQL engine's rows. Its category "A / B" is a heading and what is under it. */
export function fromSettings(settings: readonly DbSetting[]): Parameter[] {
  return settings.map((setting) => {
    const [group, ...rest] = (setting.category ?? "").split(" / ")
    return {
      name: setting.name,
      value: setting.value,
      unit: setting.unit,
      group: group ? titled(group) : OTHER,
      subgroup: rest.length > 0 ? rest.join(" / ") : undefined,
      description: setting.description,
      type: setting.type,
      options: setting.enum,
      min: setting.min,
      max: setting.max,
      default: setting.default,
      context: setting.context,
      source: setting.source,
      restart: Boolean(setting.restartRequired),
      pending: Boolean(setting.pendingRestart),
      changed: Boolean(setting.changed),
      editable: setting.editable,
      redacted: Boolean(setting.redacted),
    }
  })
}

/**
 * A key–value server's configuration. It publishes no types and no defaults;
 * every parameter is one it may be asked to set, and it has the last word on
 * the ones fixed at start.
 */
export function fromRedisConfig(config: RedisConfig): Parameter[] {
  return config.groups.flatMap((group) =>
    group.params.map((param) => ({
      name: param.name,
      value: param.value,
      group: titled(group.name),
      restart: false,
      pending: false,
      changed: false,
      editable: true,
      secret: param.secret ? { set: Boolean(param.set) } : undefined,
      redacted: false,
    })),
  )
}

export type ParameterGroup = {
  name: string
  parameters: Parameter[]
  changed: number
  pending: number
}

/** The list under its headings, in the headings' order, "Other" last. */
export function groupParameters(parameters: readonly Parameter[]): ParameterGroup[] {
  const groups = new Map<string, Parameter[]>()
  for (const parameter of parameters) {
    const held = groups.get(parameter.group)
    if (held) held.push(parameter)
    else groups.set(parameter.group, [parameter])
  }
  return [...groups.entries()]
    .map(([name, list]) => ({
      name,
      parameters: [...list].sort(
        (a, b) =>
          (a.subgroup ?? "").localeCompare(b.subgroup ?? "") || a.name.localeCompare(b.name),
      ),
      changed: list.filter((one) => one.changed).length,
      pending: list.filter((one) => one.pending).length,
    }))
    .sort(
      (a, b) => Number(a.name === OTHER) - Number(b.name === OTHER) || a.name.localeCompare(b.name),
    )
}

export type ParameterFilter = "" | "changed" | "pending" | "editable"

/** The parameters a search and a chip leave: every word typed is somewhere in it. */
export function filterParameters(
  parameters: readonly Parameter[],
  search: string,
  only: ParameterFilter,
): Parameter[] {
  const words = search.toLowerCase().split(/\s+/).filter(Boolean)
  return parameters.filter((one) => {
    if (only === "changed" && !one.changed) return false
    if (only === "pending" && !one.pending) return false
    if (only === "editable" && !one.editable) return false
    if (words.length === 0) return true
    const text = [one.name, one.description, one.group, one.subgroup, one.redacted ? "" : one.value]
      .join(" ")
      .toLowerCase()
    return words.every((word) => text.includes(word))
  })
}

const BYTE_UNITS: Record<string, number> = {
  B: 1,
  kB: 1024,
  MB: 1024 ** 2,
  GB: 1024 ** 3,
  TB: 1024 ** 4,
}
const SECOND_UNITS: Record<string, number> = {
  us: 1e-6,
  ms: 1e-3,
  s: 1,
  min: 60,
  h: 3600,
  d: 86400,
}

/**
 * A value in the unit a person thinks in, when the engine's own is not one:
 * `16384` of `8kB` pages is 128 MB, `300000 ms` is five minutes. Nothing when
 * the raw value already reads well, or is one of the engine's "off" numbers.
 */
export function readableValue(parameter: Pick<Parameter, "value" | "unit">): string | undefined {
  const { value, unit } = parameter
  if (!unit || !/^\d+(\.\d+)?$/.test(value)) return undefined
  const amount = Number(value)
  if (!Number.isFinite(amount) || amount <= 0) return undefined
  const pages = /^(\d+)(B|kB|MB|GB|TB)$/.exec(unit)
  const size = pages ? Number(pages[1]) * BYTE_UNITS[pages[2]] : BYTE_UNITS[unit]
  if (size !== undefined) {
    const total = amount * size
    return total >= 1024 && (unit !== "MB" || amount >= 1024) ? bytes(total) : undefined
  }
  const seconds = SECOND_UNITS[unit]
  if (seconds !== undefined) {
    const total = amount * seconds
    return total >= 60 && (unit !== "min" || amount >= 60) ? duration(total) : undefined
  }
  return undefined
}

const TRUE_WORDS = ["on", "true", "yes", "1"]
const FALSE_WORDS = ["off", "false", "no", "0"]

/** Whether a yes/no parameter is on, whichever pair of words the engine prints. */
export function isOn(value: string): boolean {
  return TRUE_WORDS.includes(value.trim().toLowerCase())
}

/**
 * The two words a yes/no parameter is set with: the engine's own pair, in its
 * own case, read off the value it has now (`ON`/`OFF`, `on`/`off`, `1`/`0`).
 */
export function boolWords(value: string): { on: string; off: string } {
  const lower = value.trim().toLowerCase()
  const at = Math.max(TRUE_WORDS.indexOf(lower), FALSE_WORDS.indexOf(lower))
  const index = at < 0 ? 0 : at
  const upper = value.trim() !== lower
  const cased = (word: string) => (upper ? word.toUpperCase() : word)
  return { on: cased(TRUE_WORDS[index]), off: cased(FALSE_WORDS[index]) }
}

/**
 * Why a typed value would be refused, in the bound's own words — or nothing,
 * when it is the server's to judge: a number written with a unit (`64MB`), in
 * octal or hexadecimal, or a parameter the engine publishes no type for.
 */
export function parameterProblem(parameter: Parameter, input: string): string | undefined {
  const typed = input.trim()
  if (parameter.secret) return typed ? undefined : "Type the new value."
  if (parameter.type === "bool") {
    return [...TRUE_WORDS, ...FALSE_WORDS].includes(typed.toLowerCase()) ? undefined : "On or off."
  }
  if (parameter.type === "enum" && parameter.options?.length) {
    return parameter.options.some((option) => option.toLowerCase() === typed.toLowerCase())
      ? undefined
      : `One of ${parameter.options.join(", ")}.`
  }
  if (parameter.type === "integer" || parameter.type === "real") {
    if (!typed) return "A number."
    const plain = parameter.type === "integer" ? /^-?\d+$/ : /^-?\d+(\.\d+)?$/
    // A unit, a leading zero or 0x: the engine's own notation, checked there.
    if (!plain.test(typed) || /^-?0\d/.test(typed)) {
      return /^-?(0x[0-9a-f]+|\d+(\.\d+)?\s*[A-Za-z]+|0\d+)$/i.test(typed)
        ? undefined
        : parameter.type === "integer"
          ? "A whole number."
          : "A number."
    }
    const below = parameter.min !== undefined && compareNumbers(typed, parameter.min) < 0
    const above = parameter.max !== undefined && compareNumbers(typed, parameter.max) > 0
    if (below || above) {
      return `Between ${parameter.min ?? "−∞"} and ${parameter.max ?? "∞"}${parameter.unit ? ` ${parameter.unit}` : ""}.`
    }
  }
  return undefined
}

/**
 * Two numbers in writing, compared without losing the digits a 64-bit bound
 * carries (`18446744073709551615` is not a number JavaScript holds exactly).
 */
function compareNumbers(a: string, b: string): number {
  const whole = /^-?\d+$/
  if (whole.test(a) && whole.test(b)) {
    const left = BigInt(a)
    const right = BigInt(b)
    return left < right ? -1 : left > right ? 1 : 0
  }
  return Number(a) - Number(b)
}

/** What a change did, in the sentence the page keeps beside the statement that ran. */
export function changeWords(change: DbSettingChange): string {
  const parts = [
    change.restartRequired
      ? "Stored. It takes effect when the server is restarted."
      : "In effect now.",
    change.persisted ? "" : "It is not kept across a restart.",
    change.note ?? "",
  ]
  return parts.filter(Boolean).join(" ")
}
