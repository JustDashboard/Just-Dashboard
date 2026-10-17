import { plural } from "@/lib/format"
import type { DdlAnswer } from "@/components/database/schema/types"

/** One extension of `GET /databases/{id}/server/extensions`. */
export type DbExtension = {
  name: string
  version?: string
  /** The server's default version: what a create makes and an update reaches. */
  availableVersion?: string
  installed: boolean
  /** The schema it lives in; for a plugin catalogue, the kind of plugin. */
  schema?: string
  comment?: string
  /** What the dashboard's catalogue says of it. */
  category?: string
  about?: string
  /** The library the server has to load at start before it works. */
  library?: string
  /** A library with no extension to create: enabled is loaded at start. */
  module?: boolean
  /** In the catalogue, and not shipped by this server's image. */
  unavailable?: boolean
  /** Only a superuser may create it. */
  superuserOnly?: boolean
}

/** What the server loaded at start, and what it loads from its next start. */
export type PreloadState = {
  loaded: string[]
  configured: string[]
  pendingRestart: boolean
  /** The list can be changed from this connection; `reason` says why not. */
  settable: boolean
  reason?: string
}

export type DbExtensions = {
  extensions: DbExtension[]
  supported: boolean
  /** Enabling, updating and disabling work from here. */
  editable?: boolean
  reason?: string
  preload?: PreloadState
}

export type ExtensionVersion = {
  version: string
  superuser: boolean
  trusted: boolean
  relocatable: boolean
  /** The schema its control file fixes, where it fixes one. */
  schema?: string
  requires: string[]
  /** What it requires that the database lacks: CASCADE creates these first. */
  missing: string[]
}

export type ExtensionDependent = { kind: string; object: string; via?: string }

/** `GET /databases/{id}/server/extensions/{name}`: what a dialog reads before it acts. */
export type ExtensionDetail = DbExtension & {
  /** Newest first. */
  versions: ExtensionVersion[]
  /** The versions the installed one can be updated to, newest first. */
  updates: string[]
  schemas: string[]
  defaultSchema: string
  members: { kind: string; count: number }[]
  dependents: ExtensionDependent[]
  dependentsTotal: number
  drop?: string
  dropCascade?: string
  /** The connection signs in as a superuser. */
  superuser: boolean
}

/** What `POST /server/preload` plans and runs. */
export type PreloadAnswer = DdlAnswer & {
  library: string
  /** The list the server loads from its next start. */
  libraries: string[]
  notes: string[]
}

/** Which of the list is drawn. */
export type ExtensionShow = "enabled" | "available" | "missing" | "updates"

/** Where an extension that needs loading at start is in being enabled. */
export type PreloadStage = "blocked" | "load" | "restart" | "create" | "done"

/** The heading of an extension the catalogue does not place. */
export const OTHER = "Other"

/** A newer version is the server's default, as a fact about an installed extension. */
export function hasUpdate(extension: DbExtension): boolean {
  return (
    extension.installed &&
    !extension.module &&
    Boolean(extension.version) &&
    Boolean(extension.availableVersion) &&
    extension.availableVersion !== extension.version
  )
}

/** The list's own counts, for the chips and the section's head. */
export function extensionCounts(list: DbExtension[]) {
  return {
    enabled: list.filter((one) => one.installed).length,
    available: list.filter((one) => !one.installed && !one.unavailable).length,
    missing: list.filter((one) => one.unavailable).length,
    updates: list.filter(hasUpdate).length,
  }
}

function inView(extension: DbExtension, show: ExtensionShow): boolean {
  switch (show) {
    case "enabled":
      return extension.installed
    case "available":
      return !extension.installed && !extension.unavailable
    case "missing":
      return Boolean(extension.unavailable)
    case "updates":
      return hasUpdate(extension)
  }
}

/** The extensions a view draws, narrowed by the words typed and the category chosen. */
export function shownExtensions(
  list: DbExtension[],
  show: ExtensionShow,
  words: string,
  category: string,
): DbExtension[] {
  const needle = words.trim().toLowerCase()
  return list.filter(
    (one) =>
      inView(one, show) &&
      (!category || (one.category ?? OTHER) === category) &&
      (!needle ||
        one.name.toLowerCase().includes(needle) ||
        Boolean(one.about?.toLowerCase().includes(needle)) ||
        Boolean(one.comment?.toLowerCase().includes(needle)) ||
        Boolean(one.category?.toLowerCase().includes(needle))),
  )
}

