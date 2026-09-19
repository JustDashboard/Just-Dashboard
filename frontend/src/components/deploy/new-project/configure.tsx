"use client"

import { useEffect, useMemo, useRef, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight, SettingsSliders } from "@/components/icons"
import { ApiError, get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { useMemoryState, useSessionState } from "@/lib/view-state"
import type {
  DeploymentBuildMethod,
  DeploymentDraft,
  DeploymentPreflight,
  DeploymentPreflightFinding,
  DeploymentRecipe,
  NodePackageManager,
  WorkloadProfile,
  GitHubBranch,
} from "@/lib/types"
import { Field, FieldRow, FormSection, OptionList, OptionRow } from "@/components/form"
import { Group, Panel, PanelBody, PanelHeader, Well } from "@/components/panel"
import { ErrorState, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"
import {
  DEPLOYMENT_NAME,
  WorkloadMark,
  frameworkLabel,
  humanize,
} from "@/components/deploy/vocabulary"
import {
  FindingRow,
  blockingFindings,
  warningFindings,
} from "@/components/deploy/deployment-findings"
import {
  discoveredEnvironmentRows,
  mergeDiscoveredRows,
  validateConfiguration,
  withPackageManagerRunner,
} from "@/components/deploy/deployment-defaults"
import { EnvironmentEditor } from "@/components/deploy/new-project/environment-editor"
import { PublicAddress } from "@/components/deploy/new-project/public-address"
import { ConfigureAdvanced } from "@/components/deploy/new-project/configure-advanced"
import {
  adoptImport,
  commitDraft,
  enqueueDeploy,
  environmentText,
  importEnvironment,
  loadDraft,
  preflightDraft,
  reinspect,
  saveConfiguration,
  selectCandidate,
  forgetNewProject,
  type ConfigureFlow,
  type EnvironmentDraft,
  type EnvironmentRow,
  type FlowUpdate,
} from "@/components/deploy/new-project/draft"

function asError(error: unknown) {
  return error instanceof Error ? error : new Error(String(error))
}

/**
 * The one configure screen every source lands on, from a picked repository to
 * an adopted container. What differs between sources is which parts show —
 * the build fields are git only, an import ends in one "Adopt workload"
 * button instead of Save/Deploy — not how any of it is built.
 */
export function Configure({
  flow,
  onFlowChange,
  onChangeSource,
  initialAdvanced,
}: {
  flow: ConfigureFlow
  // Accepts the functional updater form too: the two calls in `submit` below
  // run after an `await`, and merging onto the closed-over `flow` there would
  // revert anything the operator typed while the request was in flight.
  // `new-project.tsx` wires this straight to the remembered flow's setter,
  // which is why the type carries `| null` here even though this component
  // only ever runs once `flow` itself is set — every functional update below
  // guards it back out.
  onFlowChange: (next: FlowUpdate) => void
  onChangeSource: () => void
  initialAdvanced: boolean
}) {
  const router = useRouter()
  const advancedRef = useRef<HTMLDivElement>(null)
  const [nameTouched, setNameTouched] = useState(false)
  // The environment is remembered in memory only — never Web Storage, never
  // the URL — and belongs to this draft: a different source starts from what
  // its own detection found rather than from the last one's secrets.
  const [environment, setEnvironment] = useMemoryState<EnvironmentDraft | null>(
    "deploy.new.configure.environment",
    null,
  )
  const draftId = flow.draft.id
  const own = environment?.draftId === draftId ? environment : null
  const discovered = useMemo(() => discoveredEnvironmentRows(flow.candidate), [flow.candidate])
  const envRows = own?.rows ?? discovered
  const dotenv = own?.dotenv ?? ""
  const setEnvRows = (next: EnvironmentRow[] | ((rows: EnvironmentRow[]) => EnvironmentRow[])) =>
    setEnvironment((current) => {
      const mine = current?.draftId === draftId ? current : null
      const rows = typeof next === "function" ? next(mine?.rows ?? discovered) : next
      return { draftId, rows, dotenv: mine?.dotenv ?? "" }
    })
  const setDotenv = (value: string) =>
    setEnvironment((current) => {
      const mine = current?.draftId === draftId ? current : null
      return { draftId, rows: mine?.rows ?? discovered, dotenv: value }
    })
  // The detected rows are written down as soon as they exist, so a generated
  // value (a Laravel key) is the same one on every visit rather than minted
  // again each time the page mounts.
  useEffect(() => {
    if (own) return
    setEnvironment({ draftId, rows: discovered, dotenv: "" })
  }, [own, draftId, discovered, setEnvironment])
  // A branch change re-detects, and the new candidate may read variables the
  // old one did not; they join the rows without touching anything typed.
  const rowsCandidate = useRef(flow.candidate)
  useEffect(() => {
    const previous = rowsCandidate.current
    if (previous === flow.candidate) return
    rowsCandidate.current = flow.candidate
    const found = discoveredEnvironmentRows(flow.candidate)
    setEnvironment((current) => {
      const mine = current?.draftId === draftId ? current : null
      const rows = mine?.rows ?? discoveredEnvironmentRows(previous)
      return { draftId, rows: mergeDiscoveredRows(rows, found), dotenv: mine?.dotenv ?? "" }
    })
  }, [flow.candidate, draftId, setEnvironment])
  const [branch, setBranch] = useState(flow.source.ref ?? "main")
  const [branchBusy, setBranchBusy] = useState(false)
  // Preflight's findings and which warnings were acknowledged belong to the
  // draft they were computed for, so a fresh source never inherits them.
  const [review, setReview] = useSessionState<{
    draftId: string
    preflight?: DeploymentPreflight
    acknowledged: string[]
  } | null>("deploy.new.configure.preflight", null)
  const preflight = review?.draftId === draftId ? review.preflight : undefined
  const acknowledged = review?.draftId === draftId ? review.acknowledged : []
  const setPreflight = (next: DeploymentPreflight) =>
    setReview((current) => ({
      draftId,
      preflight: next,
      acknowledged: current?.draftId === draftId ? current.acknowledged : [],
    }))
  const setAcknowledged = (next: string[]) =>
    setReview((current) => ({
      draftId,
      preflight: current?.draftId === draftId ? current.preflight : undefined,
      acknowledged: next,
    }))
  const [configErrors, setConfigErrors] = useState<Record<string, string>>({})
  const [busy, setBusy] = useState("")
  const [failure, setFailure] = useState<Error>()
  // A blueprint's declarative variables (a generated password, a EULA
  // acceptance) are review material, not a power-user setting — open by
  // default the same way the old wizard opened them for a blueprint source.
  const [advancedOpen, setAdvancedOpen] = useSessionState(
    "deploy.new.configure.advanced",
    flow.source.mode === "blueprint" && flow.configuration.variables.length > 0,
    initialAdvanced ? true : undefined,
  )
  const [created, setCreated] = useState<{
    projectId: number
    environmentId: number
    ready: boolean
  }>()
  // Once the project exists nothing about this setup is unfinished, and the
  // next "New project" starts blank. Forgotten on the way out rather than at
  // commit, so the "Deployment created" panel is not swapped for the source
  // chooser in the frame before the project page opens.
  const done = Boolean(created)
  useEffect(() => {
    if (!done) return
    return () => forgetNewProject()
  }, [done])

  const branches = usePoll(
    (signal) =>
      get<GitHubBranch[]>(
        `/git/github/branches?repo=${encodeURIComponent(flow.githubRepo ?? "")}`,
        undefined,
        signal,
      ),
    0,
    [flow.githubRepo],
    { enabled: Boolean(flow.githubRepo) },
  )

  const configuration = flow.configuration
  const isGitSource = flow.source.kind === "git" || flow.source.kind === "local"
  const isImport = flow.source.kind === "import"
  const nameInvalid = nameTouched && !DEPLOYMENT_NAME.test(flow.name)
  // A detected row the operator left empty is skipped, not set to nothing:
  // the application may have a default for it, and an empty secret is a
  // value that fails somewhere far from here.
  const text = environmentText(
    envRows.filter((row) => !row.detected || row.value),
    dotenv,
  )

  const setConfiguration = (next: typeof configuration) =>
    onFlowChange({ ...flow, configuration: next })
  const updateBuild = (patch: Partial<typeof configuration.build>) =>
    setConfiguration({ ...configuration, build: { ...configuration.build, ...patch } })

  const changeBranch = async (nextRef: string) => {
    setBranch(nextRef)
    if (!nextRef.trim() || nextRef === flow.source.ref) return
    setBranchBusy(true)
    setFailure(undefined)
    try {
      const nextSource = { ...flow.source, ref: nextRef }
      const result = await reinspect(flow.draft, flow.profile, nextSource)
      onFlowChange({
        ...flow,
        source: nextSource,
        draft: result.draft,
        candidate: result.candidate,
        detection: result.detection,
        configuration: result.configuration,
      })
    } catch (error) {
      setFailure(asError(error))
    } finally {
      setBranchBusy(false)
    }
  }

  const changeProfile = (profile: WorkloadProfile) => {
    onFlowChange({
      ...flow,
      profile,
      configuration: {
        ...configuration,
        domains: profile === "worker" ? [] : configuration.domains,
        build: {
          ...configuration.build,
          startCommand:
            profile === "static" && configuration.build.method === "recipe"
              ? ""
              : configuration.build.startCommand || flow.candidate?.startCommand || "",
        },
        runtime: {
          ...configuration.runtime,
          internalPort:
            profile === "worker"
              ? 0
              : profile === "static" && configuration.build.method !== "dockerfile"
                ? 80
                : configuration.runtime.internalPort || 3000,
        },
      },
    })
  }

  // `detection_ambiguous`'s only remedy — /detect has always accepted a
  // `selectedId`, but nothing sent one, so the finding blocked every ambiguous
  // plan with no way to resolve it from this screen.
  const pickCandidate = async (id: string) => {
    setFailure(undefined)
    setBusy("detect")
    try {
      const result = await selectCandidate(flow.draft, flow.profile, flow.source, id)
      onFlowChange((current) =>
        current
          ? {
              ...current,
              draft: result.draft,
              candidate: result.candidate,
              detection: result.detection,
              configuration: result.configuration,
            }
          : current,
      )
    } catch (error) {
      setFailure(asError(error))
    } finally {
      setBusy("")
    }
  }

  // A compose/required-variable decision's field lives inside Advanced, which
  // starts closed; open it and bring it into view rather than leave the
  // finding's "Next:" sentence pointing at a control nobody can see.
  const openRemedyField = (finding: DeploymentPreflightFinding) => {
    if (!finding.fieldId?.startsWith("variables.")) return
    setAdvancedOpen(true)
    requestAnimationFrame(() =>
      advancedRef.current?.scrollIntoView({ block: "start", behavior: "smooth" }),
    )
  }

  const submit = async (operation: "save" | "deploy") => {
    if (!DEPLOYMENT_NAME.test(flow.name)) {
      setNameTouched(true)
      setFailure(new Error("Use 1–64 letters, numbers, dots, dashes, or underscores for the name."))
      return
    }
    if (
      envRows.some((row) => (row.name || row.value) && !/^[A-Za-z_][A-Za-z0-9_]*$/.test(row.name))
    ) {
      setFailure(
        new Error(
          "Give each environment variable a valid key, using letters, numbers and underscores.",
        ),
      )
      return
    }
    const names = envRows.map((row) => row.name).filter(Boolean)
    if (new Set(names).size !== names.length) {
      setFailure(new Error("Each environment variable needs a unique key."))
      return
    }
    if (
      flow.profile === "static" &&
      configuration.build.method === "recipe" &&
      !configuration.build.outputDirectory?.trim()
    ) {
      setFailure(
        new Error("Set the output directory for your static website, such as dist or out."),
      )
      return
    }
    const nextErrors = validateConfiguration(configuration, flow.profile)
    setConfigErrors(nextErrors)
    if (Object.keys(nextErrors).length) {
      setFailure(new Error(Object.values(nextErrors)[0]))
      return
    }

    setBusy(operation)
    setFailure(undefined)
    try {
      // Readiness follows the runtime publication, which may differ from the
      // container port when Docker allocates a free host port.
      const toSave = {
        ...configuration,
        checks: configuration.checks.map((check) =>
          check.phase === "readiness" && check.kind === "http"
            ? { ...check, config: { ...(check.config ?? {}), port: undefined } }
            : check,
        ),
      }
      let saved: DeploymentDraft
      try {
        saved = await saveConfiguration(flow.draft, toSave)
      } catch (error) {
        // saveConfiguration bumps the revision on the server whether or not
        // what follows succeeds; without this, a failure past this point
        // left `flow.draft` on the old revision and every later save 409'd
        // forever. Re-loading the draft recovers the revision this save
        // itself just produced (or another tab's, either way the current one).
        if (!(error instanceof ApiError) || error.code !== "draft_revision_conflict") throw error
        const fresh = await loadDraft(flow.draft.id)
        saved = await saveConfiguration(fresh, toSave)
      }
      onFlowChange((current) =>
        current ? { ...current, draft: saved, configuration: toSave } : current,
      )
      const checkedDraft = await preflightDraft(saved)
      onFlowChange((current) =>
        current ? { ...current, draft: checkedDraft.draft, configuration: toSave } : current,
      )
      setPreflight(checkedDraft.preflight)

      const blockers = blockingFindings(checkedDraft.preflight.findings)
      const warnings = warningFindings(checkedDraft.preflight.findings)
      if (blockers.length) return
      const outstanding = warnings.filter((finding) => !acknowledged.includes(finding.code))
      if (outstanding.length) return

      const commit = isImport
        ? await adoptImport(checkedDraft.draft, acknowledged, flow.importPreview?.unsupported ?? [])
        : await commitDraft(checkedDraft.draft, acknowledged)
      setCreated({
        projectId: commit.projectId,
        environmentId: commit.environmentId,
        ready: !text.trim(),
      })

      if (text.trim()) {
        await importEnvironment(commit.projectId, commit.environmentId, commit.planRevision, text)
      }
      setCreated({ projectId: commit.projectId, environmentId: commit.environmentId, ready: true })

      if (operation === "deploy" && !isImport) {
        const run = await enqueueDeploy(commit.projectId, commit.environmentId)
        router.push(`/deploy/${commit.projectId}/runs/${run.id}`)
        return
      }
      router.push(`/deploy/${commit.projectId}`)
    } catch (error) {
      setFailure(asError(error))
    } finally {
      setBusy("")
    }
  }

  if (created)
    return (
      <Panel>
        <PanelHeader title="Deployment created" />
        <PanelBody className="space-y-4">
          {failure ? (
            <ErrorState error={failure} />
          ) : (
            <p className="text-body">Starting the first release…</p>
          )}
          <p className="text-body text-muted-foreground">
            Your project and configuration are saved.{" "}
            {created.ready
              ? "Open the deployment to check its run history and continue."
              : "Environment setup did not finish. Review the Variables settings before starting the first release."}
          </p>
          {!created.ready && text && (
            <details>
              <summary className="cursor-pointer text-body focus-ring">
                Keep a copy of your environment variables
              </summary>
              <Textarea
                className="mt-3 font-mono text-xs"
                aria-label="Unsaved environment variables"
                readOnly
                value={text}
              />
            </details>
          )}
          <Button asChild>
            <Link
              href={
                created.ready
                  ? `/deploy/${created.projectId}/deployments`
                  : `/deploy/${created.projectId}/settings/variables`
              }
            >
              {created.ready ? "Open deployment" : "Finish environment setup"}
              <ArrowRight className="size-4" />
            </Link>
          </Button>
        </PanelBody>
      </Panel>
    )

  const blockers = preflight ? blockingFindings(preflight.findings) : []
  const warnings = preflight ? warningFindings(preflight.findings) : []
  const outstanding = warnings.filter((finding) => !acknowledged.includes(finding.code))
  const candidates = flow.detection?.candidates ?? []
  const ambiguous =
    candidates.length > 1 || blockers.some((finding) => finding.code === "detection_ambiguous")

  return (
    <div className="mx-auto w-full max-w-3xl space-y-6">
      {/* Disabled while a submit is in flight: inputs left editable during the
          async save/preflight round trip could be typed into and then
          silently reverted once the response handler lands (§14). */}
      <fieldset disabled={Boolean(busy)} className="contents space-y-6">
        {failure && <ErrorState error={failure} />}

        <FormSection
          title="Source"
          actions={
            <Button
              variant="ghost"
              size="xs"
              className="text-muted-foreground"
              onClick={onChangeSource}
            >
              Change source
            </Button>
          }
        >
          <div className="flex min-w-0 flex-wrap items-center justify-between gap-3">
            <div className="flex min-w-0 items-center gap-3">
              <WorkloadMark profile={flow.profile} size="sm" />
              <span className="min-w-0 truncate text-body font-medium">{flow.sourceLabel}</span>
              {isGitSource &&
                (flow.githubRepo ? (
                  <Select value={branch} onValueChange={(value) => void changeBranch(value)}>
                    <SelectTrigger aria-label="Branch" className="w-36 font-mono">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {(branches.data ?? []).map((entry) => (
                        <SelectItem key={entry.name} value={entry.name}>
                          {entry.name}
                          {entry.default ? " (default)" : ""}
                        </SelectItem>
                      ))}
                      {!(branches.data ?? []).some((entry) => entry.name === branch) && (
                        <SelectItem value={branch}>{branch}</SelectItem>
                      )}
                    </SelectContent>
                  </Select>
                ) : (
                  <Input
                    aria-label="Branch"
                    value={branch}
                    disabled={branchBusy}
                    className="w-36 font-mono"
                    onChange={(event) => setBranch(event.target.value)}
                    onBlur={(event) => void changeBranch(event.target.value)}
                  />
                ))}
            </div>
            {flow.candidate?.framework && (
              <Tag tone="success">{frameworkLabel(flow.candidate.framework)}</Tag>
            )}
          </div>
          {flow.detection?.unavailable && (
            <Notice tone="warning" title="Some evidence is unavailable">
              {flow.detection.unavailable}
            </Notice>
          )}
          {flow.detection?.compose && (
            <div className="space-y-2 pt-1">
              <div className="flex flex-wrap gap-1.5">
                {flow.detection.compose.services.map((service) => (
                  <Tag key={service.name} mono>
                    {service.name}
                  </Tag>
                ))}
              </div>
              {flow.detection.compose.unsupported.map((item) => (
                <Notice key={item} tone="warning" title="Needs explicit review">
                  {item}
                </Notice>
              ))}
              <details>
                <summary className="cursor-pointer rounded-sm py-2 text-xs font-medium focus-ring">
                  Show effective Compose plan
                </summary>
                <Well className="max-h-72 text-hint whitespace-pre-wrap">
                  {flow.detection.compose.preview}
                </Well>
              </details>
            </div>
          )}
        </FormSection>

        {ambiguous && (
          <FormSection
            title="Choose the detected candidate"
            hint="Multiple equally strong roots or build methods were found."
          >
            <OptionList role="group" aria-label="Detected candidates">
              {candidates.map((item) => (
                <OptionRow
                  key={item.id}
                  title={item.name}
                  hint={`${humanize(item.confidence)} confidence · ${
                    item.framework ? `${item.framework} via ` : ""
                  }${humanize(item.buildMethod)}${
                    item.root && item.root !== "." ? ` in ${item.root}` : ""
                  }`}
                  checked={item.id === flow.detection?.selectedId}
                  onCheckedChange={(checked) => checked && void pickCandidate(item.id)}
                  disabled={busy === "detect"}
                />
              ))}
            </OptionList>
          </FormSection>
        )}

        <FieldRow columns={2}>
          <Field
            label="Project name"
            htmlFor="deployment-name"
            hint="Used in URLs, container labels and release history."
            error={
              nameInvalid
                ? "Start with a letter or number. Use up to 64 letters, numbers, dots, dashes, or underscores."
                : undefined
            }
          >
            <Input
              id="deployment-name"
              required
              maxLength={64}
              autoComplete="off"
              aria-invalid={nameInvalid}
              value={flow.name}
              onBlur={() => setNameTouched(true)}
              onChange={(event) => onFlowChange({ ...flow, name: event.target.value })}
            />
          </Field>
          {isGitSource && (
            <Field label="Project type" htmlFor="workload-type">
              <Select
                value={flow.profile}
                onValueChange={(value) => changeProfile(value as WorkloadProfile)}
              >
                <SelectTrigger id="workload-type" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="web">Web application</SelectItem>
                  <SelectItem value="static">Static website</SelectItem>
                  <SelectItem value="worker">Worker or bot</SelectItem>
                </SelectContent>
              </Select>
            </Field>
          )}
        </FieldRow>

        {isGitSource && (
          <details
            open={
              !flow.candidate ||
              Boolean(flow.detection?.unavailable) ||
              (flow.profile === "static" &&
                configuration.build.method === "recipe" &&
                !configuration.build.outputDirectory)
            }
          >
            <summary className="cursor-pointer rounded-md py-2 text-body font-medium focus-ring">
              Build & output settings
              {flow.candidate?.framework ? ` · ${frameworkLabel(flow.candidate.framework)}` : ""}
            </summary>
            <div className="grid gap-4 pt-3 sm:grid-cols-2">
              <Field label="Build method" htmlFor="build-method">
                <Select
                  value={configuration.build.method}
                  onValueChange={(value) => updateBuild({ method: value as DeploymentBuildMethod })}
                >
                  <SelectTrigger id="build-method" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="recipe">Automatic recipe</SelectItem>
                    <SelectItem value="dockerfile">Dockerfile</SelectItem>
                    <SelectItem value="static">Static files</SelectItem>
                  </SelectContent>
                </Select>
              </Field>
              <Field
                label="Port the app listens on"
                htmlFor="internal-port"
                hint="What the container serves on, not the host port."
              >
                <Input
                  id="internal-port"
                  type="number"
                  min={0}
                  max={65535}
                  value={configuration.runtime.internalPort ?? 0}
                  onChange={(event) =>
                    setConfiguration({
                      ...configuration,
                      runtime: {
                        ...configuration.runtime,
                        internalPort: Number(event.target.value) || 0,
                      },
                    })
                  }
                  className="font-mono"
                />
              </Field>
              {configuration.build.method === "recipe" && (
                <Field label="Language" htmlFor="recipe">
                  <Select
                    value={configuration.build.recipe ?? "node"}
                    onValueChange={(value) => {
                      const recipe = value as DeploymentRecipe
                      updateBuild({
                        recipe,
                        goVersion: recipe === "go" ? configuration.build.goVersion : undefined,
                        pythonVersion:
                          recipe === "python" ? configuration.build.pythonVersion : undefined,
                        packageManager:
                          recipe === "node" ? configuration.build.packageManager : undefined,
                      })
                    }}
                  >
                    <SelectTrigger id="recipe" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="node">JavaScript / TypeScript</SelectItem>
                      <SelectItem value="go">Go</SelectItem>
                      <SelectItem value="python">Python</SelectItem>
                    </SelectContent>
                  </Select>
                </Field>
              )}
              {configuration.build.method === "recipe" && configuration.build.recipe === "node" && (
                <Field
                  label="Package manager"
                  htmlFor="package-manager"
                  hint={
                    (flow.candidate?.packageManagers?.length ?? 0) > 1 &&
                    !flow.candidate?.packageManager
                      ? `This repository has lockfiles for ${flow.candidate?.packageManagers?.join(" and ")}. Choose the one it uses.`
                      : "Leave on the lockfile unless the repository has more than one."
                  }
                >
                  <Select
                    value={configuration.build.packageManager ?? "lockfile"}
                    onValueChange={(value) => {
                      const packageManager =
                        value === "lockfile" ? undefined : (value as NodePackageManager)
                      updateBuild({
                        packageManager,
                        buildCommand: withPackageManagerRunner(
                          configuration.build.buildCommand ?? "",
                          packageManager,
                        ),
                        startCommand: withPackageManagerRunner(
                          configuration.build.startCommand ?? "",
                          packageManager,
                        ),
                      })
                    }}
                  >
                    <SelectTrigger id="package-manager" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="lockfile">From the lockfile</SelectItem>
                      <SelectItem value="bun">Bun</SelectItem>
                      <SelectItem value="npm">npm</SelectItem>
                      <SelectItem value="pnpm">pnpm</SelectItem>
                      <SelectItem value="yarn">Yarn</SelectItem>
                    </SelectContent>
                  </Select>
                </Field>
              )}
              {configuration.build.method === "recipe" && configuration.build.recipe === "go" && (
                <Field
                  label="Go version"
                  htmlFor="go-version"
                  hint="Leave empty to use .go-version or go.mod."
                >
                  <Input
                    id="go-version"
                    value={configuration.build.goVersion ?? ""}
                    onChange={(event) => updateBuild({ goVersion: event.target.value })}
                    placeholder="1.26"
                  />
                </Field>
              )}
              {configuration.build.method === "recipe" &&
                configuration.build.recipe === "python" && (
                  <Field
                    label="Python version"
                    htmlFor="python-version"
                    hint="Leave empty to use .python-version, runtime.txt or pyproject.toml."
                  >
                    <Input
                      id="python-version"
                      value={configuration.build.pythonVersion ?? ""}
                      onChange={(event) => updateBuild({ pythonVersion: event.target.value })}
                      placeholder="3.13"
                    />
                  </Field>
                )}
              {configuration.build.method === "dockerfile" && (
                <Field label="Dockerfile path" htmlFor="dockerfile">
                  <Input
                    id="dockerfile"
                    className="font-mono"
                    value={configuration.build.dockerfile ?? "Dockerfile"}
                    onChange={(event) => updateBuild({ dockerfile: event.target.value })}
                  />
                </Field>
              )}
              {configuration.build.method !== "dockerfile" && (
                <>
                  <Field label="Build command" htmlFor="build-command">
                    <Input
                      id="build-command"
                      value={configuration.build.buildCommand ?? ""}
                      onChange={(event) => updateBuild({ buildCommand: event.target.value })}
                      placeholder="npm run build"
                      className="font-mono"
                    />
                  </Field>
                  <Field
                    label="Start command"
                    htmlFor="start-command"
                    hint="Leave empty and set an output directory to serve static files instead."
                  >
                    <Input
                      id="start-command"
                      value={configuration.build.startCommand ?? ""}
                      onChange={(event) => updateBuild({ startCommand: event.target.value })}
                      placeholder="npm run start"
                      className="font-mono"
                    />
                  </Field>
                </>
              )}
              <Field label="Root directory" htmlFor="root-directory" hint="For a monorepo.">
                <Input
                  id="root-directory"
                  value={configuration.build.rootDirectory ?? ""}
                  onChange={(event) => updateBuild({ rootDirectory: event.target.value })}
                  placeholder="apps/web"
                  className="font-mono"
                />
              </Field>
              <Field
                label="Output directory"
                htmlFor="output-directory"
                hint="Set only for a site with no server process."
              >
                <Input
                  id="output-directory"
                  value={configuration.build.outputDirectory ?? ""}
                  onChange={(event) => updateBuild({ outputDirectory: event.target.value })}
                  placeholder="dist"
                  className="font-mono"
                />
              </Field>
              {(configuration.build.method === "static" ||
                (configuration.build.method === "recipe" &&
                  Boolean(configuration.build.outputDirectory))) && (
                <div className="sm:col-span-2">
                  <OptionRow
                    title="Single-page application"
                    hint="Answers paths without a file with index.html, so client-side routes open directly."
                    checked={configuration.build.spaFallback ?? false}
                    onCheckedChange={(spaFallback) =>
                      updateBuild({ spaFallback: spaFallback || undefined })
                    }
                  />
                </div>
              )}
            </div>
          </details>
        )}

        <EnvironmentEditor
          rows={envRows}
          onRowsChange={setEnvRows}
          dotenv={dotenv}
          onDotenvChange={setDotenv}
          hostNetwork={configuration.runtime.hostNetwork}
          databases={flow.candidate?.databases}
          onConnectDatabase={(connection, url, variable) => {
            setEnvRows((current) => [
              ...current.filter((row) => row.name && row.name !== variable),
              { name: variable, value: url },
            ])
            setConfiguration({
              ...configuration,
              dependencies: [
                ...configuration.dependencies.filter(
                  (item) =>
                    item.resourceKind !== "database_connection" ||
                    item.resourceId !== String(connection.id),
                ),
                {
                  kind: "database",
                  ownership: "linked",
                  resourceKind: "database_connection",
                  resourceId: String(connection.id),
                  config: {},
                },
              ],
            })
          }}
        />

        {flow.profile !== "worker" && (
          <PublicAddress
            domain={configuration.domains[0]}
            suggestion={flow.hostname}
            onChange={(domain) =>
              setConfiguration({ ...configuration, domains: domain ? [domain] : [] })
            }
          />
        )}

        <div ref={advancedRef} className="space-y-6">
          <button
            type="button"
            aria-expanded={advancedOpen}
            onClick={() => setAdvancedOpen(!advancedOpen)}
            className="flex min-h-11 w-full items-center justify-between rounded-xl border border-hairline bg-surface-header px-3.5 text-body font-medium focus-ring"
          >
            <span className="flex items-center gap-2">
              <SettingsSliders className="size-4" />
              Advanced
            </span>
            <span className="text-xs font-normal text-muted-foreground">
              {advancedOpen ? "Hide" : "Show"}
            </span>
          </button>
          {advancedOpen && (
            <ConfigureAdvanced
              configuration={configuration}
              onChange={setConfiguration}
              errors={configErrors}
            />
          )}
        </div>

        {(blockers.length > 0 || warnings.length > 0) && (
          <FormSection title="Findings">
            <div className="space-y-2">
              {blockers.map((finding, index) => (
                <FindingRow
                  key={`${finding.code}:${finding.fieldId ?? index}`}
                  finding={finding}
                  index={index}
                  onOpenRemedy={openRemedyField}
                />
              ))}
            </div>
            {warnings.length > 0 && (
              <Group tone="warning" className="space-y-2">
                {warnings.map((finding, index) => (
                  <Label
                    key={`${finding.code}:${finding.fieldId ?? index}`}
                    className="flex min-h-11 items-start gap-3 text-xs"
                  >
                    <Checkbox
                      className="mt-0.5"
                      checked={acknowledged.includes(finding.code)}
                      onCheckedChange={(checked) =>
                        setAcknowledged(
                          checked
                            ? [...new Set([...acknowledged, finding.code])]
                            : acknowledged.filter((code) => code !== finding.code),
                        )
                      }
                    />
                    <span>
                      <span className="block font-medium">{finding.title}</span>
                      <span className="mt-0.5 block text-muted-foreground">
                        {finding.measured || finding.means}
                      </span>
                    </span>
                  </Label>
                ))}
              </Group>
            )}
          </FormSection>
        )}

        <div className="flex flex-wrap items-center justify-between gap-3 border-t border-hairline pt-4">
          <p className="text-hint text-muted-foreground">
            {isImport
              ? "Adopting records this workload as a deployment without starting, stopping, or changing it."
              : "Deploy saves the plan, applies the environment, and starts the release."}
          </p>
          <div className="flex flex-wrap items-center gap-2">
            {isImport ? (
              <Button
                className="h-11 sm:h-9"
                onClick={() => void submit("deploy")}
                pending={busy === "deploy"}
                disabled={Boolean(busy)}
              >
                Adopt workload
              </Button>
            ) : (
              <>
                <Button
                  variant="ghost"
                  className="h-11 sm:h-9"
                  onClick={() => void submit("save")}
                  pending={busy === "save"}
                  disabled={Boolean(busy)}
                >
                  Save only
                </Button>
                <Button
                  className="h-11 sm:h-9"
                  onClick={() => void submit("deploy")}
                  pending={busy === "deploy"}
                  disabled={Boolean(busy)}
                >
                  <ArrowRight className="size-4" />
                  {blockers.length
                    ? "Re-check and deploy"
                    : outstanding.length
                      ? "Acknowledge, then deploy"
                      : "Deploy"}
                </Button>
              </>
            )}
          </div>
        </div>
      </fieldset>
    </div>
  )
}
