"use client"

import { useCallback, useRef, useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import {
  ArrowLeft,
  ArrowRight,
  Plus,
  Trash,
  Box,
  CheckCircle,
  Clipboard,
  CloudUpload,
  Database,
  Layers,
  Servers,
  Code,
  GitBranch,
  LockClosed,
  RefreshClockwise,
  SettingsSliders,
  Warning,
} from "@/components/icons"
import { get, post, put } from "@/lib/api"
import { cn } from "@/lib/utils"
import type {
  DeploymentConfiguration,
  DeploymentBuildMethod,
  DeploymentDetectionCandidate,
  DeploymentDraft,
  DeploymentDraftSource,
  DeploymentHostnameSuggestion,
  DeploymentPreflight,
  DockerImage,
  GitHubBranch,
  GitHubRepoSummary,
  WorkloadProfile,
} from "@/lib/types"
import { useCopy } from "@/hooks/use-copy"
import { GitHubAccountControl } from "@/components/git/github-account"
import { ProjectDatabase } from "@/components/deploy/project-database"
import { useGitHubAccount } from "@/hooks/use-github"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageHeader, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle } from "@/components/choice-card"
import {
  FindingRow,
  blockingFindings,
  warningFindings,
} from "@/components/deploy/deployment-findings"
import { defaultConfiguration } from "@/components/deploy/deployment-defaults"
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
import { Switch } from "@/components/ui/switch"
import { Textarea } from "@/components/ui/textarea"
import { Tag } from "@/components/tag"
import { DatabaseQuickDeploy } from "@/components/deploy/quick-database"
import { setDeploymentHandoff } from "@/components/deploy/deployment-handoff"

type Lane = "github" | "image" | "database"

/** What the server accepts as a deployment name, checked before it is sent. */
const DEPLOYMENT_NAME = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/

/**
 * A repository or image name as a deployment name.
 *
 * The two sources this screen derives a name from have their own rules —
 * GitHub allows a leading dot, a registry path allows slashes — and the draft
 * has a narrower one. Trimming here rather than rejecting there means the
 * field arrives filled in and legal, and the operator edits a name rather than
 * a validation message.
 */
function deploymentName(raw: string) {
  const cleaned = raw
    .trim()
    .replace(/[^A-Za-z0-9._-]+/g, "-")
    .replace(/^[^A-Za-z0-9]+/, "")
    .slice(0, 64)
  return cleaned || "app"
}

const LANES: { lane: Lane; title: string; description: string; icon: typeof CloudUpload }[] = [
  {
    lane: "github",
    title: "From GitHub",
    description: "Pick a repository, set the environment, deploy. Detection reads the framework.",
    icon: GitBranch,
  },
  {
    lane: "image",
    title: "From a Docker image",
    description: "Choose an image already on this server, or name one in a registry.",
    icon: Box,
  },
  {
    lane: "database",
    title: "A database",
    description: "Start Postgres, MySQL, MariaDB, Redis or Mongo and copy its connection string.",
    icon: Database,
  },
]

export function QuickDeploy() {
  const search = useSearchParams()
  const initial = search.get("source")
  const [lane, setLane] = useState<Lane>(
    initial === "database" || initial === "image" ? initial : "github",
  )
  return (
    <Page className="max-w-[1320px]">
      <PageHeader
        eyebrow={
          <Link href="/deploy" className="inline-flex items-center gap-1 hover:underline">
            <ArrowLeft className="size-3" /> Projects
          </Link>
        }
        title="Let's deploy something new"
        actions={
          <Button variant="outline" size="sm" asChild>
            <Link href="/deploy">Back to projects</Link>
          </Button>
        }
      />
      <div role="group" aria-label="Project source" className="flex flex-wrap gap-2">
        {LANES.map((option) => (
          <Button
            key={option.lane}
            variant={lane === option.lane ? "secondary" : "ghost"}
            size="sm"
            aria-pressed={lane === option.lane}
            onClick={() => setLane(option.lane)}
          >
            <option.icon className="size-4" />
            {option.title}
          </Button>
        ))}
      </div>
      {lane === "database" ? (
        <div className="mx-auto w-full max-w-3xl">
          <DatabaseQuickDeploy />
        </div>
      ) : (
        <ApplicationFlow key={lane} lane={lane} onLane={setLane} />
      )}
    </Page>
  )
}

function SourceCatalog({ onLane }: { onLane: (lane: Lane) => void }) {
  return (
    <Panel>
      <PanelHeader title="Start with something ready" />
      <PanelBody className="space-y-1 p-2">
        {[
          {
            title: "Database",
            hint: "Postgres, MySQL, Redis, MongoDB & more",
            icon: Database,
            lane: "database" as const,
          },
          {
            title: "Docker image",
            hint: "Run an image from any registry",
            icon: Box,
            lane: "image" as const,
          },
        ].map((option) => (
          <button
            key={option.title}
            onClick={() => onLane(option.lane)}
            className="flex min-h-20 w-full items-center gap-3 rounded-md p-3 text-left focus-ring-inset hover:bg-row-hover"
          >
            <option.icon className="size-5 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1">
              <span className="block text-sm font-medium">{option.title}</span>
              <span className="mt-1 block text-xs text-muted-foreground">{option.hint}</span>
            </span>
            <ArrowRight className="size-3.5 shrink-0 text-muted-foreground" />
          </button>
        ))}
        {[
          {
            profile: "compose",
            title: "Compose stack",
            hint: "An application and its services, together",
            icon: Layers,
          },
          {
            profile: "service",
            title: "Application template",
            hint: "Reviewed apps with ready-to-use defaults",
            icon: CloudUpload,
          },
          {
            profile: "game",
            title: "Game server",
            hint: "Persistent worlds, players and a console",
            icon: Servers,
          },
          {
            profile: "worker",
            title: "Worker or bot",
            hint: "Background jobs without a public website",
            icon: Code,
          },
          {
            profile: "static",
            title: "Static website",
            hint: "Build and publish your site's files",
            icon: CloudUpload,
          },
          {
            profile: "imported",
            title: "Existing workload",
            hint: "Bring a running service into your projects",
            icon: Box,
          },
        ].map((option) => (
          <Link
            key={option.profile}
            href={`/deploy/new?mode=advanced&profile=${option.profile}`}
            className="flex min-h-20 items-center gap-3 rounded-md p-3 focus-ring-inset hover:bg-row-hover"
          >
            <option.icon className="size-5 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1">
              <span className="block text-sm font-medium">{option.title}</span>
              <span className="mt-1 block text-xs text-muted-foreground">{option.hint}</span>
            </span>
            <ArrowRight className="size-3.5 shrink-0 text-muted-foreground" />
          </Link>
        ))}
      </PanelBody>
      <PanelFooter>
        <Button variant="ghost" size="sm" asChild>
          <Link href="/deploy/new?mode=advanced">
            All configuration options <ArrowRight className="size-3.5" />
          </Link>
        </Button>
      </PanelFooter>
    </Panel>
  )
}