/** The categories of a list, by name, with the ones the catalogue does not place last. */
export function extensionCategories(list: DbExtension[]): string[] {
  const named = [...new Set(list.map((one) => one.category ?? OTHER))]
  return named.sort((a, b) => (a === OTHER ? 1 : b === OTHER ? -1 : a.localeCompare(b)))
}

/** A list in its categories' order, each run under its heading. */
export function groupExtensions(list: DbExtension[]) {
  return extensionCategories(list).map((category) => ({
    category,
    extensions: list.filter((one) => (one.category ?? OTHER) === category),
  }))
}

/**
 * Where enabling an extension that needs a library at start has got to, from
 * what the server says now — so a dialog closed half-way opens again on the
 * step it was at, whichever page or tab took the step before it.
 */
export function preloadStage(extension: DbExtension, preload: PreloadState): PreloadStage {
  const library = extension.library ?? ""
  if (preload.loaded.includes(library)) {
    return extension.module || extension.installed ? "done" : "create"
  }
  if (preload.configured.includes(library)) return "restart"
  return preload.settable ? "load" : "blocked"
}

/** Whether enabling it is more than CREATE EXTENSION: its library is not loaded yet. */
export function needsLoading(extension: DbExtension, preload: PreloadState | undefined): boolean {
  if (!extension.library) return false
  if (extension.module) return true
  return !preload?.loaded.includes(extension.library)
}

/** What a restart would change: the libraries it starts loading, and the ones it stops. */
export function waitingLibraries(preload: PreloadState) {
  return {
    adding: preload.configured.filter((one) => !preload.loaded.includes(one)),
    removing: preload.loaded.filter((one) => !preload.configured.includes(one)),
  }
}

/** The engine's word for a kind of object, for more than one: "operator families". */
function kinds(kind: string): string {
  if (/(s|x|ch|sh)$/.test(kind)) return `${kind}es`
  if (/[^aeiou]y$/.test(kind)) return `${kind.slice(0, -1)}ies`
  return `${kind}s`
}

/** The kinds an engine names, as a reader counts them: "57 functions, 22 operators and 4 types". */
export function memberWords(members: { kind: string; count: number }[]): string {
  const words = members.map(({ kind, count }) => plural(count, kind, kinds(kind)))
  if (words.length <= 1) return words[0] ?? "nothing"
  return `${words.slice(0, -1).join(", ")} and ${words.at(-1)}`
}

/** A list of names as a sentence's object: "a", "a and b", "a, b and c". */
export function listWords(names: string[]): string {
  if (names.length <= 1) return names[0] ?? ""
  return `${names.slice(0, -1).join(", ")} and ${names.at(-1)}`
}

/**
 * The extensions PostgreSQL ships in its own contrib package, and its own
 * procedural language: the elephant is the honest mark for these, because
 * they are PostgreSQL's. Anything else is somebody else's project, drawn as
 * its own logo where one is bundled and as a plain piece of a puzzle where
 * none is — a mark is never guessed (`product-logo.tsx`).
 */
const POSTGRES_OWN = new Set([
  "adminpack",
  "amcheck",
  "auto_explain",
  "autoinc",
  "bloom",
  "btree_gin",
  "btree_gist",
  "citext",
  "cube",
  "dblink",
  "dict_int",
  "dict_xsyn",
  "earthdistance",
  "file_fdw",
  "fuzzystrmatch",
  "hstore",
  "insert_username",
  "intagg",
  "intarray",
  "isn",
  "lo",
  "ltree",
  "moddatetime",
  "old_snapshot",
  "pageinspect",
  "passwordcheck",
  "pg_buffercache",
  "pg_freespacemap",
  "pg_logicalinspect",
  "pg_prewarm",
  "pg_stat_statements",
  "pg_surgery",
  "pg_trgm",
  "pg_visibility",
  "pg_walinspect",
  "pgcrypto",
  "pgrowlocks",
  "pgstattuple",
  "plpgsql",
  "postgres_fdw",
  "refint",
  "seg",
  "sslinfo",
  "tablefunc",
  "tcn",
  "tsm_system_rows",
  "tsm_system_time",
  "unaccent",
  "uuid-ossp",
  "xml2",
])

/** The logo an extension is drawn as, or `undefined` for one with none bundled. */
export function extensionLogo(name: string, engineLogo: string | undefined, catalogue: boolean) {
  if (name.startsWith("timescaledb")) return "timescaledb"
  // A plugin catalogue is the server's own: MySQL's plugins are MySQL's.
  if (catalogue || POSTGRES_OWN.has(name)) return engineLogo
  return undefined
}
