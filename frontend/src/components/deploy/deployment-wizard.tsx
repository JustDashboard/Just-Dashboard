"use client"

import { forwardRef, useCallback, useEffect, useRef, useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import {
  ArrowLeft,
  ArrowRight,
  ArrowDown,
  ArrowUp,
  Box,
  Check,
  CheckCircle,
  CloudUpload,
  Code,
  Database,
  FileText,
  GitBranch,
  Layers,
  Plus,
  Servers,
  SettingsSliders,
  Trash,
  Warning,
} from "@/components/icons"
import { ApiError, errorMessage, get, post, put } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import {
  clearDeploymentHandoff,
  getDeploymentHandoff,
  setDeploymentHandoff,
} from "@/components/deploy/deployment-handoff"
import { cn } from "@/lib/utils"
import type {
  BlueprintSummary,
  DeploymentBuildMethod,
  DeploymentConfiguration,
  DeploymentDetection,
  DeploymentDetectionCandidate,
  DeploymentDraft,
  DeploymentDraftSource,
  DeploymentPreflight,
  DeploymentPreflightFinding,
  DeploymentSourceMode,
  WorkloadProfile,
} from "@/lib/types"
import { Detail, DetailList, Page, PageHeader } from "@/components/page"
import { Group, Panel, PanelBody, PanelFooter, PanelHeader, Well } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { humanize } from "@/components/deploy/deployment-ui"
import { FindingRow, findingVerdict } from "@/components/deploy/deployment-findings"
import {
  blueprintAcceptances,
  defaultConfiguration,
  sourceForMode,
  sourceForProfile,
  sourceModes,
  validateConfiguration,
  validateSource,
  type WizardErrors,
} from "@/components/deploy/deployment-defaults"
import { BlueprintPicker } from "@/components/deploy/blueprint-picker"
import { ProjectDatabase } from "@/components/deploy/project-database"
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
import { ExplainIcon } from "@/components/docker/explain"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { choiceCardClasses } from "@/components/choice-card"

const STEPS = [
  ["intent", "What", "Choose the outcome"],
  ["source", "Source", "Connect the source"],
  ["detection", "Detect", "Review evidence"],
  ["configuration", "Configure", "Set runtime decisions"],
  ["preflight", "Review", "Check the release path"],
] as const

const OUTCOMES: {
  profile: WorkloadProfile
  title: string
  description: string
  example: string
  icon: typeof CloudUpload
}[] = [
  {
    profile: "web",
    title: "Web app or API",
    description: "Build a repository and publish an HTTP route.",
    example: "Next.js, Rails, Go API",
    icon: CloudUpload,
  },
  {
    profile: "static",
    title: "Static site",
    description: "Build files that need no application process.",
    example: "Vite, Astro, documentation",
    icon: FileText,
  },
  {
    profile: "worker",
    title: "Worker, bot, or task",
    description: "Run a private process with no public route.",
    example: "Queue worker, Discord bot",
    icon: Code,
  },
  {
    profile: "image",
    title: "Docker image",
    description: "Run a versioned image from a registry.",
    example: "ghcr.io/owner/app:tag",
    icon: Box,
  },
  {
    profile: "compose",
    title: "Docker Compose stack",
    description: "Plan several services from one or more files.",
    example: "Paste, upload, Git, or local",
    icon: Layers,
  },
  {
    profile: "service",
    title: "Service from a blueprint",
    description: "Start from a reviewed application recipe.",
    example: "Uptime Kuma and more",
    icon: Database,
  },
  {
    profile: "game",
    title: "Game server",
    description: "Use safe game defaults, storage, and readiness.",
    example: "Minecraft Java",
    icon: Servers,
  },
  {
    profile: "imported",
    title: "Existing workload",
    description: "Inspect before the dashboard takes ownership.",
    example: "Container, Compose stack, checkout",
    icon: GitBranch,
  },
]

type ImportPreview = {
  name: string
  unsupported: string[]
  warnings: string[]
  wouldChange: string[]
}
type CommitResult = {
  projectId: number
  environmentId: number
  planRevision: number
  created: boolean
}

export function DeploymentWizard() {
  const router = useRouter()
  const search = useSearchParams()
  const draftID = search.get("draft") ?? ""
  const initialProfile =
    OUTCOMES.find((option) => option.profile === search.get("profile"))?.profile ?? "web"
  const requestedStep = search.get("step")
  const [draft, setDraft] = useState<DeploymentDraft>()
  const [savedProject, setSavedProject] = useState<CommitResult>()
  const [loadError, setLoadError] = useState<Error>()
  const [loading, setLoading] = useState(true)
  const creating = useRef(false)
  const [step, setStep] = useState(0)
  const [intent, setIntent] = useState<{ name: string; profile: WorkloadProfile }>({
    name: "",
    profile: initialProfile,
  })
  const [source, setSource] = useState<DeploymentDraftSource>(() =>
    sourceForProfile(initialProfile),
  )
  const blueprints = usePoll(
    (signal) => get<BlueprintSummary[]>("/deploy/blueprints/", undefined, signal),
    0,
    [],
    { enabled: source.kind === "blueprint" },
  )
  const selectedBlueprint = blueprints.data?.find((entry) => entry.id === source.blueprintId)
  const blueprintBlocked =
    source.kind === "blueprint" &&
    (!selectedBlueprint || selectedBlueprint.deploymentSupported === false)
  const [selectedCandidate, setSelectedCandidate] = useState("")
  const [configuration, setConfiguration] = useState<DeploymentConfiguration>(() =>
    defaultConfiguration(initialProfile),
  )
  const [preflight, setPreflight] = useState<DeploymentPreflight>()
  const [errors, setErrors] = useState<WizardErrors>({})
  const [jsonErrors, setJSONErrors] = useState<WizardErrors>({})
  const [busy, setBusy] = useState("")
  const [advanced, setAdvanced] = useState(false)
  const [acknowledgedWarnings, setAcknowledgedWarnings] = useState<string[]>([])
  const [importPreview, setImportPreview] = useState<ImportPreview>()
  const [importAcknowledged, setImportAcknowledged] = useState(false)
  const errorRef = useRef<HTMLDivElement>(null)

  const hydrateDraft = useCallback(
    (next: DeploymentDraft) => {
      setDraft(next)
      if (next.data.intent) setIntent(next.data.intent)
      if (next.data.source) setSource(next.data.source)
      if (next.data.detection?.selectedId) setSelectedCandidate(next.data.detection.selectedId)
      if (next.data.configuration) setConfiguration(next.data.configuration)
      const serverStep = Math.max(
        0,
        STEPS.findIndex(([key]) => key === next.currentStep),
      )
      const queryStep = STEPS.findIndex(([key]) => key === requestedStep)
      // currentStep is the last server-persisted stage. The immediately next
      // screen is allowed because its prerequisite now exists (intent opens
      // Source; accepted detection opens Configure). Without this +1, the URL
      // update triggered a reload that bounced the operator back one screen.
      setStep(
        queryStep >= 0 && queryStep <= Math.min(serverStep + 1, STEPS.length - 1)
          ? queryStep
          : serverStep,
      )
    },
    [requestedStep],
  )

  const load = useCallback(
    async (id: string) => {
      try {
        const next = await get<DeploymentDraft>(`/deploy/drafts/${id}`)
        hydrateDraft(next)
        setLoadError(undefined)
      } catch (error) {
        setLoadError(error instanceof Error ? error : new Error(String(error)))
      } finally {
        setLoading(false)
      }
    },
    [hydrateDraft],
  )

  useEffect(() => {
    if (draftID) {
      // The draft id is external URL state; loading its server-owned snapshot
      // is exactly the synchronization this effect owns.
      // eslint-disable-next-line react-hooks/set-state-in-effect
      void load(draftID)
      return
    }
    if (creating.current) return
    creating.current = true
    post<DeploymentDraft>("/deploy/drafts", {})
      .then((next) => {
        hydrateDraft(next)
        router.replace(
          `/deploy/new?draft=${encodeURIComponent(next.id)}&step=intent&profile=${initialProfile}`,
        )
      })
      .catch((error) => setLoadError(error instanceof Error ? error : new Error(String(error))))
      .finally(() => setLoading(false))
  }, [draftID, hydrateDraft, load, router, initialProfile])

  const navigate = (next: number) => {
    setErrors({})
    setStep(next)
    if (draft)
      router.replace(`/deploy/new?draft=${encodeURIComponent(draft.id)}&step=${STEPS[next][0]}`)
    requestAnimationFrame(() => document.querySelector<HTMLElement>("#wizard-step-title")?.focus())
  }

  const showErrors = (next: WizardErrors) => {
    setErrors(next)
    requestAnimationFrame(() => errorRef.current?.focus())
  }

  const changeSource = (next: DeploymentDraftSource) => {
    setSource(next)
    setImportPreview(undefined)
    setImportAcknowledged(false)
  }

  const perform = async (label: string, action: () => Promise<void>) => {
    setBusy(label)
    setErrors({})
    try {
      await action()
    } catch (error) {
      if (error instanceof ApiError && error.code === "draft_revision_conflict" && draftID) {
        await load(draftID)
      }
      showErrors({ form: errorMessage(error) })
    } finally {
      setBusy("")
    }
  }

  const saveIntent = () => {
    const next: WizardErrors = {}
    if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(intent.name))
      next.name = "Use 1–64 letters, numbers, dots, dashes, or underscores."
    if (Object.keys(next).length) return showErrors(next)
    if (!draft) return
    void perform("intent", async () => {
      const saved = await put<DeploymentDraft>(`/deploy/drafts/${draft.id}`, {
        revision: draft.revision,
        step: "intent",
        intent,
      })
      setDraft(saved)
      setSource(sourceForProfile(intent.profile))
      setImportPreview(undefined)
      setImportAcknowledged(false)
      setConfiguration(defaultConfiguration(intent.profile))
      setJSONErrors({})
      navigate(1)
    })
  }

  const saveSource = () => {
    if (blueprintBlocked)
      return showErrors({
        blueprint:
          selectedBlueprint?.unavailableReason ||
          "Choose an available blueprint or a different source.",
      })
    const next = validateSource(source)
    if (Object.keys(next).length) return showErrors(next)
    if (!draft) return
    void perform("source", async () => {
      if (source.kind === "import") {
        const preview = await post<ImportPreview>("/deploy/import/preview", source)
        setImportPreview(preview)
        if (
          (preview.unsupported.length > 0 || preview.warnings.length > 0) &&
          !importAcknowledged
        ) {
          showErrors({
            import: "Review the observed differences and acknowledge them before continuing.",
          })
          return
        }
      }
      const saved = await put<DeploymentDraft>(`/deploy/drafts/${draft.id}`, {
        revision: draft.revision,
        step: "source",
        source,
      })
      const detected = await post<DeploymentDraft>(`/deploy/drafts/${draft.id}/detect`, {
        revision: saved.revision,
      })
      setDraft(detected)
      setSelectedCandidate(detected.data.detection?.selectedId ?? "")
      navigate(2)
    })
  }

  const acceptDetection = () => {
    if (!draft?.data.detection) return
    if (draft.data.detection.unavailable && draft.data.detection.candidates.length === 0) {
      return showErrors({ detection: draft.data.detection.unavailable })
    }
    if (!selectedCandidate)
      return showErrors({
        candidate: "Choose the candidate that should become the deployment plan.",
      })
    void perform("detection", async () => {
      let saved = draft
      if (draft.data.detection?.selectedId !== selectedCandidate) {
        saved = await post<DeploymentDraft>(`/deploy/drafts/${draft.id}/detect`, {
          revision: draft.revision,
          selectedId: selectedCandidate,
        })
        setDraft(saved)
      }
      const selected = saved.data.detection?.candidates.find(
        (candidate) => candidate.id === selectedCandidate,
      )
      setConfiguration(defaultConfiguration(intent.profile, selected, source, saved.data.detection))
      navigate(3)
    })
  }

  const saveConfiguration = () => {
    const next = { ...validateConfiguration(configuration, intent.profile), ...jsonErrors }
    if (Object.keys(next).length) return showErrors(next)
    if (!draft) return
    void perform("configuration", async () => {
      const saved = await put<DeploymentDraft>(`/deploy/drafts/${draft.id}`, {
        revision: draft.revision,
        step: "configuration",
        configuration,
      })
      const checked = await post<{ draft: DeploymentDraft; preflight: DeploymentPreflight }>(
        `/deploy/drafts/${draft.id}/preflight`,
        { revision: saved.revision },
      )
      setDraft(checked.draft)
      setPreflight(checked.preflight)
      setAcknowledgedWarnings([])
      navigate(4)
    })
  }

  const commit = () => {
    if (!draft || !preflight || savedProject) return
    const blocking = preflight.findings.filter(
      (finding) => finding.severity === "blocked" || finding.severity === "decision",
    )
    const warnings = preflight.findings.filter((finding) => finding.severity === "warning")
    if (blocking.length)
      return showErrors({
        preflight: "Resolve every blocked item and decision before saving this deployment.",
      })
    if (warnings.some((finding) => !acknowledgedWarnings.includes(finding.code)))
      return showErrors({ warnings: "Acknowledge each warning before saving this deployment." })
    if (source.kind === "import" && !importPreview)
      return showErrors({
        import: "Review the current import preview before adopting this workload.",
      })
    void perform("commit", async () => {
      const result =
        source.kind === "import"
          ? await post<CommitResult>("/deploy/import/adopt", {
              draftId: draft.id,
              revision: draft.revision,
              acknowledgedWarnings,
              acknowledgedUnsupported: importPreview?.unsupported ?? [],
            })
          : await post<CommitResult>(`/deploy/drafts/${draft.id}/commit`, {
              revision: draft.revision,
              acknowledgedWarnings,
            })
      setSavedProject(result)
      const dotenv = getDeploymentHandoff(draft.id)
      if (dotenv?.trim()) {
        await post(
          `/deploy/${result.projectId}/environments/${result.environmentId}/variables/import`,
          {
            revision: result.planRevision,
            dotenv,
            sensitivity: "secret",
            scopes: ["runtime", "build"],
          },
        )
      }
      clearDeploymentHandoff(draft.id)
      router.push(`/deploy/${result.projectId}`)
    })
  }

  if (loading && !draft)
    return (
      <Page>
        <PageHeader eyebrow="Deployments" title="New project" />
        <LoadingPanel rows={5} />
      </Page>
    )
  if (loadError && !draft)
    return (
      <Page>
        <PageHeader eyebrow="Deployments" title="New project" />
        <ErrorState error={loadError} />
        <Button variant="outline" onClick={() => (draftID ? void load(draftID) : router.refresh())}>
          Try again
        </Button>
      </Page>
    )
  if (!draft) return null

  return (
    <Page className="max-w-[1320px]">
      <PageHeader
        eyebrow="Deployments"
        title="New project"
        actions={
          <Button variant="outline" size="sm" asChild>
            <Link href="/deploy">Back to projects</Link>
          </Button>
        }
      />
      <WizardProgress
        step={step}
        maxStep={Math.max(
          step,
          STEPS.findIndex(([key]) => key === draft.currentStep),
        )}
        onNavigate={navigate}
      />
      <div className="grid min-w-0 gap-4 lg:grid-cols-[minmax(0,1fr)_16rem] [&>*]:min-w-0">
        <Panel>
          <PanelHeader
            eyebrow="Project setup"
            title={
              <span id="wizard-step-title" tabIndex={-1} className="focus-ring">
                {STEPS[step][2]}
              </span>
            }
          />
          <PanelBody className="space-y-5">
            <ErrorSummary errors={errors} ref={errorRef} />
            {step > 1 && selectedBlueprint?.deploymentSupported === false && (
              <Notice title="Blueprint deployment unavailable" icon={Warning}>
                {selectedBlueprint.unavailableReason || "Choose a different source to continue."}
              </Notice>
            )}
            {savedProject && (
              <Notice title="Deployment saved" icon={CheckCircle}>
                <p>
                  Your deployment exists. If environment setup failed, finish it from Variables
                  before deploying.
                </p>
                <Link
                  className="underline focus-ring"
                  href={`/deploy/${savedProject.projectId}?tab=variables`}
                >
                  Open saved deployment
                </Link>
              </Notice>
            )}
            {(step === 4 || savedProject) && getDeploymentHandoff(draft.id) !== undefined && (
              <HandoffEnvironment key={draft.id} draftId={draft.id} />
            )}
            {step === 0 && <IntentStep intent={intent} onChange={setIntent} errors={errors} />}
            {step === 1 && (
              <SourceStep
                source={source}
                profile={intent.profile}
                onChange={changeSource}
                errors={errors}
                importPreview={importPreview}
                acknowledged={importAcknowledged}
                onAcknowledged={setImportAcknowledged}
                advanced={advanced}
                onAdvanced={setAdvanced}
              />
            )}
            {step === 2 && (
              <DetectionStep
                detection={draft.data.detection}
                selected={selectedCandidate}
                onSelect={setSelectedCandidate}
                errors={errors}
              />
            )}
            {step === 3 && (
              <ConfigurationStep
                configuration={configuration}
                draftId={draft.id}
                profile={intent.profile}
                source={source}
                onChange={setConfiguration}
                advanced={advanced}
                onAdvanced={setAdvanced}
                errors={{ ...errors, ...jsonErrors }}
                onJSONError={(field, error) =>
                  setJSONErrors((current) => {
                    if (error) return { ...current, [field]: error }
                    const next = { ...current }
                    delete next[field]
                    return next
                  })
                }
              />
            )}
            {step === 4 && (
              <PreflightStep
                preflight={preflight}
                draft={draft}
                acknowledged={acknowledgedWarnings}
                onAcknowledged={setAcknowledgedWarnings}
                errors={errors}
              />
            )}
          </PanelBody>
          <PanelFooter className="justify-between">
            <Button
              variant="outline"
              onClick={() => navigate(step - 1)}
              disabled={step === 0 || Boolean(busy)}
            >
              <ArrowLeft className="size-4" />
              Back
            </Button>
            {step === 0 && (
              <ActionButton busy={busy === "intent"} onClick={saveIntent}>
                Continue
              </ActionButton>
            )}
            {step === 1 && (
              <ActionButton
                busy={busy === "source"}
                disabled={blueprintBlocked}
                onClick={saveSource}
              >
                Inspect source
              </ActionButton>
            )}
            {step === 2 && (
              <ActionButton
                busy={busy === "detection"}
                disabled={blueprintBlocked}
                onClick={acceptDetection}
              >
                Use this detection
              </ActionButton>
            )}
            {step === 3 && (
              <ActionButton
                busy={busy === "configuration"}
                disabled={blueprintBlocked}
                onClick={saveConfiguration}
              >
                Run preflight
              </ActionButton>
            )}
            {step === 4 && !savedProject && (
              <ActionButton busy={busy === "commit"} disabled={blueprintBlocked} onClick={commit}>
                {source.kind === "import" ? "Adopt workload" : "Save deployment"}
              </ActionButton>
            )}
          </PanelFooter>
        </Panel>
        <aside className="space-y-3" aria-label="Current plan summary">
          <Panel>
            <PanelHeader title={intent.name || "New deployment"} />
            <PanelBody>
              <DetailList>
                <Detail label="Source">{humanize(source.mode)}</Detail>
                <Detail label="Build">{humanize(configuration.build.method)}</Detail>
                <Detail label="Release">
                  {configuration.runtime.strategy === "blue_green"
                    ? "Candidate first"
                    : "Stop first"}
                </Detail>
                <Detail label="Downtime">
                  {(preflight?.expectedDowntime ?? configuration.runtime.strategy === "stop_first")
                    ? "Expected during cutover"
                    : "No planned interruption"}
                </Detail>
                <Detail label="Exposure" className="truncate">
                  {configuration.domains[0]?.hostname ||
                    (configuration.runtime.hostPort
                      ? `Port ${configuration.runtime.hostPort}`
                      : "Private")}
                </Detail>
                <Detail label="Storage">
                  {configuration.runtime.mounts?.length ?? 0}{" "}
                  {(configuration.runtime.mounts?.length ?? 0) === 1 ? "mount" : "mounts"}
                </Detail>
              </DetailList>
            </PanelBody>
          </Panel>
          <p className="px-1 text-xs leading-relaxed text-muted-foreground">
            Save reviews the project plan. Deploy it from the project when you’re ready. Databases
            you explicitly create are started immediately.
          </p>
        </aside>
      </div>
    </Page>
  )
}

