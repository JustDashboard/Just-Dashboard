import { del, get, post, put } from "@/lib/api"
import { forgetMemoryState, forgetSessionState } from "@/lib/view-state"
import {
  defaultConfiguration,
  discoveredEnvironmentRows,
} from "@/components/deploy/deployment-defaults"
import { DEPLOYMENT_NAME } from "@/components/deploy/vocabulary"
import type {
  DeploymentConfiguration,
  DeploymentDetection,
  DeploymentDetectionCandidate,
  DeploymentDraft,
  DeploymentDraftSource,
  DeploymentHostnameSuggestion,
  DeploymentPreflight,
  WorkloadProfile,
} from "@/lib/types"

/** The five ways into a new project — the source strip's own tab keys. */
export type SourceTabKey = "git" | "image" | "template" | "database" | "compose"

/**
 * The draft state machine every source drives the same way.
 *
 * A source tab's only job is to produce a `DeploymentDraftSource` and hand it
 * to `inspectSource`, which creates the draft, names it, saves the source, and
 * detects — the same four requests `quick-deploy.tsx` made inline. Configure
 * then owns the draft for the rest of its life: re-saving the source when the
 * branch (or a blueprint input) changes, saving the configuration, running
 * preflight, and committing. One place drives the API so five sources cannot
 * disagree about the sequence.
 */

export type ImportPreview = {
  name: string
  unsupported: string[]
  warnings: string[]
  wouldChange: string[]
}

export type DraftCommitResult = {
  projectId: number
  environmentId: number
  planRevision: number
  created: boolean
}

export async function createDraft() {
  return post<DeploymentDraft>("/deploy/drafts", {})
}

export async function loadDraft(id: string) {
  return get<DeploymentDraft>(`/deploy/drafts/${id}`)
}

export async function saveIntent(
  draft: DeploymentDraft,
  intent: { name: string; profile: WorkloadProfile },
) {
  return put<DeploymentDraft>(`/deploy/drafts/${draft.id}`, {
    revision: draft.revision,
    step: "intent",
    intent,
  })
}

export async function saveSource(draft: DeploymentDraft, source: DeploymentDraftSource) {
  return put<DeploymentDraft>(`/deploy/drafts/${draft.id}`, {
    revision: draft.revision,
    step: "source",
    source,
  })
}

export async function detectSource(draft: DeploymentDraft, selectedId?: string) {
  return post<DeploymentDraft>(`/deploy/drafts/${draft.id}/detect`, {
    revision: draft.revision,
    selectedId,
  })
}

export async function saveConfiguration(
  draft: DeploymentDraft,
  configuration: DeploymentConfiguration,
) {
  return put<DeploymentDraft>(`/deploy/drafts/${draft.id}`, {
    revision: draft.revision,
    step: "configuration",
    configuration,
  })
}

export async function preflightDraft(draft: DeploymentDraft) {
  return post<{ draft: DeploymentDraft; preflight: DeploymentPreflight }>(
    `/deploy/drafts/${draft.id}/preflight`,
    { revision: draft.revision },
  )
}

export async function commitDraft(
  draft: DeploymentDraft,
  acknowledgedWarnings: string[],
  gitPolicy?: DraftGitPolicy,
) {
  return post<DraftCommitResult>(`/deploy/drafts/${draft.id}/commit`, {
    revision: draft.revision,
    acknowledgedWarnings,
    gitPolicy,
  })
}

/**
 * The automatic-deployment decision, taken while the project is being created.
 *
 * A Git deployment polls its branch from the moment it exists and the default
 * is to deploy every push, so until this travelled with the commit "manual
 * only" was a setting reachable only after the first unintended release.
 */
export type DraftGitPolicy = {
  automatic: boolean
  watchInclude: string[]
  watchExclude: string[]
  commitStatuses: boolean
}

