"use client"

import { useCallback, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import {
  ArrowRight,
  Box,
  CheckCircle,
  Clipboard,
  CloudUpload,
  Database,
  GitBranch,
  Globe,
  LockClosed,
  RefreshClockwise,
  SettingsSliders,
  Warning,
} from "@/components/icons"
import { get, post, put } from "@/lib/api"
import { cn } from "@/lib/utils"
import type {
  DeploymentConfiguration,
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

/**
 * The short way to put something online.
 *
 * The wizard this sits beside asks five screens of questions because it can
 * describe every workload this dashboard supports. That generality is the
 * problem for the case that matters most: a repository that serves HTTP. An
 * operator who has one of those was being asked to pick an outcome from eight
 * cards, then a source mode from a list, then to type a clone URL, a branch, a
 * credential id and a build method — before reaching a preflight that could
 * still refuse the plan over a control they were never shown.
 *
 * So this screen asks the three questions that actually vary — what to deploy,
 * what to call it, and what the environment needs — and derives the rest:
 * detection fills in the framework's commands, the public hostname is
 * generated from a name that already resolves here, and preflight's findings
 * are rendered where the decision is made rather than on a screen after it.
 *
 * Everything else — Compose stacks, blueprints, game servers, adopting an
 * existing container — stays in the wizard, one link away.
 */

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
  const [lane, setLane] = useState<Lane>()

  if (lane === "database")
    return (
      <QuickPage onBack={() => setLane(undefined)}>
        <DatabaseQuickDeploy />
      </QuickPage>
    )
  if (lane === "github" || lane === "image")
    return (
      <QuickPage onBack={() => setLane(undefined)}>
        <ApplicationFlow lane={lane} />
      </QuickPage>
    )

  return (
    <QuickPage>
      <Panel>
        <PanelHeader
          icon={CloudUpload}
          title="What are you putting online?"
        />
        <PanelBody className="grid gap-3 sm:grid-cols-3">
          {LANES.map((option) => (
            <ChoiceCard
              key={option.lane}
              onClick={() => setLane(option.lane)}
              className="min-h-32 justify-start"
            >
              <span className="flex size-8 items-center justify-center rounded-lg bg-muted text-muted-foreground">
                <option.icon className="size-4" />
              </span>
              <ChoiceCardTitle>{option.title}</ChoiceCardTitle>
              <ChoiceCardHint>{option.description}</ChoiceCardHint>
            </ChoiceCard>
          ))}
        </PanelBody>
        <PanelFooter className="justify-between">
          <p className="text-hint text-muted-foreground">
            Compose stacks, blueprints, game servers and adopting an existing container live in the
            full wizard.
          </p>
          <Button variant="outline" size="sm" asChild>
            <Link href="/deploy/new?mode=advanced">
              <SettingsSliders className="size-3.5" />
              Advanced wizard
            </Link>
          </Button>
        </PanelFooter>
      </Panel>
    </QuickPage>
  )
}

function QuickPage({ children, onBack }: { children: React.ReactNode; onBack?: () => void }) {
  return (
    <Page className="max-w-[1100px]">
      <PageHeader
        eyebrow="Deployments"
        title="Deploy something"
        actions={
          onBack ? (
            <Button variant="outline" size="sm" onClick={onBack}>
              Start over
            </Button>
          ) : (
            <Button variant="outline" size="sm" asChild>
              <Link href="/deploy">Exit to fleet</Link>
            </Button>
          )
        }
      />
      {children}
    </Page>
  )
}

type ApplicationForm = {
  name: string
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

function ApplicationFlow({ lane }: { lane: "github" | "image" }) {
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

  const profile: WorkloadProfile = lane === "image" ? "image" : "web"

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
        const defaults = defaultConfiguration(profile, selected, chosen, detection)
        setSource(chosen)
        setDraft(detected)
        setCandidate(selected)
        setForm({
          name,
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
                https: hostname.covered || Boolean(hostname.certificateMethod),
                publish: hostname.method !== "none",
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

  const deploy = async () => {
    if (!draft || !form || !source) return
    if (!DEPLOYMENT_NAME.test(form.name)) {
      setFailure(new Error("Use 1–64 letters, numbers, dots, dashes, or underscores for the name."))
      return
    }
    setBusy("deploy")
    setFailure(undefined)
    try {
      let current = draft
      // The name is editable on the configure screen, so it can differ from the
      // one the draft was opened with. Saving the intent again only replaces
      // that field — the source and detection it already holds are untouched.
      if (form.name !== current.data.intent?.name) {
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

      let revision = committed.planRevision
      if (form.dotenv.trim()) {
        const imported = await post<{ desiredRevision: number }>(
          `/deploy/${committed.projectId}/environments/${committed.environmentId}/variables/import`,
          {
            revision,
            dotenv: form.dotenv,
            sensitivity: "secret",
            scopes: ["runtime", "build"],
          },
        )
        revision = imported.desiredRevision
      }

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

  if (!form || !draft)
    return (
      <div className="space-y-4">
        {failure && <ErrorState error={failure} />}
        {lane === "github" ? (
          <GitHubSource busy={busy === "inspect"} onChoose={inspect} />
        ) : (
          <ImageSource busy={busy === "inspect"} onChoose={inspect} />
        )}
      </div>
    )

  const blockers = preflight ? blockingFindings(preflight.findings) : []
  const warnings = preflight ? warningFindings(preflight.findings) : []
  const outstanding = warnings.filter((finding) => !acknowledged.includes(finding.code))

  return (
    <div className="space-y-4">
      {failure && <ErrorState error={failure} />}
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
            icon={Warning}
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
        <div className="flex items-center gap-2">
          <Button variant="outline" asChild>
            <Link href={`/deploy/new?mode=advanced&draft=${encodeURIComponent(draft.id)}`}>
              <SettingsSliders className="size-4" />
              Open in full wizard
            </Link>
          </Button>
          <Button className="h-11 sm:h-9" onClick={() => void deploy()} pending={busy === "deploy"}>
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
    build: {
      ...defaults.build,
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

  const choose = async (repo: GitHubRepoSummary) => {
    setSelected(repo)
    setRef(repo.defaultBranch ?? "main")
    setBranches(undefined)
    try {
      setBranches(
        await get<GitHubBranch[]>(
          `/git/github/branches?repo=${encodeURIComponent(repo.nameWithOwner)}`,
        ),
      )
    } catch {
      // A repository whose branches cannot be listed is still deployable: the
      // default branch is already known and the field stays typeable.
      setBranches([])
    }
  }

  const needle = filter.trim().toLowerCase()
  const visible = (repos.data ?? []).filter(
    (repo) =>
      !needle ||
      repo.nameWithOwner.toLowerCase().includes(needle) ||
      (repo.description ?? "").toLowerCase().includes(needle),
  )

  return (
    <Panel>
      <PanelHeader
        icon={GitBranch}
        title="Choose a repository"
        actions={
          signedIn && (
            <>
              {/* Which account these repositories came from. It was the panel's
                  description, and it is the one fact a reader actually needs
                  here — you cannot tell a private repo list apart from the
                  wrong account's private repo list without it. */}
              <span className="text-hint text-muted-foreground">
                Signed in as {status.data?.account?.login ?? "your account"}
              </span>
              <Button variant="ghost" size="icon-sm" onClick={repos.refresh} title="Refresh">
                <RefreshClockwise className="size-3.5" />
              </Button>
            </>
          )
        }
      />
      <PanelBody className="space-y-4">
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
            <ul className="max-h-96 space-y-2 overflow-y-auto">
              {visible.map((repo) => (
                <li key={repo.nameWithOwner}>
                  <ChoiceCard
                    selected={selected?.nameWithOwner === repo.nameWithOwner}
                    onClick={() => void choose(repo)}
                    className="min-h-0 w-full flex-row items-center gap-3"
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
                    {repo.private && <Tag>private</Tag>}
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
        <details className="border-t border-hairline pt-3">
          <summary className="flex min-h-9 cursor-pointer items-center text-xs font-medium">
            Deploy a repository that is not in this list
          </summary>
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
            <div className="flex items-end">
              <Button
                variant="outline"
                pending={busy}
                disabled={!manualUrl.trim()}
                onClick={() =>
                  void onChoose(
                    { kind: "git", mode: "git_url", url: manualUrl.trim(), ref: "main" },
                    repositoryName(manualUrl),
                  )
                }
              >
                Continue
              </Button>
            </div>
          </div>
        </details>
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
      <PanelHeader
        icon={Box}
        title="Choose an image"
      />
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
}: {
  lane: "github" | "image"
  form: ApplicationForm
  onChange: (next: ApplicationForm) => void
  candidate?: DeploymentDetectionCandidate
  detectionUnavailable?: string
  suggestion?: DeploymentHostnameSuggestion
}) {
  const set = <K extends keyof ApplicationForm>(key: K, value: ApplicationForm[K]) =>
    onChange({ ...form, [key]: value })

  return (
    <div className="space-y-4">
      <Panel>
        <PanelHeader
          icon={CloudUpload}
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
              value={form.name}
              onChange={(event) => set("name", event.target.value)}
            />
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
            </>
          )}
        </PanelBody>
      </Panel>

      <Panel>
        <PanelHeader
          icon={LockClosed}
          title="Environment variables"
        />
        <PanelBody>
          <Textarea
            id="dotenv"
            aria-label="Environment variables"
            value={form.dotenv}
            onChange={(event) => set("dotenv", event.target.value)}
            rows={form.dotenv ? Math.min(14, form.dotenv.split("\n").length + 2) : 4}
            placeholder={"DATABASE_URL=postgres://…\nNEXT_PUBLIC_SITE_URL=https://…"}
            className="font-mono text-xs"
          />
          <p className="mt-2 text-hint leading-relaxed text-muted-foreground">
            Each variable is available to the build and to the running container, which is what a
            framework that inlines values at build time needs.
          </p>
        </PanelBody>
      </Panel>

      <PublicAddress form={form} onChange={onChange} suggestion={suggestion} />
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
      <PanelHeader
        icon={Globe}
        title="Public address"
      />
      <PanelBody className="space-y-4">
        <Label className="flex min-h-11 items-center gap-3 text-xs">
          <Switch
            checked={form.publish}
            onCheckedChange={(publish) => set("publish", publish)}
            disabled={current?.method === "none"}
          />
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
                : "This host cannot issue a certificate"
            }
          >
            {current.certificateMethod ? (
              <>
                The run orders one for <code className="font-mono">{form.hostname}</code> over the{" "}
                {current.certificateMethod} challenge before it starts anything, and certbot renews
                it from then on. Nothing to do here.
              </>
            ) : (
              <>
                Install certbot, or issue one elsewhere and import it on the{" "}
                <Link href="/proxy/certificates" className="underline underline-offset-2">
                  Certificates page
                </Link>
                . Turning HTTPS off publishes over plain HTTP in the meantime.
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
