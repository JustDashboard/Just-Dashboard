"use client"

import { useState } from "react"
import { External, GitHubMark, LockClosed, RefreshClockwise, Terminal } from "@/components/icons"
import { get } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { githubAppStage, useGitHubAccount, useGitHubApp } from "@/hooks/use-github"
import { useSessionState } from "@/lib/view-state"
import { CredentialSelect } from "@/components/deploy/credentials-page"
import type { DeploymentDraftSource, GitHubAppRepository, GitHubRepoSummary } from "@/lib/types"
import { Field, FormNote, OptionList, OptionRow } from "@/components/form"
import { ChoiceList, ChoiceRow, FlowPanel, FlowPanelBody, FlowPanelHeader } from "@/components/flow"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { SearchInput } from "@/components/page"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { IconAction } from "@/components/icon-action"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import Link from "next/link"
import { deploymentName } from "@/components/deploy/vocabulary"
import {
  inspectAndPrepare,
  repositoryName,
  type ConfigureFlow,
} from "@/components/deploy/new-project/draft"

function asError(error: unknown) {
  return error instanceof Error ? error : new Error(String(error))
}

/**
 * Import a repository one of this dashboard's two GitHub identities can
 * reach, or paste any Git URL. Importing inspects immediately with the default
 * branch — the branch itself is changed on Configure, once a plan exists to
 * re-detect.
 */
/** Only a clone URL is prefilled from a link: anything else stays out of the field. */
function cloneURL(value?: string) {
  const trimmed = value?.trim() ?? ""
  return /^(https?:\/\/|ssh:\/\/|git@)[^\s]+$/.test(trimmed) ? trimmed : ""
}

/** A repository the picker can import, from either GitHub identity. */
type PickableRepo = {
  nameWithOwner: string
  name: string
  owner: string
  description?: string
  language?: string
  private: boolean
  fork?: boolean
  archived?: boolean
  pushedAt?: string
  htmlUrl?: string
  cloneUrl: string
  defaultBranch: string
  credentialId?: number
  viaApp: boolean
}

/**
 * Newest push first, and anything that never reported one last. The repository
 * somebody wants to deploy is almost always the one they touched this week,
 * and the list arrived in the order the API happened to answer in.
 */
function byRecency(a: PickableRepo, b: PickableRepo) {
  if (a.pushedAt && b.pushedAt) return b.pushedAt.localeCompare(a.pushedAt)
  if (a.pushedAt) return -1
  if (b.pushedAt) return 1
  return a.nameWithOwner.localeCompare(b.nameWithOwner)
}

