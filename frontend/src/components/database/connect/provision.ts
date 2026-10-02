import type { Engine } from "@/components/database/engine"
import {
  ACCOUNT_NAME,
  CONTAINER_NAME,
  DATABASE_NAME,
  PROVISION_PASSWORD,
} from "@/components/database/connect/rules"
import type { DbProvisionRequest, DbProvisionTemplate } from "@/components/database/fleet/types"

/**
 * Starting a new database, decided apart from its drawing: which shelf an
 * engine stands on, what the form's answers send, which of them the server
 * would refuse, and the steps the work passes through.
 */

export type Shelf = "SQL" | "Documents" | "Key–value" | "Analytics"

export const SHELVES: readonly Shelf[] = ["SQL", "Documents", "Key–value", "Analytics"]

/**
 * The shelf an engine is picked from. A SQL engine whose rows are not edited
 * one at a time — a column store, loaded in batches and read in aggregate —
 * is an analytics store, and that is what the registry's `changeSets` says of
 * it. Before the catalogue has answered nothing is known of any of them, and
 * every SQL engine stands on the SQL shelf.
 */
export function shelfOf(engine: Engine, catalogued: boolean): Shelf {
  if (engine.kind === "document") return "Documents"
  if (engine.kind === "keyvalue" || engine.kind === "cache") return "Key–value"
  return catalogued && !engine.can("changeSets") ? "Analytics" : "SQL"
}

/** What the start form holds. Every answer has a default, so empty is a valid form. */
export type ProvisionDraft = {
  name: string
  version: string
  database: string
  user: string
  password: string
  /** Reachable from anywhere: the port on every interface and the firewall opened. */
  shared: boolean
}

export const EMPTY_PROVISION: ProvisionDraft = {
  name: "",
  version: "",
  database: "",
  user: "",
  password: "",
  shared: false,
}

/** The rules the server will hold the answers to, as one message per field that breaks one. */
export function provisionProblems(
  template: DbProvisionTemplate,
  draft: ProvisionDraft,
): Partial<Record<"name" | "database" | "user" | "password", string>> {
  const problems: Partial<Record<"name" | "database" | "user" | "password", string>> = {}
  const name = draft.name.trim()
  if (name && !CONTAINER_NAME.test(name)) {
    problems.name =
      "Letters, digits, dots, dashes and underscores, starting with a letter or digit."
  }
  const database = draft.database.trim()
  if (template.database && database && !DATABASE_NAME.test(database)) {
    problems.database = "Letters, digits and underscores, starting with a letter."
  }
  const user = draft.user.trim()
  if (template.defaultUser && user && !ACCOUNT_NAME.test(user)) {
    problems.user = "Letters, digits and underscores, starting with a letter."
  }
  if (draft.password && !PROVISION_PASSWORD.test(draft.password)) {
    problems.password = "8 to 128 of letters, digits and . _ ~ ! * + = , -"
  }
  return problems
}

/**
 * What the form sends. An answer left empty is left out, so the server's own
 * default stands; a field the engine has no use for is never sent, because
 * the server refuses a user for an engine with no accounts.
 */
export function provisionRequest(
  template: DbProvisionTemplate,
  draft: ProvisionDraft,
): DbProvisionRequest {
  const version = template.versions.some((one) => one.version === draft.version)
    ? draft.version
    : template.defaultVersion
  return {
    engine: template.engine,
    name: draft.name.trim() || undefined,
    version,
    database: template.database ? draft.database.trim() || undefined : undefined,
    user: template.defaultUser ? draft.user.trim() || undefined : undefined,
    password: draft.password || undefined,
    exposure: draft.shared ? "public" : "local",
  }
}

/** The image a draft would start. */
export function provisionImage(template: DbProvisionTemplate, draft: ProvisionDraft): string {
  return (
    template.versions.find((one) => one.version === draft.version)?.image ??
    template.versions.find((one) => one.version === template.defaultVersion)?.image ??
    template.image
  )
}

/**
 * The work, in the order it happens. The container is running a moment after
 * the first request returns; the engine inside it accepts connections some
 * seconds to a minute later, and there is no way to know which but to ask.
 */
export const PROVISION_STEPS = [
  { key: "start", label: "Start the container" },
  { key: "wait", label: "Wait for the engine" },
  { key: "connect", label: "Connect it" },
] as const

export type ProvisionPhase = (typeof PROVISION_STEPS)[number]["key"]

export const PHASE_SENTENCE: Record<ProvisionPhase, string> = {
  start: "Pulling the image and starting the container…",
  wait: "Waiting for the engine to accept connections…",
  connect: "Verifying the saved connection…",
}
