"use client"

import Link from "next/link"
import { useCallback, useEffect, useMemo, useState } from "react"
import { useParams, useRouter, useSearchParams } from "next/navigation"
import { Clock, GitHubMark } from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { DetailPage, useDetailTab, useSiblingSteps } from "@/components/detail-page"
import { ForgeFace } from "@/components/git/marks"
import { CloneToServerDialog } from "@/components/github/clone-dialog"
import { RepoActions } from "@/components/github/repo/actions"
import { RepoBranches } from "@/components/github/repo/branches"
import { RepoCollaborators } from "@/components/github/repo/collaborators"
import { RepoCommits } from "@/components/github/repo/commits"
import { RepoIdentity } from "@/components/github/repo/identity"
import { RepoIssues } from "@/components/github/repo/issues"
import {
  REPO_TABS,
  REPO_TAB_LABEL,
  repoFailure,
  type RepoTab,
} from "@/components/github/repo/model"
import { RepoOverview } from "@/components/github/repo/overview"
import { RepoPulls } from "@/components/github/repo/pulls"
import { RepoRail, repoKey, useRepoRail } from "@/components/github/repo/rail"
import { RepoSettings } from "@/components/github/repo/settings"
import { RepoProvider, repoPathOf } from "@/components/github/repo/shared"
import { IdentityHeader } from "@/components/identity-header"
import { LoadingRows } from "@/components/loading"
import { MissingThing } from "@/components/missing-thing"
import { ErrorState } from "@/components/state"
import { ViewTabList, type TabView } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import { clockMinute } from "@/lib/format"
import { githubRepoApi, githubRepoHref, localCheckoutsOf } from "@/lib/github"
import type {
  GitHubConnection,
  GitHubRepositoryDetail,
  GitHubRepositoryList,
} from "@/lib/github-types"
import { decodeSegment } from "@/lib/route-params"
import type { GitRepo } from "@/lib/types"

/**
 * One GitHub repository, as a place of its own (D9): the way back to the
 * GitHub page, the identity line, and its views in the address — Overview,
 * Branches, Commits, Pull requests, Issues, Actions, Collaborators and
 * Settings. From `xl` the list the reader came from stands beside it, in the
 * list page's own filters and order (`useRepoRail`), and Alt+↑ and Alt+↓
 * walk it with the view carried.
 *
 * Everything is read through one connection, the GitHub account the page
 * acts as (`?account=`). Without one in the address, the first active
 * connection that lists the repository is the one. The repository is read
 * once and again after every change, never on a timer: what GitHub says
 * about it changes when somebody changes it, and every view that lists
 * something reads its own list.
 *
 * What the reader may do is the repository's `can`, the backend's reading of
 * their role there and of the App's grant; a control it holds back is drawn
 * off with the reason, never as a button that does nothing. The backend
 * checks again on every write.
 */
export function GitHubRepoPage() {
  const params = useParams<{ owner: string; repo: string }>()
  const fullName = `${decodeSegment(params.owner)}/${decodeSegment(params.repo)}`
  const key = fullName.toLowerCase()
  const router = useRouter()
  const search = useSearchParams()
  const [tab, setTab] = useDetailTab(REPO_TABS, { remember: "github.repo.tab" })
  const account = useAccountFor(fullName, search.get("account"))
  const connection = account.connection
  const rail = useRepoRail(connection)
  const byKey = useMemo(
    () => new Map(rail.rows.map((row) => [repoKey(row), row.fullName])),
    [rail.rows],
  )
  const open = useCallback(
    (next: string) =>
      router.push(
        githubRepoHref(byKey.get(next) ?? next, { account: connection ?? undefined, tab }),
      ),
    [router, byKey, connection, tab],
  )
  const steps = useSiblingSteps(rail.keys, key, open)
  const path = connection === null ? "" : githubRepoApi(connection, fullName)
  const detail = usePoll(
    (signal) => get<GitHubRepositoryDetail>(path, undefined, signal),
    0,
    [path],
    { enabled: connection !== null },
  )
  const repo = detail.data
  const listed = rail.rows.find((row) => repoKey(row) === key)
  const back = {
    href: connection === null ? "/github" : `/github?account=${connection}`,
    label: "GitHub",
  }

  const frame = (identity: React.ReactNode, body: React.ReactNode) => (
    <DetailPage
      name="Repository"
      what="repository"
      stateKey="github.repo.page"
      refresh={detail.refresh}
      steps={steps}
      back={back}
      title={repo?.fullName ?? fullName}
      rail={<RepoRail rail={rail} connection={connection} current={key} pinned={repo} />}
      identity={identity}
    >
      {body}
    </DetailPage>
  )

  const failure = account.failure ?? (detail.error && !repo ? detail.error : undefined)
  if (failure || account.none) {
    return frame(
      undefined,
      <RepoUnavailable
        fullName={fullName}
        error={failure}
        none={account.none}
        back={back.href}
        onRetry={detail.refresh}
      />,
    )
  }
  if (!repo || connection === null) {
    return frame(
      // The line's final shape while the repository is read, its command's
      // and menu's places held, so nothing under it moves when it lands.
      <IdentityHeader
        loading
        className="border-b-0 pb-0"
        logo={
          <ForgeFace
            login={fullName.split("/")[0]}
            provider="github"
            className="size-10 rounded-lg max-sm:size-8"
          />
        }
        title={listed?.name ?? fullName.split("/")[1]}
        command
        menu={false}
      />,
      <LoadingRows />,
    )
  }
  return (
    <RepoLoaded
      key={`${connection}:${key}`}
      connection={connection}
      repo={repo}
      refresh={detail.refresh}
      tab={tab}
      setTab={setTab}
      frame={frame}
      onRenamed={(name) =>
        router.replace(githubRepoHref(`${repo.owner.login}/${name}`, { account: connection, tab }))
      }
    />
  )
}