type ApplicationForm = {
  name: string
  variables: { name: string; value: string }[]
  databaseIds: number[]
  profile: WorkloadProfile
  buildMethod: DeploymentBuildMethod
  recipe: "node" | "go" | "python"
  dockerfile: string
  rootDirectory: string
  buildCommand: string
  startCommand: string
  outputDirectory: string
  internalPort: number
  dotenv: string
  hostname: string
  https: boolean
  publish: boolean
}

function ApplicationFlow({
  lane,
  onLane,
}: {
  lane: "github" | "image"
  onLane: (lane: Lane) => void
}) {
  const router = useRouter()
  const [source, setSource] = useState<DeploymentDraftSource>()
  const [draft, setDraft] = useState<DeploymentDraft>()
  const [candidate, setCandidate] = useState<DeploymentDetectionCandidate>()
  const [form, setForm] = useState<ApplicationForm>()
  const [suggestion, setSuggestion] = useState<DeploymentHostnameSuggestion>()
  const [preflight, setPreflight] = useState<DeploymentPreflight>()
  const [acknowledged, setAcknowledged] = useState<string[]>([])
  const [busy, setBusy] = useState("")
  const [failure, setFailure] = useState<Error>()
  const [createdProject, setCreatedProject] = useState<{
    projectId: number
    environmentId: number
    ready: boolean
  }>()

  const profile: WorkloadProfile = form?.profile ?? (lane === "image" ? "image" : "web")

  // The whole point of this screen is that choosing a source is the last
  // decision before the configuration is already filled in, so detection runs
  // as part of the same action rather than as a step of its own.
  const inspect = useCallback(
    async (chosen: DeploymentDraftSource, name: string) => {
      setBusy("inspect")
      setFailure(undefined)
      try {
        const created = await post<DeploymentDraft>("/deploy/drafts", {})
        const named = await put<DeploymentDraft>(`/deploy/drafts/${created.id}`, {
          revision: created.revision,
          step: "intent",
          intent: { name, profile },
        })
        const sourced = await put<DeploymentDraft>(`/deploy/drafts/${created.id}`, {
          revision: named.revision,
          step: "source",
          source: chosen,
        })
        const detected = await post<DeploymentDraft>(`/deploy/drafts/${created.id}/detect`, {
          revision: sourced.revision,
        })
        const detection = detected.data.detection
        const selected =
          detection?.candidates.find((item) => item.id === detection.selectedId) ??
          detection?.candidates[0]
        const defaults = defaultConfiguration(
          selected?.profile ?? profile,
          selected,
          chosen,
          detection,
        )
        setSource(chosen)
        setDraft(detected)
        setCandidate(selected)
        setForm({
          name,
          profile: selected?.profile ?? profile,
          variables: [{ name: "", value: "" }],
          databaseIds: [],
          buildMethod: defaults.build.method,
          recipe: defaults.build.recipe ?? "node",
          dockerfile: defaults.build.dockerfile ?? "Dockerfile",
          rootDirectory: defaults.build.rootDirectory ?? "",
          buildCommand: defaults.build.buildCommand ?? "",
          startCommand: defaults.build.startCommand ?? "",
          outputDirectory: defaults.build.outputDirectory ?? "",
          internalPort: defaults.runtime.internalPort ?? (lane === "github" ? 3000 : 0),
          dotenv: "",
          hostname: "",
          https: true,
          publish: true,
        })
        setPreflight(undefined)
        setAcknowledged([])
        const hostname = await get<DeploymentHostnameSuggestion>(
          `/deploy/hostname?name=${encodeURIComponent(name)}`,
        )
        setSuggestion(hostname)
        setForm((current) =>
          current
            ? {
                ...current,
                hostname: hostname.hostname,
                // HTTPS is offered by default because it is what anyone wants,
                // but only pre-selected when it can actually be delivered:
                // activation refuses a TLS route with no certificate, and a
                // toggle that guarantees a failed release is not a default.
                https: true,
                publish: current.profile !== "worker" && hostname.method !== "none",
              }
            : current,
        )
      } catch (error) {
        setFailure(error instanceof Error ? error : new Error(String(error)))
      } finally {
        setBusy("")
      }
    },
    [lane, profile],
  )

  const openWizard = async () => {
    if (!draft || !form || !source || busy) return
    if (!DEPLOYMENT_NAME.test(form.name)) {
      setFailure(new Error("Use 1–64 letters, numbers, dots, dashes, or underscores for the name."))
      return
    }
    setBusy("handoff")
    setFailure(undefined)
    try {
      let current = draft
      if (form.name !== current.data.intent?.name || profile !== current.data.intent?.profile) {
        current = await put<DeploymentDraft>(`/deploy/drafts/${current.id}`, {
          revision: current.revision,
          step: "intent",
          intent: { name: form.name, profile },
        })
        setDraft(current)
      }
      current = await put<DeploymentDraft>(`/deploy/drafts/${current.id}`, {
        revision: current.revision,
        step: "configuration",
        configuration: configurationFor(profile, candidate, source, current, form),
      })
      setDraft(current)
      setDeploymentHandoff(current.id, environmentText(form))
      router.push(`/deploy/new?draft=${encodeURIComponent(current.id)}&step=configuration`)
    } catch (error) {
      setFailure(error instanceof Error ? error : new Error(String(error)))
    } finally {
      setBusy("")
    }
  }

  const deploy = async () => {
    if (!draft || !form || !source) return
    if (!DEPLOYMENT_NAME.test(form.name)) {
      setFailure(new Error("Use 1–64 letters, numbers, dots, dashes, or underscores for the name."))
      return
    }
    if (
      form.variables.some(
        (entry) => (entry.name || entry.value) && !/^[A-Za-z_][A-Za-z0-9_]*$/.test(entry.name),
      )
    ) {
      setFailure(
        new Error(
          "Give each environment variable a valid key, using letters, numbers and underscores.",
        ),
      )
      return
    }
    if (
      form.profile === "static" &&
      form.buildMethod === "recipe" &&
      !form.outputDirectory.trim()
    ) {
      setFailure(
        new Error("Set the output directory for your static website, such as dist or out."),
      )
      return
    }
    const names = form.variables.map((entry) => entry.name).filter(Boolean)
    if (new Set(names).size !== names.length) {
      setFailure(new Error("Each environment variable needs a unique key."))
      return
    }
    setBusy("deploy")
    setFailure(undefined)
    try {
      let current = draft
      // The name is editable on the configure screen, so it can differ from the
      // one the draft was opened with. Saving the intent again only replaces
      // that field — the source and detection it already holds are untouched.
      if (form.name !== current.data.intent?.name || profile !== current.data.intent?.profile) {
        current = await put<DeploymentDraft>(`/deploy/drafts/${current.id}`, {
          revision: current.revision,
          step: "intent",
          intent: { name: form.name, profile },
        })
        setDraft(current)
      }
      const configuration = configurationFor(profile, candidate, source, current, form)
      const saved = await put<DeploymentDraft>(`/deploy/drafts/${current.id}`, {
        revision: current.revision,
        step: "configuration",
        configuration,
      })
      setDraft(saved)
      const checked = await post<{ draft: DeploymentDraft; preflight: DeploymentPreflight }>(
        `/deploy/drafts/${current.id}/preflight`,
        { revision: saved.revision },
      )
      setDraft(checked.draft)
      setPreflight(checked.preflight)

      const blockers = blockingFindings(checked.preflight.findings)
      const warnings = warningFindings(checked.preflight.findings)
      if (blockers.length) return
      const unacknowledged = warnings.filter((finding) => !acknowledged.includes(finding.code))
      if (unacknowledged.length) return

      const committed = await post<{
        projectId: number
        environmentId: number
        planRevision: number
      }>(`/deploy/drafts/${current.id}/commit`, {
        revision: checked.draft.revision,
        acknowledgedWarnings: acknowledged,
      })
      setCreatedProject({ ...committed, ready: !environmentText(form).trim() })

      let revision = committed.planRevision
      if (environmentText(form).trim()) {
        const imported = await post<{ desiredRevision: number }>(
          `/deploy/${committed.projectId}/environments/${committed.environmentId}/variables/import`,
          {
            revision,
            dotenv: environmentText(form),
            sensitivity: "secret",
            scopes: ["runtime", "build"],
          },
        )
        revision = imported.desiredRevision
      }
      setCreatedProject({ ...committed, ready: true })

      const run = await post<{ id: number }>(
        `/deploy/${committed.projectId}/environments/${committed.environmentId}/runs`,
        { operation: "deploy" },
      )
      router.push(`/deploy/${committed.projectId}/runs/${run.id}`)
    } catch (error) {
      setFailure(error instanceof Error ? error : new Error(String(error)))
    } finally {
      setBusy("")
    }
  }

  if (createdProject)
    return (
      <Panel>
        <PanelHeader title="Deployment created" />
        <PanelBody className="space-y-4">
          {failure ? (
            <ErrorState error={failure} />
          ) : (
            <p className="text-sm">Starting the first release…</p>
          )}
          <p className="text-sm text-muted-foreground">
            Your project and configuration are saved.{" "}
            {createdProject.ready
              ? "Open the deployment to check its run history and continue."
              : "Environment setup did not finish. Review the Variables tab before starting the first release."}
          </p>
          {!createdProject.ready && form && environmentText(form) && (
            <details>
              <summary className="cursor-pointer text-sm focus-ring">
                Keep a copy of your environment variables
              </summary>
              <Textarea
                className="mt-3 font-mono text-xs"
                aria-label="Unsaved environment variables"
                readOnly
                value={environmentText(form)}
              />
            </details>
          )}
          <Button asChild>
            <Link
              href={`/deploy/${createdProject.projectId}?tab=${createdProject.ready ? "deployments" : "variables"}`}
            >
              {createdProject.ready ? "Open deployment" : "Finish environment setup"}
              <ArrowRight className="size-4" />
            </Link>
          </Button>
        </PanelBody>
      </Panel>
    )

  if (!form || !draft)
    return (
      <div className="grid min-w-0 items-start gap-5 lg:grid-cols-[minmax(0,1.6fr)_minmax(0,1fr)]">
        <div className="min-w-0 space-y-4">
          {failure && <ErrorState error={failure} />}
          {lane === "github" ? (
            <GitHubSource busy={busy === "inspect"} onChoose={inspect} />
          ) : (
            <ImageSource busy={busy === "inspect"} onChoose={inspect} />
          )}
        </div>
        <SourceCatalog onLane={onLane} />
      </div>
    )

  const blockers = preflight ? blockingFindings(preflight.findings) : []
  const warnings = preflight ? warningFindings(preflight.findings) : []
  const outstanding = warnings.filter((finding) => !acknowledged.includes(finding.code))

  return (
    <div className="mx-auto w-full max-w-3xl space-y-4">
      {failure && <ErrorState error={failure} />}
      <nav aria-label="Deployment setup" className="flex flex-wrap items-center gap-3 text-xs">
        <span className="text-muted-foreground">1. Source selected</span>
        <ArrowRight className="size-3 text-muted-foreground" />
        <span aria-current="step" className="font-medium">
          2. Configure & deploy
        </span>
      </nav>
      <Panel>
        <PanelHeader
          title="Selected source"
          actions={
            <Button
              size="xs"
              variant="ghost"
              disabled={Boolean(busy)}
              onClick={() => {
                setForm(undefined)
                setDraft(undefined)
              }}
            >
              Change source
            </Button>
          }
        />
        <PanelBody className="flex min-w-0 flex-wrap items-center justify-between gap-2 text-sm">
          <span className="min-w-0 font-medium break-all">
            {source?.repository || source?.image || source?.url}
          </span>
          {source?.ref && (
            <span className="font-mono text-xs text-muted-foreground">{source.ref}</span>
          )}
        </PanelBody>
      </Panel>
      <ConfigureStep
        lane={lane}
        form={form}
        onChange={setForm}
        candidate={candidate}
        detectionUnavailable={draft.data.detection?.unavailable}
        suggestion={suggestion}
      />
      {preflight && (blockers.length > 0 || warnings.length > 0) && (
        <Panel>
          <PanelHeader
            title={blockers.length ? "This plan cannot deploy yet" : "Acknowledge before deploying"}
          />
          <PanelBody className="space-y-2">
            {blockers.map((finding) => (
              <FindingRow key={finding.code} finding={finding} />
            ))}
            {warnings.map((finding) => (
              <Label
                key={finding.code}
                className="flex min-h-11 items-start gap-3 rounded-xl border border-rule-warning bg-wash-warning p-3 text-xs"
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
          </PanelBody>
        </Panel>
      )}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <p className="text-hint text-muted-foreground">
          Deploy saves the plan, applies the environment, and starts the release.
        </p>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="outline"
            onClick={() => void openWizard()}
            pending={busy === "handoff"}
            disabled={Boolean(busy)}
          >
            <SettingsSliders className="size-4" />
            Open in full wizard
          </Button>
          <Button
            className="h-11 sm:h-9"
            onClick={() => void deploy()}
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
        </div>
      </div>
    </div>
  )
}

function environmentText(form: ApplicationForm) {
  return [
    form.dotenv.trim(),
    ...form.variables
      .filter((entry) => entry.name.trim())
      .map((entry) => `${entry.name.trim()}=${JSON.stringify(entry.value)}`),
  ]
    .filter(Boolean)
    .join("\n")
}

/** The plan the form means, with detection filling in everything not asked about. */
function configurationFor(
  profile: WorkloadProfile,
  candidate: DeploymentDetectionCandidate | undefined,
  source: DeploymentDraftSource,
  draft: DeploymentDraft,
  form: ApplicationForm,
): DeploymentConfiguration {
  const defaults = defaultConfiguration(profile, candidate, source, draft.data.detection)
  const port = Number(form.internalPort) || 0
  return {
    ...defaults,
    dependencies: [
      ...defaults.dependencies,
      ...form.databaseIds.map((id) => ({
        kind: "database",
        ownership: "linked" as const,
        resourceKind: "database_connection",
        resourceId: String(id),
        config: {},
      })),
    ],
    build: {
      ...defaults.build,
      method: form.buildMethod,
      recipe: form.buildMethod === "recipe" ? form.recipe : undefined,
      dockerfile: form.buildMethod === "dockerfile" ? form.dockerfile : undefined,
      rootDirectory: form.rootDirectory.trim() || undefined,
      buildCommand: form.buildCommand.trim() || undefined,
      startCommand: form.startCommand.trim() || undefined,
      outputDirectory: form.outputDirectory.trim() || undefined,
    },
    runtime: { ...defaults.runtime, internalPort: port },
    // Readiness follows the runtime publication, which may differ from the
    // container port when Docker allocates a free host port.
    checks: defaults.checks.map((check) =>
      check.phase === "readiness" && check.kind === "http"
        ? { ...check, config: { ...(check.config ?? {}), port: undefined } }
        : check,
    ),
    domains:
      form.publish && form.hostname.trim()
        ? [
            {
              hostname: form.hostname.trim().toLowerCase(),
              https: form.https,
              ownership: "managed",
            },
          ]
        : [],
  }
}

/**
 * The repository chooser.
 *
 * gh is already signed in on most installs — it is how the git page pushes —
 * and that credential is the dashboard's own, so it is the one every
 * deployment clone will use. Listing through it means the operator picks a
 * project they recognise instead of finding a clone URL and typing it, and
 * private repositories appear on the same footing as public ones.
 */
function GitHubSource({
  busy,
  onChoose,
}: {
  busy: boolean
  onChoose: (source: DeploymentDraftSource, name: string) => Promise<void>
}) {
  const status = useGitHubAccount()
  const signedIn = Boolean(status.data?.available && status.data.account?.loggedIn)
  const repos = usePoll(
    (signal) => get<GitHubRepoSummary[]>("/git/github/repos", undefined, signal),
    0,
    [signedIn],
    { enabled: signedIn },
  )
  const [filter, setFilter] = useState("")
  const [selected, setSelected] = useState<GitHubRepoSummary>()
  const [branches, setBranches] = useState<GitHubBranch[]>()
  const [ref, setRef] = useState("")
  const [manualUrl, setManualUrl] = useState("")
  const [manualRef, setManualRef] = useState("main")
  const [credentialId, setCredentialId] = useState(0)
  const [owner, setOwner] = useState("all")

  const branchRequest = useRef(0)
  const choose = async (repo: GitHubRepoSummary) => {
    const request = ++branchRequest.current
    setSelected(repo)
    setRef(repo.defaultBranch ?? "main")
    setBranches(undefined)
    try {
      const result = await get<GitHubBranch[]>(
        `/git/github/branches?repo=${encodeURIComponent(repo.nameWithOwner)}`,
      )
      if (request === branchRequest.current) setBranches(result)
    } catch {
      // A repository whose branches cannot be listed is still deployable: the
      // default branch is already known and the field stays typeable.
      if (request === branchRequest.current) setBranches([])
    }
  }

  const needle = filter.trim().toLowerCase()
  const visible = (repos.data ?? []).filter(
    (repo) =>
      (owner === "all" || repo.nameWithOwner.split("/")[0] === owner) &&
      (!needle ||
        repo.nameWithOwner.toLowerCase().includes(needle) ||
        (repo.description ?? "").toLowerCase().includes(needle)),
  )

  return (
    <Panel>
      <PanelHeader
        title="Import Git repository"
        actions={<GitHubAccountControl status={status} compact />}
      />
      <PanelBody className="space-y-4">
        <div className="space-y-3 border-b border-hairline pb-5">
          <div className="mt-3 grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto]">
            <QuickField id="manual-url" label="Clone URL" hint="Any HTTPS or SSH Git URL.">
              <Input
                id="manual-url"
                value={manualUrl}
                onChange={(event) => setManualUrl(event.target.value)}
                placeholder="https://github.com/owner/repository.git"
                className="font-mono"
              />
            </QuickField>
            <div className="flex items-start sm:pt-6">
              <Button
                variant="outline"
                pending={busy}
                disabled={!manualUrl.trim()}
                onClick={() =>
                  void onChoose(
                    {
                      kind: "git",
                      mode: "git_url",
                      url: manualUrl.trim(),
                      ref: manualRef.trim() || "main",
                      credentialId: credentialId || undefined,
                    },
                    deploymentName(repositoryName(manualUrl)),
                  )
                }
              >
                Import
              </Button>
            </div>
          </div>
          <details>
            <summary className="cursor-pointer rounded-sm py-2 text-xs text-muted-foreground focus-ring">
              Branch & authentication
            </summary>
            <div className="grid gap-3 pt-2 sm:grid-cols-2">
              <QuickField id="manual-ref" label="Branch or tag">
                <Input
                  id="manual-ref"
                  value={manualRef}
                  onChange={(event) => setManualRef(event.target.value)}
                />
              </QuickField>
              <QuickField
                id="manual-credential"
                label="Saved credential ID"
                hint="Optional. Uses the server's Git credentials by default."
              >
                <Input
                  id="manual-credential"
                  type="number"
                  min={0}
                  value={credentialId || ""}
                  onChange={(event) => setCredentialId(Number(event.target.value))}
                />
              </QuickField>
            </div>
          </details>
        </div>
        {status.error && <ErrorState error={status.error} />}
        {repos.error && <ErrorState error={repos.error} />}
        {!signedIn && status.data && (
          <Notice
            tone="warning"
            icon={Warning}
            title={
              status.data.available
                ? "Not signed in to GitHub on this server"
                : "The GitHub CLI is not installed on this host"
            }
          >
            {status.data.available ? (
              <>
                Sign in once on the{" "}
                <Link href="/git" className="underline underline-offset-2">
                  Git page
                </Link>{" "}
                and every private repository becomes selectable here.
              </>
            ) : (
              "Install gh to browse repositories. A public clone URL works without it."
            )}
          </Notice>
        )}
        {signedIn && (
          <>
            <div className="flex flex-wrap items-center gap-2">
              <Select value={owner} onValueChange={setOwner}>
                <SelectTrigger aria-label="Repository owner" className="w-40">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">All repositories</SelectItem>
                  {[
                    ...new Set((repos.data ?? []).map((repo) => repo.nameWithOwner.split("/")[0])),
                  ].map((name) => (
                    <SelectItem key={name} value={name}>
                      {name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label="Refresh repositories"
                onClick={repos.refresh}
              >
                <RefreshClockwise className="size-3.5" />
              </Button>
            </div>
            <SearchInput
              value={filter}
              onChange={(event) => setFilter(event.target.value)}
              placeholder="Filter repositories"
              aria-label="Filter repositories"
              containerClassName="sm:w-full"
            />
            {repos.loading && <LoadingRows rows={5} />}
            {repos.data && visible.length === 0 && (
              <EmptyNote>
                {repos.data.length === 0
                  ? "This account has no repositories the credential can list."
                  : "No repository matches that filter."}
              </EmptyNote>
            )}
            <ul className="max-h-[32rem] divide-y divide-hairline overflow-y-auto">
              {visible.map((repo) => (
                <li key={repo.nameWithOwner}>
                  <ChoiceCard
                    selected={selected?.nameWithOwner === repo.nameWithOwner}
                    onClick={() => void choose(repo)}
                    className="min-h-20 w-full flex-row items-center gap-3 rounded-none border-0"
                  >
                    <span className="min-w-0 flex-1">
                      <ChoiceCardTitle className="block truncate">
                        {repo.nameWithOwner}
                      </ChoiceCardTitle>
                      {repo.description && (
                        <ChoiceCardHint className="block truncate">
                          {repo.description}
                        </ChoiceCardHint>
                      )}
                    </span>
                    {repo.language && <Tag>{repo.language}</Tag>}
                    {repo.private && (
                      <LockClosed
                        className="size-3.5 shrink-0 text-muted-foreground"
                        aria-label="Private repository"
                      />
                    )}
                    <ArrowRight className="size-3.5 shrink-0 text-muted-foreground" />
                  </ChoiceCard>
                </li>
              ))}
            </ul>
          </>
        )}
        {selected && (
          <div className="grid gap-3 rounded-xl border border-hairline bg-surface-header p-3 sm:grid-cols-2">
            <QuickField id="branch" label="Branch">
              {branches && branches.length > 0 ? (
                <Select value={ref} onValueChange={setRef}>
                  <SelectTrigger id="branch" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {branches.map((branch) => (
                      <SelectItem key={branch.name} value={branch.name}>
                        {branch.name}
                        {branch.default ? " (default)" : ""}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ) : (
                <Input
                  id="branch"
                  value={ref}
                  onChange={(event) => setRef(event.target.value)}
                  className="font-mono"
                />
              )}
            </QuickField>
            <div className="flex items-end">
              <Button
                className="h-11 w-full sm:h-9"
                pending={busy}
                onClick={() =>
                  void onChoose(
                    { kind: "git", mode: "git_url", url: selected.cloneUrl, ref: ref || "main" },
                    deploymentName(selected.name),
                  )
                }
              >
                <ArrowRight className="size-4" />
                Continue
              </Button>
            </div>
          </div>
        )}
      </PanelBody>
    </Panel>
  )
}

/** The last path segment of a clone URL, which is the project's name. */
function repositoryName(url: string) {
  const trimmed = url
    .trim()
    .replace(/\.git$/, "")
    .replace(/\/+$/, "")
  const last = trimmed.split(/[/:]/).pop() ?? ""
  return last || "app"
}

/**
 * The image chooser.
 *
 * Deploying an image used to mean typing a reference from memory and finding
 * out at inspection whether it was the one meant. Every image already pulled
 * onto this server has a name the dashboard can read, so those are listed —
 * and the free-text field stays for anything in a registry that is not here
 * yet.
 */
function ImageSource({
  busy,
  onChoose,
}: {
  busy: boolean
  onChoose: (source: DeploymentDraftSource, name: string) => Promise<void>
}) {
  const images = usePoll((signal) => get<DockerImage[]>("/docker/images", undefined, signal), 0)
  const [filter, setFilter] = useState("")
  const [reference, setReference] = useState("")

  const needle = filter.trim().toLowerCase()
  const tags = (images.data ?? [])
    .flatMap((image) => image.repoTags.map((tag) => ({ tag, size: image.size })))
    .filter(
      ({ tag }) =>
        tag && tag !== "<none>:<none>" && (!needle || tag.toLowerCase().includes(needle)),
    )
    .sort((a, b) => a.tag.localeCompare(b.tag))

  return (
    <Panel>
      <PanelHeader title="Choose an image" />
      <PanelBody className="space-y-4">
        {images.error && <ErrorState error={images.error} />}
        <SearchInput
          value={filter}
          onChange={(event) => setFilter(event.target.value)}
          placeholder="Filter images on this server"
          aria-label="Filter images"
          containerClassName="sm:w-full"
        />
        {images.loading && <LoadingRows rows={4} />}
        {images.data && tags.length === 0 && (
          <EmptyNote>
            {images.data.length === 0
              ? "This server has no images pulled yet. Name one in a registry below."
              : "No image on this server matches that filter."}
          </EmptyNote>
        )}
        <ul className="max-h-80 space-y-2 overflow-y-auto">
          {tags.map(({ tag, size }) => (
            <li key={tag}>
              <ChoiceCard
                selected={reference === tag}
                onClick={() => setReference(tag)}
                className="min-h-0 w-full flex-row items-center gap-3"
              >
                <span className="min-w-0 flex-1 truncate font-mono text-xs">{tag}</span>
                <span className="numeric shrink-0 text-micro text-muted-foreground">
                  {formatSize(size)}
                </span>
              </ChoiceCard>
            </li>
          ))}
        </ul>
        <div className="grid gap-3 border-t border-hairline pt-4 sm:grid-cols-[minmax(0,1fr)_auto]">
          <QuickField
            id="image-reference"
            label="Image reference"
            hint="A tag is resolved to an immutable digest during inspection."
          >
            <Input
              id="image-reference"
              value={reference}
              onChange={(event) => setReference(event.target.value)}
              placeholder="ghcr.io/owner/app:tag"
              className="font-mono"
            />
          </QuickField>
          <div className="flex items-end">
            <Button
              className="h-11 sm:h-9"
              pending={busy}
              disabled={!reference.trim()}
              onClick={() =>
                void onChoose(
                  { kind: "image", mode: "image_reference", image: reference.trim() },
                  imageName(reference),
                )
              }
            >
              <ArrowRight className="size-4" />
              Continue
            </Button>
          </div>
        </div>
      </PanelBody>
    </Panel>
  )
}

/** The repository half of an image reference, which is what to call it. */
function imageName(reference: string) {
  const withoutTag = reference
    .trim()
    .split("@")[0]
    .replace(/:[^:/]+$/, "")
  const last = withoutTag.split("/").pop() ?? ""
  return last.replace(/[^A-Za-z0-9._-]/g, "-") || "app"
}

function formatSize(bytes: number) {
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KiB`
  if (bytes < 1024 * 1024 * 1024) return `${Math.round(bytes / 1024 / 1024)} MiB`
  return `${(bytes / 1024 / 1024 / 1024).toFixed(1)} GiB`
}

function ConfigureStep({
  lane,
  form,
  onChange,
  candidate,
  suggestion,
  detectionUnavailable,
}: {
  lane: "github" | "image"
  form: ApplicationForm
  onChange: (next: ApplicationForm) => void
  candidate?: DeploymentDetectionCandidate
  detectionUnavailable?: string
  suggestion?: DeploymentHostnameSuggestion
}) {
  const [nameTouched, setNameTouched] = useState(false)
  const nameInvalid = nameTouched && !DEPLOYMENT_NAME.test(form.name)
  const set = <K extends keyof ApplicationForm>(key: K, value: ApplicationForm[K]) =>
    onChange({ ...form, [key]: value })

  return (
    <div className="space-y-4">
      {lane === "github" && (
        <p className="text-sm text-muted-foreground">
          New commits to the selected branch deploy automatically after your first deployment.
        </p>
      )}
      <Panel>
        <PanelHeader
          title={form.name || "New deployment"}
          actions={candidate?.framework && <Tag tone="success">{candidate.framework}</Tag>}
        />
        <PanelBody className="grid gap-4 sm:grid-cols-2">
          <QuickField
            id="deployment-name"
            label="Name"
            hint="Used in URLs, container labels and release history."
          >
            <Input
              id="deployment-name"
              required
              maxLength={64}
              autoComplete="off"
              aria-invalid={nameInvalid}
              aria-describedby={nameInvalid ? "deployment-name-error" : undefined}
              onBlur={() => setNameTouched(true)}
              value={form.name}
              onChange={(event) => set("name", event.target.value)}
            />
            {nameInvalid && (
              <p id="deployment-name-error" className="text-xs text-destructive">
                Start with a letter or number. Use up to 64 letters, numbers, dots, dashes, or
                underscores.
              </p>
            )}
          </QuickField>
          <QuickField
            id="internal-port"
            label="Port the app listens on"
            hint="What the container serves on, not the host port."
          >
            <Input
              id="internal-port"
              type="number"
              min={0}
              max={65535}
              value={form.internalPort}
              onChange={(event) => set("internalPort", Number(event.target.value) || 0)}
              className="font-mono"
            />
          </QuickField>
          {lane === "github" && (
            <QuickField id="workload-type" label="Project type">
              <Select
                value={form.profile}
                onValueChange={(value) =>
                  onChange({
                    ...form,
                    profile: value as WorkloadProfile,
                    publish: value !== "worker" && form.publish,
                    startCommand:
                      value === "static" && form.buildMethod === "recipe"
                        ? ""
                        : form.startCommand || candidate?.startCommand || "",
                    internalPort:
                      value === "worker"
                        ? 0
                        : value === "static" && form.buildMethod !== "dockerfile"
                          ? 80
                          : form.internalPort || 3000,
                  })
                }
              >
                <SelectTrigger id="workload-type" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="web">Web application</SelectItem>
                  <SelectItem value="static">Static website</SelectItem>
                  <SelectItem value="worker">Worker or bot</SelectItem>
                  {!["web", "static", "worker"].includes(form.profile) && (
                    <SelectItem value={form.profile}>{form.profile}</SelectItem>
                  )}
                </SelectContent>
              </Select>
            </QuickField>
          )}
          {lane === "github" && (
            <details
              className="min-w-0 sm:col-span-2"
              open={
                !candidate ||
                Boolean(detectionUnavailable) ||
                (form.profile === "static" &&
                  form.buildMethod === "recipe" &&
                  !form.outputDirectory)
              }
            >
              <summary className="cursor-pointer rounded-md py-2 text-body font-medium focus-ring">
                Build settings{candidate?.framework ? ` · ${candidate.framework}` : ""}
              </summary>
              <div className="grid gap-4 pt-3 sm:grid-cols-2">
                <QuickField id="quick-build-method" label="Build method">
                  <Select
                    value={form.buildMethod}
                    onValueChange={(value) => set("buildMethod", value as DeploymentBuildMethod)}
                  >
                    <SelectTrigger id="quick-build-method" className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="recipe">Automatic recipe</SelectItem>
                      <SelectItem value="dockerfile">Dockerfile</SelectItem>
                      <SelectItem value="static">Static files</SelectItem>
                      {!["recipe", "dockerfile", "static"].includes(form.buildMethod) && (
                        <SelectItem value={form.buildMethod}>{form.buildMethod}</SelectItem>
                      )}
                    </SelectContent>
                  </Select>
                </QuickField>
                {form.buildMethod === "recipe" && (
                  <QuickField id="quick-recipe" label="Language">
                    <Select
                      value={form.recipe}
                      onValueChange={(value) => set("recipe", value as ApplicationForm["recipe"])}
                    >
                      <SelectTrigger id="quick-recipe" className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="node">JavaScript / TypeScript</SelectItem>
                        <SelectItem value="go">Go</SelectItem>
                        <SelectItem value="python">Python</SelectItem>
                      </SelectContent>
                    </Select>
                  </QuickField>
                )}
                {form.buildMethod === "dockerfile" && (
                  <QuickField
                    id="quick-dockerfile"
                    label="Dockerfile path"
                    hint="Use a Dockerfile for any language or custom build."
                  >
                    <Input
                      id="quick-dockerfile"
                      className="font-mono"
                      value={form.dockerfile}
                      onChange={(event) => set("dockerfile", event.target.value)}
                    />
                  </QuickField>
                )}

                {form.buildMethod !== "dockerfile" && (
                  <>
                    <QuickField
                      id="build-command"
                      label="Build command"
                      hint="Detected from the package manager this repository locks to."
                    >
                      <Input
                        id="build-command"
                        value={form.buildCommand}
                        onChange={(event) => set("buildCommand", event.target.value)}
                        placeholder="npm run build"
                        className="font-mono"
                      />
                    </QuickField>
                    <QuickField
                      id="start-command"
                      label="Start command"
                      hint="Leave empty and set an output directory to serve static files instead."
                    >
                      <Input
                        id="start-command"
                        value={form.startCommand}
                        onChange={(event) => set("startCommand", event.target.value)}
                        placeholder="npm run start"
                        className="font-mono"
                      />
                    </QuickField>
                  </>
                )}
                <QuickField
                  id="root-directory"
                  label="Root directory"
                  hint="For a monorepo. Relative to the repository root."
                >
                  <Input
                    id="root-directory"
                    value={form.rootDirectory}
                    onChange={(event) => set("rootDirectory", event.target.value)}
                    placeholder="apps/web"
                    className="font-mono"
                  />
                </QuickField>
                <QuickField
                  id="output-directory"
                  label="Static output directory"
                  hint="Set only for a site with no server process."
                >
                  <Input
                    id="output-directory"
                    value={form.outputDirectory}
                    onChange={(event) => set("outputDirectory", event.target.value)}
                    placeholder="dist"
                    className="font-mono"
                  />
                </QuickField>
              </div>
            </details>
          )}
        </PanelBody>
      </Panel>

      <Panel>
        <PanelHeader title="Environment variables" actions={<Tag>Optional</Tag>} />
        <PanelBody className="space-y-3">
          {form.variables.map((entry, index) => (
            <div
              key={index}
              className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.5fr)_auto] items-end gap-2"
            >
              <QuickField id={`env-key-${index}`} label="Key">
                <Input
                  id={`env-key-${index}`}
                  value={entry.name}
                  autoComplete="off"
                  spellCheck={false}
                  placeholder="DATABASE_URL"
                  className="font-mono"
                  onChange={(event) =>
                    set(
                      "variables",
                      form.variables.map((item, i) =>
                        i === index ? { ...item, name: event.target.value } : item,
                      ),
                    )
                  }
                />
              </QuickField>
              <QuickField id={`env-value-${index}`} label="Value">
                <Input
                  id={`env-value-${index}`}
                  type="password"
                  autoComplete="new-password"
                  value={entry.value}
                  placeholder="Enter a value"
                  className="font-mono"
                  onChange={(event) =>
                    set(
                      "variables",
                      form.variables.map((item, i) =>
                        i === index ? { ...item, value: event.target.value } : item,
                      ),
                    )
                  }
                />
              </QuickField>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={`Remove variable ${entry.name || index + 1}`}
                onClick={() =>
                  set(
                    "variables",
                    form.variables.filter((_, i) => i !== index),
                  )
                }
              >
                <Trash className="size-3.5" />
              </Button>
            </div>
          ))}
          <Button
            variant="outline"
            size="sm"
            onClick={() => set("variables", [...form.variables, { name: "", value: "" }])}
          >
            <Plus className="size-3.5" /> Add variable
          </Button>
          <details>
            <summary className="cursor-pointer rounded-sm py-2 text-xs focus-ring">
              Import .env
            </summary>
            <Textarea
              id="dotenv"
              aria-label="Environment variables"
              value={form.dotenv}
              onChange={(event) => set("dotenv", event.target.value)}
              rows={4}
              placeholder={"API_KEY=…\nNEXT_PUBLIC_SITE_URL=https://…"}
              className="mt-2 font-mono text-xs"
            />
          </details>
          <p className="text-xs text-muted-foreground">
            Encrypted when saved. Available during build and at runtime.
          </p>
        </PanelBody>
      </Panel>
      <ProjectDatabase
        onConnect={(connection, url, variable) =>
          onChange({
            ...form,
            variables: [
              ...form.variables.filter((entry) => entry.name && entry.name !== variable),
              { name: variable, value: url },
            ],
            databaseIds: [...new Set([...form.databaseIds, connection.id])],
          })
        }
      />

      {form.profile !== "worker" && (
        <PublicAddress form={form} onChange={onChange} suggestion={suggestion} />
      )}
    </div>
  )
}

/**
 * The address the deployment answers on, and the certificate it needs to do it
 * over HTTPS.
 *
 * Activation resolves an existing certificate or it refuses the cutover, so a
 * name with no certificate is a release that fails at the last step. Rather
 * than let that happen and explain it afterwards, the missing certificate is
 * offered here as one button: certbot already runs on most of these hosts, the
 * generated name already resolves here, and the only thing nobody can guess is
 * the address expiry warnings should go to.
 */
function PublicAddress({
  form,
  onChange,
  suggestion,
}: {
  form: ApplicationForm
  onChange: (next: ApplicationForm) => void
  suggestion?: DeploymentHostnameSuggestion
}) {
  const set = <K extends keyof ApplicationForm>(key: K, value: ApplicationForm[K]) =>
    onChange({ ...form, [key]: value })
  const [state, setState] = useState<DeploymentHostnameSuggestion | undefined>(suggestion)
  const [checking, setChecking] = useState(false)

  const current = state ?? suggestion
  const hostname = form.hostname.trim().toLowerCase()
  const matches = current?.hostname.toLowerCase() === hostname

  const recheck = async () => {
    if (!hostname) return
    setChecking(true)
    try {
      setState(
        await get<DeploymentHostnameSuggestion>(
          `/deploy/hostname?hostname=${encodeURIComponent(hostname)}`,
        ),
      )
    } catch {
      // Leaving the last answer on screen is better than blanking the panel:
      // it was true a moment ago, and the run itself is what settles this.
    } finally {
      setChecking(false)
    }
  }

  return (
    <Panel>
      <PanelHeader title="Public address" />
      <PanelBody className="space-y-4">
        <Label className="flex min-h-11 items-center gap-3 text-xs">
          <Switch checked={form.publish} onCheckedChange={(publish) => set("publish", publish)} />
          Publish this deployment on a public hostname
        </Label>
        {form.publish && (
          <div className="grid gap-4 sm:grid-cols-[minmax(0,1fr)_auto]">
            <QuickField
              id="hostname"
              label="Hostname"
              hint={
                matches
                  ? current?.detail
                  : "Point a record at this server. The deploy checks it and gets the certificate."
              }
            >
              <Input
                id="hostname"
                value={form.hostname}
                onChange={(event) => set("hostname", event.target.value)}
                onBlur={() => void recheck()}
                placeholder="app.example.com"
                className="font-mono"
              />
            </QuickField>
            <div className="flex items-end gap-3 pb-1">
              <Label className="flex min-h-11 items-center gap-2 text-xs">
                <Switch checked={form.https} onCheckedChange={(https) => set("https", https)} />
                HTTPS
              </Label>
              <Button
                size="sm"
                variant="outline"
                pending={checking}
                onClick={() => void recheck()}
                title="Check this name again"
              >
                <RefreshClockwise className="size-3.5" />
                Re-check
              </Button>
            </div>
          </div>
        )}
        {form.publish && form.https && matches && current?.covered && (
          <Notice tone="success" icon={CheckCircle} title="HTTPS is ready for this name">
            The <code className="font-mono">{current.certificateName}</code> certificate already
            covers it, so the deploy reuses it rather than ordering another.
          </Notice>
        )}
        {form.publish && form.https && matches && current && !current.covered && (
          <Notice
            tone={current.certificateMethod ? "default" : "warning"}
            icon={current.certificateMethod ? LockClosed : Warning}
            title={
              current.certificateMethod
                ? "A certificate will be issued during the deploy"
                : "Automatic HTTPS needs attention"
            }
          >
            {current.certificateMethod ? (
              <>
                The run orders one for <code className="font-mono">{form.hostname}</code> over the{" "}
                {current.certificateMethod === "caddy"
                  ? "managed Caddy ingress"
                  : `${current.certificateMethod} challenge`}{" "}
                before it starts anything.{" "}
                {current.certificateMethod === "caddy" ? "Caddy" : "Certbot"} handles renewal
                automatically.
              </>
            ) : (
              <>
                {current.certificateIssue ?? "Certificate readiness could not be confirmed."} Review
                certificate options on the{" "}
                <Link href="/proxy/certificates" className="underline underline-offset-2">
                  Certificates page
                </Link>
                .
              </>
            )}
          </Notice>
        )}
        {current?.method === "none" && (
          <Notice icon={Warning} title="This server has no public address">
            A hostname cannot be generated. The deployment still runs; reach it through the port it
            publishes, or set a domain that resolves here.
          </Notice>
        )}
      </PanelBody>
    </Panel>
  )
}

export function QuickField({
  id,
  label,
  hint,
  className,
  children,
}: {
  id: string
  label: string
  hint?: string
  className?: string
  children: React.ReactNode
}) {
  return (
    <div className={cn("space-y-1.5", className)}>
      <Label htmlFor={id}>{label}</Label>
      {children}
      {hint && <p className="text-hint leading-relaxed text-muted-foreground">{hint}</p>}
    </div>
  )
}

/**
 * A value whose only purpose is to be pasted somewhere else.
 *
 * A connection string is useless in a panel you can only read it from: the
 * next thing that happens to it is always a paste into another deployment's
 * DATABASE_URL. The tick rather than a toast, because the operator's eye is
 * already on the string they are about to paste.
 */
export function CopyValue({ value, label }: { value: string; label: string }) {
  const { copy, copied } = useCopy()
  return (
    <div className="flex min-w-0 items-center gap-2 rounded-lg border border-hairline bg-surface-header p-2">
      <code className="min-w-0 flex-1 overflow-x-auto font-mono text-xs whitespace-nowrap">
        {value}
      </code>
      <Button
        size="xs"
        variant="outline"
        onClick={() => void copy(value, label)}
        aria-label={`Copy ${label}`}
      >
        {copied ? <CheckCircle className="size-3" /> : <Clipboard className="size-3" />}
        {copied ? "Copied" : "Copy"}
      </Button>
    </div>
  )
}
