import { del, get, post, put } from "@/lib/api"
import { forgetMemoryState, forgetSessionState } from "@/lib/view-state"
import {
  defaultConfiguration,
  detectedVariableDeclarations,
  discoveredEnvironmentRows,
  rowNeedsOperator,
} from "@/components/deploy/deployment-defaults"
import {
  domainValue,
  synchronizePrimaryDomain,
} from "@/components/deploy/new-project/domain-bindings"
import { DEPLOYMENT_NAME } from "@/components/deploy/vocabulary"
import type {
  DeploymentConfiguration,
  DeploymentDetection,
  DeploymentDetectionCandidate,
  DeploymentDraft,
  DeploymentDraftSource,
  DeploymentHostnameSuggestion,
  DeploymentPreflight,
  DeploymentVariableSetup,
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
  dotenv?: string,
  retainEnvironmentKeys?: string[],
) {
  return put<DeploymentDraft>(`/deploy/drafts/${draft.id}`, {
    revision: draft.revision,
    step: "configuration",
    configuration,
    dotenv,
    retainEnvironmentKeys,
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
 * Whether the plan's own declared variables are review material.
 *
 * Two kinds of variable land in this list and neither can be answered
 * anywhere else: a blueprint's declared inputs — a generated password, a EULA
 * acceptance — and the `${VAR}` references a Compose file was read as
 * needing, which arrive required with nothing in them. Preflight refuses the
 * second kind at Deploy as `variable_required_*`, so a setup that lands
 * anywhere but the variables screen is one the reader is sent back from.
 *
 * Shared with the variables screen's `referencesOpen`: landing there with the
 * fold that holds the answer shut is the same defect as not landing there.
 */
export function declaredVariablesNeedReview(flow: ConfigureFlow) {
  const declared = flow.configuration.variables
  return (
    (flow.source.mode === "blueprint" && declared.length > 0) ||
    declared.some(
      (variable) =>
        variable.required && !variable.reference && !variable.value && !variable.generate,
    )
  )
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
    // What the repository is, or a better way to run it, is this screen's
    // to say: a library is not a plan to review, and a reviewed template of
    // the same application is offered beside the source.
    Boolean(candidate?.notDeployable) ||
    (detection?.alternatives?.length ?? 0) > 0 ||
    Boolean(detection?.unavailable) ||
    !DEPLOYMENT_NAME.test(flow.name) ||
    // The name was asked about while the source was inspected, so a collision
    // is known before the first screen is drawn. Without this the reader lands
    // on Review and is sent back here by the Deploy they just pressed.
    Boolean(flow.hostname?.nameTaken) ||
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
    declaredVariablesNeedReview(flow) ||
    discoveredEnvironmentRows(candidate).some(rowNeedsOperator)
  )
    return "variables"
  return "review"
}

/** What Configure hands back: a whole flow, nothing (back to the chooser), or an update of the current one. */
export type FlowUpdate =
  ConfigureFlow | null | ((current: ConfigureFlow | null) => ConfigureFlow | null)

/** Session storage remembers the form, while credentials stay in the live memory copy. */
export function persistableFlow(flow: ConfigureFlow | null): ConfigureFlow | null {
  if (!flow) return null
  const safe = structuredClone(flow)
  const scrubSource = (source: DeploymentDraftSource | undefined) => {
    if (!source) return
    for (const document of source.composeFiles ?? []) document.content = ""
    if (source.url && /^[a-z]+:\/\//i.test(source.url)) {
      try {
        const url = new URL(source.url)
        if (url.password || (url.protocol.startsWith("http") && url.username)) {
          url.username = ""
          url.password = ""
          source.url = url.toString()
        }
      } catch {
        source.url = ""
      }
    }
  }
  const scrubConfiguration = (configuration: DeploymentConfiguration | undefined) => {
    if (!configuration) return
    for (const domain of configuration.domains) {
      if (domain.protection) delete domain.protection.password
    }
    for (const variable of configuration.variables) {
      if (variable.sensitivity === "secret") delete variable.value
    }
  }
  scrubSource(safe.source)
  scrubSource(safe.draft.data.source)
  scrubConfiguration(safe.configuration)
  scrubConfiguration(safe.draft.data.configuration)
  if (safe.detection?.compose) safe.detection.compose.preview = ""
  if (safe.draft.data.detection?.compose) safe.draft.data.detection.compose.preview = ""
  safe.draft.planPreview = ""
  return safe
}

/**
 * The environment as typed on Configure, held in memory for one draft. Never
 * Web Storage and never the URL: it is the one part of a setup that carries
 * secrets, and a reload is the one thing it does not survive.
 */
export type EnvironmentDraft = {
  draftId: string
  rows: EnvironmentRow[]
  dotenv: string
  retainedKeys: string[]
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
  return defaultConfiguration(
    candidate?.profile ?? fallbackProfile,
    candidate,
    source,
    detection,
    draft.data.intent?.name,
    draft.id,
  )
}

type PlannedVariable = DeploymentConfiguration["variables"][number]

/**
 * An address that follows the domain has nothing to follow until one is
 * planned. Committed empty, it would be set to "" — which an application
 * reads as a value, unlike an unset variable: the runtime would inject
 * `NEXTAUTH_URL=`, on which next-auth 4 fails every sign-in request — so it
 * waits in the form, and preflight names it while it is missing.
 */
function awaitsDomain(variable: PlannedVariable) {
  return Boolean(
    variable.domainTemplate &&
    !variable.value &&
    !variable.reference &&
    !variable.generate &&
    !variable.required,
  )
}

/**
 * Match the saved plan so pruning an empty address never triggers another
 * preflight. A blueprint's variables are its reviewed definition's, kept as
 * rendered.
 */
export function configurationForSave(
  configuration: DeploymentConfiguration,
  source?: DeploymentDraftSource,
): DeploymentConfiguration {
  const blueprint = source?.kind === "blueprint" || source?.mode === "blueprint"
  return {
    ...configuration,
    domains: configuration.domains.filter((domain) => domain.hostname.trim()),
    variables: blueprint
      ? configuration.variables
      : configuration.variables.filter((variable) => !awaitsDomain(variable)),
    checks: configuration.checks.map((check) =>
      check.phase === "readiness" && check.kind === "http"
        ? { ...check, config: { ...(check.config ?? {}), port: undefined } }
        : check,
    ),
  }
}

/**
 * Puts the addresses `configurationForSave` held back into a plan the server
 * handed back, bound to whatever domain it plans. The saved copy never has
 * them, and without them a domain added after Review's automatic check has
 * nothing to bind: AUTH_URL or ORIGIN would reach the release unset while
 * its row still read "Follows the project's domain".
 */
export function withHeldDomainVariables(
  configuration: DeploymentConfiguration,
  held: PlannedVariable[],
): DeploymentConfiguration {
  const present = new Set(configuration.variables.map((variable) => variable.name))
  const missing = held.filter((variable) => awaitsDomain(variable) && !present.has(variable.name))
  if (!missing.length) return configuration
  return {
    ...configuration,
    variables: [
      ...configuration.variables,
      ...missing.map((variable) => ({
        ...variable,
        value: domainValue(variable.domainTemplate ?? "", configuration.domains[0]),
      })),
    ],
  }
}

/** Non-HTTP templates must never receive a Caddy route to their database or game port. */
export function withSuggestedHostname(
  configuration: DeploymentConfiguration,
  profile: WorkloadProfile,
  source: DeploymentDraftSource,
  hostname?: DeploymentHostnameSuggestion,
): DeploymentConfiguration {
  const http =
    profile === "web" ||
    profile === "static" ||
    configuration.checks.some((check) => check.kind === "http")
  if (
    !http ||
    ["worker", "game", "compose", "imported"].includes(profile) ||
    source.kind === "compose" ||
    configuration.domains.length ||
    !hostname ||
    hostname.method === "none"
  )
    return configuration
  // Through the same binding a typed domain goes through, so the addresses
  // detection bound to the domain follow the suggested one from the start.
  return synchronizePrimaryDomain(configuration, [
    { hostname: hostname.hostname.toLowerCase(), https: true, ownership: "managed" },
  ])
}

/** Re-detection updates defaults; an explicit edit remains the operator's choice. */
export function mergeDetectedConfiguration(
  previous: DeploymentConfiguration,
  current: DeploymentConfiguration,
  detected: DeploymentConfiguration,
): DeploymentConfiguration {
  function merge(before: unknown, edited: unknown, after: unknown): unknown {
    if (JSON.stringify(before) === JSON.stringify(edited)) return after
    if (
      before &&
      edited &&
      after &&
      typeof before === "object" &&
      typeof edited === "object" &&
      typeof after === "object" &&
      !Array.isArray(before) &&
      !Array.isArray(edited) &&
      !Array.isArray(after)
    ) {
      const old = before as Record<string, unknown>
      const own = edited as Record<string, unknown>
      const next = after as Record<string, unknown>
      return Object.fromEntries(
        [...new Set([...Object.keys(own), ...Object.keys(next)])].map((key) => [
          key,
          merge(old[key], own[key], next[key]),
        ]),
      )
    }
    return edited
  }
  return merge(previous, current, detected) as DeploymentConfiguration
}

export function redetectedConfiguration(
  flow: ConfigureFlow,
  result: InspectOutcome,
  profile = result.candidate?.profile ?? flow.profile,
): DeploymentConfiguration {
  const previous = effectiveConfiguration(
    flow.profile,
    flow.source,
    flow.draft,
    flow.candidate,
    flow.detection,
  )
  const merged = mergeDetectedConfiguration(previous, flow.configuration, result.configuration)
  if (profile !== "worker") return merged
  return {
    ...merged,
    domains: [],
    runtime: { ...merged.runtime, internalPort: 0, hostPort: 0, strategy: "stop_first" },
    checks: result.configuration.checks,
  }
}

/** Runs `/detect` against a draft already carrying a saved source, and derives
 * the outcome every caller below needs from its result. */
async function detectAndResolve(
  draft: DeploymentDraft,
  profile: WorkloadProfile,
  source: DeploymentDraftSource,
  selectedId?: string,
): Promise<InspectOutcome> {
  let detected = await detectSource(draft, selectedId)
  const detection = detected.data.detection
  const candidate =
    detection?.candidates.find((entry) => entry.id === detection.selectedId) ??
    detection?.candidates[0]
  const intent = detected.data.intent
  if (intent && candidate && intent.profile !== candidate.profile) {
    detected = await saveIntent(detected, { ...intent, profile: candidate.profile })
  }
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
  try {
    const named = await saveIntent(created, { name, profile })
    const sourced = await saveSource(named, source)
    return await detectAndResolve(sourced, profile, source)
  } catch (error) {
    // The draft exists from the first of those four requests, so a source that
    // cannot be read left one behind on every attempt — a repository URL typed
    // with a typo, a blueprint missing a required input — and three tries at
    // the same thing read as three pieces of unfinished work in the resume
    // list. A press that did not work is not an unfinished setup.
    await discardAbandoned(created.id)
    throw error
  }
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
  initialSource: DeploymentDraftSource,
  extra: { sourceLabel: string; githubRepo?: string; importPreview?: ImportPreview },
): Promise<ConfigureFlow> {
  let source = initialSource
  let result = await inspectSource(name, profile, source)
  // A theme submodule on the repository's own host, or images the build root
  // keeps in LFS, are part of what gets built: turning them on here is
  // answering a question detection already answered, the way the Source
  // section's switches would. A submodule on another host is still asked.
  const clone = automaticCloneOptions(result.detection, result.candidate, source)
  if (clone) {
    source = { ...source, ...clone }
    result = await reinspect(result.draft, profile, source)
  }
  const hostname = await fetchHostnameSuggestion(name)
  const effectiveProfile = result.candidate?.profile ?? profile
  // HTTPS is offered by default because it is what anyone wants, but a
  // hostname is only pre-filled when one can actually be delivered: a worker
  // has no route, and a host with no address at all has nothing to suggest.
  const configuration = withSuggestedHostname(
    result.configuration,
    effectiveProfile,
    source,
    hostname,
  )
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
  const profile =
    draft.data.configuration && source.kind !== "blueprint"
      ? intent.profile
      : (candidate?.profile ?? intent.profile)
  const hostname = await fetchHostnameSuggestion(intent.name)
  if (!draft.data.configuration)
    configuration = withSuggestedHostname(configuration, profile, source, hostname)
  else if (source.kind !== "blueprint")
    configuration = withHeldDomainVariables(
      configuration,
      detectedVariableDeclarations(candidate, profile),
    )
  return {
    name: intent.name,
    // A saved configuration records the operator's profile choice; detection
    // is a default only until that choice has been saved.
    profile,
    source,
    draft: current,
    candidate,
    detection,
    configuration,
    sourceLabel: sourceLabelFromDraft(source),
    githubRepo: source.kind === "git" ? (source.repository ?? githubRepoOf(source.url)) : undefined,
    hostname,
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
  /** Why the value was filled in, said under it. */
  note?: string
  /** How the plan answers the row when nothing is typed, and why (`DeploymentDetectedVariable.setup`). */
  setup?: DeploymentVariableSetup
  reason?: string
  /** The application cannot start without it. */
  required?: boolean
  /** Compiled into the browser bundle, so its value is public. */
  browser?: boolean
  /** A committed file gives it a loopback value when nothing is set here. */
  localhostIn?: string
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

/** The further database URLs a Rails 8 application reads, as references to the linked server. */
export function railsDatabaseRows(
  connectionId: number,
  database: string | undefined,
  names: string[] = [],
): EnvironmentRow[] {
  if (!database || !/^[A-Za-z0-9_]+$/.test(database)) return []
  return names.map((name) => ({
    name,
    value: `\${{database.${connectionId}.url.${database}_${name.replace(/_DATABASE_URL$/, "").toLowerCase()}}}`,
  }))
}

// ---------------------------------------------------------------------------
// Repository shape
// ---------------------------------------------------------------------------

function underRoot(path: string, root: string) {
  return !root || path === root || path.startsWith(`${root}/`)
}

/**
 * The declared submodules a build root needs: those inside it, and the one
 * it is itself inside. Preflight judges them the same way.
 */
export function submodulesForRoot(
  requirements: DeploymentDetection["gitRequirements"] | undefined,
  root: string,
) {
  return (requirements?.submoduleList ?? []).filter(
    (submodule) => underRoot(submodule.path, root) || underRoot(root, submodule.path),
  )
}

/**
 * How many LFS-tracked files a build root holds. The repository root's count
 * is exact; a nested root is counted from the listed paths, and when the
 * list was cut short the count is unknown rather than zero.
 */
export function lfsFilesForRoot(
  requirements: DeploymentDetection["gitRequirements"] | undefined,
  root: string,
): number | undefined {
  if (!root) return requirements?.lfsFiles ?? 0
  const paths = requirements?.lfsPaths ?? []
  const listed = paths.filter((path) => underRoot(path, root)).length
  if (listed === 0 && (requirements?.lfsFiles ?? 0) > paths.length) return undefined
  return listed
}

/**
 * The clone options detection has already answered for the chosen root: its
 * submodules when every one it holds is on the repository's own host, and
 * Git LFS when files under it are tracked by LFS. Nothing when the source
 * already includes them, when a submodule needs access to another host, or
 * when the source is not a Git checkout.
 */
export function automaticCloneOptions(
  detection: DeploymentDetection | undefined,
  candidate: DeploymentDetectionCandidate | undefined,
  source: DeploymentDraftSource,
): Pick<DeploymentDraftSource, "includeSubmodules" | "includeLfs"> | undefined {
  if (!detection || (source.kind !== "git" && source.kind !== "local")) return undefined
  const root = candidate?.root ?? ""
  const requirements = detection.gitRequirements
  const options: Pick<DeploymentDraftSource, "includeSubmodules" | "includeLfs"> = {}
  const submodules = submodulesForRoot(requirements, root)
  if (
    requirements.submodulesChecked &&
    !source.includeSubmodules &&
    submodules.length > 0 &&
    submodules.every((submodule) => submodule.sameSource)
  )
    options.includeSubmodules = true
  const lfsFiles = lfsFilesForRoot(requirements, root)
  if (requirements.lfsChecked && !source.includeLfs && lfsFiles) options.includeLfs = true
  return Object.keys(options).length ? options : undefined
}

const NOT_DEPLOYABLE: Record<string, string> = {
  library: "a library",
  cli: "a command-line tool",
  "editor-extension": "an editor extension",
  "browser-extension": "a browser extension",
  "github-action": "a GitHub Action",
  "desktop-app": "a desktop app",
  "mobile-app": "a mobile app",
  notebook: "notebooks",
  "windows-only": "Windows-only",
}

/**
 * What the candidate chooser says about a candidate beyond its confidence:
 * that it is not a service, or why it ranks below the application.
 */
export function candidateStanding(candidate: DeploymentDetectionCandidate) {
  if (candidate.notDeployable)
    return `Not a service: ${NOT_DEPLOYABLE[candidate.notDeployable] ?? candidate.notDeployable}`
  if (candidate.demotion) return candidate.demotion
  if (candidate.recipeIssue && !candidate.recipe) return "No automatic recipe"
  return undefined
}
