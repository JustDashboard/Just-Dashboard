"use client"

import { useRef, useState } from "react"
import type { FormEvent } from "react"
import { Clock, External } from "@/components/icons"
import { ApiError, get, put } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentDetectionChange,
  DeploymentDetectionProposal,
  DeployProject,
  DeploymentDraftSource,
  DeploymentEnvironmentConfiguration,
  DeploymentGitPolicy,
  DeploymentGitWatch,
  DeploymentSummary,
} from "@/lib/types"
import { Field, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import { SourceBranch } from "@/components/git/glyphs"
import { BranchChip, ShortSha } from "@/components/git/marks"
import { Detail, DetailList } from "@/components/page"
import { ProductGlyph, hostProduct, imageProduct } from "@/components/product-logo"
import { Status, type DotTone } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import { Skeleton } from "@/components/ui/skeleton"
import { Textarea } from "@/components/ui/textarea"
import { useProject } from "@/components/deploy/project-context"
import { ProjectMark } from "@/components/deploy/project-mark"
import {
  DEPLOYMENT_NAME,
  WORKLOAD_GLYPH,
  WORKLOAD_LABELS,
  shortIdentity,
  sourceProduct,
} from "@/components/deploy/vocabulary"
import { WireLabel, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { useConfiguration, useSettingDraft } from "@/components/deploy/settings/use-configuration"
import {
  SettingForm,
  SettingSection,
  SettingsPage,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { SettingPicture } from "@/components/deploy/settings/setting-picture"
import { CredentialSelect } from "@/components/deploy/credentials-page"
import { DetectionProposalPanel } from "@/components/deploy/settings/detection-proposal"
import { applyDetectionChanges } from "@/components/deploy/settings/detection-changes"

/**
 * What the project is called, where it is built from, and — for a Git
 * source — whether a push deploys it by itself. A legacy Compose project has
 * no source or policy to set, so it reads its checkout instead.
 *
 * No row of figures (§15 pass 2's exit, as Configuration and account Security
 * took it): the project header's facts line — host, branch and commit,
 * release, auto-deploy — already is this page's reading line, and a tile row
 * under it would draw it twice. Each figure the old "Project" card repeated
 * moved beside the control that sets it: the source, repository and branch to
 * the Source head; when the project was created and what kind it is to the
 * Name head; the release strategy to the Runtime page's Releases reading;
 * whether it deploys itself, how often it looks, when it last did and what
 * it watches to the Automatic deployment head.
 */

const NAME_ERROR =
  "Use 1–64 letters, numbers, dots, dashes, or underscores, starting with a letter or number."

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

  return (
    <SettingsPage state={state} pageKinds={["source"]}>
      {(configuration) => (
        <>
          <NameForm
            projectId={projectId}
            record={record}
            deployment={deployment}
            product={project.product}
            canEdit={canEdit && project.normalized}
            legacy={!project.normalized}
            onSaved={project.refresh}
          />

          {!project.normalized ? (
            <Checkout record={record} />
          ) : (
            <>
              {/* The endpoint's own errors (git_unavailable, invalid_image) are
                  the tell: only these two kinds have a re-enterable source
                  today, so the form is scoped to them rather than to every
                  normalized kind. */}
              {(deployment.sourceKind === "git" || deployment.sourceKind === "image") && (
                <SourceForm
                  projectId={projectId}
                  environmentId={environmentId}
                  canEdit={canEdit}
                  configuration={configuration}
                  deployment={deployment}
                  repoPath={record.repoPath}
                  onSaved={() => {
                    state.refresh()
                    project.refresh()
                  }}
                />
              )}
              {deployment.sourceKind === "git" && (
                <AutomaticDeployment
                  projectId={projectId}
                  environmentId={environmentId}
                  canEdit={canEdit}
                  configuration={configuration}
                  deployment={deployment}
                  projectName={record.name}
                />
              )}
            </>
          )}
        </>
      )}
    </SettingsPage>
  )
}

/**
 * The project's name, and the two facts about it nothing on this page sets:
 * when it was made and what kind of workload it is.
 *
 * The count sits inside the field's edge because the limit belongs to the
 * value, not to the sentence under it: 64 is where a name stops being usable
 * in a container label, and the reader should see the edge coming.
 */
function NameForm({
  projectId,
  record,
  deployment,
  product,
  canEdit,
  legacy,
  onSaved,
}: {
  projectId: number
  record: DeployProject
  deployment: DeploymentSummary
  product?: string
  canEdit: boolean
  legacy: boolean
  onSaved: () => void
}) {
  const draft = useSettingDraft(`deploy.${projectId}.settings.name`, record.name)
  const value = draft.value
  const [error, setError] = useState<string>()
  // Whether a save was turned down, kept apart from the error line: leaving
  // the field with a bad name says so under it, but only a save that was
  // refused puts "Not saved" on the head.
  const [refused, setRefused] = useState(false)
  const [saving, setSaving] = useState(false)

  const save = async (event: FormEvent) => {
    event.preventDefault()
    setRefused(false)
    if (!DEPLOYMENT_NAME.test(value)) {
      setError(NAME_ERROR)
      setRefused(true)
      return
    }
    setError(undefined)
    setSaving(true)
    try {
      await put(`/deploy/${projectId}`, { name: value })
      notify.success("Project renamed")
      onSaved()
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 409 && caught.code === "name_taken") {
        setError("That name is already used by another project")
        setRefused(true)
      } else {
        notify.error("Could not rename project", caught)
      }
    } finally {
      setSaving(false)
    }
  }

  return (
    <SettingForm
      name="Project name"
      onSubmit={save}
      dirty={draft.dirty}
      changes={draft.changes}
      saving={saving}
      canEdit={canEdit}
      onDiscard={() => {
        setError(undefined)
        setRefused(false)
        draft.discard()
      }}
      applies="immediately"
      note={legacy ? "Renaming is available for normalized projects." : undefined}
    >
      <SettingSection
        id="name"
        title="Name"
        state={
          <span className="block space-y-1">
            <span className="flex min-w-0 items-center gap-1.5 text-foreground">
              <ProjectMark deployment={deployment} product={product} size="xs" />
              <span className="truncate">{WORKLOAD_LABELS[deployment.profile]}</span>
            </span>
            <span className="block">
              created{" "}
              <time dateTime={record.createdAt} title={timestamp(record.createdAt)}>
                {relativeTime(record.createdAt)}
              </time>
            </span>
          </span>
        }
        status={settingStatus({ dirty: draft.dirty, refused })}
      >
        <Field
          label="Project name"
          htmlFor="project-name"
          hint="Used in URLs, container labels and release history."
          error={error}
        >
          <InputGroup>
            <InputGroupInput
              id="project-name"
              value={value}
              onChange={(event) => {
                draft.set(event.target.value)
                // A name corrected after the blur check stops being wrong at once.
                if (error === NAME_ERROR && DEPLOYMENT_NAME.test(event.target.value))
                  setError(undefined)
              }}
              onBlur={() => {
                if (value && !DEPLOYMENT_NAME.test(value)) setError(NAME_ERROR)
              }}
              readOnly={!canEdit}
              aria-invalid={Boolean(error)}
              autoComplete="off"
              spellCheck={false}
              className="font-mono"
            />
            {/* A count is for typing; on a name that cannot be edited it made
                the field look editable. */}
            {canEdit && (
              <InputGroupAddon align="inline-end">
                <InputGroupText
                  className={cn("numeric", value.length > 64 && "font-medium text-destructive")}
                >
                  {value.length}/64
                </InputGroupText>
              </InputGroupAddon>
            )}
          </InputGroup>
        </Field>
      </SettingSection>
    </SettingForm>
  )
}

/**
 * A legacy Compose project's checkout, read-only: it builds from its compose
 * file where it stands, and nothing here moves it.
 */
function Checkout({ record }: { record: DeployProject }) {
  const literal = (value: string | undefined) =>
    value ? <span className="font-mono break-all">{value}</span> : "—"
  return (
    <SettingSection
      id="checkout"
      title="Checkout"
      state={
        <span className="flex min-w-0 items-center gap-1.5 text-foreground">
          <ProductGlyph id="docker-compose" />
          <span className="truncate font-mono">{record.composeFile || "compose.yml"}</span>
        </span>
      }
    >
      <DetailList className="gap-y-2.5">
        <Detail label="Path">{literal(record.repoPath)}</Detail>
        <Detail label="Branch">
          {record.branch ? <BranchChip branch={record.branch} className="max-w-full" /> : "—"}
        </Detail>
        <Detail label="Compose file">{literal(record.composeFile)}</Detail>
        <Detail label="Pre-deploy command">{literal(record.preCommand)}</Detail>
        <Detail label="Post-deploy command">{literal(record.postCommand)}</Detail>
      </DetailList>
    </SettingSection>
  )
}

/** The plain URL a connected GitHub repository is reachable at, for the one
 *  provider this form knows how to derive a URL for without asking again. */
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
 * The page a remote is browsed at, for the link on the Source head: an https
 * remote without its `.git` and any credentials, and an scp-style or ssh://
 * one as the https page on the same host. `undefined` for anything else,
 * which is drawn as plain text rather than as a link that goes nowhere.
 */
function repositoryPage(remote: string | undefined) {
  if (!remote) return undefined
  const scp = /^[\w.-]+@([^:/]+):(.+)$/.exec(remote)
  const candidate = scp
    ? `https://${scp[1]}/${scp[2]}`
    : remote.replace(/^ssh:\/\/(?:[^@/]+@)?([^/:]+)(?::\d+)?/, "https://$1")
  let url: URL
  try {
    url = new URL(candidate)
  } catch {
    return undefined
  }
  if (url.protocol !== "https:" && url.protocol !== "http:") return undefined
  return `${url.origin}${url.pathname.replace(/\.git$/, "").replace(/\/$/, "")}`
}

/** `owner/name` from a remote whose path is one, for a source saved as a bare URL. */
function repositoryName(remote: string | undefined) {
  const page = repositoryPage(remote)
  return page ? new URL(page).pathname.replace(/^\//, "") : undefined
}

type SourceDraft = {
  url: string
  ref: string
  subdirectory: string
  image: string
  platform: string
  credentialId?: number
  includeSubmodules: boolean
  includeLfs: boolean
}

/**
 * Where the project builds from, editable in place. The backend fills
 * `source` on the configuration read; until it does the fields fall back to
 * the fleet summary's `sourceKind`/`sourceRef`, and the URL or image field
 * opens empty with a hint to enter it again rather than guess.
 *
 * A connected GitHub repository has no plain URL of its own — `provider` +
 * `repository` instead — so its URL is derived rather than left blank, and
 * saving without editing it keeps the connected shape (`mode`, `provider`,
 * `providerBaseUrl`, `repository`) rather than silently converting the
 * project to a bare Git URL. Typing a different URL is what makes that
 * conversion, deliberately: it is the one action that means it.
 *
 * The head draws the source as itself — the forge it is on, the repository
 * as a link to its page, the branch and the commit the saved source resolved
 * to — and each field draws what it holds as it is typed: the
 * forge's mark in front of the URL, the image's product in front of the
 * reference.
 */
function SourceForm({
  projectId,
  environmentId,
  canEdit,
  configuration,
  deployment,
  repoPath,
  onSaved,
}: {
  projectId: number
  environmentId: number
  canEdit: boolean
  configuration: DeploymentEnvironmentConfiguration
  deployment: DeploymentSummary
  repoPath: string
  onSaved: () => void
}) {
  const { revision, source, identity, pending } = configuration
  const isGit = deployment.sourceKind === "git"
  const sourceRef = deployment.sourceRef
  const prefillUrl = source?.url ?? connectedGithubUrl(source) ?? ""
  const draft = useSettingDraft<SourceDraft>(`deploy.${projectId}.settings.source`, {
    url: prefillUrl,
    ref: source?.ref ?? sourceRef ?? "",
    subdirectory: source?.subdirectory ?? "",
    image: source?.image ?? (isGit ? "" : (sourceRef ?? "")),
    platform: source?.platform ?? "",
    credentialId: source?.credentialId,
    includeSubmodules: source?.includeSubmodules ?? false,
    includeLfs: source?.includeLfs ?? false,
  })
  const value = draft.value
  const patch = (fields: Partial<SourceDraft>) => draft.set((prev) => ({ ...prev, ...fields }))
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({})
  const [formError, setFormError] = useState<string>()
  const [saving, setSaving] = useState(false)
  // What detection read at the source just saved, where it answers a build
  // field differently than it did when the plan was saved: the plan keeps
  // the old answer until one is applied.
  const [proposal, setProposal] = useState<DeploymentDetectionProposal>()
  const [applying, setApplying] = useState(false)
  const detectionChanges = (proposal?.changes ?? []).filter((change) => change.changed)
  // Applied onto the plan detection was compared with, under that plan's
  // revision: a plan saved since — another tab, another reader — refuses
  // the save rather than taking values compared with a plan that is gone.
  const applyDetection = async (changes: DeploymentDetectionChange[]) => {
    if (!proposal) return
    setApplying(true)
    try {
      const path = `/deploy/${projectId}/environments/${environmentId}/configuration`
      const compared = await get<DeploymentEnvironmentConfiguration>(path)
      if (compared.revision !== proposal.revision)
        throw new Error(
          "The settings changed after detection read the source. Use Detect again in Build settings to compare with them.",
        )
      const next = applyDetectionChanges(compared.build, compared.runtime, changes)
      const saved = await put<DeploymentEnvironmentConfiguration>(path, {
        revision: proposal.revision,
        build: next.build,
        runtime: next.runtime,
        dependencies: compared.dependencies,
        checks: compared.checks,
        domains: compared.domains,
      })
      // The rest of the proposal was compared with the plan this save
      // started from, which it changed only where it applied.
      setProposal(
        (current) =>
          current && {
            ...current,
            revision: saved.revision,
            changes: current.changes.filter((change) => !changes.includes(change)),
          },
      )
      notify.success(changes.length === 1 ? `${changes[0].label} applied` : "Detection applied", {
        description: "The next deployment builds with it.",
      })
      onSaved()
    } catch (caught) {
      notify.error("Could not apply what detection found", caught)
    } finally {
      setApplying(false)
    }
  }

  // Absent entirely (the backend has not shipped it yet) or missing the one
  // field this kind actually identifies itself by — either way, the value on
  // screen is a guess and the reader is told rather than left to assume it.
  const needsReentry = isGit ? !prefillUrl : !source || !source.image

  const save = async (event: FormEvent) => {
    event.preventDefault()
    setSaving(true)
    setFieldErrors({})
    setFormError(undefined)
    const keepConnected =
      isGit && source?.mode === "connected_repository" && value.url.trim() === prefillUrl
    const common = {
      ref: value.ref.trim() || undefined,
      subdirectory: value.subdirectory.trim() || undefined,
      credentialId: value.credentialId,
      includeSubmodules: value.includeSubmodules,
      includeLfs: value.includeLfs,
    }
    const body =
      isGit && keepConnected && source
        ? {
            revision,
            kind: "git" as const,
            mode: source.mode,
            provider: source.provider,
            providerBaseUrl: source.providerBaseUrl,
            repository: source.repository,
            ...common,
          }
        : isGit
          ? {
              revision,
              kind: "git" as const,
              mode: "git_url" as const,
              url: value.url.trim(),
              ...common,
            }
          : {
              revision,
              kind: "image" as const,
              mode: "image_reference" as const,
              image: value.image.trim(),
              platform: value.platform.trim() || undefined,
              credentialId: value.credentialId,
            }
    try {
      const updated = await put<{ proposal?: DeploymentDetectionProposal }>(
        `/deploy/${projectId}/environments/${environmentId}/source`,
        body,
      )
      setProposal(updated.proposal)
      // The fields went out trimmed; the draft takes what was sent, or a
      // trailing space would read as an edit the save did not make.
      patch({
        url: value.url.trim(),
        ref: value.ref.trim(),
        subdirectory: value.subdirectory.trim(),
        image: value.image.trim(),
        platform: value.platform.trim(),
      })
      notify.success("Source updated", { description: "The next deployment builds from it." })
      onSaved()
    } catch (caught) {
      if (caught instanceof ApiError && caught.field) {
        setFieldErrors({ [caught.field]: caught.message })
      } else if (caught instanceof ApiError && (caught.status === 422 || caught.status === 400)) {
        // The adapter's own refusal — an unreachable branch, an unknown image —
        // names no field, so it reads as the form's own sentence rather than a
        // generic toast.
        setFormError(caught.message)
      } else {
        notify.error("Could not update source", caught)
      }
    } finally {
      setSaving(false)
    }
  }

  const refused = Boolean(formError) || Object.keys(fieldErrors).length > 0
  const throughApp =
    isGit &&
    source?.mode === "connected_repository" &&
    source.provider === "github" &&
    value.url.trim() === prefillUrl
  const saved = sourceProduct(deployment, source)
  // What the URL field holds, drawn as it is typed; an empty field keeps the
  // saved source's mark rather than dropping to a bare git glyph.
  const typedHost = value.url.trim() ? (hostProduct(value.url) ?? "git") : (saved ?? "git")
  const typedImage = value.image.trim() ? imageProduct(value.image) : "docker"

  return (
    <SettingForm
      name="Source"
      onSubmit={save}
      dirty={draft.dirty}
      changes={draft.changes}
      saving={saving}
      canEdit={canEdit}
      onDiscard={() => {
        setFieldErrors({})
        setFormError(undefined)
        draft.discard()
      }}
      applies="next-deployment"
      error={formError}
    >
      <SettingSection
        id="source"
        title="Source"
        state={
          isGit ? (
            <GitSourceState
              product={saved ?? "git"}
              remote={source?.url ?? connectedGithubUrl(source) ?? deployment.sourceRemote}
              repository={
                identity?.repository ??
                source?.repository ??
                deployment.sourceRepository ??
                repositoryName(source?.url ?? deployment.sourceRemote)
              }
              branch={identity?.ref ?? source?.ref ?? sourceRef}
              revision={identity?.revision ?? deployment.sourceRevision}
              mode={source?.mode}
              repoPath={repoPath}
            />
          ) : (
            <ImageSourceState
              reference={source?.image ?? deployment.sourceRepository ?? sourceRef}
              digest={identity?.digest}
              platform={
                identity?.os && identity.architecture
                  ? `${identity.os}/${identity.architecture}`
                  : source?.platform
              }
            />
          )
        }
        status={settingStatus({
          dirty: draft.dirty,
          refused,
          notLive: pending.changes.some((change) => change.kind === "source"),
        })}
      >
        {proposal && (
          <DetectionProposalPanel
            projectId={projectId}
            title="Detection changed"
            proposal={proposal}
            changes={detectionChanges}
            canEdit={canEdit}
            applying={applying}
            onApply={(changes) => void applyDetection(changes)}
            onDismiss={() => setProposal(undefined)}
          />
        )}
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
              <InputGroup>
                <InputGroupAddon align="inline-start">
                  <ProductGlyph id={typedHost} />
                </InputGroupAddon>
                <InputGroupInput
                  id="source-url"
                  value={value.url}
                  onChange={(event) => patch({ url: event.target.value })}
                  readOnly={!canEdit}
                  aria-invalid={Boolean(fieldErrors.url)}
                  placeholder="https://github.com/owner/repository.git"
                  className="font-mono"
                  autoComplete="off"
                  spellCheck={false}
                />
              </InputGroup>
            </Field>
            <FieldRow>
              <Field label="Branch or tag" htmlFor="source-ref" error={fieldErrors.ref}>
                <InputGroup>
                  <InputGroupAddon align="inline-start">
                    <SourceBranch aria-hidden />
                  </InputGroupAddon>
                  <InputGroupInput
                    id="source-ref"
                    value={value.ref}
                    onChange={(event) => patch({ ref: event.target.value })}
                    readOnly={!canEdit}
                    aria-invalid={Boolean(fieldErrors.ref)}
                    placeholder="main"
                    className="font-mono"
                    autoComplete="off"
                    spellCheck={false}
                  />
                </InputGroup>
              </Field>
              <Field
                label="Root directory"
                htmlFor="source-subdirectory"
                hint="Empty builds from the repository root."
                error={fieldErrors.subdirectory}
              >
                <InputGroup>
                  <InputGroupAddon align="inline-start">
                    <InputGroupText className="font-mono">/</InputGroupText>
                  </InputGroupAddon>
                  <InputGroupInput
                    id="source-subdirectory"
                    value={value.subdirectory}
                    onChange={(event) => patch({ subdirectory: event.target.value })}
                    readOnly={!canEdit}
                    aria-invalid={Boolean(fieldErrors.subdirectory)}
                    placeholder="apps/api"
                    className="font-mono"
                    autoComplete="off"
                    spellCheck={false}
                  />
                </InputGroup>
              </Field>
            </FieldRow>
            {throughApp ? (
              // The App's installation is the credential a connected
              // repository is read with; the picker lists tokens and keys,
              // so it had nothing to show here but an empty trigger. A fact,
              // not a field: a <label> over it would name no control.
              <div className="min-w-0 space-y-1.5">
                <p className="text-body leading-none font-medium">Credential</p>
                <p className="flex min-h-9 items-center gap-2 text-body">
                  <ProductGlyph id="github" />
                  Read through the GitHub App&rsquo;s installation
                </p>
                {fieldErrors.credentialId ? (
                  <p role="alert" className="text-hint leading-relaxed text-destructive">
                    {fieldErrors.credentialId}
                  </p>
                ) : (
                  <p className="text-hint leading-relaxed text-muted-foreground">
                    A different URL above is read with a saved credential instead.
                  </p>
                )}
              </div>
            ) : (
              <Field
                label="Credential"
                htmlFor="source-credential"
                error={fieldErrors.credentialId}
              >
                <CredentialSelect
                  id="source-credential"
                  kind="git"
                  value={value.credentialId}
                  onChange={(credentialId) => patch({ credentialId })}
                  disabled={!canEdit}
                />
              </Field>
            )}
            <OptionList>
              <OptionRow
                title="Include Git submodules"
                checked={value.includeSubmodules}
                onCheckedChange={(includeSubmodules) => patch({ includeSubmodules })}
                disabled={!canEdit}
              />
              <OptionRow
                title="Include Git LFS objects"
                checked={value.includeLfs}
                onCheckedChange={(includeLfs) => patch({ includeLfs })}
                disabled={!canEdit}
              />
            </OptionList>
          </>
        ) : (
          <>
            <Field
              label="Image reference"
              htmlFor="source-image"
              hint={needsReentry ? "Enter the image reference again." : undefined}
              error={fieldErrors.image}
            >
              <InputGroup>
                <InputGroupAddon align="inline-start">
                  <ProductGlyph id={typedImage} />
                </InputGroupAddon>
                <InputGroupInput
                  id="source-image"
                  value={value.image}
                  onChange={(event) => patch({ image: event.target.value })}
                  readOnly={!canEdit}
                  aria-invalid={Boolean(fieldErrors.image)}
                  placeholder="ghcr.io/owner/image:tag"
                  className="font-mono"
                  autoComplete="off"
                  spellCheck={false}
                />
              </InputGroup>
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
                  value={value.platform}
                  onChange={(event) => patch({ platform: event.target.value })}
                  readOnly={!canEdit}
                  aria-invalid={Boolean(fieldErrors.platform)}
                  placeholder={
                    identity?.os && identity.architecture
                      ? `${identity.os}/${identity.architecture}`
                      : "linux/amd64"
                  }
                  className="font-mono"
                  autoComplete="off"
                  spellCheck={false}
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
                  value={value.credentialId}
                  onChange={(credentialId) => patch({ credentialId })}
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
      </SettingSection>
    </SettingForm>
  )
}

const MODE_WORD: Partial<Record<DeploymentDraftSource["mode"], string>> = {
  connected_repository: "GitHub App",
  git_url: "Git URL",
}

/**
 * A Git source as its head reads it: the forge's mark and the repository,
 * which opens its page; the branch and the commit the configuration resolved
 * to; how it is reached; and where the checkout lives on this server.
 */
function GitSourceState({
  product,
  remote,
  repository,
  branch,
  revision,
  mode,
  repoPath,
}: {
  product: string
  remote?: string
  repository?: string
  branch?: string
  revision?: string
  mode?: DeploymentDraftSource["mode"]
  repoPath: string
}) {
  const page = repositoryPage(remote)
  const name = (
    <>
      <ProductGlyph id={product} />
      <span className="truncate font-medium text-foreground">{repository ?? "Repository"}</span>
    </>
  )
  return (
    <span className="block space-y-1.5">
      {page ? (
        <a
          href={page}
          target="_blank"
          rel="noreferrer"
          className="flex w-fit max-w-full min-w-0 items-center gap-1.5 rounded-sm focus-ring transition-colors hover:text-foreground"
        >
          {name}
          <External aria-hidden className="size-3 shrink-0" />
        </a>
      ) : (
        <span className="flex min-w-0 items-center gap-1.5">{name}</span>
      )}
      <span className="flex min-w-0 flex-wrap items-center gap-1.5">
        {branch && <BranchChip branch={branch} className="max-w-40" />}
        <ShortSha sha={revision} />
        {mode && MODE_WORD[mode] && <Tag>{MODE_WORD[mode]}</Tag>}
      </span>
      {repoPath && (
        <span className="block truncate font-mono" title={repoPath}>
          {repoPath}
        </span>
      )}
    </span>
  )
}

/** An image source as its head reads it: the product it is, its reference, digest and platform. */
function ImageSourceState({
  reference,
  digest,
  platform,
}: {
  reference?: string
  digest?: string
  platform?: string
}) {
  return (
    <span className="block space-y-1">
      <span className="flex min-w-0 items-center gap-1.5">
        <ProductGlyph id={reference ? imageProduct(reference) : "docker"} />
        <span className="truncate font-mono text-foreground" title={reference}>
          {reference || "No image yet"}
        </span>
      </span>
      {(digest || platform) && (
        <span className="block truncate font-mono">
          {[digest && shortIdentity(digest), platform].filter(Boolean).join(" · ")}
        </span>
      )}
    </span>
  )
}

/** What the last automatic check decided, as a state and the sentence that explains it. */
const DECISIONS: Record<string, { tone: DotTone; label: string; sentence: string }> = {
  watched_paths_changed: {
    tone: "running",
    label: "Deployment queued",
    sentence: "The latest changes matched the watched paths and a deployment was queued.",
  },
  branch_changed: {
    tone: "running",
    label: "New revision queued",
    sentence: "A new branch revision was queued for deployment.",
  },
  watch_paths_ignored: {
    tone: "notice",
    label: "Changes ignored",
    sentence: "The latest changes did not match the watched paths. No deployment was queued.",
  },
  already_attempted: {
    tone: "notice",
    label: "Already deployed",
    sentence: "This commit already has a deployment run. Use Retry if it failed.",
  },
  changes_unavailable: {
    tone: "warning",
    label: "Changes unreadable",
    sentence:
      "The complete changed paths could not be read. Automatic deployment is paused until comparison succeeds.",
  },
  enqueue_failed: {
    tone: "warning",
    label: "Could not queue",
    sentence: "The change could not be queued. The next branch check will retry.",
  },
  policy_conflict: {
    tone: "warning",
    label: "Filters disagree",
    sentence: "Existing webhook filters disagree. Save one deployment policy to resume automation.",
  },
}

const UNAVAILABLE = ["unavailable", "stale", "policy_conflict"]

function linesOf(text: string) {
  return text
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
}

/**
 * Whether a push deploys the project by itself, and which pushes count. It
 * reads the branch watch on its own five-second poll, because what the head
 * says — when it last looked, what it decided — is what an operator comes
 * here to find out after a push that did or did not deploy.
 *
 * A poll that fails once the watch has been read keeps the form: swapping it
 * for the unavailable sentence unmounted the textarea being typed in. The
 * head says the reading has stopped moving instead.
 */
function AutomaticDeployment({
  projectId,
  environmentId,
  canEdit,
  configuration,
  deployment,
  projectName,
}: {
  projectId: number
  environmentId: number
  canEdit: boolean
  configuration: DeploymentEnvironmentConfiguration
  deployment: DeploymentSummary
  projectName: string
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
  if (watch.loading && !watch.data) {
    return (
      <SettingSection
        id="automatic-deployment"
        title="Automatic deployment"
        state={<Skeleton className="h-2.5 w-40" />}
      >
        <Skeleton className="h-28 w-full rounded-xl" />
        <Skeleton className="h-9 w-full" />
        <Skeleton className="h-9 w-full" />
      </SettingSection>
    )
  }
  if (!watch.data) {
    return (
      <SettingSection
        id="automatic-deployment"
        title="Automatic deployment"
        status={<Status tone="warning" label="Unavailable" />}
      >
        <FormNote tone="warning">Automatic deployment status is unavailable.</FormNote>
        <Button type="button" variant="ghost" size="sm" onClick={watch.refresh}>
          Try again
        </Button>
      </SettingSection>
    )
  }
  const product = sourceProduct(deployment, configuration.source) ?? "git"
  return (
    <AutomaticDeploymentForm
      watch={watch.data}
      projectId={projectId}
      environmentId={environmentId}
      canEdit={canEdit}
      github={product === "github"}
      product={product}
      repository={
        configuration.source?.repository ??
        deployment.sourceRepository ??
        repositoryName(configuration.source?.url ?? deployment.sourceRemote)
      }
      projectName={projectName}
      stale={Boolean(watch.error)}
      onSaved={watch.refresh}
    />
  )
}

type PolicyDraft = { automatic: boolean; commitStatuses: boolean; include: string; exclude: string }

function AutomaticDeploymentForm({
  watch,
  projectId,
  environmentId,
  canEdit,
  github,
  product,
  repository,
  projectName,
  stale,
  onSaved,
}: {
  watch: DeploymentGitWatch
  projectId: number
  environmentId: number
  canEdit: boolean
  github: boolean
  product: string
  repository?: string
  projectName: string
  /** The last poll failed; what is drawn is the watch as it was last read. */
  stale: boolean
  onSaved: () => void
}) {
  const policy: DeploymentGitPolicy = watch.policy ?? {
    automatic: true,
    commitStatuses: true,
    watchInclude: [],
    watchExclude: [],
    revision: 0,
  }
  const draft = useSettingDraft<PolicyDraft>(`deploy.${projectId}.settings.policy`, {
    automatic: policy.automatic,
    commitStatuses: policy.commitStatuses ?? true,
    include: policy.watchInclude.join("\n"),
    exclude: policy.watchExclude.join("\n"),
  })
  const { automatic, commitStatuses, include, exclude } = draft.value
  const patch = (fields: Partial<PolicyDraft>) => draft.set((prev) => ({ ...prev, ...fields }))
  const [saving, setSaving] = useState(false)

  const unavailable = UNAVAILABLE.includes(watch.status)
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

  // Only what the picture above the switch cannot say. It already draws the
  // branch, how often it is read and whether a push deploys by itself, so
  // the steady states need no sentence; these three are the exceptions.
  const explanation = !automatic
    ? undefined
    : unavailable
      ? "Could not check the production branch. Check repository access and credentials."
      : watch.status === "not_applicable"
        ? "Signed hooks and schedules can request deployments. This source has no branch to poll."
        : watch.status === "awaiting_first_deployment"
          ? `After your first deployment, new commits to ${watch.branch} deploy automatically.`
          : undefined

  const included = linesOf(include).length
  const excluded = linesOf(exclude).length
  const decision = watch.reason ? DECISIONS[watch.reason] : undefined

  const save = async (event: FormEvent) => {
    event.preventDefault()
    setSaving(true)
    try {
      await put(`/deploy/${projectId}/environments/${environmentId}/git-policy`, {
        automatic,
        commitStatuses,
        revision: policy.revision,
        watchInclude: linesOf(include),
        watchExclude: linesOf(exclude),
      })
      // The globs went out one per line, trimmed; the draft takes that shape
      // too, or a blank line left in would read as an edit the save did not make.
      patch({ include: linesOf(include).join("\n"), exclude: linesOf(exclude).join("\n") })
      notify.success("Deployment policy saved")
      onSaved()
    } catch (error) {
      notify.error("Could not save deployment policy", error)
    } finally {
      setSaving(false)
    }
  }

  return (
    <SettingForm
      name="Automatic deployment"
      onSubmit={save}
      dirty={draft.dirty}
      changes={draft.changes}
      saving={saving}
      canEdit={canEdit}
      onDiscard={draft.discard}
      applies="immediately"
    >
      <SettingSection
        id="automatic-deployment"
        title="Automatic deployment"
        state={
          <span className="block space-y-1">
            {/* How often it looks is the picture's Watch node. */}
            <span className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1">
              {watch.branch && <BranchChip branch={watch.branch} className="max-w-40" />}
              {watch.checkedAt && (
                <span>
                  checked{" "}
                  <time dateTime={watch.checkedAt} title={timestamp(watch.checkedAt)}>
                    {relativeTime(watch.checkedAt)}
                  </time>
                </span>
              )}
            </span>
            {(included > 0 || excluded > 0) && (
              <span className="block">
                {included > 0 ? (
                  <>
                    <span className="numeric">{included}</span> {included === 1 ? "path" : "paths"}{" "}
                    watched
                  </>
                ) : (
                  "every path watched"
                )}
                {excluded > 0 && (
                  <>
                    {" · "}
                    <span className="numeric">{excluded}</span> excluded
                  </>
                )}
              </span>
            )}
          </span>
        }
        status={
          <span className="flex flex-col items-start gap-1">
            <Status tone={tone} label={label} />
            {stale && <Status key="stale" tone="warning" label="Not refreshing" />}
            {decision && <Status key={watch.reason} tone={decision.tone} label={decision.label} />}
            {decision && <span className="block">{decision.sentence}</span>}
            {settingStatus({ dirty: draft.dirty })}
          </span>
        }
      >
        <AutoDeployPicture
          watch={watch}
          automatic={automatic}
          product={product}
          repository={repository}
          filters={included}
          projectName={projectName}
        />
        <OptionList>
          <OptionRow
            title="Deploy automatically"
            hint={explanation}
            checked={automatic}
            onCheckedChange={(next) => patch({ automatic: next })}
            disabled={!canEdit}
          >
            {/* Only automatic deployments read these, so they live under the
                switch that turns them on rather than beside it (§7). */}
            <FieldRow columns={2}>
              <Field
                label="Include paths"
                htmlFor="git-policy-include"
                hint="One glob per line · empty watches everything"
                info="Polling and push webhooks compare the complete Git changes since the last attempted deployment. Use directory/** for a directory and everything in it."
              >
                <Textarea
                  id="git-policy-include"
                  value={include}
                  onChange={(event) => patch({ include: event.target.value })}
                  readOnly={!canEdit}
                  placeholder="services/api/**"
                  className="min-h-20 font-mono sm:text-xs"
                />
              </Field>
              <Field
                label="Exclude paths"
                htmlFor="git-policy-exclude"
                hint="Wins over include"
                info="A change that matches both lists is ignored."
              >
                <Textarea
                  id="git-policy-exclude"
                  value={exclude}
                  onChange={(event) => patch({ exclude: event.target.value })}
                  readOnly={!canEdit}
                  placeholder="docs/**"
                  className="min-h-20 font-mono sm:text-xs"
                />
              </Field>
            </FieldRow>
          </OptionRow>
          <OptionRow
            title={
              // In the run of the words rather than beside them, so a title
              // that wraps on a phone keeps its mark at the start of its first
              // line instead of centred against both.
              <span>
                <ProductGlyph id="github" className="mr-1.5 inline-block align-[-2px]" />
                Report each release as a GitHub commit status
              </span>
            }
            hint={github ? undefined : "This source is not on GitHub.com — nothing is reported."}
            checked={commitStatuses}
            onCheckedChange={(next) => patch({ commitStatuses: next })}
            disabled={!canEdit}
          />
        </OptionList>
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
      </SettingSection>
    </SettingForm>
  )
}

/**
 * How a push reaches a deployment: the repository, the watch that reads its
 * branch, and the project it deploys.
 *
 * The lines say what the policy does, in the vocabulary every other wiring
 * picture uses: a pulse travels while pushes deploy by themselves, the line
 * is still while deployments are manual, and dashed — amber where something
 * is wrong — while there is nothing to carry yet (no first deployment) or the
 * watch cannot read the branch. It is drawn from the draft, so turning the
 * switch off stills the line before anything is saved.
 */
function AutoDeployPicture({
  watch,
  automatic,
  product,
  repository,
  filters,
  projectName,
}: {
  watch: DeploymentGitWatch
  automatic: boolean
  product: string
  repository?: string
  filters: number
  projectName: string
}) {
  const project = useProject()
  const { deployment } = project.detail
  const container = useRef<HTMLDivElement>(null)
  const sourceMark = useRef<HTMLDivElement>(null)
  const watchMark = useRef<HTMLDivElement>(null)
  const projectMark = useRef<HTMLDivElement>(null)
  const broken = UNAVAILABLE.includes(watch.status)
  const waiting = watch.status === "awaiting_first_deployment"
  const carries = automatic && !broken && !waiting && watch.status !== "not_applicable"

  return (
    <SettingPicture
      label="How a push reaches a deployment"
      containerRef={container}
      lines={
        <>
          <AnimatedBeam
            containerRef={container}
            fromRef={sourceMark}
            toRef={watchMark}
            still={!carries}
            dashed={broken}
            tone={broken ? "warning" : "default"}
            duration={2.6}
          />
          <AnimatedBeam
            containerRef={container}
            fromRef={watchMark}
            toRef={projectMark}
            still={!carries}
            dashed={broken || waiting || !automatic}
            tone={broken ? "warning" : "default"}
            duration={2.6}
            delay={0.8}
          />
        </>
      }
      start={[
        <WireNode
          key="source"
          nodeRef={sourceMark}
          align="end"
          mark={
            <WireMark tone="logo" size="md">
              <ProductGlyph id={product} />
            </WireMark>
          }
          eyebrow="Repository"
          title={<span className="block truncate">{repository ?? "Repository"}</span>}
          hint={
            watch.branch && (
              <span className="mt-0.5 inline-flex max-w-full">
                <BranchChip branch={watch.branch} className="max-w-40" />
              </span>
            )
          }
        />,
      ]}
      startLabel={
        filters > 0 && (
          <WireLabel lit={carries} className="bottom-1/2 mb-2">
            {filters} {filters === 1 ? "path" : "paths"}
          </WireLabel>
        )
      }
      middle={
        <WireNode
          nodeRef={watchMark}
          align="center"
          mark={
            broken ? (
              <WireMark tone="warning" size="md">
                <Clock />
              </WireMark>
            ) : (
              <WireMark size="md">
                <Clock />
              </WireMark>
            )
          }
          eyebrow="Watch"
          title={
            broken
              ? "Cannot read the branch"
              : watch.status === "not_applicable"
                ? "No branch to poll"
                : `Every ${watch.intervalSeconds} s`
          }
        />
      }
      end={
        <WireNode
          nodeRef={projectMark}
          align="start"
          // The project as itself, as the Automation and Databases pictures
          // draw it: the brand is this dashboard, not a project it deploys.
          mark={
            automatic && !broken ? (
              <span className="flex size-11 items-center justify-center">
                <ProjectMark deployment={deployment} product={project.product} size="md" />
              </span>
            ) : (
              <WirePlaceholder
                size="md"
                product={project.product}
                fallback={WORKLOAD_GLYPH[deployment.profile]}
              />
            )
          }
          eyebrow="Deploys"
          title={<span className="block truncate">{projectName}</span>}
          hint={
            !automatic
              ? "only when you press Deploy"
              : waiting
                ? "after its first deployment"
                : broken
                  ? "paused until the branch reads"
                  : "each matching commit"
          }
        />
      }
    />
  )
}