/**
 * Throws away an unfinished setup.
 *
 * Every press of Import creates a draft on the server, so without this an
 * abandoned attempt sat in the resume list for its whole thirty-day life and
 * three tries at the same repository read as three pieces of unfinished work.
 * Best-effort at the call sites that clean up after themselves: failing to
 * tidy is not a reason to fail the thing the operator actually asked for.
 */
export async function discardDraft(id: string) {
  return del<void>(`/deploy/drafts/${id}`)
}

/**
 * Throws away a setup that has just been walked away from — a second source
 * inspected, or "Change source" pressed. Inspecting a different source is a
 * different project, not a revision of the first one.
 *
 * Deliberately silent: the draft may already be gone, expired, or committed in
 * another tab, and none of those is worth interrupting what the operator
 * actually asked for.
 */
export async function discardAbandoned(id: string | undefined) {
  if (!id) return
  try {
    await discardDraft(id)
  } catch {
    // Tidying is best-effort; the unfinished-setups list has its own Discard.
  }
}

export async function previewImport(source: DeploymentDraftSource) {
  return post<ImportPreview>("/deploy/import/preview", source)
}

export async function adoptImport(
  draft: DeploymentDraft,
  acknowledgedWarnings: string[],
  acknowledgedUnsupported: string[],
) {
  return post<DraftCommitResult>("/deploy/import/adopt", {
    draftId: draft.id,
    revision: draft.revision,
    acknowledgedWarnings,
    acknowledgedUnsupported,
  })
}

export async function importEnvironment(
  projectId: number,
  environmentId: number,
  revision: number,
  dotenv: string,
) {
  return post<{ desiredRevision: number }>(
    `/deploy/${projectId}/environments/${environmentId}/variables/import`,
    { revision, dotenv, sensitivity: "secret", scopes: ["runtime", "build"] },
  )
}

export async function enqueueDeploy(projectId: number, environmentId: number) {
  return post<{ id: number }>(`/deploy/${projectId}/environments/${environmentId}/runs`, {
    operation: "deploy",
  })
}

/** What a source tab hands to Configure once it has inspected. */
export type InspectOutcome = {
  draft: DeploymentDraft
  detection?: DeploymentDetection
  candidate?: DeploymentDetectionCandidate
  configuration: DeploymentConfiguration
}

/**
 * Everything Configure needs, carried from whichever source tab produced it.
 *
 * One shape for all five sources rather than one state tree per source: a
 * source tab's only job is to fill this in and hand it up, and Configure
 * never has to ask which tab it came from except for the three things that
 * genuinely differ (`sourceLabel`, `githubRepo`, `importPreview`).
 */
export type ConfigureFlow = {
  name: string
  profile: WorkloadProfile
  source: DeploymentDraftSource
  draft: DeploymentDraft
  candidate?: DeploymentDetectionCandidate
  detection?: DeploymentDetection
  configuration: DeploymentConfiguration
  /** What the source row names itself — "Wayy01/wesmokefish", an image, a blueprint. */
  sourceLabel: string
  /** Set when the repository came from the signed-in GitHub account: feeds the branch Select. */
  githubRepo?: string
  hostname?: DeploymentHostnameSuggestion
  /** Carried from the existing-workload tab through to the final adopt call. */
  importPreview?: ImportPreview
}

/**
 * The four screens Configure was split into, in order.
 *
 * One screen held the source controls, the name, the type, a ten-field build
 * fold, the environment editor, the public address, the automatic-deployment
 * switches, an "Advanced" fold holding seven more sections, and the findings —
 * somewhere past forty controls for a Git repository, all of them present
 * before the reader had answered the first one. They are the same controls,
 * dealt into the four questions a deployment actually asks: what is it, how
 * does it run, what does it need, and is it right.
 */
export type ConfigureStepKey = "project" | "runtime" | "variables" | "review" | "done"

export const CONFIGURE_STEPS: ConfigureStepKey[] = ["project", "runtime", "variables", "review"]

/** Where this step sits on the spine, whose first segment is the chooser. */
export function creationStepIndex(step: ConfigureStepKey) {
  // Past the last segment, so a finished sequence reads as every step done
  // rather than as one still standing on Review.
  return step === "done" ? CONFIGURE_STEPS.length + 1 : CONFIGURE_STEPS.indexOf(step) + 1
}