export function SourceGit({
  onInspected,
  initialUrl,
  initialRef,
}: {
  onInspected: (flow: ConfigureFlow) => void
  initialUrl?: string
  initialRef?: string
}) {
  const status = useGitHubAccount()
  const signedIn = Boolean(status.data?.available && status.data.account?.loggedIn)
  const repos = usePoll(
    (signal) => get<GitHubRepoSummary[]>("/git/github/repos", undefined, signal),
    0,
    [signedIn],
    { enabled: signedIn },
  )
  // Repositories the dashboard's GitHub App is installed on: cloned with the
  // installation's own token, so nothing has to be signed in or pasted.
  const appRepos = usePoll(
    (signal) => get<GitHubAppRepository[]>("/deploy/github-app/repositories", undefined, signal),
    0,
  )
  // The App is *asked about*, not inferred from how many repositories came
  // back. An App installed on an account that has granted it nothing is
  // configured and correct, and counting rows reported it as absent — which
  // is how the page came to tell an operator to connect an App they had
  // already connected.
  const app = useGitHubApp()
  const stage = githubAppStage(app.data)
  // Every read is guarded: an unmocked path answers `[]` in the design-system
  // browser run, so `app.data` is sometimes an array with no fields at all.
  const installations = app.data?.installations ?? []
  // Every field is remembered for the tab, so a look at another page — the
  // credential this repository needs, say — never means finding it again.
  const [filter, setFilter] = useSessionState("deploy.new.git.filter", "")
  const [owner, setOwner] = useSessionState("deploy.new.git.owner", "all")
  // A deploy link sets the field, and a link that is not a clone URL clears
  // it: what arrives in the address bar is the whole answer, never a mix of
  // the link and what was typed last time.
  const linked = initialUrl !== undefined
  const [manualUrl, setManualUrl] = useSessionState(
    "deploy.new.git.url",
    "",
    linked ? cloneURL(initialUrl) : undefined,
  )
  const [manualRef, setManualRef] = useSessionState(
    "deploy.new.git.ref",
    "main",
    linked ? initialRef?.trim() || "main" : undefined,
  )
  const [credentialId, setCredentialId] = useSessionState<number | undefined>(
    "deploy.new.git.credential",
    undefined,
  )
  // Apply to whichever import fires — the picked-repo rows and the pasted
  // URL both become the same `DeploymentDraftSource` the fields already are.
  const [includeSubmodules, setIncludeSubmodules] = useSessionState(
    "deploy.new.git.submodules",
    false,
  )
  const [includeLfs, setIncludeLfs] = useSessionState("deploy.new.git.lfs", false)
  const [busy, setBusy] = useState("")
  const [failure, setFailure] = useState<Error>()

  const doImport = async (
    source: DeploymentDraftSource,
    name: string,
    sourceLabel: string,
    githubRepo: string | undefined,
    key: string,
  ) => {
    setBusy(key)
    setFailure(undefined)
    try {
      onInspected(await inspectAndPrepare(name, "web", source, { sourceLabel, githubRepo }))
    } catch (error) {
      setFailure(asError(error))
    } finally {
      setBusy("")
    }
  }

  const importRepo = (repo: PickableRepo) =>
    void doImport(
      {
        kind: "git",
        mode: "git_url",
        url: repo.cloneUrl,
        ref: repo.defaultBranch || "main",
        credentialId: repo.credentialId,
        includeSubmodules,
        includeLfs,
      },
      deploymentName(repo.name),
      repo.nameWithOwner,
      repo.nameWithOwner,
      repo.nameWithOwner,
    )

  const needle = filter.trim().toLowerCase()
  // One list: what the App grants first, then what the signed-in account can
  // reach and the App cannot. A repository both can reach imports through the
  // App, whose token never expires under the operator's feet.
  const appNames = new Set((appRepos.data ?? []).map((repo) => repo.nameWithOwner))
  const fromApp: PickableRepo[] = (appRepos.data ?? []).map((repo) => ({
    nameWithOwner: repo.nameWithOwner,
    name: repo.name,
    owner: repo.account || repo.nameWithOwner.split("/")[0],
    description: repo.description,
    language: repo.language,
    private: repo.private,
    fork: repo.fork,
    archived: repo.archived,
    pushedAt: repo.pushedAt,
    htmlUrl: repo.htmlUrl,
    cloneUrl: repo.cloneUrl,
    defaultBranch: repo.defaultBranch,
    credentialId: repo.credentialId,
    viaApp: true,
  }))
  const fromCli: PickableRepo[] = (repos.data ?? [])
    .filter((repo) => !appNames.has(repo.nameWithOwner))
    .map((repo) => ({
      nameWithOwner: repo.nameWithOwner,
      name: repo.name,
      owner: repo.owner || repo.nameWithOwner.split("/")[0],
      description: repo.description,
      language: repo.language,
      private: repo.private,
      fork: repo.fork,
      archived: repo.archived,
      pushedAt: repo.pushedAt,
      htmlUrl: repo.url,
      cloneUrl: repo.cloneUrl,
      defaultBranch: repo.defaultBranch || "main",
      viaApp: false,
    }))
  const pickable = [...fromApp, ...fromCli]
  const owners = [...new Set(pickable.map((repo) => repo.owner))].sort((a, b) => a.localeCompare(b))
  const matches = (repo: PickableRepo) =>
    (owner === "all" || repo.owner === owner) &&
    (!needle ||
      repo.nameWithOwner.toLowerCase().includes(needle) ||
      (repo.description ?? "").toLowerCase().includes(needle))

  // Which identity clones a row used to be a bare `App` tag at its right
  // edge, three characters at 10px beside the language and nothing on the
  // page defining them. Provenance is a property of the whole run of rows, so
  // it is a heading over the run — and only when there is more than one, since
  // a single group is a heading over everything.
  const groups = [
    { key: "app", label: "From the GitHub App", repos: fromApp.filter(matches).sort(byRecency) },
    { key: "cli", label: "From the GitHub CLI", repos: fromCli.filter(matches).sort(byRecency) },
  ].filter((group) => group.repos.length > 0)
  const visibleCount = groups.reduce((total, group) => total + group.repos.length, 0)
  const listing = (signedIn && repos.loading) || (appRepos.loading && !appRepos.data)
  const canBrowse = signedIn || stage === "import" || pickable.length > 0

  return (
    <div className="min-w-0 space-y-6">
      {failure && <ErrorState error={failure} />}

      {/* Said once, in one place. Two identities reach GitHub from this
          server and the screen used to represent them with a 20px avatar and
          a three-letter tag, so "why are these the repositories I can see"
          had no answer anywhere on the page. */}
      <Panel plain className="animate-rise">
        <PanelHeader
          title="Where these repositories come from"
          actions={
            app.data?.installUrl && (
              <Button variant="outline" size="xs" asChild>
                <a href={app.data.installUrl} target="_blank" rel="noreferrer noopener">
                  <External className="size-3" />
                  Adjust repository access
                </a>
              </Button>
            )
          }
        />
        <PanelBody flush>
          <RowList aria-label="GitHub connections">
            <Row
              leading={<GitHubMark className="size-4 text-muted-foreground" />}
              title={app.data?.app?.name ? `GitHub App · ${app.data.app.name}` : "GitHub App"}
              subtitle={
                stage === "import"
                  ? `Installed on ${installations.map((one) => one.account).join(", ")} · clones with an installation token, so nothing expires under you`
                  : stage === "install"
                    ? "Created on GitHub, waiting for an account to install it"
                    : stage === "create"
                      ? "One App in place of a token and a webhook for every repository"
                      : "Reading the App…"
              }
              trailing={
                <>
                  {stage === "import" && (
                    <span className="numeric text-hint text-muted-foreground">
                      {plural(fromApp.length, "repository", "repositories")}
                    </span>
                  )}
                  <Status
                    tone={
                      stage === "import" ? "running" : stage === "install" ? "warning" : "unknown"
                    }
                    label={
                      stage === "import"
                        ? "Installed"
                        : stage === "install"
                          ? "Uninstalled"
                          : "Disconnected"
                    }
                  />
                  {stage === "create" && (
                    <Link
                      href="/deploy/credentials"
                      className="rounded-sm text-hint underline underline-offset-2 focus-ring"
                    >
                      Connect
                    </Link>
                  )}
                </>
              }
            />
            <Row
              leading={<Terminal className="size-4 text-muted-foreground" />}
              title={signedIn ? `GitHub CLI · ${status.data?.account?.login}` : "GitHub CLI"}
              subtitle={
                signedIn
                  ? "Clones and pushes as this account, with the token gh stores on this host"
                  : status.data?.available === false
                    ? "gh is not installed on this host, so signing in is not available here"
                    : "Installed on this host, nobody signed in"
              }
              trailing={
                <>
                  {signedIn && (
                    <span className="numeric text-hint text-muted-foreground">
                      {plural(fromCli.length, "repository", "repositories")}
                    </span>
                  )}
                  <Status
                    tone={signedIn ? "running" : "unknown"}
                    label={
                      signedIn
                        ? "Signed in"
                        : status.data?.available === false
                          ? "Unavailable"
                          : "Signed out"
                    }
                  />
                  {!signedIn && status.data?.available !== false && (
                    <Link
                      href="/git"
                      className="rounded-sm text-hint underline underline-offset-2 focus-ring"
                    >
                      Sign in
                    </Link>
                  )}
                </>
              }
            />
          </RowList>
          {/* An optional integration that cannot be read is information, not a
              failure: the page used to paint a red error block across the
              primary import path whenever GitHub was unreachable. */}
          {app.error && (
            <p className="px-5 pt-3 text-hint text-muted-foreground">
              The GitHub App could not be read just now. Anything it grants is missing from the list
              below.
            </p>
          )}
        </PanelBody>
      </Panel>

      <div className="grid min-w-0 items-start gap-x-6 gap-y-6 xl:grid-cols-[minmax(0,1fr)_22rem]">
        {/* The one surface on this screen that carries depth (§16): choosing a
            repository is what the reader came here to do, and the paste field
            beside it is the fallback for when this list has not got it. Two
            surfaces with depth would be two foregrounds, which is none. */}
        <FlowPanel className="min-w-0">
          <FlowPanelHeader
            title="Import Git repository"
            actions={
              canBrowse && (
                <span className="numeric text-hint text-muted-foreground">
                  {needle || owner !== "all"
                    ? `${visibleCount} of ${pickable.length}`
                    : plural(pickable.length, "repository", "repositories")}
                </span>
              )
            }
          />
          <FlowPanelBody className="space-y-4">
            {status.error && <ErrorState error={status.error} />}
            {repos.error && <ErrorState error={repos.error} />}
            {canBrowse && (
              <>
                <div className="flex flex-wrap items-center gap-2">
                  <Select value={owner} onValueChange={setOwner}>
                    <SelectTrigger aria-label="Repository owner" className="w-44">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="all">All repositories</SelectItem>
                      {owners.map((name) => (
                        <SelectItem key={name} value={name}>
                          {name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <SearchInput
                    value={filter}
                    onChange={(event) => setFilter(event.target.value)}
                    placeholder="Filter repositories"
                    aria-label="Filter repositories"
                    containerClassName="min-w-0 flex-1 sm:w-auto"
                  />
                  <IconAction
                    label="Refresh repositories"
                    onClick={() => {
                      repos.refresh()
                      appRepos.refresh()
                      app.refresh()
                    }}
                  >
                    <RefreshClockwise />
                  </IconAction>
                </div>
                {listing && <LoadingRows rows={6} />}
                {!listing && visibleCount === 0 && (
                  // The repair for "my repository is not here" belongs where the
                  // absence is felt, not on a settings page two navigations
                  // away — which is where every platform that got this right
                  // ended up putting it.
                  <EmptyNote>
                    {pickable.length === 0
                      ? "Neither identity above lists a repository yet."
                      : "No repository matches that filter."}{" "}
                    {app.data?.installUrl && (
                      <>
                        If the one you want is missing, the App has probably not been granted it —{" "}
                        <a
                          href={app.data.installUrl}
                          target="_blank"
                          rel="noreferrer noopener"
                          className="rounded-sm underline underline-offset-2 focus-ring"
                        >
                          adjust repository access
                        </a>
                        . A public clone URL below works without either identity.
                      </>
                    )}
                  </EmptyNote>
                )}
                {/* Padded by the rows' own bleed. A scroll container over rows
                  that bleed grows a sideways scrollbar otherwise, which is
                  what it had. */}
                <div
                  className="-mx-3 max-h-[min(60vh,42rem)] overflow-y-auto px-3"
                  key={listing ? "loading" : "listed"}
                >
                  {groups.map((group) => (
                    <div key={group.key} className="animate-rise space-y-2 pt-3 first:pt-0">
                      {groups.length > 1 && <p className="eyebrow">{group.label}</p>}
                      <ChoiceList aria-label={group.label}>
                        {group.repos.map((repo) => (
                          <RepoRow
                            key={repo.nameWithOwner}
                            repo={repo}
                            pending={busy === repo.nameWithOwner}
                            onImport={() => importRepo(repo)}
                          />
                        ))}
                      </ChoiceList>
                    </div>
                  ))}
                </div>
              </>
            )}

            {/* Under the rows they govern, not inside the paste panel: these
                apply to whichever import fires — a row's press as much as the
                pasted URL. They lived inside "Branch & authentication" once,
                which reads as being about the field beside it, and a
                repository that needed its submodules failed its build with no
                hint that the switch existed. */}
            <details className="border-t border-hairline pt-4">
              <summary className="cursor-pointer rounded-sm py-1 text-xs text-muted-foreground focus-ring">
                Clone options · apply to every import here
              </summary>
              <OptionList className="pt-2">
                <OptionRow
                  title="Include submodules"
                  hint="Fetch the repositories declared in .gitmodules along with this one."
                  checked={includeSubmodules}
                  onCheckedChange={setIncludeSubmodules}
                />
                <OptionRow
                  title="Include Git LFS files"
                  hint="Download Git LFS objects instead of leaving their pointer files."
                  checked={includeLfs}
                  onCheckedChange={setIncludeLfs}
                />
              </OptionList>
            </details>
          </FlowPanelBody>
        </FlowPanel>

        {/* Beside the rows at this width, under them at every other — which is
          what spec §5.4 asks for. It is the fallback for a repository neither
          identity lists, never the primary way in, and it is second in the
          DOM so a phone reaches the rows first. */}
        <Panel plain className="min-w-0 xl:sticky xl:top-6">
          <PanelHeader title="Or paste a Git URL" />
          <PanelBody className="space-y-3">
            <Field label="Clone URL" htmlFor="manual-url">
              <Input
                id="manual-url"
                value={manualUrl}
                onChange={(event) => setManualUrl(event.target.value)}
                placeholder="https://github.com/owner/repository.git"
                className="font-mono"
              />
            </Field>
            <details>
              <summary className="cursor-pointer rounded-sm py-2 text-xs text-muted-foreground focus-ring">
                Branch & authentication
              </summary>
              <div className="grid gap-3 pt-2">
                <Field label="Branch or tag" htmlFor="manual-ref">
                  <Input
                    id="manual-ref"
                    value={manualRef}
                    onChange={(event) => setManualRef(event.target.value)}
                  />
                </Field>
                <Field label="Credential" htmlFor="manual-credential">
                  <CredentialSelect
                    id="manual-credential"
                    kind="git"
                    value={credentialId}
                    onChange={setCredentialId}
                  />
                </Field>
              </div>
            </details>
            {/* Outline while there are repositories to pick, because then the
                rows are the advance (§16). With neither identity listing one,
                there is nothing to choose and this field is the way forward,
                so it takes the command face. */}
            <Button
              className="h-11 w-full sm:h-9"
              variant={pickable.length > 0 ? "outline" : "default"}
              pending={busy === "manual"}
              disabled={!manualUrl.trim()}
              onClick={() =>
                void doImport(
                  {
                    kind: "git",
                    mode: "git_url",
                    url: manualUrl.trim(),
                    ref: manualRef.trim() || "main",
                    credentialId,
                    includeSubmodules,
                    includeLfs,
                  },
                  deploymentName(repositoryName(manualUrl)),
                  manualUrl.trim(),
                  undefined,
                  "manual",
                )
              }
            >
              Import
            </Button>
          </PanelBody>
        </Panel>
      </div>

      <FormNote>
        New commits to the selected branch deploy automatically after your first deployment, unless
        you turn that off on the next screen.
      </FormNote>
    </div>
  )
}

/**
 * One repository, as a target rather than a row with a button bolted to it.
 *
 * The shape is §12's: the title is a real `<button>` carrying the verb in its
 * accessible name, and the press on the surrounding row is a convenience for
 * the pointer. A single button wrapping the whole row would have been simpler
 * and wrong — an ARIA button takes its name from its contents, so the
 * description, the language and the last push would be read out as part of the
 * control's name, or silenced by an `aria-label` that overrides them.
 */
function RepoRow({
  repo,
  pending,
  onImport,
}: {
  repo: PickableRepo
  pending: boolean
  onImport: () => void
}) {
  return (
    <ChoiceRow
      verb={`Import ${repo.nameWithOwner}`}
      onSelect={onImport}
      title={repo.nameWithOwner}
      description={repo.description}
      leading={
        repo.private && (
          <LockClosed
            className="size-3.5 shrink-0 text-muted-foreground"
            aria-label="Private repository"
          />
        )
      }
      trailing={
        <>
          {repo.archived && <Tag>archived</Tag>}
          {repo.fork && <Tag>fork</Tag>}
          {repo.language && <Tag>{repo.language}</Tag>}
          {/* The last push is the reading that decides which of forty
              repositories is the one, and it was on the wire and drawn
              nowhere. While an import is in flight the same slot carries the
              present participle (§13), so a press that takes a second says so
              rather than sitting still — and nothing has to reserve a column
              that is empty on every other row. */}
          {(repo.pushedAt || pending) && (
            <span
              className={cn(
                "numeric hidden w-20 shrink-0 text-right text-hint sm:inline",
                pending ? "text-foreground" : "text-muted-foreground",
              )}
            >
              {pending ? "Importing…" : relativeTime(repo.pushedAt)}
            </span>
          )}
        </>
      }
    />
  )
}
