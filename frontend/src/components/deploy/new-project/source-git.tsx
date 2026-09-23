"use client"

import { useState } from "react"
import {
  External,
  GitHubMark,
  LockClosed,
  RefreshClockwise,
  Terminal,
  type Icon,
} from "@/components/icons"
import { get } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { githubAppStage, githubAvatarUrl, useGitHubAccount, useGitHubApp } from "@/hooks/use-github"
import { useMemoryState, useSessionState } from "@/lib/view-state"
import { CredentialSelect } from "@/components/deploy/credentials-page"
import type { DeploymentDraftSource, GitHubAppRepository, GitHubRepoSummary } from "@/lib/types"
import { Disclosure, Field, FormNote, OptionList, OptionRow } from "@/components/form"
import { ChoiceList, ChoiceRow, FlowPanel, FlowPanelBody, FlowPanelHeader } from "@/components/flow"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { SearchInput } from "@/components/page"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { IconAction } from "@/components/icon-action"
import { Tag } from "@/components/tag"
import { LanguageMark } from "@/components/language-icon"
import { Avatar, AvatarFallback, AvatarImage } from "@/components/ui/avatar"
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
import { useSourceInspection } from "./use-source-inspection"
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
 * An account's own face on GitHub — an identity's, and a repository owner's.
 *
 * Whose repositories these are is the question the identities answer, and a
 * face answers it before a name is read; two grey glyphs never did. Where there
 * is no account yet — no App installed, nobody signed in — the kind's glyph
 * stands in, because there is no face to draw.
 */