export function stepBefore(step: ConfigureStepKey) {
  const at = CONFIGURE_STEPS.indexOf(step)
  return at > 0 ? CONFIGURE_STEPS[at - 1] : undefined
}

export function stepAfter(step: ConfigureStepKey) {
  const at = CONFIGURE_STEPS.indexOf(step)
  return at >= 0 && at < CONFIGURE_STEPS.length - 1 ? CONFIGURE_STEPS[at + 1] : undefined
}

/**
 * Which of the four screens a freshly inspected source opens on.
 *
 * Splitting one screen into four buys the reader four small questions and
 * charges them three presses for it, and the repository whose detection
 * answered everything should not pay that: importing it used to be two
 * presses — Import, then Deploy — and the browser test that says so ("One
 * press: plan saved, preflight run, environment applied, release started") is
 * the contract this must not break.
 *
 * So the steps are the *correction* path. Everything detection left open
 * decides where the reader lands; when nothing is open they land on Review
 * with the whole plan read back and Deploy under it, and the earlier steps
 * read as done because they were **answered**, not because they were visited.
 */
export function landingStep(flow: ConfigureFlow, advanced = false): ConfigureStepKey {
  // `?mode=advanced` is a link asking for the settings that used to live
  // behind one fold; they are the runtime screen's three folds now.
  if (advanced) return "runtime"
  const { configuration, candidate, detection } = flow
  if (
    (detection?.candidates?.length ?? 0) > 1 ||
    (candidate?.needsDecision?.length ?? 0) > 0 ||
    Boolean(detection?.unavailable) ||
    !DEPLOYMENT_NAME.test(flow.name) ||
    (flow.profile === "static" &&
      configuration.build.method === "recipe" &&
      !configuration.build.outputDirectory?.trim())
  )
    return "project"
  // A worker answers no requests, and a Compose stack or an adopted workload
  // publishes whatever its own file or its own container already publishes —
  // so an unset `internalPort` is only an unanswered question for the profiles
  // that serve one.
  const serves = !["worker", "compose", "imported"].includes(flow.profile)
  if (serves && (configuration.runtime.internalPort ?? 0) === 0) return "runtime"
  // A blueprint's declared variables are review material — a generated
  // password, a EULA acceptance — the way the old screen opened Advanced for
  // one; and a variable the source was read as needing, with nothing in it,
  // is the one thing nobody else can answer.
  if (
    (flow.source.mode === "blueprint" && configuration.variables.length > 0) ||
    discoveredEnvironmentRows(candidate).some((row) => row.detected && !row.value)
  )
    return "variables"
  return "review"
}

/** What Configure hands back: a whole flow, nothing (back to the chooser), or an update of the current one. */
export type FlowUpdate =
  ConfigureFlow | null | ((current: ConfigureFlow | null) => ConfigureFlow | null)

/**
 * The environment as typed on Configure, held in memory for one draft. Never
 * Web Storage and never the URL: it is the one part of a setup that carries
 * secrets, and a reload is the one thing it does not survive.
 */
export type EnvironmentDraft = {
  draftId: string
  rows: EnvironmentRow[]
  dotenv: string
}

function effectiveConfiguration(
  fallbackProfile: WorkloadProfile,
  source: DeploymentDraftSource,
  draft: DeploymentDraft,
  candidate: DeploymentDetectionCandidate | undefined,
  detection: DeploymentDetection | undefined,
): DeploymentConfiguration {
  // A blueprint's plan is rendered by the server from the reviewed definition;
  // the browser only offers it for review, never recomputes it.
  if (source.mode === "blueprint" && draft.data.configuration) return draft.data.configuration
  return defaultConfiguration(candidate?.profile ?? fallbackProfile, candidate, source, detection)
}

/** Runs `/detect` against a draft already carrying a saved source, and derives
 * the outcome every caller below needs from its result. */
