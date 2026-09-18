import { get, post, put } from "@/lib/api"
import { defaultConfiguration } from "@/components/deploy/deployment-defaults"
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

/** The six ways into a new project — the source strip's own tab keys. */
export type SourceTabKey = "git" | "image" | "template" | "database" | "compose" | "existing"

/**
 * The draft state machine every source drives the same way.
 *
 * A source tab's only job is to produce a `DeploymentDraftSource` and hand it
 * to `inspectSource`, which creates the draft, names it, saves the source, and
 * detects — the same four requests `quick-deploy.tsx` made inline. Configure
 * then owns the draft for the rest of its life: re-saving the source when the
 * branch (or a blueprint input) changes, saving the configuration, running
 * preflight, and committing. One place drives the API so six sources cannot
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

export async function commitDraft(draft: DeploymentDraft, acknowledgedWarnings: string[]) {
  return post<DraftCommitResult>(`/deploy/drafts/${draft.id}/commit`, {
    revision: draft.revision,
    acknowledgedWarnings,
  })
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
 * One shape for all six sources rather than one state tree per source: a
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