function RepoLoaded({
  connection,
  repo,
  refresh,
  tab,
  setTab,
  frame,
  onRenamed,
}: {
  connection: number
  repo: GitHubRepositoryDetail
  refresh: () => void
  tab: RepoTab
  setTab: (tab: string) => void
  frame: (identity: React.ReactNode, body: React.ReactNode) => React.ReactNode
  onRenamed: (name: string) => void
}) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const [cloning, setCloning] = useState(false)
  // Local Git's own list, matched by remote rather than by folder name.
  const local = usePoll(
    (signal) => get<{ available: boolean; repos: GitRepo[] }>("/git/", undefined, signal),
    0,
    [],
  )
  const checkouts = localCheckoutsOf(local.data?.repos, repo.fullName)
  // The settings are mounted once opened and then kept, so an edit is
  // still held, and its bar still up, while another view is looked at.
  const [settingsOpened, setSettingsOpened] = useState(tab === "settings")
  if (tab === "settings" && !settingsOpened) setSettingsOpened(true)
  const views: TabView<RepoTab>[] = REPO_TABS.map((key) => ({
    key,
    label: REPO_TAB_LABEL[key],
    count:
      key === "issues" && repo.settings.hasIssues
        ? repo.openIssues || undefined
        : undefined,
  }))
  const value = {
    connection,
    repo,
    path: repoPathOf(connection, repo.fullName),
    refresh,
    confirm,
    controlReason: can("service.control")
      ? undefined
      : "Your dashboard account may only read here; changes need the service control right.",
    destructiveReason: can("destructive")
      ? undefined
      : "Your dashboard account may not take destructive actions.",
  }
  return (
    <RepoProvider value={value}>
      {frame(
        <RepoIdentity checkouts={checkouts} onClone={() => setCloning(true)} />,
        <>
          <Tabs value={tab} onValueChange={setTab} className="gap-0">
            <ViewTabList
              label={`Views of ${repo.fullName}`}
              views={views}
              value={tab}
              sticky
            />
            <TabsContent value="overview" className="pt-6">
              {tab === "overview" && (
                <RepoOverview
                  checkouts={checkouts}
                  checkoutsLoading={!local.data && local.loading}
                  onClone={() => setCloning(true)}
                />
              )}
            </TabsContent>
            <TabsContent value="branches" className="pt-4">
              {tab === "branches" && <RepoBranches />}
            </TabsContent>
            <TabsContent value="commits" className="pt-4">
              {tab === "commits" && <RepoCommits />}
            </TabsContent>
            <TabsContent value="pulls" className="pt-4">
              {tab === "pulls" && <RepoPulls />}
            </TabsContent>
            <TabsContent value="issues" className="pt-4">
              {tab === "issues" && <RepoIssues />}
            </TabsContent>
            <TabsContent value="actions" className="pt-4">
              {tab === "actions" && <RepoActions />}
            </TabsContent>
            <TabsContent value="collaborators" className="pt-4">
              {tab === "collaborators" && <RepoCollaborators />}
            </TabsContent>
            <TabsContent
              value="settings"
              forceMount
              hidden={tab !== "settings"}
              className="pt-6"
            >
              {settingsOpened && <RepoSettings onRenamed={onRenamed} />}
            </TabsContent>
          </Tabs>
          <CloneToServerDialog
            open={cloning}
            onOpenChange={setCloning}
            connection={connection}
            repository={repo}
            onCloned={() => local.refresh()}
          />
          {dialog}
        </>,
      )}
    </RepoProvider>
  )
}