async function detectAndResolve(
  draft: DeploymentDraft,
  profile: WorkloadProfile,
  source: DeploymentDraftSource,
  selectedId?: string,
): Promise<InspectOutcome> {
  const detected = await detectSource(draft, selectedId)
  const detection = detected.data.detection
  const candidate =
    detection?.candidates.find((entry) => entry.id === detection.selectedId) ??
    detection?.candidates[0]
  return {
    draft: detected,
    detection,
    candidate,
    configuration: effectiveConfiguration(profile, source, detected, candidate, detection),
  }
}

/** create → name → source → detect: the sequence every source drives identically. */
export async function inspectSource(
  name: string,
  profile: WorkloadProfile,
  source: DeploymentDraftSource,
): Promise<InspectOutcome> {
  const created = await createDraft()
  const named = await saveIntent(created, { name, profile })
  const sourced = await saveSource(named, source)
  return detectAndResolve(sourced, profile, source)
}

/** Changing the branch, or a blueprint input: re-save the source, re-detect. */
export async function reinspect(
  draft: DeploymentDraft,
  profile: WorkloadProfile,
  source: DeploymentDraftSource,
): Promise<InspectOutcome> {
  const sourced = await saveSource(draft, source)
  return detectAndResolve(sourced, profile, source)
}

/** Re-runs detection against the same saved source, naming which candidate
 * the operator picked when more than one was found equally plausible. */
export async function selectCandidate(
  draft: DeploymentDraft,
  profile: WorkloadProfile,
  source: DeploymentDraftSource,
  selectedId: string,
): Promise<InspectOutcome> {
  return detectAndResolve(draft, profile, source, selectedId)
}

/** Best-effort; a hostname a reader never sees suggested is not worth failing the inspect over. */
export async function fetchHostnameSuggestion(name: string) {
  try {
    return await get<DeploymentHostnameSuggestion>(
      `/deploy/hostname?name=${encodeURIComponent(name)}`,
    )
  } catch {
    return undefined
  }
}

/**
 * What every source tab does once it knows what it is choosing: inspect, ask
 * for a hostname, and hand Configure a complete flow. The three fields a tab
 * cannot derive generically — how the source names itself, whether it is a
 * GitHub repository, an import's acknowledgement — are its own to supply.
 */
export async function inspectAndPrepare(
  name: string,
  profile: WorkloadProfile,
  source: DeploymentDraftSource,
  extra: { sourceLabel: string; githubRepo?: string; importPreview?: ImportPreview },
): Promise<ConfigureFlow> {
  const result = await inspectSource(name, profile, source)
  const hostname = await fetchHostnameSuggestion(name)
  const effectiveProfile = result.candidate?.profile ?? profile
  // HTTPS is offered by default because it is what anyone wants, but a
  // hostname is only pre-filled when one can actually be delivered: a worker
  // has no route, and a host with no address at all has nothing to suggest.
  const configuration =
    effectiveProfile !== "worker" &&
    result.configuration.domains.length === 0 &&
    hostname &&
    hostname.method !== "none"
      ? {
          ...result.configuration,
          domains: [
            {
              hostname: hostname.hostname.toLowerCase(),
              https: true,
              ownership: "managed" as const,
            },
          ],
        }
      : result.configuration
  return {
    name,
    profile: effectiveProfile,
    source,
    draft: result.draft,
    candidate: result.candidate,
    detection: result.detection,
    configuration,
    hostname,
    ...extra,
  }
}

// ---------------------------------------------------------------------------
// Resuming and forgetting
// ---------------------------------------------------------------------------

/** A github.com clone URL as `owner/name`, which is what the branch list is asked for. */
export function githubRepoOf(url: string | undefined) {
  const match = (url ?? "")
    .trim()
    .match(
      /^(?:https?:\/\/|ssh:\/\/(?:git@)?|git@)github\.com[/:]([^/\s]+)\/([^/\s]+?)(?:\.git)?\/?$/i,
    )
  return match ? `${match[1]}/${match[2]}` : undefined
}