function WizardEnvironment({
  draftId,
  configuration,
  onChange,
}: {
  draftId: string
  configuration: DeploymentConfiguration
  onChange: (configuration: DeploymentConfiguration) => void
}) {
  const [dotenv, setDotenv] = useState(() => getDeploymentHandoff(draftId) ?? "")
  const update = (text: string) => {
    setDotenv(text)
    setDeploymentHandoff(draftId, text)
  }
  return (
    <div className="space-y-4 border-t border-hairline pt-4">
      <div className="space-y-2">
        <Label htmlFor="handoff-environment">Environment variables</Label>
        <Textarea
          id="handoff-environment"
          className="font-mono text-xs"
          rows={4}
          placeholder="DATABASE_URL=…"
          value={dotenv}
          onChange={(event) => update(event.target.value)}
        />
        <p className="text-xs text-muted-foreground">
          Paste .env values for build and runtime. Keep this tab open until saving; unsaved values
          stay only in memory.
        </p>
      </div>
      {configuration.build.method !== "compose" && (
        <ProjectDatabase
          target={configuration.runtime.hostNetwork ? "host" : "container"}
          onConnect={(connection, url, variable) => {
            update([dotenv.trim(), `${variable}=${JSON.stringify(url)}`].filter(Boolean).join("\n"))
            onChange({
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
      )}
      {configuration.build.method === "compose" && (
        <p className="text-xs text-muted-foreground">
          For Compose projects, declare database services in your Compose file so the application
          and database share a network.
        </p>
      )}
    </div>
  )
}

function HandoffEnvironment({ draftId }: { draftId: string }) {
  const [dotenv, setDotenv] = useState(() => getDeploymentHandoff(draftId) ?? "")
  return (
    <section className="space-y-2">
      <Label htmlFor="handoff-environment">Environment variables</Label>
      <Textarea
        id="handoff-environment"
        className="font-mono text-xs"
        value={dotenv}
        onChange={(event) => {
          setDotenv(event.target.value)
          setDeploymentHandoff(draftId, event.target.value)
        }}
      />
      <p className="text-xs text-muted-foreground">
        These values will be saved with the deployment. Keep this tab open until saving; refreshing
        clears unsaved values.
      </p>
    </section>
  )
}

function WizardProgress({
  step,
  maxStep,
  onNavigate,
}: {
  step: number
  maxStep: number
  onNavigate: (step: number) => void
}) {
  return (
    <nav
      aria-label="Deployment setup progress"
      className="rounded-xl border bg-card px-2 py-2 sm:px-3"
    >
      <ol className="grid grid-cols-5 gap-1">
        {STEPS.map(([, label], index) => {
          const available = index <= maxStep
          return (
            <li key={label} className="min-w-0">
              <button
                type="button"
                disabled={!available}
                aria-current={step === index ? "step" : undefined}
                aria-label={`Step ${index + 1} of ${STEPS.length}: ${label}${index < maxStep ? ", completed" : ""}`}
                onClick={() => onNavigate(index)}
                className={cn(
                  "flex min-h-11 w-full min-w-0 items-center justify-center gap-2 rounded-md px-1.5 text-xs font-medium focus-ring transition-colors sm:justify-start sm:px-2.5",
                  step === index
                    ? "bg-accent text-foreground"
                    : "text-muted-foreground hover:bg-row-hover hover:text-foreground",
                )}
              >
                <span
                  className={cn(
                    "numeric flex size-5 shrink-0 items-center justify-center rounded-full border text-micro",
                    step === index ? "border-primary-foreground/35" : "border-hairline",
                  )}
                >
                  {index < maxStep ? <Check className="size-3" /> : index + 1}
                </span>
                <span className="hidden truncate sm:block">{label}</span>
              </button>
            </li>
          )
        })}
      </ol>
    </nav>
  )
}

const ErrorSummary = forwardRef<HTMLDivElement, { errors: WizardErrors }>(function ErrorSummary(
  { errors },
  ref,
) {
  const entries = Object.entries(errors)
  if (!entries.length) return null
  return (
    <Group
      ref={ref}
      role="alert"
      tabIndex={-1}
      aria-labelledby="wizard-errors-title"
      className="focus-ring"
      tone="danger"
    >
      <h2 id="wizard-errors-title" className="text-body font-medium text-destructive">
        There is a problem
      </h2>
      <ul className="mt-2 list-disc space-y-1 pl-5 text-xs">
        {entries.map(([field, message]) => (
          <li key={field}>
            <a
              href={field === "form" ? "#wizard-step-title" : `#${field}`}
              onClick={() => {
                const target = document.getElementById(field)
                let parent = target?.parentElement
                while (parent) {
                  if (parent instanceof HTMLDetailsElement) parent.open = true
                  parent = parent.parentElement
                }
                target?.focus()
              }}
              className="underline underline-offset-2"
            >
              {message}
            </a>
          </li>
        ))}
      </ul>
    </Group>
  )
})

function IntentStep({
  intent,
  onChange,
  errors,
}: {
  intent: { name: string; profile: WorkloadProfile }
  onChange: (intent: { name: string; profile: WorkloadProfile }) => void
  errors: WizardErrors
}) {
  return (
    <div className="space-y-5">
      <Field
        id="name"
        label="Deployment name"
        hint="Used in URLs, container labels, and release history."
        error={errors.name}
      >
        <Input
          id="name"
          value={intent.name}
          onChange={(event) => onChange({ ...intent, name: event.target.value })}
          placeholder="api-production"
          autoComplete="off"
          aria-invalid={Boolean(errors.name)}
          aria-describedby={errors.name ? "name-error" : "name-hint"}
        />
      </Field>
      <fieldset>
        <legend className="text-body font-medium">What are you deploying?</legend>
        <p className="mt-1 text-xs text-muted-foreground">
          This chooses useful defaults. Advanced settings remain available later.
        </p>
        <div className="mt-3 grid gap-2 sm:grid-cols-2">
          {OUTCOMES.map((outcome) => {
            const Icon = outcome.icon
            const checked = intent.profile === outcome.profile
            return (
              <label
                key={outcome.profile}
                className={cn(
                  choiceCardClasses(checked),
                  "min-h-24 cursor-pointer flex-row items-start gap-3 p-3.5",
                )}
              >
                <input
                  type="radio"
                  name="profile"
                  value={outcome.profile}
                  checked={checked}
                  onChange={() => onChange({ ...intent, profile: outcome.profile })}
                  className="sr-only"
                />
                <span
                  className={cn(
                    "mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-lg",
                    checked ? "bg-accent text-foreground" : "bg-muted text-muted-foreground",
                  )}
                >
                  <Icon className="size-4" />
                </span>
                <span className="min-w-0">
                  <span className="block text-body font-medium">{outcome.title}</span>
                  <span className="mt-0.5 block text-xs leading-relaxed text-muted-foreground">
                    {outcome.description}
                  </span>
                  <span className="mt-1 block text-micro text-muted-foreground">
                    {outcome.example}
                  </span>
                </span>
              </label>
            )
          })}
        </div>
      </fieldset>
    </div>
  )
}

function SourceStep({
  source,
  profile,
  onChange,
  errors,
  importPreview,
  acknowledged,
  onAcknowledged,
  advanced,
  onAdvanced,
}: {
  source: DeploymentDraftSource
  profile: WorkloadProfile
  onChange: (source: DeploymentDraftSource) => void
  errors: WizardErrors
  importPreview?: ImportPreview
  acknowledged: boolean
  onAcknowledged: (value: boolean) => void
  advanced: boolean
  onAdvanced: (value: boolean) => void
}) {
  const modes = sourceModes(profile)
  const setMode = (mode: string) => onChange(sourceForMode(mode as DeploymentSourceMode, profile))
  return (
    <div className="space-y-5">
      <Field
        id="source-mode"
        label="Where does it come from?"
        hint="Credentials are referenced by id and are never included in the plan."
        error={errors.source}
      >
        <Select value={source.mode} onValueChange={setMode}>
          <SelectTrigger id="source-mode" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {modes.map(([mode, label]) => (
              <SelectItem key={mode} value={mode}>
                {label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      {(source.mode === "git_url" || source.mode === "compose_git") && (
        <GitURLFields source={source} onChange={onChange} errors={errors} />
      )}
      {source.mode === "connected_repository" && (
        <ConnectedRepositoryFields source={source} onChange={onChange} errors={errors} />
      )}
      {(source.mode === "local_checkout" ||
        source.mode === "compose_local" ||
        source.mode === "existing_checkout") && (
        <LocalFields source={source} onChange={onChange} errors={errors} />
      )}
      {(source.mode === "compose_git" || source.mode === "compose_local") && (
        <ComposeSelectorFields source={source} onChange={onChange} errors={errors} />
      )}
      {source.mode === "image_reference" && (
        <ImageFields source={source} profile={profile} onChange={onChange} errors={errors} />
      )}
      {(source.mode === "compose_paste" || source.mode === "compose_upload") && (
        <ComposeFields
          source={source}
          onChange={onChange}
          errors={errors}
          upload={source.mode === "compose_upload"}
        />
      )}
      {source.mode === "blueprint" && (
        <BlueprintPicker
          profile={profile}
          blueprintId={source.blueprintId ?? ""}
          inputs={source.blueprintInputs ?? {}}
          showAdvanced={advanced}
          errors={errors}
          onSelect={(id, version) =>
            onChange({
              ...source,
              blueprintId: id,
              blueprintVersion: version,
              // Inputs belong to the blueprint that declared them; keeping them
              // across a change would send one blueprint's answers to another.
              blueprintInputs: {},
            })
          }
          onInput={(name, value) =>
            onChange({
              ...source,
              blueprintInputs: { ...(source.blueprintInputs ?? {}), [name]: value },
            })
          }
          onInputs={(values) =>
            onChange({
              ...source,
              blueprintInputs: { ...(source.blueprintInputs ?? {}), ...values },
            })
          }
        />
      )}
      {source.mode === "blueprint" && (
        <button
          type="button"
          aria-expanded={advanced}
          onClick={() => onAdvanced(!advanced)}
          className="inline-flex min-h-9 items-center text-xs underline underline-offset-4 focus-ring"
        >
          {advanced ? "Hide" : "Show"} advanced blueprint settings
        </button>
      )}
      {(source.mode === "existing_container" || source.mode === "existing_stack") && (
        <Field
          id="resource-id"
          label={
            source.mode === "existing_container" ? "Container name or id" : "Compose project name"
          }
          hint="Inspection is read-only. Nothing is stopped, renamed, or adopted."
          error={errors.resource}
        >
          <Input
            id="resource-id"
            value={source.resourceId ?? ""}
            onChange={(event) => onChange({ ...source, resourceId: event.target.value })}
            className="font-mono"
            aria-invalid={Boolean(errors.resource)}
          />
        </Field>
      )}
      {importPreview &&
        (importPreview.unsupported.length > 0 || importPreview.warnings.length > 0) && (
          <Notice tone="warning" icon={Warning} title={`Review ${importPreview.name}`}>
            <ul className="list-disc space-y-1 pl-4">
              {[...importPreview.unsupported, ...importPreview.warnings].map((item) => (
                <li key={item}>{item}</li>
              ))}
            </ul>
            <Label
              id="import"
              className="mt-3 flex min-h-11 items-center gap-2 text-xs text-foreground"
            >
              <Checkbox
                checked={acknowledged}
                onCheckedChange={(checked) => onAcknowledged(checked === true)}
              />
              I understand which settings cannot be adopted automatically.
            </Label>
          </Notice>
        )}
    </div>
  )
}

function GitURLFields({ source, onChange, errors }: SourceFieldsProps) {
  return (
    <div className="grid gap-4 sm:grid-cols-2">
      <Field
        id="source-url"
        label="Git URL"
        hint="HTTPS or SSH; do not embed a password or token."
        error={errors.url}
        className="sm:col-span-2"
      >
        <Input
          id="source-url"
          value={source.url ?? ""}
          onChange={(event) => onChange({ ...source, url: event.target.value })}
          placeholder="https://github.com/owner/repository.git"
          className="font-mono"
          aria-invalid={Boolean(errors.url)}
        />
      </Field>
      <Field id="source-ref" label="Branch or tag" error={errors.ref}>
        <Input
          id="source-ref"
          value={source.ref ?? "main"}
          onChange={(event) => onChange({ ...source, ref: event.target.value })}
          className="font-mono"
        />
      </Field>
      <CredentialField source={source} onChange={onChange} />
      <GitOptions source={source} onChange={onChange} />
    </div>
  )
}

type SourceFieldsProps = {
  source: DeploymentDraftSource
  onChange: (source: DeploymentDraftSource) => void
  errors: WizardErrors
}

function ConnectedRepositoryFields({ source, onChange, errors }: SourceFieldsProps) {
  return (
    <div className="grid gap-4 sm:grid-cols-2">
      <Field id="provider" label="Provider">
        <Select
          value={source.provider ?? "github"}
          onValueChange={(provider) => onChange({ ...source, provider })}
        >
          <SelectTrigger id="provider" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {["github", "gitlab", "bitbucket", "gitea"].map((provider) => (
              <SelectItem key={provider} value={provider}>
                {humanize(provider)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      <CredentialField source={source} onChange={onChange} />
      <Field
        id="repository"
        label="Repository"
        hint="Owner and repository name."
        error={errors.repository}
      >
        <Input
          id="repository"
          value={source.repository ?? ""}
          onChange={(event) => onChange({ ...source, repository: event.target.value })}
          placeholder="owner/repository"
          className="font-mono"
        />
      </Field>
      <Field id="source-ref" label="Branch or tag">
        <Input
          id="source-ref"
          value={source.ref ?? "main"}
          onChange={(event) => onChange({ ...source, ref: event.target.value })}
          className="font-mono"
        />
      </Field>
      {source.provider === "gitea" && (
        <Field id="provider-base" label="Gitea base URL" className="sm:col-span-2">
          <Input
            id="provider-base"
            value={source.providerBaseUrl ?? ""}
            onChange={(event) => onChange({ ...source, providerBaseUrl: event.target.value })}
            className="font-mono"
          />
        </Field>
      )}
      <GitOptions source={source} onChange={onChange} />
    </div>
  )
}

function GitOptions({ source, onChange }: Omit<SourceFieldsProps, "errors">) {
  return (
    <div className="grid gap-3 sm:col-span-2 sm:grid-cols-2">
      <Field
        id="source-subdirectory"
        label="Source subdirectory"
        hint="Optional relative application or Compose root."
      >
        <Input
          id="source-subdirectory"
          value={source.subdirectory ?? ""}
          onChange={(event) => onChange({ ...source, subdirectory: event.target.value })}
          className="font-mono"
        />
      </Field>
      <div className="flex flex-wrap items-end gap-x-4">
        <Label className="flex min-h-11 items-center gap-2 text-xs">
          <Checkbox
            checked={source.includeSubmodules ?? false}
            onCheckedChange={(checked) =>
              onChange({ ...source, includeSubmodules: checked === true })
            }
          />
          Include Git submodules
        </Label>
        <Label className="flex min-h-11 items-center gap-2 text-xs">
          <Checkbox
            checked={source.includeLfs ?? false}
            onCheckedChange={(checked) => onChange({ ...source, includeLfs: checked === true })}
          />
          Include Git LFS objects
        </Label>
      </div>
    </div>
  )
}

function LocalFields({ source, onChange, errors }: SourceFieldsProps) {
  return (
    <div className="grid gap-4 sm:grid-cols-2">
      <Field
        id="local-path"
        label="Path on this server"
        hint="Must be inside a configured deployment root."
        error={errors.localPath}
        className="sm:col-span-2"
      >
        <Input
          id="local-path"
          value={source.localPath ?? ""}
          onChange={(event) => onChange({ ...source, localPath: event.target.value })}
          placeholder="/srv/app"
          className="font-mono"
        />
      </Field>
      <Field id="subdirectory" label="Subdirectory" hint="Optional relative application root.">
        <Input
          id="subdirectory"
          value={source.subdirectory ?? ""}
          onChange={(event) => onChange({ ...source, subdirectory: event.target.value })}
          className="font-mono"
        />
      </Field>
      {source.mode === "existing_checkout" && (
        <Label className="flex min-h-11 items-center gap-2 text-xs">
          <Switch
            checked={source.managedInPlace ?? false}
            onCheckedChange={(managedInPlace) => onChange({ ...source, managedInPlace })}
          />
          Allow managed changes in this checkout
        </Label>
      )}
      {(source.mode === "local_checkout" || source.mode === "existing_checkout") && (
        <div className="flex flex-wrap items-end gap-x-4 sm:col-span-2">
          <Label className="flex min-h-11 items-center gap-2 text-xs">
            <Checkbox
              checked={source.includeSubmodules ?? false}
              onCheckedChange={(checked) =>
                onChange({ ...source, includeSubmodules: checked === true })
              }
            />
            Include Git submodules
          </Label>
          <Label className="flex min-h-11 items-center gap-2 text-xs">
            <Checkbox
              checked={source.includeLfs ?? false}
              onCheckedChange={(checked) => onChange({ ...source, includeLfs: checked === true })}
            />
            Include Git LFS objects
          </Label>
        </div>
      )}
    </div>
  )
}

function ImageFields({
  source,
  profile,
  onChange,
  errors,
}: SourceFieldsProps & { profile: WorkloadProfile }) {
  return (
    <div className="grid gap-4 sm:grid-cols-2">
      <Field
        id="image-reference"
        label="Image"
        hint={
          profile === "game"
            ? "Minecraft uses the maintained itzg image; change it only if you know the replacement."
            : "A tag is resolved to an immutable digest during inspection."
        }
        error={errors.image}
        className="sm:col-span-2"
      >
        <Input
          id="image-reference"
          value={source.image ?? ""}
          onChange={(event) => onChange({ ...source, image: event.target.value })}
          className="font-mono"
          aria-invalid={Boolean(errors.image)}
        />
      </Field>
      <Field id="platform" label="Platform" hint="Optional, for multi-platform images.">
        <Input
          id="platform"
          value={source.platform ?? ""}
          onChange={(event) => onChange({ ...source, platform: event.target.value })}
          placeholder="linux/amd64"
          className="font-mono"
        />
      </Field>
      <CredentialField source={source} onChange={onChange} />
    </div>
  )
}

function CredentialField({ source, onChange }: Omit<SourceFieldsProps, "errors">) {
  return (
    <Field id="credential-id" label="Credential id" hint="Leave 0 for a public source.">
      <Input
        id="credential-id"
        type="number"
        min={0}
        value={source.credentialId ?? 0}
        onChange={(event) =>
          onChange({ ...source, credentialId: Number(event.target.value) || undefined })
        }
        className="font-mono"
      />
    </Field>
  )
}

function ComposeSelectorFields({ source, onChange, errors }: SourceFieldsProps) {
  const documents = source.composeFiles?.length
    ? source.composeFiles
    : [{ path: "compose.yml", content: "", order: 0 }]
  const setDocuments = (next: DeploymentDraftSource["composeFiles"]) =>
    onChange({
      ...source,
      composeFiles: next?.map((document, order) => ({ ...document, content: "", order })),
    })
  const move = (index: number, offset: number) => {
    const target = index + offset
    if (target < 0 || target >= documents.length) return
    const next = [...documents]
    ;[next[index], next[target]] = [next[target], next[index]]
    setDocuments(next)
  }
  return (
    <fieldset className="space-y-3 rounded-xl border border-hairline p-3">
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div>
          <legend className="text-body font-medium">Compose file order</legend>
          <p className="text-xs text-muted-foreground">
            Paths are relative to the selected source. Later files override earlier ones.
          </p>
        </div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() =>
            setDocuments([
              ...documents,
              { path: `compose.${documents.length + 1}.yml`, content: "", order: documents.length },
            ])
          }
        >
          <Plus className="size-3.5" /> Add file
        </Button>
      </div>
      {errors.compose && (
        <p id="compose-error" role="alert" className="text-xs text-destructive">
          {errors.compose}
        </p>
      )}
      {documents.map((document, index) => (
        <div key={`${document.order}-${index}`} className="flex min-w-0 items-center gap-1.5">
          <span className="numeric w-5 shrink-0 text-center text-hint text-muted-foreground">
            {index + 1}
          </span>
          <Input
            value={document.path}
            onChange={(event) =>
              setDocuments(
                documents.map((item, itemIndex) =>
                  itemIndex === index ? { ...item, path: event.target.value } : item,
                ),
              )
            }
            aria-label={`Compose file ${index + 1} path`}
            aria-invalid={Boolean(errors.compose)}
            className="min-w-0 font-mono text-xs"
          />
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            className="size-11 sm:size-8"
            aria-label={`Move ${document.path} earlier`}
            disabled={index === 0}
            onClick={() => move(index, -1)}
          >
            <ArrowUp className="size-3.5" />
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            className="size-11 sm:size-8"
            aria-label={`Move ${document.path} later`}
            disabled={index === documents.length - 1}
            onClick={() => move(index, 1)}
          >
            <ArrowDown className="size-3.5" />
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            className="size-11 sm:size-8"
            aria-label={`Remove ${document.path}`}
            disabled={documents.length === 1}
            onClick={() => setDocuments(documents.filter((_, itemIndex) => itemIndex !== index))}
          >
            <Trash className="size-3.5" />
          </Button>
        </div>
      ))}
    </fieldset>
  )
}

function ComposeFields({
  source,
  onChange,
  errors,
  upload,
}: SourceFieldsProps & { upload: boolean }) {
  const documents = source.composeFiles?.length
    ? source.composeFiles
    : [{ path: "compose.yml", content: "", order: 0 }]
  const update = (index: number, field: "path" | "content", value: string) =>
    onChange({
      ...source,
      composeFiles: documents.map((document, i) =>
        i === index ? { ...document, [field]: value } : document,
      ),
    })
  const remove = (index: number) =>
    onChange({
      ...source,
      composeFiles: documents
        .filter((_, i) => i !== index)
        .map((document, order) => ({ ...document, order })),
    })
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div>
          <p className="text-body font-medium">Compose files</p>
          <p className="text-xs text-muted-foreground">
            Order is preserved when Compose merges the files.
          </p>
        </div>
        <Button
          size="sm"
          variant="outline"
          onClick={() =>
            onChange({
              ...source,
              composeFiles: [
                ...documents,
                {
                  path: `compose.${documents.length + 1}.yml`,
                  content: "",
                  order: documents.length,
                },
              ],
            })
          }
        >
          <Plus className="size-3.5" />
          Add file
        </Button>
      </div>
      {upload && (
        <Input
          type="file"
          accept=".yml,.yaml,text/yaml"
          multiple
          className="pt-2 sm:pt-1.5"
          aria-label="Upload Compose files"
          onChange={async (event) => {
            const files = Array.from(event.target.files ?? [])
            onChange({
              ...source,
              composeFiles: await Promise.all(
                files.map(async (file, order) => ({
                  path: file.name,
                  content: await file.text(),
                  order,
                })),
              ),
            })
          }}
        />
      )}
      {errors.compose && (
        <p id="compose-error" className="text-xs text-destructive">
          {errors.compose}
        </p>
      )}
      {documents.map((document, index) => (
        <Group key={`${document.order}-${index}`}>
          <div className="mb-2 flex gap-2">
            <Input
              value={document.path}
              onChange={(event) => update(index, "path", event.target.value)}
              aria-label={`Compose file ${index + 1} path`}
              className="h-9 font-mono text-xs"
            />
            <Button
              size="icon"
              variant="ghost"
              className="size-11 sm:size-9"
              aria-label={`Remove ${document.path}`}
              onClick={() => remove(index)}
              disabled={documents.length === 1}
            >
              <Trash className="size-4" />
            </Button>
          </div>
          <Textarea
            value={document.content}
            onChange={(event) => update(index, "content", event.target.value)}
            aria-label={`${document.path} content`}
            rows={10}
            className="min-h-48 resize-y font-mono text-xs"
            placeholder={"services:\n  web:\n    image: nginx:alpine"}
          />
        </Group>
      ))}
    </div>
  )
}

function DetectionStep({
  detection,
  selected,
  onSelect,
  errors,
}: {
  detection?: DeploymentDetection
  selected: string
  onSelect: (id: string) => void
  errors: WizardErrors
}) {
  if (!detection)
    return (
      <Notice tone="warning" title="No detection evidence">
        Go back and inspect the source again.
      </Notice>
    )
  return (
    <div className="space-y-4">
      {detection.unavailable && (
        <Notice tone="warning" icon={Warning} title="Some evidence is unavailable">
          {detection.unavailable}
        </Notice>
      )}
      <div className="flex flex-wrap gap-x-4 gap-y-1 text-hint text-muted-foreground">
        <span>{detection.scannedFiles.toLocaleString()} files scanned</span>
        <span>{formatBytes(detection.scannedBytes)}</span>
        {detection.source.revision && (
          <span className="font-mono">{detection.source.revision.slice(0, 12)}</span>
        )}
        {detection.truncated && <span>Bound reached: {detection.truncatedReason}</span>}
      </div>
      {errors.candidate && (
        <p id="candidate" className="text-xs text-destructive">
          {errors.candidate}
        </p>
      )}
      {detection.candidates.length === 0 ? (
        <Notice title="No deployable candidate found" icon={Code}>
          Choose a different source or wait for the named adapter to become available.
        </Notice>
      ) : (
        <fieldset>
          <legend className="sr-only">Detected candidates</legend>
          <div className="space-y-2">
            {detection.candidates.map((candidate) => (
              <CandidateChoice
                key={candidate.id}
                candidate={candidate}
                checked={selected === candidate.id}
                onSelect={onSelect}
              />
            ))}
          </div>
        </fieldset>
      )}
      {detection.compose && (
        <Panel>
          <PanelHeader title={`${detection.compose.services.length} Compose services`} />
          <PanelBody className="space-y-3">
            <div className="flex flex-wrap gap-1.5">
              {detection.compose.services.map((service) => (
                <Tag key={service.name} mono>
                  {service.name}
                </Tag>
              ))}
            </div>
            {detection.compose.unsupported.map((item) => (
              <Notice key={item} tone="warning" title="Needs explicit review">
                {item}
              </Notice>
            ))}
            <details>
              <summary className="flex min-h-11 cursor-pointer items-center text-xs font-medium">
                Show effective Compose plan
              </summary>
              <Well className="max-h-72 whitespace-pre-wrap">{detection.compose.preview}</Well>
            </details>
          </PanelBody>
        </Panel>
      )}
    </div>
  )
}

function CandidateChoice({
  candidate,
  checked,
  onSelect,
}: {
  candidate: DeploymentDetectionCandidate
  checked: boolean
  onSelect: (id: string) => void
}) {
  return (
    <label
      className={cn(
        choiceCardClasses(checked),
        "min-h-24 cursor-pointer flex-row items-start gap-3 p-3.5",
      )}
    >
      <input
        type="radio"
        name="candidate"
        value={candidate.id}
        checked={checked}
        onChange={() => onSelect(candidate.id)}
        className="sr-only"
      />
      <span
        className={cn(
          "mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-full border",
          checked ? "border-primary bg-primary text-primary-foreground" : "border-hairline",
        )}
      >
        {checked ? <Check className="size-3.5" /> : null}
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex flex-wrap items-center gap-2">
          <span className="text-body font-medium">{candidate.name}</span>
          <Status
            verdict={
              candidate.confidence === "high"
                ? "ok"
                : candidate.confidence === "medium"
                  ? "warning"
                  : "notice"
            }
            label={`${candidate.confidence} confidence`}
          />
        </span>
        <span className="mt-1 block text-xs text-muted-foreground">
          {candidate.framework ? `${candidate.framework} · ` : ""}
          {humanize(candidate.buildMethod)}
          {candidate.root ? ` · ${candidate.root}` : ""}
        </span>
        <span className="mt-2 grid gap-1 text-hint text-muted-foreground sm:grid-cols-2">
          {candidate.buildCommand && (
            <span>
              <b className="font-medium text-foreground">Build:</b>{" "}
              <code>{candidate.buildCommand}</code>
            </span>
          )}
          {candidate.startCommand && (
            <span>
              <b className="font-medium text-foreground">Start:</b>{" "}
              <code>{candidate.startCommand}</code>
            </span>
          )}
          {candidate.port ? (
            <span>
              <b className="font-medium text-foreground">Port:</b> {candidate.port}
            </span>
          ) : null}
          {candidate.evidence.slice(0, 3).map((evidence) => (
            <span key={`${evidence.path}-${evidence.reason}`} className="min-w-0 break-words">
              <b className="font-mono font-medium text-foreground">{evidence.path}</b> —{" "}
              {evidence.reason}
            </span>
          ))}
        </span>
        {candidate.needsDecision.length > 0 && (
          <span className="mt-2 block text-hint text-warning">
            Confirm: {candidate.needsDecision.join(" · ")}
          </span>
        )}
      </span>
    </label>
  )
}

function ConfigurationStep({
  configuration,
  draftId,
  profile,
  onChange,
  advanced,
  onAdvanced,
  errors,
  onJSONError,
  source,
}: {
  configuration: DeploymentConfiguration
  draftId: string
  profile: WorkloadProfile
  onChange: (configuration: DeploymentConfiguration) => void
  advanced: boolean
  onAdvanced: (value: boolean) => void
  errors: WizardErrors
  onJSONError: (field: string, error?: string) => void
  source?: DeploymentDraftSource
}) {
  const updateBuild = (value: Partial<DeploymentConfiguration["build"]>) =>
    onChange({ ...configuration, build: { ...configuration.build, ...value } })
  const updateRuntime = (value: Partial<DeploymentConfiguration["runtime"]>) =>
    onChange({ ...configuration, runtime: { ...configuration.runtime, ...value } })
  const domain = configuration.domains[0]
  return (
    <div className="space-y-6">
      <details open className="space-y-4">
        <summary
          id="build-title"
          className="cursor-pointer rounded-sm py-2 text-sm font-medium focus-ring"
        >
          Build and start
        </summary>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field id="build-method" label="Build method" error={errors.buildMethod}>
            <Select
              value={configuration.build.method}
              onValueChange={(method) => updateBuild({ method: method as DeploymentBuildMethod })}
            >
              <SelectTrigger id="build-method" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {(["recipe", "dockerfile", "static", "image", "compose", "none"] as const).map(
                  (method) => (
                    <SelectItem key={method} value={method}>
                      {humanize(method)}
                    </SelectItem>
                  ),
                )}
              </SelectContent>
            </Select>
          </Field>
          <Field
            id="root-directory"
            label="Application root"
            hint="Relative to the inspected source."
          >
            <Input
              id="root-directory"
              value={configuration.build.rootDirectory ?? ""}
              onChange={(event) => updateBuild({ rootDirectory: event.target.value })}
              className="font-mono"
            />
          </Field>
          {["recipe", "static", "none"].includes(configuration.build.method) && (
            <>
              <Field id="build-command" label="Build command">
                <Input
                  id="build-command"
                  value={configuration.build.buildCommand ?? ""}
                  onChange={(event) => updateBuild({ buildCommand: event.target.value })}
                  className="font-mono"
                />
              </Field>
              <Field id="start-command" label="Start command">
                <Input
                  id="start-command"
                  value={configuration.build.startCommand ?? ""}
                  onChange={(event) => updateBuild({ startCommand: event.target.value })}
                  className="font-mono"
                />
              </Field>
            </>
          )}
          {configuration.build.method === "dockerfile" && (
            <Field id="dockerfile" label="Dockerfile">
              <Input
                id="dockerfile"
                value={configuration.build.dockerfile ?? "Dockerfile"}
                onChange={(event) => updateBuild({ dockerfile: event.target.value })}
                className="font-mono"
              />
            </Field>
          )}
          {configuration.build.method === "recipe" && (
            <Field
              id="automatic-recipe"
              label="Automatic recipe"
              hint="Selected from the inspected lockfile and source evidence."
            >
              <Select
                value={configuration.build.recipe ?? "node"}
                onValueChange={(recipe) =>
                  updateBuild({ recipe: recipe as "node" | "go" | "python" })
                }
              >
                <SelectTrigger id="automatic-recipe" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="node">Node.js</SelectItem>
                  <SelectItem value="go">Go</SelectItem>
                  <SelectItem value="python">Python</SelectItem>
                </SelectContent>
              </Select>
            </Field>
          )}
          {configuration.build.method === "static" && (
            <Field id="output-directory" label="Output directory">
              <Input
                id="output-directory"
                value={configuration.build.outputDirectory ?? "dist"}
                onChange={(event) => updateBuild({ outputDirectory: event.target.value })}
                className="font-mono"
              />
            </Field>
          )}
        </div>
      </details>
      <details open={profile === "game"} className="space-y-4 border-t border-hairline pt-3">
        <summary
          id="runtime-title"
          className="cursor-pointer rounded-sm py-2 text-sm font-medium focus-ring"
        >
          Runtime and route
        </summary>
        {profile === "game" && source?.mode === "blueprint" && (
          <Notice title="The licence you accepted is part of this plan" icon={Warning}>
            {blueprintAcceptances(source).length > 0
              ? "Your acceptance was recorded with the blueprint in the previous step, and is saved with the plan under your name."
              : "Go back to the source step and accept the licence this blueprint requires."}
          </Notice>
        )}
        {profile === "game" && source?.mode !== "blueprint" && (
          <Label
            id="minecraft-eula"
            className={cn(
              "flex min-h-11 items-start gap-3 rounded-xl border p-3 text-xs",
              errors.eula && "border-rule-danger",
            )}
          >
            <Checkbox
              className="mt-0.5"
              checked={configuration.variables.some((variable) => variable.name === "EULA")}
              onCheckedChange={(checked) =>
                onChange({
                  ...configuration,
                  variables: checked
                    ? [
                        ...configuration.variables.filter((variable) => variable.name !== "EULA"),
                        {
                          name: "EULA",
                          sensitivity: "plain",
                          scopes: ["runtime"],
                          required: true,
                          reference: "${{blueprint.minecraft-eula-accepted}}",
                        },
                      ]
                    : configuration.variables.filter((variable) => variable.name !== "EULA"),
                })
              }
            />
            <span>
              <span className="block font-medium">I accept the Minecraft EULA</span>
              <span className="mt-1 block text-muted-foreground">
                Required before this server can start. The acknowledgement becomes part of the
                reviewed plan.
              </span>
            </span>
          </Label>
        )}
        <div className="grid gap-4 sm:grid-cols-2">
          <Field id="runtime-image" label="Runtime image">
            <Input
              id="runtime-image"
              value={configuration.runtime.image ?? ""}
              onChange={(event) => updateRuntime({ image: event.target.value })}
              className="font-mono"
            />
          </Field>
          <Field id="release-strategy" label="Release strategy">
            <Select
              value={configuration.runtime.strategy}
              onValueChange={(strategy) =>
                updateRuntime({ strategy: strategy as "blue_green" | "stop_first" })
              }
            >
              <SelectTrigger id="release-strategy" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="blue_green">Candidate first</SelectItem>
                <SelectItem value="stop_first">Stop first</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field id="internal-port" label="Application port" error={errors.internalPort}>
            <Input
              id="internal-port"
              type="number"
              min={0}
              max={65535}
              value={configuration.runtime.internalPort ?? 0}
              onChange={(event) => updateRuntime({ internalPort: Number(event.target.value) })}
              className="font-mono"
            />
          </Field>
          <Field
            id="host-port"
            label="Published host port"
            hint="0 leaves the service private behind its managed route."
            error={errors.hostPort}
          >
            <Input
              id="host-port"
              type="number"
              min={0}
              max={65535}
              value={configuration.runtime.hostPort ?? 0}
              onChange={(event) => updateRuntime({ hostPort: Number(event.target.value) })}
              className="font-mono"
            />
          </Field>
          <Field id="bind-address" label="Bind address">
            <Select
              value={configuration.runtime.bindAddress || "127.0.0.1"}
              onValueChange={(bindAddress) => updateRuntime({ bindAddress })}
            >
              <SelectTrigger id="bind-address" className="w-full font-mono">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="127.0.0.1">127.0.0.1 · loopback</SelectItem>
                <SelectItem value="::1">::1 · loopback IPv6</SelectItem>
                <SelectItem value="0.0.0.0">0.0.0.0 · every interface</SelectItem>
                <SelectItem value="::">:: · every IPv6 interface</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          <Field
            id="domain"
            label="Public domain"
            hint="Optional. DNS and proxy ownership are checked before save."
          >
            <Input
              id="domain"
              value={domain?.hostname ?? ""}
              onChange={(event) =>
                onChange({
                  ...configuration,
                  domains: event.target.value
                    ? [
                        {
                          hostname: event.target.value.toLowerCase().trim(),
                          https: domain?.https ?? true,
                          ownership: domain?.ownership ?? "managed",
                        },
                      ]
                    : [],
                })
              }
              placeholder="app.example.com"
              className="font-mono"
            />
          </Field>
          {domain && (
            <Label className="flex min-h-11 items-center gap-2 text-xs">
              <Switch
                checked={domain.https}
                onCheckedChange={(https) =>
                  onChange({ ...configuration, domains: [{ ...domain, https }] })
                }
              />
              Request HTTPS
            </Label>
          )}
        </div>
      </details>
      <WizardEnvironment draftId={draftId} configuration={configuration} onChange={onChange} />
      <details className="border-t border-hairline pt-3">
        <summary className="cursor-pointer rounded-sm py-2 text-sm font-medium focus-ring">
          Variable references & scopes
        </summary>
        <div className="pt-3">
          <VariableEditor
            variables={configuration.variables}
            onChange={(variables) => onChange({ ...configuration, variables })}
          />
        </div>
      </details>
      <button
        type="button"
        aria-expanded={advanced}
        onClick={() => onAdvanced(!advanced)}
        className="flex min-h-11 w-full items-center justify-between rounded-xl border border-hairline bg-surface-header px-3.5 text-body font-medium focus-ring"
      >
        <span className="flex items-center gap-2">
          <SettingsSliders className="size-4" />
          Advanced
        </span>
        <span className="text-xs font-normal text-muted-foreground">
          {advanced ? "Hide" : "Show"}
        </span>
      </button>
      {advanced && (
        <AdvancedConfiguration
          configuration={configuration}
          onChange={onChange}
          errors={errors}
          onJSONError={onJSONError}
        />
      )}
    </div>
  )
}

function VariableEditor({
  variables,
  onChange,
}: {
  variables: DeploymentConfiguration["variables"]
  onChange: (variables: DeploymentConfiguration["variables"]) => void
}) {
  return (
    <section className="space-y-3 border-t border-hairline pt-5" aria-labelledby="variables-title">
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div>
          <h3 id="variables-title" className="text-body font-medium">
            Variables
          </h3>
          <p className="text-xs text-muted-foreground">
            References name a stored/generated value; secret literals do not belong in a plan.
          </p>
        </div>
        <Button
          size="sm"
          variant="outline"
          onClick={() =>
            onChange([
              ...variables,
              {
                name: "",
                sensitivity: "secret",
                scopes: ["runtime"],
                required: false,
                reference: "",
              },
            ])
          }
        >
          <Plus className="size-3.5" />
          Add variable
        </Button>
      </div>
      {variables.length === 0 ? (
        <EmptyNote>No variables configured.</EmptyNote>
      ) : (
        <div className="space-y-2">
          {variables.map((variable, index) => (
            <Group
              key={index}
              className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_8rem_minmax(0,1.4fr)_auto]"
            >
              <Input
                aria-label={`Variable ${index + 1} name`}
                value={variable.name}
                onChange={(event) =>
                  onChange(
                    variables.map((item, i) =>
                      i === index ? { ...item, name: event.target.value.toUpperCase() } : item,
                    ),
                  )
                }
                placeholder="DATABASE_URL"
                className="font-mono"
              />
              <Select
                value={variable.sensitivity}
                onValueChange={(sensitivity) =>
                  onChange(
                    variables.map((item, i) =>
                      i === index
                        ? { ...item, sensitivity: sensitivity as "plain" | "secret" }
                        : item,
                    ),
                  )
                }
              >
                <SelectTrigger
                  aria-label={`Variable ${variable.name || index + 1} sensitivity`}
                  className="w-full"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="secret">Secret</SelectItem>
                  <SelectItem value="plain">Plain</SelectItem>
                </SelectContent>
              </Select>
              <Input
                aria-label={`Variable ${variable.name || index + 1} reference`}
                value={variable.reference ?? ""}
                onChange={(event) =>
                  onChange(
                    variables.map((item, i) =>
                      i === index ? { ...item, reference: event.target.value } : item,
                    ),
                  )
                }
                placeholder="${{credential.name}}"
                className="font-mono"
              />
              <Button
                size="icon"
                variant="ghost"
                className="size-11 sm:size-9"
                aria-label={`Remove variable ${variable.name || index + 1}`}
                onClick={() => onChange(variables.filter((_, i) => i !== index))}
              >
                <Trash className="size-4" />
              </Button>
              <Well plain className="flex flex-wrap gap-x-4 gap-y-2 sm:col-span-4">
                {["build", "runtime", "release_task"].map((scope) => (
                  <Label
                    key={scope}
                    className="flex min-h-11 items-center gap-2 text-xs sm:min-h-8"
                  >
                    <Checkbox
                      checked={variable.scopes.includes(scope)}
                      onCheckedChange={(checked) =>
                        onChange(
                          variables.map((item, i) =>
                            i === index
                              ? {
                                  ...item,
                                  scopes: checked
                                    ? [...new Set([...item.scopes, scope])]
                                    : item.scopes.filter((value) => value !== scope),
                                }
                              : item,
                          ),
                        )
                      }
                    />
                    {humanize(scope)}
                  </Label>
                ))}
                <Label className="flex min-h-11 items-center gap-2 text-xs sm:min-h-8">
                  <Checkbox
                    checked={variable.required ?? false}
                    onCheckedChange={(checked) =>
                      onChange(
                        variables.map((item, i) =>
                          i === index ? { ...item, required: checked === true } : item,
                        ),
                      )
                    }
                  />
                  Required
                </Label>
              </Well>
            </Group>
          ))}
        </div>
      )}
    </section>
  )
}

function AdvancedConfiguration({
  configuration,
  onChange,
  errors,
  onJSONError,
}: {
  configuration: DeploymentConfiguration
  onChange: (configuration: DeploymentConfiguration) => void
  errors: WizardErrors
  onJSONError: (field: string, error?: string) => void
}) {
  const [command, setCommand] = useState((configuration.runtime.command ?? []).join("\n"))
  const [capabilities, setCapabilities] = useState(
    (configuration.runtime.capabilities ?? []).join("\n"),
  )
  const [devices, setDevices] = useState((configuration.runtime.devices ?? []).join("\n"))
  const [mounts, setMounts] = useState(JSON.stringify(configuration.runtime.mounts ?? [], null, 2))
  const [dependencies, setDependencies] = useState(
    JSON.stringify(configuration.dependencies, null, 2),
  )
  const [checks, setChecks] = useState(JSON.stringify(configuration.checks, null, 2))
  const [domains, setDomains] = useState(JSON.stringify(configuration.domains, null, 2))
  const applyJSON = (kind: "mounts" | "dependencies" | "checks" | "domains", value: string) => {
    try {
      const parsed = JSON.parse(value)
      if (!Array.isArray(parsed)) {
        onJSONError(kind, "Enter a JSON array.")
        return
      }
      onJSONError(kind)
      if (kind === "mounts")
        onChange({ ...configuration, runtime: { ...configuration.runtime, mounts: parsed } })
      else onChange({ ...configuration, [kind]: parsed })
    } catch {
      onJSONError(kind, "Enter valid JSON before running preflight.")
    }
  }
  return (
    <section id="advanced" className="space-y-5 p-4" aria-labelledby="advanced-title">
      <div>
        <h3 id="advanced-title" className="text-body font-medium">
          Advanced build and runtime plan
        </h3>
        <p className="text-xs text-muted-foreground">
          These fields are uncommon and can require administrator authorization. JSON arrays use the
          exact normalized plan schema.
        </p>
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        <Field
          id="target-platform"
          label="Target platform"
          hint="Optional OCI platform, for example linux/amd64."
        >
          <Input
            id="target-platform"
            value={configuration.build.targetPlatform ?? ""}
            onChange={(event) =>
              onChange({
                ...configuration,
                build: { ...configuration.build, targetPlatform: event.target.value },
              })
            }
            placeholder="linux/amd64"
            className="font-mono"
          />
        </Field>
        <Label className="flex min-h-11 items-center gap-2 text-xs">
          <Switch
            checked={configuration.build.noCache ?? false}
            onCheckedChange={(noCache) =>
              onChange({ ...configuration, build: { ...configuration.build, noCache } })
            }
          />
          Force a clean build
        </Label>
        {/* The definition sits outside the Label on purpose: a button inside
            one activates the control it labels, so a hover card asking "what
            is privileged?" would have turned it on. */}
        <div className="flex min-h-11 items-center gap-1.5 text-xs">
          <Label className="flex items-center gap-2 text-xs">
            <Switch
              checked={configuration.runtime.privileged ?? false}
              onCheckedChange={(privileged) =>
                onChange({ ...configuration, runtime: { ...configuration.runtime, privileged } })
              }
            />
            Privileged container
          </Label>
          <ExplainIcon name="privileged" />
        </div>
        <div className="flex min-h-11 items-center gap-1.5 text-xs">
          <Label className="flex items-center gap-2 text-xs">
            <Switch
              checked={configuration.runtime.hostNetwork ?? false}
              onCheckedChange={(hostNetwork) =>
                onChange({ ...configuration, runtime: { ...configuration.runtime, hostNetwork } })
              }
            />
            Use the host network
          </Label>
          <ExplainIcon name="hostNetwork" />
        </div>
      </div>
      {configuration.build.method === "recipe" && (
        <BuildSecretEditor
          secrets={configuration.build.secrets ?? []}
          variables={configuration.variables}
          error={errors.buildSecrets}
          onChange={(secrets) =>
            onChange({ ...configuration, build: { ...configuration.build, secrets } })
          }
        />
      )}
      <ReleaseTaskEditor
        tasks={configuration.build.releaseTasks ?? []}
        variables={configuration.variables}
        error={errors.releaseTasks}
        onChange={(releaseTasks) =>
          onChange({ ...configuration, build: { ...configuration.build, releaseTasks } })
        }
      />
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id="runtime-command" label="Command argv" hint="One argument per line.">
          <Textarea
            id="runtime-command"
            value={command}
            onChange={(event) => {
              setCommand(event.target.value)
              onChange({
                ...configuration,
                runtime: { ...configuration.runtime, command: nonemptyLines(event.target.value) },
              })
            }}
            rows={5}
            className="font-mono text-xs"
          />
        </Field>
        <Field
          id="capabilities"
          label="Linux capabilities"
          hint="One uppercase capability per line."
        >
          <Textarea
            id="capabilities"
            value={capabilities}
            onChange={(event) => {
              setCapabilities(event.target.value)
              onChange({
                ...configuration,
                runtime: {
                  ...configuration.runtime,
                  capabilities: nonemptyLines(event.target.value),
                },
              })
            }}
            rows={5}
            className="font-mono text-xs"
          />
        </Field>
        <Field id="devices" label="Host devices" hint="One absolute path per line.">
          <Textarea
            id="devices"
            value={devices}
            onChange={(event) => {
              setDevices(event.target.value)
              onChange({
                ...configuration,
                runtime: { ...configuration.runtime, devices: nonemptyLines(event.target.value) },
              })
            }}
            rows={5}
            className="font-mono text-xs"
          />
        </Field>
        <Field id="mounts" label="Mounts JSON" error={errors.mounts}>
          <Textarea
            id="mounts"
            value={mounts}
            onChange={(event) => {
              setMounts(event.target.value)
              applyJSON("mounts", event.target.value)
            }}
            rows={7}
            className="font-mono text-xs"
            aria-invalid={Boolean(errors.mounts)}
            aria-describedby={errors.mounts ? "mounts-error" : undefined}
          />
        </Field>
        <Field id="dependencies" label="Dependencies JSON" error={errors.dependencies}>
          <Textarea
            id="dependencies"
            value={dependencies}
            onChange={(event) => {
              setDependencies(event.target.value)
              applyJSON("dependencies", event.target.value)
            }}
            rows={7}
            className="font-mono text-xs"
            aria-invalid={Boolean(errors.dependencies)}
            aria-describedby={errors.dependencies ? "dependencies-error" : undefined}
          />
        </Field>
        <Field id="checks" label="Readiness and smoke checks JSON" error={errors.checks}>
          <Textarea
            id="checks"
            value={checks}
            onChange={(event) => {
              setChecks(event.target.value)
              applyJSON("checks", event.target.value)
            }}
            rows={7}
            className="font-mono text-xs"
            aria-invalid={Boolean(errors.checks)}
            aria-describedby={errors.checks ? "checks-error" : undefined}
          />
        </Field>
        <Field
          id="domains"
          label="Additional domains JSON"
          hint="Edit the complete ordered domain and ownership list."
          error={errors.domains}
        >
          <Textarea
            id="domains"
            value={domains}
            onChange={(event) => {
              setDomains(event.target.value)
              applyJSON("domains", event.target.value)
            }}
            rows={7}
            className="font-mono text-xs"
            aria-invalid={Boolean(errors.domains)}
            aria-describedby={errors.domains ? "domains-error" : "domains-hint"}
          />
        </Field>
      </div>
    </section>
  )
}

function BuildSecretEditor({
  secrets,
  variables,
  error,
  onChange,
}: {
  secrets: NonNullable<DeploymentConfiguration["build"]["secrets"]>
  variables: DeploymentConfiguration["variables"]
  error?: string
  onChange: (secrets: NonNullable<DeploymentConfiguration["build"]["secrets"]>) => void
}) {
  const buildVariables = variables.filter((variable) => variable.scopes.includes("build"))
  return (
    <section
      className="space-y-3 border-t border-hairline pt-5"
      aria-labelledby="build-secrets-title"
    >
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div>
          <h4 id="build-secrets-title" className="text-xs font-medium">
            Build secrets
          </h4>
          <p className="text-hint text-muted-foreground">
            Reviewed recipes mount these only for the named BuildKit step; values never enter argv.
          </p>
        </div>
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() => onChange([...secrets, { variable: "", step: "install" }])}
        >
          <Plus className="size-3.5" />
          Add build secret
        </Button>
      </div>
      {error && (
        <p id="build-secrets-error" role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}
      {secrets.length === 0 ? (
        <p className="text-xs text-muted-foreground">No build secrets configured.</p>
      ) : (
        <div className="space-y-2">
          {secrets.map((secret, index) => (
            <Group key={index} className="grid gap-2 sm:grid-cols-[minmax(0,1fr)_10rem_auto]">
              <Input
                aria-label={`Build secret ${index + 1} variable`}
                value={secret.variable}
                onChange={(event) =>
                  onChange(
                    secrets.map((item, itemIndex) =>
                      itemIndex === index
                        ? { ...item, variable: event.target.value.toUpperCase() }
                        : item,
                    ),
                  )
                }
                list="build-variable-names"
                placeholder="NPM_TOKEN"
                className="font-mono"
                aria-invalid={Boolean(error)}
                aria-describedby={error ? "build-secrets-error" : undefined}
              />
              <Select
                value={secret.step}
                onValueChange={(step) =>
                  onChange(
                    secrets.map((item, itemIndex) =>
                      itemIndex === index ? { ...item, step: step as "install" | "build" } : item,
                    ),
                  )
                }
              >
                <SelectTrigger
                  aria-label={`Build secret ${secret.variable || index + 1} step`}
                  className="w-full"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="install">Install step</SelectItem>
                  <SelectItem value="build">Build step</SelectItem>
                </SelectContent>
              </Select>
              <Button
                type="button"
                size="icon"
                variant="ghost"
                className="size-11 sm:size-9"
                aria-label={`Remove build secret ${secret.variable || index + 1}`}
                onClick={() => onChange(secrets.filter((_, itemIndex) => itemIndex !== index))}
              >
                <Trash className="size-4" />
              </Button>
            </Group>
          ))}
        </div>
      )}
      <datalist id="build-variable-names">
        {buildVariables.map((variable) => (
          <option key={variable.name} value={variable.name} />
        ))}
      </datalist>
    </section>
  )
}

function ReleaseTaskEditor({
  tasks,
  variables,
  error,
  onChange,
}: {
  tasks: NonNullable<DeploymentConfiguration["build"]["releaseTasks"]>
  variables: DeploymentConfiguration["variables"]
  error?: string
  onChange: (tasks: NonNullable<DeploymentConfiguration["build"]["releaseTasks"]>) => void
}) {
  const releaseVariables = variables.filter((variable) => variable.scopes.includes("release_task"))
  const update = (
    index: number,
    patch: Partial<NonNullable<DeploymentConfiguration["build"]["releaseTasks"]>[number]>,
  ) =>
    onChange(tasks.map((task, itemIndex) => (itemIndex === index ? { ...task, ...patch } : task)))
  return (
    <section
      className="space-y-3 border-t border-hairline pt-5"
      aria-labelledby="release-tasks-title"
    >
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div>
          <h4 id="release-tasks-title" className="text-xs font-medium">
            Release tasks
          </h4>
          <p className="text-hint text-muted-foreground">
            Named, timed gates run after the artifact is recorded and before activation.
          </p>
        </div>
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={() =>
            onChange([
              ...tasks,
              { name: "", command: "", workingDirectory: "", timeoutSeconds: 300, env: [] },
            ])
          }
        >
          <Plus className="size-3.5" />
          Add release task
        </Button>
      </div>
      {error && (
        <p id="release-tasks-error" role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}
      {tasks.length === 0 ? (
        <p className="text-xs text-muted-foreground">No release tasks configured.</p>
      ) : (
        <div className="space-y-3">
          {tasks.map((task, index) => (
            <fieldset
              key={index}
              className="space-y-3 rounded-xl border border-hairline p-3"
              aria-describedby={error ? "release-tasks-error" : undefined}
            >
              <legend className="px-1 text-hint font-medium text-muted-foreground">
                Task {index + 1}
              </legend>
              <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_9rem_auto]">
                <Input
                  aria-label={`Release task ${index + 1} name`}
                  value={task.name}
                  onChange={(event) => update(index, { name: event.target.value })}
                  placeholder="Database migration"
                />
                <Input
                  aria-label={`Release task ${index + 1} timeout seconds`}
                  type="number"
                  min={1}
                  max={3600}
                  value={task.timeoutSeconds}
                  onChange={(event) =>
                    update(index, { timeoutSeconds: Number(event.target.value) })
                  }
                  className="font-mono"
                />
                <Button
                  type="button"
                  size="icon"
                  variant="ghost"
                  className="size-11 sm:size-9"
                  aria-label={`Remove release task ${task.name || index + 1}`}
                  onClick={() => onChange(tasks.filter((_, itemIndex) => itemIndex !== index))}
                >
                  <Trash className="size-4" />
                </Button>
              </div>
              <Input
                aria-label={`Release task ${index + 1} working directory`}
                value={task.workingDirectory ?? ""}
                onChange={(event) => update(index, { workingDirectory: event.target.value })}
                placeholder="Working directory (source root by default)"
                className="font-mono"
              />
              <Textarea
                aria-label={`Release task ${index + 1} command`}
                value={task.command}
                onChange={(event) => update(index, { command: event.target.value })}
                placeholder="./bin/migrate"
                rows={3}
                className="font-mono text-xs"
              />
              <div>
                <p className="text-hint text-muted-foreground">Explicit Release task environment</p>
                {releaseVariables.length === 0 ? (
                  <p className="mt-1 text-hint text-muted-foreground">
                    Add Release task scope to a variable to make it selectable here.
                  </p>
                ) : (
                  <div className="mt-1 flex flex-wrap gap-x-4 gap-y-1">
                    {releaseVariables.map((variable) => (
                      <Label
                        key={variable.name}
                        className="flex min-h-11 items-center gap-2 text-xs sm:min-h-8"
                      >
                        <Checkbox
                          checked={task.env.includes(variable.name)}
                          onCheckedChange={(checked) =>
                            update(index, {
                              env: checked
                                ? [...new Set([...task.env, variable.name])]
                                : task.env.filter((name) => name !== variable.name),
                            })
                          }
                        />
                        <span className="font-mono">{variable.name}</span>
                      </Label>
                    ))}
                  </div>
                )}
              </div>
            </fieldset>
          ))}
        </div>
      )}
    </section>
  )
}

function PreflightStep({
  preflight,
  draft,
  acknowledged,
  onAcknowledged,
  errors,
}: {
  preflight?: DeploymentPreflight
  draft: DeploymentDraft
  acknowledged: string[]
  onAcknowledged: (codes: string[]) => void
  errors: WizardErrors
}) {
  const findings = preflight?.findings ?? draft.findings
  const groups = preflightGroups(findings)
  const warnings = findings.filter((finding) => finding.severity === "warning")
  const blockers = findings.filter(
    (finding) => finding.severity === "blocked" || finding.severity === "decision",
  )
  return (
    <div className="space-y-5">
      {!preflight && (
        <Notice tone="warning" title="Preflight evidence is not loaded">
          Go back to Configure and run preflight again.
        </Notice>
      )}
      <section aria-labelledby="release-path-title">
        <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
          <div>
            <h3 id="release-path-title" className="text-body font-medium">
              Release path
            </h3>
            <p className="text-xs text-muted-foreground">
              Each node reports measured evidence; unavailable is never treated as pass.
            </p>
          </div>
          {preflight && (
            <code className="text-micro text-muted-foreground" title={preflight.digest}>
              {preflight.digest.slice(0, 20)}…
            </code>
          )}
        </div>
        <ol className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
          {groups.map((group, index) => (
            <li
              key={group.label}
              className="relative rounded-xl border border-hairline bg-surface-header p-3"
            >
              <div className="flex items-center gap-2">
                <span className="numeric flex size-6 shrink-0 items-center justify-center rounded-full border border-hairline text-micro">
                  {index + 1}
                </span>
                <span className="text-xs font-medium">{group.label}</span>
                <Status
                  verdict={findingVerdict(group.severity)}
                  label={humanize(group.severity)}
                  className="ml-auto"
                />
              </div>
              <p className="mt-2 text-hint leading-relaxed text-muted-foreground">
                {group.summary}
              </p>
            </li>
          ))}
        </ol>
      </section>
      {errors.preflight && (
        <Notice tone="danger" title="The plan is not ready">
          {errors.preflight}
        </Notice>
      )}
      <section className="space-y-2" aria-labelledby="findings-title">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h3 id="findings-title" className="text-body font-medium">
            Findings
          </h3>
          <span className="text-hint text-muted-foreground">
            {blockers.length} blocking · {warnings.length} warnings
          </span>
        </div>
        {findings.map((finding) => (
          <FindingRow key={finding.code} finding={finding} />
        ))}
      </section>
      {warnings.length > 0 && (
        <fieldset
          id="warnings"
          className="space-y-2 rounded-xl border border-rule-warning bg-wash-warning p-4"
        >
          <legend className="px-1 text-body font-medium">Acknowledge warnings</legend>
          {warnings.map((finding) => (
            <Label key={finding.code} className="flex min-h-11 items-start gap-3 text-xs">
              <Checkbox
                className="mt-0.5"
                checked={acknowledged.includes(finding.code)}
                onCheckedChange={(checked) =>
                  onAcknowledged(
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
        </fieldset>
      )}
      <details>
        <summary className="flex min-h-11 cursor-pointer items-center text-xs font-medium">
          Show exact, secret-free plan
        </summary>
        <Well className="max-h-[32rem] break-words whitespace-pre-wrap">
          {preflight?.preview || draft.planPreview || "No plan preview is available."}
        </Well>
      </details>
      <Notice title="Ready to save, not execute" icon={CheckCircle}>
        {blockers.length
          ? "Resolve the blocking findings, then run preflight again."
          : "Save creates the deployment and production environment atomically. Starting a run is a separate action."}
      </Notice>
    </div>
  )
}

function ActionButton({
  busy,
  disabled,
  onClick,
  children,
}: {
  busy: boolean
  disabled?: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <Button className="h-11 sm:h-9" onClick={onClick} pending={busy} disabled={disabled}>
      <ArrowRight className="size-4" />
      {busy ? "Working…" : children}
    </Button>
  )
}

function Field({
  id,
  label,
  hint,
  error,
  className,
  children,
}: {
  id: string
  label: string
  hint?: string
  error?: string
  className?: string
  children: React.ReactNode
}) {
  return (
    <div className={cn("space-y-1.5", className)}>
      <Label htmlFor={id}>{label}</Label>
      {children}
      {hint && !error && (
        <p id={`${id}-hint`} className="text-hint leading-relaxed text-muted-foreground">
          {hint}
        </p>
      )}
      {error && (
        <p id={`${id}-error`} role="alert" className="text-hint leading-relaxed text-destructive">
          {error}
        </p>
      )}
    </div>
  )
}

function preflightGroups(findings: DeploymentPreflightFinding[]) {
  const definitions = [
    ["Source", ["git", "source"]],
    ["Build", ["builder", "build"]],
    ["Runtime", ["docker", "runtime"]],
    ["Storage", ["backup", "files", "storage"]],
    ["Verify", ["checks", "health"]],
    ["Public route", ["proxy", "dns", "certificate"]],
  ] as const
  return definitions.map(([label, owners]) => {
    const group = findings.filter((finding) =>
      owners.some((owner) => finding.owner === owner || finding.code.includes(owner)),
    )
    const severity = worstSeverity(group.map((finding) => finding.severity))
    return {
      label,
      severity,
      summary: group[0]?.title ?? "No configured action in this part of the release.",
    }
  })
}

function worstSeverity(values: DeploymentPreflightFinding["severity"][]) {
  for (const severity of ["blocked", "decision", "warning", "unavailable", "pass"] as const)
    if (values.includes(severity)) return severity
  return "unavailable" as const
}

function nonemptyLines(value: string) {
  return value
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
}
function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KiB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MiB`
}