/**
 * Which connection the page reads through: the address's `account`, or the
 * first active connection whose list holds the repository, read one at a
 * time so an account with several connections asks only as far as it must.
 */
function useAccountFor(
  fullName: string,
  param: string | null,
): { connection: number | null; none: boolean; failure?: Error } {
  const chosen = param && /^\d+$/.test(param) ? Number(param) : null
  const connections = usePoll(
    (signal) => get<GitHubConnection[]>("/github/connections", undefined, signal),
    0,
    [],
    { enabled: chosen === null },
  )
  const [found, setFound] = useState<{ name: string; connection: number | null } | null>(null)
  const active = useMemo(
    () => (connections.data ?? []).filter((c) => c.status === "active"),
    [connections.data],
  )
  useEffect(() => {
    if (chosen !== null || !connections.data) return
    const controller = new AbortController()
    void (async () => {
      const key = fullName.toLowerCase()
      for (const connection of active) {
        try {
          const list = await get<GitHubRepositoryList>(
            `/github/connections/${connection.id}/repositories`,
            undefined,
            controller.signal,
          )
          if (list.repositories.some((repo) => repo.fullName.toLowerCase() === key)) {
            setFound({ name: fullName, connection: connection.id })
            return
          }
        } catch {
          if (controller.signal.aborted) return
        }
      }
      if (!controller.signal.aborted) setFound({ name: fullName, connection: null })
    })()
    return () => controller.abort()
  }, [chosen, connections.data, active, fullName])
  if (chosen !== null) return { connection: chosen, none: false }
  if (connections.error && !connections.data) {
    return { connection: null, none: false, failure: connections.error }
  }
  const result = found?.name === fullName ? found : null
  return { connection: result?.connection ?? null, none: result !== null && result.connection === null }
}

/**
 * The repository could not be shown: GitHub says it is not there or this
 * account cannot see it, the sign-in has to be made again, the rate limit is
 * spent, or the read failed. Each says what happened in one sentence and
 * offers the one step that helps; the rail stays beside it.
 */
function RepoUnavailable({
  fullName,
  error,
  none,
  back,
  onRetry,
}: {
  fullName: string
  error?: Error
  none: boolean
  back: string
  onRetry: () => void
}) {
  const failure = error ? repoFailure(error) : undefined
  const toList = (
    <Button asChild>
      <Link href={back}>Back to GitHub</Link>
    </Button>
  )
  if (none || failure?.kind === "missing") {
    return (
      <MissingThing
        thing="github"
        icon={GitHubMark}
        title={`${fullName} is not reachable`}
        step={toList}
      >
        {none
          ? "None of your connected GitHub accounts lists it. Connect the account that can see it, or install the App where it lives."
          : "GitHub has no such repository, or the account this page reads through cannot see it."}
      </MissingThing>
    )
  }
  if (failure?.kind === "reconnect") {
    return (
      <MissingThing
        thing="github"
        icon={GitHubMark}
        title="GitHub needs you to sign in again"
        step={
          <Button asChild>
            <Link href={back}>Reconnect</Link>
          </Button>
        }
      >
        {failure.message}
      </MissingThing>
    )
  }
  if (failure?.kind === "rate") {
    return (
      <MissingThing
        icon={Clock}
        title={
          failure.until
            ? `GitHub's rate limit lifts at ${clockMinute(new Date(failure.until).toISOString())}`
            : "GitHub's rate limit is spent"
        }
        step={
          <Button variant="outline" onClick={onRetry}>
            Try again
          </Button>
        }
      >
        {failure.message}
      </MissingThing>
    )
  }
  return <ErrorState error={error ?? new Error("The repository could not be read.")} onRetry={onRetry} retryable />
}