/** What the source row calls a draft's source — a repository, an image, a blueprint. */
export function sourceLabelFromDraft(source: DeploymentDraftSource) {
  return (
    source.repository ||
    source.url ||
    source.image ||
    source.blueprintId ||
    source.resourceId ||
    source.localPath ||
    "Source"
  )
}

/**
 * A saved draft, back on Configure.
 *
 * Only the source is something a draft has to carry: the configuration is
 * saved at Deploy, so a draft abandoned by walking away from Configure has
 * none, and the unfinished-setups list used to refuse exactly those with
 * "has not reached configuration yet". The plan is derived from the saved
 * detection here, the way the source tab derived it; a draft with neither —
 * one that never reached detection — is detected first; and a duplicate,
 * which arrives with a configuration and no detection, is taken as it is.
 */
export async function resumeFlow(draft: DeploymentDraft): Promise<ConfigureFlow> {
  const { intent, source } = draft.data
  if (!intent || !source) {
    throw new Error("This draft has no source yet. Choose one to start again.")
  }
  let current = draft
  let detection = draft.data.detection
  let configuration = draft.data.configuration
  if (!detection && !configuration) {
    const result = await detectAndResolve(draft, intent.profile, source)
    current = result.draft
    detection = result.detection
    configuration = result.configuration
  }
  const candidate =
    detection?.candidates.find((entry) => entry.id === detection?.selectedId) ??
    detection?.candidates[0]
  configuration ??= effectiveConfiguration(intent.profile, source, current, candidate, detection)
  return {
    name: intent.name,
    profile: candidate?.profile ?? intent.profile,
    source,
    draft: current,
    candidate,
    detection,
    configuration,
    sourceLabel: sourceLabelFromDraft(source),
    githubRepo: source.kind === "git" ? (source.repository ?? githubRepoOf(source.url)) : undefined,
    hostname: await fetchHostnameSuggestion(intent.name),
  }
}

/**
 * What the new-project page remembers, and for how long. The source tab and
 * its form live under `deploy.new.<tab>.`, Configure's flow, findings and
 * disclosure under `deploy.new.configure.`, and the environment in memory
 * under the same prefix. "Change source" forgets Configure; a created
 * project forgets everything.
 */
export function forgetConfigure() {
  forgetSessionState("deploy.new.configure.")
  forgetMemoryState("deploy.new.configure.")
}

export function forgetNewProject() {
  forgetSessionState("deploy.new.")
  forgetMemoryState("deploy.new.")
}

// ---------------------------------------------------------------------------
// Naming
// ---------------------------------------------------------------------------

/** The last path segment of a clone URL, which is the project's name. */
export function repositoryName(url: string) {
  const trimmed = url
    .trim()
    .replace(/\.git$/, "")
    .replace(/\/+$/, "")
  return trimmed.split(/[/:]/).pop() || "app"
}

/** The repository half of an image reference, which is what to call it. */
export function imageName(reference: string) {
  const withoutTag = reference
    .trim()
    .split("@")[0]
    .replace(/:[^:/]+$/, "")
  const last = withoutTag.split("/").pop() ?? ""
  return last.replace(/[^A-Za-z0-9._-]/g, "-") || "app"
}

// ---------------------------------------------------------------------------
// Environment text
// ---------------------------------------------------------------------------

export type EnvironmentRow = {
  name: string
  value: string
  /** Set on a row detection listed: its example value, where it was read, and that it was not typed. */
  example?: string
  source?: string
  detected?: boolean
  /** The value was minted here rather than typed or copied from anywhere. */
  generated?: boolean
}

/** The typed rows and the pasted block, joined into one .env document. */
export function environmentText(rows: EnvironmentRow[], dotenv: string) {
  return [
    dotenv.trim(),
    ...rows
      .filter((row) => row.name.trim())
      .map((row) => `${row.name.trim()}=${JSON.stringify(row.value)}`),
  ]
    .filter(Boolean)
    .join("\n")
}
