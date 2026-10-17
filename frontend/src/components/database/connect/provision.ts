import type { Engine } from "@/components/database/engine"
import {
  ACCOUNT_NAME,
  CONTAINER_NAME,
  DATABASE_NAME,
  PROVISION_PASSWORD,
} from "@/components/database/connect/rules"
import type {
  DbProvisionRelease,
  DbProvisionRequest,
  DbProvisionTemplate,
} from "@/components/database/fleet/types"

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

/** The release a draft would start: the one chosen, or the template's own. */
export function chosenRelease(
  template: DbProvisionTemplate,
  draft: ProvisionDraft,
): DbProvisionRelease | undefined {
  return (
    template.versions.find((one) => one.version === draft.version) ??
    template.versions.find((one) => one.version === template.defaultVersion)
  )
}

/**
 * The releases a template offers, as the picker sets them out: the ones still
 * maintained, and apart from them the ones past their end of life — kept on
 * the list for a server that has to match one it replaces, and never the
 * first thing offered. A release the table has no date for is offered with
 * the maintained ones; nothing is known against it.
 */
export function releaseShelves(template: DbProvisionTemplate): {
  current: DbProvisionRelease[]
  older: DbProvisionRelease[]
} {
  return {
    current: template.versions.filter((one) => !one.endOfLife?.past),
    older: template.versions.filter((one) => one.endOfLife?.past),
  }
}

/**
 * Whether starting a release means building its image first: one made on
 * this server from the dashboard's recipe, not built yet — or built from an
 * older recipe — and not built since the page asked.
 */
export function needsBuild(release: DbProvisionRelease | undefined, built: ReadonlySet<string>) {
  return Boolean(release?.build && !release.build.ready && !built.has(release.image))
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
  return {
    engine: template.engine,
    name: draft.name.trim() || undefined,
    version: chosenRelease(template, draft)?.version ?? template.defaultVersion,
    database: template.database ? draft.database.trim() || undefined : undefined,
    user: template.defaultUser ? draft.user.trim() || undefined : undefined,
    password: draft.password || undefined,
    exposure: draft.shared ? "public" : "local",
  }
}

/** The image a draft would start. */
export function provisionImage(template: DbProvisionTemplate, draft: ProvisionDraft): string {
  return chosenRelease(template, draft)?.image ?? template.image
}

/**
 * The work, in the order it happens. An image built on this server is built
 * first, as a job that goes on if the page is left. The container is running
 * a moment after the next request returns; the engine inside it accepts
 * connections some seconds to a minute later, and there is no way to know
 * which but to ask.
 */
const STEPS = [
  { key: "build", label: "Build the image" },
  { key: "start", label: "Start the container" },
  { key: "wait", label: "Wait for the engine" },
  { key: "connect", label: "Connect it" },
] as const

export type ProvisionPhase = (typeof STEPS)[number]["key"]

/** The steps of one start: the build only where there is an image to build. */
export function provisionSteps(build: boolean) {
  return build ? STEPS : STEPS.filter((step) => step.key !== "build")
}

const PHASE_SENTENCE: Record<ProvisionPhase, string> = {
  build: "Building the image on this server…",
  start: "Pulling the image and starting the container…",
  wait: "Waiting for the engine to accept connections…",
  connect: "Verifying the saved connection…",
}

/** What is happening now. An image built here is never pulled. */
export function phaseSentence(phase: ProvisionPhase, built: boolean): string {
  return built && phase === "start" ? "Starting the container…" : PHASE_SENTENCE[phase]
}