function IdentityMark({ account, fallback: Fallback }: { account?: string; fallback: Icon }) {
  // The glyph keeps the avatar's footprint, so the two titles start at the
  // same place when one identity is connected and the other is not.
  if (!account)
    return (
      <span className="flex size-6 items-center justify-center">
        <Fallback className="size-4 text-muted-foreground" />
      </span>
    )
  return (
    <Avatar size="sm" className="border border-hairline">
      <AvatarImage src={githubAvatarUrl(account)} alt="" />
      {/* The kind's glyph rather than initials while the picture is on its
          way: two letters in a filled circle is the pill §4 deleted, and a
          column of twenty of them is a column of pills. */}
      <AvatarFallback>
        <Fallback className="size-3.5" />
      </AvatarFallback>
    </Avatar>
  )
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
  const cliLogin = status.data?.account?.login
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
  // One operator, one GitHub account, both identities on it — the ordinary
  // install, and the one where the CLI's own count is always zero because the
  // App already grants everything it could offer. GitHub logins are
  // case-insensitive, so the comparison is too.
  const sameAccount = Boolean(
    cliLogin && installations.some((one) => one.account.toLowerCase() === cliLogin.toLowerCase()),
  )
  // Every field is remembered for the tab, so a look at another page — the
  // credential this repository needs, say — never means finding it again.
  const [filter, setFilter] = useSessionState("deploy.new.git.filter", "")
  const [owner, setOwner] = useSessionState("deploy.new.git.owner", "all")
  // A deploy link sets the field, and a link that is not a clone URL clears
  // it: what arrives in the address bar is the whole answer, never a mix of
  // the link and what was typed last time.
  const linked = initialUrl !== undefined
  const [manualUrl, setManualUrl] = useMemoryState(
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
  const inspection = useSourceInspection(onInspected)

  const doImport = async (
    source: DeploymentDraftSource,
    name: string,
    sourceLabel: string,
    githubRepo: string | undefined,
    key: string,
  ) => {
    if (busy) return
    setBusy(key)
    setFailure(undefined)
    try {
      await inspection.inspect(() =>
        inspectAndPrepare(name, "web", source, { sourceLabel, githubRepo }),
      )
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
  // The list is drawn once, when every identity that can add to it has
  // answered. The App's rows arrive first and the CLI's a `gh` round trip
  // later, and drawing the first batch under placeholders for the second —
  // then dropping the placeholders and remounting the list when it landed —
  // is a list that visibly loaded twice. Only the first answer counts: a
  // refresh keeps the rows on screen while it asks again.
  const listing = status.loading || (signedIn && repos.loading) || appRepos.loading || app.loading
  const canBrowse = listing || signedIn || stage === "import" || pickable.length > 0

  // The two identities, as the accounts they are. When the App is installed on
  // the account the CLI is signed in as — the ordinary install — that is one
  // account reached two ways, and two rows with the same face and the same
  // name read as two accounts.
  const appAccount = installations.map((one) => one.account).join(", ")
  const merged = sameAccount && stage === "import" && signedIn

  return (
    // The list and the ways around it, each the height of the window: the
    // rows scroll inside the panel that holds them and everything else stays
    // put, which is what the Git page's list does for the checkouts on disk.
    <div className="grid min-w-0 gap-x-6 gap-y-6 xl:h-full xl:min-h-0 xl:grid-cols-[minmax(0,1fr)_22rem] xl:grid-rows-[minmax(0,1fr)]">
      {/* The one surface on this screen that carries depth (§16): choosing a
          repository is what the reader came here to do, and the paste field
          beside it is the fallback for when this list has not got it. Two
          surfaces with depth would be two foregrounds, which is none. */}
      <FlowPanel className="min-w-0 xl:max-h-full xl:min-h-0 xl:self-start">
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
        <FlowPanelBody className="flex min-h-0 flex-1 flex-col gap-4">
          {failure && <ErrorState error={failure} />}
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
                    ? "Neither GitHub identity lists a repository yet."
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
                      . A public clone URL works without either identity.
                    </>
                  )}
                </EmptyNote>
              )}
              {/* Padded by the rows' own bleed. A scroll container over rows
                  that bleed grows a sideways scrollbar otherwise, which is
                  what it had. */}
              <div className="-mx-3 max-h-[min(60vh,42rem)] overflow-y-auto px-3 xl:max-h-none xl:min-h-0 xl:flex-1">
                {!listing &&
                  groups.map((group) => (
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
        </FlowPanelBody>
        {/* Under the rows they govern, not inside the paste panel: these apply
            to whichever import fires — a row's press as much as the pasted
            URL. They lived inside "Branch & authentication" once, which reads
            as being about the field beside it, and a repository that needed
            its submodules failed its build with no hint that the switch
            existed. The qualifier is in the title, not in `facts`: `facts`
            says what a fold holds and yields the row on a phone, and this is a
            caveat about what the switches inside reach. */}
        <div className="shrink-0 border-t border-hairline px-4 py-2">
          <Disclosure quiet summary="Clone options · apply to every import here">
            <OptionList>
              <OptionRow
                title="Clone the submodules listed in .gitmodules"
                checked={includeSubmodules}
                onCheckedChange={setIncludeSubmodules}
              />
              <OptionRow
                title="Download Git LFS objects, not their pointer files"
                checked={includeLfs}
                onCheckedChange={setIncludeLfs}
              />
            </OptionList>
          </Disclosure>
        </div>
      </FlowPanel>

      {/* Beside the rows at this width, under them at every other. Who the
          rows come from, then the fallback for a repository neither identity
          lists — never the primary way in, and second in the DOM so a phone
          reaches the rows first. */}
      <div className="flex min-w-0 flex-col gap-6 xl:min-h-0 xl:overflow-y-auto">
        {/* Said once, in one place: "why are these the repositories I can
            see" used to have no answer anywhere on the page. Each identity is
            a face, a name and a count — and one account reached through both
            is one face, with the two ways it is reached as its second line. */}
        <Panel plain className="animate-rise">
          <PanelHeader
            title="GitHub"
            actions={
              app.data?.installUrl && (
                <Button variant="outline" size="xs" asChild>
                  <a href={app.data.installUrl} target="_blank" rel="noreferrer noopener">
                    <External className="size-3" />
                    Adjust access
                  </a>
                </Button>
              )
            }
          />
          <PanelBody flush>
            {/* Held until both identities have answered: drawn as two rows and then
                folded into one when the second arrived, the one account read as two
                for a moment and then changed its mind. */}
            {status.loading || app.loading ? (
              <LoadingRows rows={1} className="pt-1" />
            ) : (
              <RowList aria-label="GitHub connections">
                {merged ? (
                  <Row
                    leading={<IdentityMark account={appAccount} fallback={GitHubMark} />}
                    title={appAccount}
                    subtitle="GitHub App · GitHub CLI"
                    trailing={
                      <>
                        <span className="numeric text-hint text-muted-foreground">
                          {plural(pickable.length, "repository", "repositories")}
                        </span>
                        <Status tone="running" label="Connected" />
                      </>
                    }
                  />
                ) : (
                  <>
                    <Row
                      leading={
                        <IdentityMark account={installations[0]?.account} fallback={GitHubMark} />
                      }
                      // The account, not the App's generated name: "Just
                      // Dashboard e4e5" is this dashboard's own label for
                      // itself, and what the reader is checking is whose
                      // repositories these are.
                      title={installations.length > 0 ? appAccount : "GitHub App"}
                      subtitle={
                        stage === "import"
                          ? "GitHub App"
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
                              stage === "import"
                                ? "running"
                                : stage === "install"
                                  ? "warning"
                                  : "unknown"
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
                      leading={
                        <IdentityMark
                          account={signedIn ? cliLogin : undefined}
                          fallback={Terminal}
                        />
                      }
                      title={signedIn ? cliLogin : "GitHub CLI"}
                      subtitle={
                        signedIn
                          ? "GitHub CLI"
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
                  </>
                )}
              </RowList>
            )}
            {/* An optional integration that cannot be read is information, not
                a failure: the page used to paint a red error block across the
                primary import path whenever GitHub was unreachable. */}
            {app.error && (
              <p className="pt-3 text-hint text-muted-foreground">
                The GitHub App could not be read just now. Anything it grants is missing from the
                list.
              </p>
            )}
          </PanelBody>
        </Panel>

        <Panel plain>
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
            <Disclosure
              quiet
              summary="Branch & authentication"
              facts={credentialId ? "Ref and credential set" : `Defaults to ${manualRef || "main"}`}
            >
              <div className="grid gap-3">
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
            </Disclosure>
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

        <FormNote>
          New commits to the selected branch deploy automatically after your first deployment,
          unless you turn that off on the next screen.
        </FormNote>
      </div>
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
      // The owner is the face beside the row, so in the title it steps back
      // and the name — what tells forty rows apart — is the ink.
      title={
        <>
          <span className="text-muted-foreground">{repo.owner}/</span>
          {repo.name}
        </>
      }
      description={repo.description}
      leading={<IdentityMark account={repo.owner} fallback={GitHubMark} />}
      trailing={
        <>
          {repo.private && (
            <LockClosed
              className="size-3.5 shrink-0 text-muted-foreground"
              aria-label="Private repository"
            />
          )}
          {repo.archived && <Tag>archived</Tag>}
          {repo.fork && <Tag>fork</Tag>}
          {repo.language && (
            <Tag>
              <LanguageMark language={repo.language} />
            </Tag>
          )}
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
