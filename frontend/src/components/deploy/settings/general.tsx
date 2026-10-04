"use client"

import { useRef, useState } from "react"
import { Clock, External } from "@/components/icons"
import { ApiError, get, put } from "@/lib/api"
import { plural, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { LANES, hueFor } from "@/lib/hue"
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
import { CredentialSelect } from "@/components/deploy/credentials-page"
import { DetectionProposalPanel } from "@/components/deploy/settings/detection-proposal"
import { applyDetectionChanges } from "@/components/deploy/settings/detection-changes"
import { ComposeSourceSettings } from "@/components/deploy/settings/compose-source"

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
 * Name head; the release strategy to the Runtime page's Releases section;
 * whether it deploys itself to the Automatic deployment head, and how often
 * it looks, when it last did and what it watches to that section's picture.
 *
 * What a field needs while it is typed stays under it; the reasoning behind a
 * field is behind its ⓘ, so the page reads as heads, fields and switches.
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
              {configuration.source?.kind === "compose" && (
                <ComposeSourceSettings
                  key={configuration.revision}
                  projectId={projectId}
                  environmentId={environmentId}
                  canEdit={canEdit}
                  configuration={configuration}
                  onSaved={() => {
                    state.refresh()
                    project.refresh()
                  }}
                />
              )}
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

  const save = async () => {
    setRefused(false)
    if (!DEPLOYMENT_NAME.test(value)) {
      setError(NAME_ERROR)
      setRefused(true)
      return false
    }
    setError(undefined)
    setSaving(true)
    try {
      await put(`/deploy/${projectId}`, { name: value })
      onSaved()
      return true
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 409 && caught.code === "name_taken") {
        setError("That name is already used by another project")
        setRefused(true)
      } else {
        notify.error("Could not rename project", caught)
      }
      return false
    } finally {
      setSaving(false)
    }
  }

  return (
    <SettingForm
      name="Project name"
      onSave={save}
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
          <span className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1">
            <ProjectMark deployment={deployment} product={product} size="xs" />
            <span className="text-foreground">{WORKLOAD_LABELS[deployment.profile]}</span>
            <span aria-hidden>·</span>
            <span>
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
          info="Used in URLs, container labels and release history."
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

  const save = async () => {
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
      onSaved()
      return true
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
      return false
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
      onSave={save}
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
              info="HTTPS or SSH, with no password or token in it. A connected GitHub repository is read through the App; any other URL is read with the saved credential you pick."
              hint={
                needsReentry ? (
                  "Enter the repository again."
                ) : throughApp ? (
                  // The App's installation is the credential a connected
                  // repository is read with; the picker lists tokens and keys,
                  // so it had nothing to show here but an empty trigger.
                  <span className="inline-flex items-center gap-1.5">
                    <ProductGlyph id="github" className="size-3" />
                    Read through the GitHub App&rsquo;s installation
                  </span>
                ) : undefined
              }
              error={fieldErrors.url ?? (throughApp ? fieldErrors.credentialId : undefined)}
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
                    {/* The branch's own hue, as the git views draw it, so the
                        same name is the same colour wherever it is read. */}
                    <SourceBranch
                      aria-hidden
                      style={value.ref ? { color: hueFor(value.ref, LANES) } : undefined}
                    />
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
                info="Empty builds from the repository root."
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
            {!throughApp && (
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
              <Field label="Platform" htmlFor="source-platform" error={fieldErrors.platform}>
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
      </SettingSection>
    </SettingForm>
  )
}

/**
 * A Git source as its head reads it: the forge's mark and the repository,
 * which opens its page, on one line with the branch and the commit the
 * configuration resolved to; and where the checkout lives on this server.
 *
 * How it is reached is not said here: the URL field under the head says
 * whether the GitHub App reads it, and a small-caps "Git URL" beside a URL
 * was the same fact again at 10px.
 */
function GitSourceState({
  product,
  remote,
  repository,
  branch,
  revision,
  repoPath,
}: {
  product: string
  remote?: string
  repository?: string
  branch?: string
  revision?: string
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
    <span className="block space-y-1">
      <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
        {page ? (
          <a
            href={page}
            target="_blank"
            rel="noreferrer"
            className="flex max-w-full min-w-0 items-center gap-1.5 rounded-sm focus-ring transition-colors hover:text-foreground"
          >
            {name}
            <External aria-hidden className="size-3 shrink-0" />
          </a>
        ) : (
          <span className="flex min-w-0 items-center gap-1.5">{name}</span>
        )}
        {branch && <BranchChip branch={branch} className="max-w-40" />}
        <ShortSha sha={revision} />
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
      <SettingSection id="automatic-deployment" title="Automatic deployment">
        <Skeleton className="h-44 w-full rounded-xl" />
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

  const save = async () => {
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
      onSaved()
      return true
    } catch (error) {
      notify.error("Could not save deployment policy", error)
      return false
    } finally {
      setSaving(false)
    }
  }

  return (
    <SettingForm
      name="Automatic deployment"
      onSave={save}
      dirty={draft.dirty}
      changes={draft.changes}
      saving={saving}
      canEdit={canEdit}
      onDiscard={draft.discard}
      applies="immediately"
    >
      {/* No state line under the title: the branch, how often it is read,
          when it last was and the path filters are all drawn in the picture
          right under it, and saying them twice was most of the old head. */}
      <SettingSection
        id="automatic-deployment"
        title="Automatic deployment"
        status={
          <>
            <Status key={label} tone={tone} label={label} className="animate-rise" />
            {stale && (
              <Status key="stale" tone="warning" label="Not refreshing" className="animate-rise" />
            )}
            {settingStatus({ dirty: draft.dirty })}
          </>
        }
      >
        <AutoDeployPicture
          watch={watch}
          automatic={automatic}
          product={product}
          repository={repository}
          included={included}
          excluded={excluded}
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
            {/* "One glob per line" is said by the placeholders, which hold two
                lines each; the rest is the paragraph wanted once, behind ⓘ. */}
            <FieldRow columns={2}>
              <Field
                label="Include paths"
                htmlFor="git-policy-include"
                info="One glob per line; empty watches everything. Polling and push webhooks compare the complete Git changes since the last attempted deployment. Use directory/** for a directory and everything in it."
              >
                <Textarea
                  id="git-policy-include"
                  value={include}
                  onChange={(event) => patch({ include: event.target.value })}
                  readOnly={!canEdit}
                  placeholder={"services/api/**\npackages/shared/**"}
                  className="min-h-20 font-mono sm:text-xs"
                />
              </Field>
              <Field
                label="Exclude paths"
                htmlFor="git-policy-exclude"
                info="One glob per line. Exclude wins: a change that matches both lists is ignored."
              >
                <Textarea
                  id="git-policy-exclude"
                  value={exclude}
                  onChange={(event) => patch({ exclude: event.target.value })}
                  readOnly={!canEdit}
                  placeholder={"docs/**\n**/*.md"}
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
 * branch, and the project it deploys — and, under them, what the watch decided
 * the last time it looked.
 *
 * The lines say what the policy does, in the vocabulary every other wiring
 * picture uses: a pulse travels while pushes deploy by themselves, the line
 * is still while deployments are manual, and dashed — amber where something
 * is wrong — while there is nothing to carry yet (no first deployment) or the
 * watch cannot read the branch. It is drawn from the draft, so turning the
 * switch off stills the line before anything is saved, and the project's mark
 * and its line of detail rise into their new state rather than swapping.
 *
 * On the page's own ground rather than in a frame, as the GitHub App's
 * picture on Credentials is: the dot grid fades out towards its edges, so the
 * picture has a middle and needs no border to read as one thing — a framed
 * box was the one container left on a page of hairlines.
 *
 * The last decision is a line under the drawing at the size of the rest of
 * the picture's words. It used to sit in the section head's status slot,
 * which sets no size of its own, so its sentence rendered at the page's 16px —
 * the largest text in the section, and none of it a heading.
 */
function AutoDeployPicture({
  watch,
  automatic,
  product,
  repository,
  included,
  excluded,
  projectName,
}: {
  watch: DeploymentGitWatch
  automatic: boolean
  product: string
  repository?: string
  included: number
  excluded: number
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
  const decision = watch.reason ? DECISIONS[watch.reason] : undefined
  const filters = [included > 0 && plural(included, "path"), excluded > 0 && `${excluded} excluded`]
    .filter(Boolean)
    .join(" · ")
  const reach = !automatic
    ? "only when you press Deploy"
    : waiting
      ? "after its first deployment"
      : broken
        ? "paused until the branch reads"
        : "each matching commit"

  return (
    <div className="relative animate-rise py-6">
      <div aria-hidden className="wire-grid pointer-events-none absolute inset-0" />
      <div ref={container} className="relative">
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
        {/* Wide, the marks stand in one row and the watch's words hang under
            it, so the lines run level; narrow, they stand in one column with
            the words to their right and the lines run down the marks. */}
        <ol
          aria-label="How a push reaches a deployment"
          className="flex flex-col gap-6 lg:grid lg:grid-cols-[minmax(0,1fr)_minmax(3rem,0.5fr)_auto_minmax(3rem,0.5fr)_minmax(0,1fr)] lg:items-center lg:gap-0 lg:pb-16"
        >
          <li className="min-w-0">
            <WireNode
              nodeRef={sourceMark}
              align="end"
              mark={
                <WireMark tone="logo">
                  <ProductGlyph id={product} />
                </WireMark>
              }
              eyebrow="Repository"
              title={<span className="block truncate">{repository ?? "Repository"}</span>}
              hint={
                watch.branch && (
                  <span className="mt-1 inline-flex max-w-full">
                    <BranchChip branch={watch.branch} className="max-w-40" />
                  </span>
                )
              }
            />
          </li>
          <li aria-hidden className="relative hidden self-stretch lg:block">
            {filters && (
              <WireLabel lit={carries} className="bottom-1/2 mb-2">
                {filters}
              </WireLabel>
            )}
          </li>
          <li className="min-w-0">
            <WireNode
              nodeRef={watchMark}
              align="center"
              mark={
                <WireMark tone={broken ? "warning" : "neutral"}>
                  <Clock />
                </WireMark>
              }
              eyebrow="Watch"
              title={
                broken
                  ? "Cannot read the branch"
                  : watch.status === "not_applicable"
                    ? "No branch to poll"
                    : `Every ${watch.intervalSeconds} s`
              }
              hint={
                watch.checkedAt && (
                  <>
                    checked{" "}
                    <time dateTime={watch.checkedAt} title={timestamp(watch.checkedAt)}>
                      {relativeTime(watch.checkedAt)}
                    </time>
                  </>
                )
              }
            />
          </li>
          <li aria-hidden className="hidden lg:block" />
          <li className="min-w-0">
            <WireNode
              nodeRef={projectMark}
              align="start"
              // The project as itself, as the Automation and Databases pictures
              // draw it: the brand is this dashboard, not a project it deploys.
              mark={
                automatic && !broken ? (
                  <span
                    key="deploys"
                    className="flex size-14 animate-rise items-center justify-center"
                  >
                    <ProjectMark deployment={deployment} product={project.product} size="lg" />
                  </span>
                ) : (
                  <WirePlaceholder
                    key="paused"
                    product={project.product}
                    fallback={WORKLOAD_GLYPH[deployment.profile]}
                    className="animate-rise"
                  />
                )
              }
              eyebrow="Deploys"
              title={<span className="block truncate">{projectName}</span>}
              hint={
                <span key={reach} className="block animate-rise">
                  {reach}
                </span>
              }
            />
          </li>
        </ol>
      </div>
      {decision && (
        <div
          key={watch.reason}
          className="relative mt-5 flex animate-rise flex-wrap items-center gap-x-2 gap-y-1 text-xs lg:justify-center"
        >
          <Status tone={decision.tone} label={decision.label} />
          <span
            className={decision.tone === "warning" ? "text-foreground" : "text-muted-foreground"}
          >
            {decision.sentence}
          </span>
        </div>
      )}
    </div>
  )
}
