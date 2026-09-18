"use client"

import { useState } from "react"
import type { FormEvent } from "react"
import { ApiError, get, put } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentDraftSource,
  DeploymentGitPolicy,
  DeploymentGitWatch,
  DeploymentSourceKind,
} from "@/lib/types"
import {
  Field,
  FieldRow,
  FormFact,
  FormFacts,
  FormNote,
  OptionList,
  OptionRow,
} from "@/components/form"
import { LoadingPanel } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { useProject } from "@/components/deploy/project-context"
import { DEPLOYMENT_NAME, humanize } from "@/components/deploy/vocabulary"
import {
  ConfigurationState,
  useConfiguration,
} from "@/components/deploy/settings/use-configuration"
import { PendingChanges } from "@/components/deploy/settings/pending-changes"
import { SettingCard } from "@/components/deploy/settings/setting-card"
import { CredentialSelect } from "@/components/deploy/credentials-page"

/**
 * What the project is, its name, and — for a Git source — the policy that
 * decides whether a push deploys itself. A legacy Compose project has no
 * policy to set, so it reads its checkout facts instead.
 */

const NAME_ERROR =
  "Use 1–64 letters, numbers, dots, dashes, or underscores, starting with a letter or number."

const decisionReasons: Record<string, string> = {
  watch_paths_ignored:
    "The latest changes did not match the watched paths. No deployment was queued.",
  watched_paths_changed:
    "The latest changes matched the watched paths and a deployment was queued.",
  branch_changed: "A new branch revision was queued for deployment.",
  already_attempted: "This commit already has a deployment run. Use Retry if it failed.",
  changes_unavailable:
    "The complete changed paths could not be read. Automatic deployment is paused until comparison succeeds.",
  enqueue_failed: "The change could not be queued. The next branch check will retry.",
  policy_conflict:
    "Existing webhook filters disagree. Save one deployment policy to resume automation.",
}

function linesOf(text: string) {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
}

export function GeneralSettings({
  projectId,
  environmentId,
}: {
  projectId: number
  environmentId: number
}) {
  const { can } = useAuth()
  const project = useProject()
  const state = useConfiguration(projectId, environmentId)
  const { project: record, deployment } = project.detail
  const canEdit = can("system.admin")

  const identity =
    deployment.sourceKind === "image"
      ? { label: "Image", value: deployment.sourceRef }
      : { label: "Repository", value: record.repoPath }
  const branch =
    deployment.sourceKind === "git" || deployment.sourceKind === "local"
      ? deployment.sourceRef || record.branch
      : undefined

  return (
    <ConfigurationState state={state}>
      {(configuration) => (
        <div key={configuration.revision} className="space-y-6">
          <PendingChanges pending={configuration.pending} />

          <SettingCard title="Project">
            <FormFacts>
              <FormFact label="Source">{humanize(deployment.sourceKind)}</FormFact>
              <FormFact label={identity.label} mono>
                {identity.value || "—"}
              </FormFact>
              <FormFact label="Branch" mono={Boolean(branch)}>
                {branch || "—"}
              </FormFact>
              <FormFact label="Created">
                <time dateTime={record.createdAt} title={timestamp(record.createdAt)}>
                  {relativeTime(record.createdAt)}
                </time>
              </FormFact>
              <FormFact label="Strategy">{humanize(deployment.strategy)}</FormFact>
            </FormFacts>
          </SettingCard>

          <ProjectNameCard
            projectId={projectId}
            name={record.name}
            canEdit={canEdit && project.normalized}
            legacy={!project.normalized}
            onSaved={project.refresh}
          />

          {!project.normalized ? (
            <SettingCard title="Checkout">
              <FormFacts>
                <FormFact label="Path" mono>
                  {record.repoPath || "—"}
                </FormFact>
                <FormFact label="Branch" mono>
                  {record.branch || "—"}
                </FormFact>
                <FormFact label="Compose file" mono>
                  {record.composeFile || "—"}
                </FormFact>
                <FormFact label="Pre-deploy command" mono>
                  {record.preCommand || "—"}
                </FormFact>
                <FormFact label="Post-deploy command" mono>
                  {record.postCommand || "—"}
                </FormFact>
              </FormFacts>
            </SettingCard>
          ) : (
            <>
              {deployment.sourceKind === "git" && (
                <GitPolicyCard
                  projectId={projectId}
                  environmentId={environmentId}
                  canEdit={canEdit}
                />
              )}
              {/* The endpoint's own errors (git_unavailable, invalid_image) are
                  the tell: only these two kinds have a re-enterable source
                  today, so the card is scoped to them rather than to every
                  normalized kind. */}
              {(deployment.sourceKind === "git" || deployment.sourceKind === "image") && (
                <SourceSettingCard
                  projectId={projectId}
                  environmentId={environmentId}
                  canEdit={canEdit}
                  revision={configuration.revision}
                  source={configuration.source}
                  sourceKind={deployment.sourceKind}
                  sourceRef={deployment.sourceRef}
                  onSaved={() => {
                    state.refresh()
                    project.refresh()
                  }}
                />
              )}
            </>
          )}
        </div>
      )}
    </ConfigurationState>
  )
}

function ProjectNameCard({
  projectId,
  name,
  canEdit,
  legacy,
  onSaved,
}: {
  projectId: number
  name: string
  canEdit: boolean
  legacy: boolean
  onSaved: () => void
}) {
  const [value, setValue] = useState(name)
  const [error, setError] = useState<string>()
  const [busy, setBusy] = useState(false)

  const save = async (event: FormEvent) => {
    event.preventDefault()
    if (!DEPLOYMENT_NAME.test(value)) {
      setError(NAME_ERROR)
      return
    }
    setError(undefined)
    setBusy(true)
    try {
      await put(`/deploy/${projectId}`, { name: value })
      notify.success("Project renamed")
      onSaved()
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 409 && caught.code === "name_taken") {
        setError("That name is already used by another project")
      } else {
        notify.error("Could not rename project", caught)
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={save}>
      <SettingCard
        title="Project name"
        note={
          legacy
            ? "Renaming is available for normalized projects."
            : canEdit
              ? "Used in URLs, container labels, and release history."
              : undefined
        }
        action={
          canEdit && (
            <Button size="sm" type="submit" pending={busy}>
              Save
            </Button>
          )
        }
      >
        <Field label="Project name" htmlFor="project-name" error={error}>
          <Input
            id="project-name"
            value={value}
            onChange={(event) => setValue(event.target.value)}
            onBlur={() => {
              if (value && !DEPLOYMENT_NAME.test(value)) setError(NAME_ERROR)
            }}
            readOnly={!canEdit}
            className="font-mono"
          />
        </Field>
      </SettingCard>
    </form>
  )
}

function GitPolicyCard({
  projectId,
  environmentId,
  canEdit,
}: {
  projectId: number
  environmentId: number
  canEdit: boolean
}) {
  const watch = usePoll(
    (signal) =>
      get<DeploymentGitWatch>(
        `/deploy/${projectId}/environments/${environmentId}/git-watch`,
        undefined,
        signal,
      ),
    5000,
    [projectId, environmentId],
  )
  if (watch.loading && !watch.data) return <LoadingPanel rows={3} />
  if (watch.error || !watch.data) {
    return (
      <SettingCard title="Git">
        <FormNote tone="warning">Automatic deployment status is unavailable.</FormNote>
      </SettingCard>
    )
  }
  return (
    <GitPolicyForm
      key={watch.data.policy?.revision ?? 0}
      watch={watch.data}
      projectId={projectId}
      environmentId={environmentId}
      canEdit={canEdit}
      onSaved={watch.refresh}
    />
  )
}

function GitPolicyForm({
  watch,
  projectId,
  environmentId,
  canEdit,
  onSaved,
}: {
  watch: DeploymentGitWatch
  projectId: number
  environmentId: number
  canEdit: boolean
  onSaved: () => void
}) {
  const policy: DeploymentGitPolicy = watch.policy ?? {
    automatic: true,
    commitStatuses: true,
    watchInclude: [],
    watchExclude: [],
    revision: 0,
  }
  const [automatic, setAutomatic] = useState(policy.automatic)
  const [commitStatuses, setCommitStatuses] = useState(policy.commitStatuses ?? true)
  const [include, setInclude] = useState(policy.watchInclude.join("\n"))
  const [exclude, setExclude] = useState(policy.watchExclude.join("\n"))
  const [busy, setBusy] = useState(false)

  const unavailable = ["unavailable", "stale", "policy_conflict"].includes(watch.status)
  const tone: DotTone = unavailable
    ? "warning"
    : !automatic
      ? "stopped"
      : watch.status === "awaiting_first_deployment"
        ? "stopped"
        : "running"
  const label = unavailable
    ? "Needs attention"
    : !automatic
      ? "Manual"
      : watch.status === "awaiting_first_deployment"
        ? "Awaiting first deployment"
        : "Automatic"

  const explanation = !automatic
    ? "Git polling, webhooks and scheduled deployments cannot queue new deployments. Deploy manually when ready."
    : unavailable
      ? "Could not check the production branch. Check repository access and credentials."
      : watch.status === "not_applicable"
        ? "Signed hooks and schedules can request deployments. This source has no branch to poll."
        : watch.status === "awaiting_first_deployment"
          ? `After your first deployment, new commits to ${watch.branch} deploy automatically.`
          : policy.watchInclude.length || policy.watchExclude.length
            ? `Matching changes on ${watch.branch} deploy automatically. The same path filters apply to polling and push webhooks.`
            : `New commits to ${watch.branch} deploy automatically. Checked every ${watch.intervalSeconds} seconds.`

  const save = async (event: FormEvent) => {
    event.preventDefault()
    setBusy(true)
    try {
      await put(`/deploy/${projectId}/environments/${environmentId}/git-policy`, {
        automatic,
        commitStatuses,
        revision: policy.revision,
        watchInclude: linesOf(include),
        watchExclude: linesOf(exclude),
      })
      notify.success("Deployment policy saved")
      onSaved()
    } catch (error) {
      notify.error("Could not save deployment policy", error)
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={save}>
      <SettingCard
        title="Git"
        actions={<Status tone={tone} label={label} />}
        note="Applies immediately — no deployment required."
        action={
          canEdit && (
            <Button size="sm" type="submit" pending={busy}>
              Save
            </Button>
          )
        }
      >
        <OptionList>
          <OptionRow
            title="Deploy automatically"
            hint={explanation}
            checked={automatic}
            onCheckedChange={setAutomatic}
            disabled={!canEdit}
          />
          <OptionRow
            title="Report deployment status to GitHub commits"
            hint="Each run posts pending, success or failure to its commit through the dashboard's GitHub account, with a link back to the run. Only GitHub.com sources are reported."
            checked={commitStatuses}
            onCheckedChange={setCommitStatuses}
            disabled={!canEdit}
          />
        </OptionList>
        <FieldRow columns={2}>
          <Field
            label="Include paths"
            htmlFor="git-policy-include"
            hint="One repository-relative glob per line. Leave empty to include all paths. Use directory/** for a directory and its children."
          >
            <Textarea
              id="git-policy-include"
              value={include}
              onChange={(event) => setInclude(event.target.value)}
              readOnly={!canEdit}
              placeholder="services/api/**"
              className="min-h-20 font-mono text-xs"
            />
          </Field>
          <Field
            label="Exclude paths"
            htmlFor="git-policy-exclude"
            hint="Exclusions take priority. Polling and push hooks compare the complete Git changes since the last attempted deployment."
          >
            <Textarea
              id="git-policy-exclude"
              value={exclude}
              onChange={(event) => setExclude(event.target.value)}
              readOnly={!canEdit}
              placeholder="docs/**"
              className="min-h-20 font-mono text-xs"
            />
          </Field>
        </FieldRow>
        {watch.reason && decisionReasons[watch.reason] && (
          <FormNote>{decisionReasons[watch.reason]}</FormNote>
        )}
        {watch.checkedAt && <FormNote>Last checked {relativeTime(watch.checkedAt)}</FormNote>}
        {policy.inherited && (
          <FormNote>
            These filters were inherited from existing webhooks. Saving makes them the shared
            policy.
          </FormNote>
        )}
        {policy.conflict && (
          <FormNote tone="warning">
            Existing webhook filters disagree. Review both lists before saving one shared policy.
          </FormNote>
        )}
      </SettingCard>
    </form>
  )
}

/** The plain URL a connected GitHub repository is reachable at, for the one
 *  provider this card knows how to derive a URL for without asking again. */
function connectedGithubUrl(source: DeploymentDraftSource | undefined) {
  if (
    source?.mode === "connected_repository" &&
    source.provider === "github" &&
    source.repository
  ) {
    return `https://github.com/${source.repository}`
  }
  return undefined
}

/**
 * Where the project builds from, editable in place. The backend fills
 * `source` on the configuration read once it lands; until then the fields
 * fall back to the fleet summary's `sourceKind`/`sourceRef`, and the URL or
 * image field opens empty with a hint to enter it again rather than guess.
 *
 * A connected GitHub repository has no plain URL of its own — `provider` +
 * `repository` instead — so its URL is derived rather than left blank, and
 * saving without editing it keeps the connected shape (`mode`, `provider`,
 * `providerBaseUrl`, `repository`) rather than silently converting the
 * project to a bare Git URL. Typing a different URL is what makes that
 * conversion, deliberately: it is the one action that means it.
 */
function SourceSettingCard({
  projectId,
  environmentId,
  canEdit,
  revision,
  source,
  sourceKind,
  sourceRef,
  onSaved,
}: {
  projectId: number
  environmentId: number
  canEdit: boolean
  revision: number
  source: DeploymentDraftSource | undefined
  sourceKind: DeploymentSourceKind
  sourceRef: string | undefined
  onSaved: () => void
}) {
  const isGit = sourceKind === "git"
  const prefillUrl = source?.url ?? connectedGithubUrl(source) ?? ""
  const [url, setUrl] = useState(prefillUrl)
  const [ref, setRef] = useState(source?.ref ?? sourceRef ?? "")
  const [subdirectory, setSubdirectory] = useState(source?.subdirectory ?? "")
  const [image, setImage] = useState(source?.image ?? (isGit ? "" : (sourceRef ?? "")))
  const [platform, setPlatform] = useState(source?.platform ?? "")
  const [credentialId, setCredentialId] = useState<number | undefined>(source?.credentialId)
  const [includeSubmodules, setIncludeSubmodules] = useState(source?.includeSubmodules ?? false)
  const [includeLfs, setIncludeLfs] = useState(source?.includeLfs ?? false)
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [cardError, setCardError] = useState<string>()
  const [busy, setBusy] = useState(false)

  // Absent entirely (the backend has not shipped it yet) or missing the one
  // field this kind actually identifies itself by — either way, the value on
  // screen is a guess and the reader is told rather than left to assume it.
  const needsReentry = isGit ? !prefillUrl : !source || !source.image

  const save = async (event: FormEvent) => {
    event.preventDefault()
    setBusy(true)
    setFieldErrors({})
    setCardError(undefined)
    const keepConnected =
      isGit && source?.mode === "connected_repository" && url.trim() === prefillUrl
    const body =
      isGit && keepConnected && source
        ? {
            revision,
            kind: "git" as const,
            mode: source.mode,
            provider: source.provider,
            providerBaseUrl: source.providerBaseUrl,
            repository: source.repository,
            ref: ref.trim() || undefined,
            subdirectory: subdirectory.trim() || undefined,
            credentialId,
            includeSubmodules,
            includeLfs,
          }
        : isGit
          ? {
              revision,
              kind: "git" as const,
              mode: "git_url" as const,
              url: url.trim(),
              ref: ref.trim() || undefined,
              subdirectory: subdirectory.trim() || undefined,
              credentialId,
              includeSubmodules,
              includeLfs,
            }
          : {
              revision,
              kind: "image" as const,
              mode: "image_reference" as const,
              image: image.trim(),
              platform: platform.trim() || undefined,
              credentialId,
            }
    try {
      await put(`/deploy/${projectId}/environments/${environmentId}/source`, body)
      notify.success("Source updated", { description: "The next deployment builds from it." })
      onSaved()
    } catch (caught) {
      if (caught instanceof ApiError && caught.field) {
        setFieldErrors({ [caught.field]: caught.message })
      } else if (caught instanceof ApiError && (caught.status === 422 || caught.status === 400)) {
        // The adapter's own refusal — an unreachable branch, an unknown image —
        // names no field, so it reads as the card's own sentence rather than a
        // generic toast.
        setCardError(caught.message)
      } else {
        notify.error("Could not update source", caught)
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={save}>
      <SettingCard
        title="Source"
        note={canEdit ? "Applies to the next deployment." : undefined}
        action={
          canEdit && (
            <Button size="sm" type="submit" pending={busy}>
              Save
            </Button>
          )
        }
      >
        {isGit ? (
          <>
            <Field
              label="Repository URL"
              htmlFor="source-url"
              hint={
                needsReentry
                  ? "Enter the repository again."
                  : "HTTPS or SSH; do not embed a password or token."
              }
              error={fieldErrors.url}
            >
              <Input
                id="source-url"
                value={url}
                onChange={(event) => setUrl(event.target.value)}
                readOnly={!canEdit}
                className="font-mono"
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            <FieldRow>
              <Field label="Branch or tag" htmlFor="source-ref" error={fieldErrors.ref}>
                <Input
                  id="source-ref"
                  value={ref}
                  onChange={(event) => setRef(event.target.value)}
                  readOnly={!canEdit}
                  placeholder="main"
                />
              </Field>
              <Field
                label="Root directory"
                htmlFor="source-subdirectory"
                hint="Relative to the repository root. Leave empty to use the root."
                error={fieldErrors.subdirectory}
              >
                <Input
                  id="source-subdirectory"
                  value={subdirectory}
                  onChange={(event) => setSubdirectory(event.target.value)}
                  readOnly={!canEdit}
                  placeholder="apps/api"
                />
              </Field>
            </FieldRow>
            <Field label="Credential" htmlFor="source-credential" error={fieldErrors.credentialId}>
              <CredentialSelect
                id="source-credential"
                kind="git"
                value={credentialId}
                onChange={setCredentialId}
                disabled={!canEdit}
              />
            </Field>
            <OptionList>
              <OptionRow
                title="Include Git submodules"
                checked={includeSubmodules}
                onCheckedChange={setIncludeSubmodules}
                disabled={!canEdit}
              />
              <OptionRow
                title="Include Git LFS objects"
                checked={includeLfs}
                onCheckedChange={setIncludeLfs}
                disabled={!canEdit}
              />
            </OptionList>
          </>
        ) : (
          <>
            <Field
              label="Image reference"
              htmlFor="source-image"
              hint={needsReentry ? "Enter the repository again." : undefined}
              error={fieldErrors.image}
            >
              <Input
                id="source-image"
                value={image}
                onChange={(event) => setImage(event.target.value)}
                readOnly={!canEdit}
                className="font-mono"
                autoComplete="off"
                spellCheck={false}
              />
            </Field>
            <FieldRow>
              <Field
                label="Platform"
                htmlFor="source-platform"
                hint="os/arch, such as linux/amd64."
                error={fieldErrors.platform}
              >
                <Input
                  id="source-platform"
                  value={platform}
                  onChange={(event) => setPlatform(event.target.value)}
                  readOnly={!canEdit}
                  placeholder="linux/amd64"
                />
              </Field>
              <Field
                label="Credential"
                htmlFor="source-credential"
                error={fieldErrors.credentialId}
              >
                <CredentialSelect
                  id="source-credential"
                  kind="registry"
                  value={credentialId}
                  onChange={setCredentialId}
                  disabled={!canEdit}
                />
              </Field>
            </FieldRow>
          </>
        )}
        <FormNote>
          A project keeps its source kind. Start a new project to move from an image to a
          repository.
        </FormNote>
        {cardError && (
          <FormNote tone="danger" role="alert">
            {cardError}
          </FormNote>
        )}
      </SettingCard>
    </form>
  )
}
