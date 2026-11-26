"use client"

import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import {
  ChevronDown,
  Clock,
  External,
  GitHubMark,
  Link as LinkGlyph,
  Plus,
  RefreshClockwise,
  SettingsGear,
  SignIn,
  Slash,
  Trash,
} from "@/components/icons"
import { ApiError, del, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { useSessionState } from "@/lib/view-state"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import {
  GITHUB_LIST_DEFAULTS,
  GITHUB_LIST_FILTERS_KEY,
  GITHUB_SORT_LABEL,
  isNarrowed,
  languagesOf,
  narrowRepositories,
  ownersOf,
  repositoryCounts,
  type GitHubListFilters,
  type GitHubListSort,
  type GitHubListState,
} from "@/lib/github-list"
import { githubCallbackRegistered, githubCallbackUrl, localCheckoutsOf } from "@/lib/github"
import type {
  GitHubConnection,
  GitHubEventList,
  GitHubProfile,
  GitHubRepository,
  GitHubRepositoryList,
  GitHubSetup,
} from "@/lib/github-types"
import type { GitRepo } from "@/lib/types"
import { Page, PageContext } from "@/components/page"
import { IdentityHeader } from "@/components/identity-header"
import { Fact } from "@/components/fact-line"
import { ListToolbar } from "@/components/toolbar"
import { SearchInput } from "@/components/search-input"
import { ChipCount, ChipStrip, FilterChip } from "@/components/chips"
import { IconAction } from "@/components/icon-action"
import { EmptyNote, EmptyState, ErrorState, Notice } from "@/components/state"
import { ForgeFace } from "@/components/git/marks"
import { VerbMenu, type VerbSpec } from "@/components/verbs"
import { useConfirm } from "@/components/confirm-dialog"
import { Modal } from "@/components/modal"
import { Section } from "@/components/section"
import { ViewTabList, type TabView } from "@/components/tabs"
import { useDetailTab } from "@/components/detail-page"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { CloneToServerDialog } from "@/components/github/clone-dialog"
import { CreateRepositoryDialog } from "@/components/github/create-dialog"
import { DeviceSignIn } from "@/components/github/device-dialog"
import { SetupDialog } from "@/components/github/setup-dialog"
import {
  GITHUB_PAGE_ROWS,
  accountFacts,
  clockTime,
  listFailure,
  pickConnection,
} from "@/components/github/list-model"
import { ActivityFeed } from "@/components/github/home/activity"
import { ContributionCalendar } from "@/components/github/home/contributions"
import { InboxPreview, InboxView } from "@/components/github/home/inbox"
import {
  HOME_TABS,
  HOME_TAB_LABEL,
  recentRepositories,
  type HomeTab,
} from "@/components/github/home/model"
import { AccessList, ProfileColumn } from "@/components/github/home/profile"
import { REPO_CARD_GRID, RepoCard, RepoCardSkeleton } from "@/components/github/home/repo-card"

const STATES: { key: GitHubListState; label: string }[] = [
  { key: "all", label: "All" },
  { key: "public", label: "Public" },
  { key: "private", label: "Private" },
  { key: "archived", label: "Archived" },
]

const ANY = "__any"

/**
 * The connections whose next list read asks GitHub again (`?refresh=1`)
 * rather than the server's minute-long cache: set by Refresh, taken by the
 * read it starts. Module state, read only inside the fetcher.
 */
const fresh = new Set<string>()
const askFresh = (what: string, id: number | undefined) =>
  id !== undefined && fresh.add(`${what}:${id}`)
const takeBypass = (what: string, id: number | undefined) =>
  id !== undefined && fresh.delete(`${what}:${id}`)

const noSubscription = () => () => {}
const browserOrigin = () => window.location.origin
const serverOrigin = () => ""

/**
 * The GitHub page: the GitHub accounts this dashboard account connected,
 * and what they reach through the dashboard's repository App — drawn as
 * GitHub draws a person's own home, because that is the picture of it the
 * reader already has.
 *
 * Once an account is connected the page has four views in the address
 * (`?tab=`). **Overview** is the account's profile: its face, name and bio
 * and what the App reaches beside its year of contributions (the page's
 * picture, D2), then the repositories it pushed to last as cards, the pull
 * requests it opened and is asked to review, the issues assigned to it, and
 * what it did lately. **Repositories** is every repository as a card, with
 * the list's search, filters and order. **Pull requests** and **Issues** are
 * GitHub's own lists of the account's, across every repository it reaches.
 *
 * It is apart from Local Git on purpose: Local Git is the checkouts on this server and the server's own
 * git; this is GitHub, read and changed through GitHub's API as the person
 * who connected the account. Neither needs the other. A clone from here
 * lands in Local Git, and a row says when a checkout of it is on the server.
 *
 * The page opens on its identity line in every state, then the one thing
 * the state needs: setting up the App (an administrator), connecting an
 * account, reconnecting one GitHub stopped accepting, or the account's
 * access strip, toolbar and list. Filters are kept in the session under the
 * key the repository page's rail reads (`GITHUB_LIST_FILTERS_KEY`), so the
 * rail is the list the reader left. Nothing here polls: GitHub's rate limit
 * is the reader's own, so the list is read on arrival and on Refresh.
 */
export function GitHubListPage() {
  const { can } = useAuth()
  const router = useRouter()
  const params = useSearchParams()
  const { confirm, dialog } = useConfirm()
  const admin = can("system.admin")
  const control = can("service.control")

  // The address the browser reached the dashboard at, read after mount so
  // the server's render and the first client render agree.
  const origin = useSyncExternalStore(noSubscription, browserOrigin, serverOrigin)

  const setup = usePoll((signal) => get<GitHubSetup>("/github/app", undefined, signal), 0)
  const configured = Boolean(setup.data?.configured)
  const connections = usePoll(
    (signal) => get<GitHubConnection[]>("/github/connections", undefined, signal),
    0,
    [],
    { enabled: configured },
  )
  const current = pickConnection(connections.data, params.get("account"))
  const active = current?.status === "active"

  const list = usePoll(
    (signal) => {
      const fresh = takeBypass("list", current?.id)
      return get<GitHubRepositoryList>(
        `/github/connections/${current?.id}/repositories`,
        fresh ? { refresh: 1 } : undefined,
        signal,
      )
    },
    0,
    [current?.id],
    { enabled: Boolean(current && active) },
  )
  const [tab, setTab] = useDetailTab(HOME_TABS)

  // The account's profile and year, once per account, and again on Refresh.
  const profile = usePoll(
    (signal) => {
      const fresh = takeBypass("profile", current?.id)
      return get<GitHubProfile>(
        `/github/connections/${current?.id}/profile`,
        fresh ? { refresh: 1 } : undefined,
        signal,
      )
    },
    0,
    [current?.id],
    { enabled: Boolean(current && active) },
  )
  // What it did lately, read when the Overview is first shown.
  const events = usePoll(
    (signal) => {
      const fresh = takeBypass("events", current?.id)
      return get<GitHubEventList>(
        `/github/connections/${current?.id}/events`,
        fresh ? { refresh: 1 } : undefined,
        signal,
      )
    },
    0,
    [current?.id],
    { enabled: Boolean(current && active && tab === "overview") },
  )

  const refreshList = useCallback(() => {
    askFresh("list", current?.id)
    list.refresh()
  }, [list, current?.id])
  const refreshAll = useCallback(() => {
    refreshList()
    askFresh("profile", current?.id)
    profile.refresh()
    if (tab === "overview") {
      askFresh("events", current?.id)
      events.refresh()
    }
  }, [refreshList, profile, events, tab, current?.id])

  // Local Git's checkouts, once, for the "On this server" mark and Open in
  // Local Git. A server without git answers nothing, and the marks stay off.
  const local = usePoll(
    (signal) => get<{ available: boolean; repos: GitRepo[] }>("/git/", undefined, signal),
    0,
  )

  const [filters, setFilters] = useSessionState<GitHubListFilters>(
    GITHUB_LIST_FILTERS_KEY,
    GITHUB_LIST_DEFAULTS,
  )
  const set = (change: Partial<GitHubListFilters>) => setFilters({ ...filters, ...change })
  const clear = () => setFilters({ ...GITHUB_LIST_DEFAULTS, sort: filters.sort })

  // Show more holds the rows already drawn and adds the next thirty; a new
  // narrowing starts again from thirty.
  const filterKey = JSON.stringify(filters)
  const [rows, setRows] = useState({ key: filterKey, count: GITHUB_PAGE_ROWS })
  const shownRows = rows.key === filterKey ? rows.count : GITHUB_PAGE_ROWS

  const [setupOpen, setSetupOpen] = useState(false)
  const [deviceOpen, setDeviceOpen] = useState(false)
  const [helpOpen, setHelpOpen] = useState(false)
  const [createOpen, setCreateOpen] = useState(false)
  const [cloning, setCloning] = useState<GitHubRepository | null>(null)
  const [connecting, setConnecting] = useState(false)
  const [refreshingAccess, setRefreshingAccess] = useState(false)

  const registered = !origin || githubCallbackRegistered(setup.data, origin)

  // Another account opens on the view the page is on. The address says which:
  // one without `?tab=` is the Overview, and the mounted page follows it.
  const goToAccount = useCallback(
    (id: number | undefined) => router.replace(id ? viewHref(id, tab) : "/github"),
    [router, tab],
  )

  const connect = async () => {
    if (!registered) {
      setHelpOpen(true)
      return
    }
    setConnecting(true)
    try {
      const { url } = await post<{ url: string }>("/github/connections/authorize", { origin })
      window.location.assign(url)
    } catch (error) {
      setConnecting(false)
      if (error instanceof ApiError && error.code === "github_callback_unregistered") {
        setHelpOpen(true)
        return
      }
      notify.error("Could not start the GitHub sign-in", error)
    }
  }

  const refreshAccess = async (connection: GitHubConnection) => {
    setRefreshingAccess(true)
    try {
      await post(`/github/connections/${connection.id}/refresh`)
      connections.refresh()
      refreshList()
    } catch (error) {
      notify.error("Could not read the access again", error)
    } finally {
      setRefreshingAccess(false)
    }
  }

  // GitHub's setup redirect (`?installed=1`, after an installation was
  // added or changed): every connection reads its installations once, and
  // the flag leaves the address so a reload does not do it again.
  const installedHandled = useRef(false)
  const installed = params.get("installed") === "1"
  useEffect(() => {
    if (!installed || installedHandled.current || !connections.data) return
    installedHandled.current = true
    const live = connections.data.filter((connection) => connection.status === "active")
    void Promise.allSettled(
      live.map((connection) => post(`/github/connections/${connection.id}/refresh`)),
    ).then(() => {
      connections.refresh()
      refreshList()
      const rest = new URLSearchParams(params.toString())
      rest.delete("installed")
      const query = rest.toString()
      router.replace(query ? `/github?${query}` : "/github")
    })
  }, [installed, connections, refreshList, params, router])

  const disconnect = (connection: GitHubConnection) =>
    confirm({
      title: `Disconnect @${connection.login}`,
      confirmLabel: "Disconnect",
      subject: {
        mark: <ForgeFace login={connection.login} provider="github" size="sm" />,
        name: `@${connection.login}`,
      },
      description: (
        <p>
          The dashboard forgets this sign-in and asks GitHub to revoke it. The repositories on
          GitHub, and any clones on this server, stay as they are.
        </p>
      ),
      action: async () => {
        await del(`/github/connections/${connection.id}`)
      },
      onDone: () => {
        connections.refresh()
        goToAccount(undefined)
      },
    })

  const removeSetup = () =>
    confirm({
      title: "Remove GitHub setup",
      confirmLabel: "Remove",
      subject: setup.data?.app && {
        mark: <GitHubMark className="size-5" />,
        name: setup.data.app.name,
      },
      description: (
        <p>
          Every GitHub account connected on this dashboard, by anyone, is disconnected and the App
          is forgotten here. The App itself stays on GitHub until its owner deletes it there.
        </p>
      ),
      action: async () => {
        await del("/github/app")
      },
      onDone: () => {
        setup.refresh()
        goToAccount(undefined)
      },
    })

  const repos = useMemo(() => list.data?.repositories ?? [], [list.data])
  // Every repository the page lists, by its lower-cased full name: a pull
  // request or event in one of them opens here, anywhere else on GitHub.
  const listed = useMemo(() => new Set(repos.map((repo) => repo.fullName.toLowerCase())), [repos])
  const counts = useMemo(
    () => (list.data ? repositoryCounts(repos) : undefined),
    [list.data, repos],
  )
  const owners = useMemo(() => ownersOf(repos, current?.login), [repos, current?.login])
  const languages = useMemo(() => languagesOf(repos), [repos])
  const shown = useMemo(() => narrowRepositories(repos, filters), [repos, filters])
  const narrowed = isNarrowed(filters)

  const app = setup.data?.app
  const many = (connections.data?.length ?? 0) > 1

  const menu: VerbSpec[] = []
  if (configured && connections.data && connections.data.length > 0) {
    if (control) {
      menu.push(
        {
          key: "connect",
          label: "Connect another account",
          icon: SignIn,
          run: () => void connect(),
        },
        {
          key: "code",
          label: "Sign in with a code",
          icon: GitHubMark,
          run: () => setDeviceOpen(true),
        },
      )
    }
    if (active)
      menu.push({ key: "refresh", label: "Refresh", icon: RefreshClockwise, run: refreshAll })
    if (app) {
      menu.push({
        key: "install",
        label: "Install on another account",
        icon: Plus,
        run: () => window.open(app.installUrl, "_blank", "noopener"),
      })
    }
  }
  if (admin && app) {
    menu.push({
      key: "settings",
      label: "App settings",
      icon: SettingsGear,
      run: () => window.open(app.settingsUrl, "_blank", "noopener"),
    })
  }
  if (current && control) {
    menu.push({
      key: "disconnect",
      label: `Disconnect @${current.login}`,
      icon: Slash,
      danger: true,
      run: () => disconnect(current),
    })
  }
  if (admin && configured) {
    menu.push({
      key: "remove",
      label: "Remove GitHub setup",
      icon: Trash,
      danger: true,
      run: removeSetup,
    })
  }

  const accountPicker = current && many && connections.data && (
    <VerbMenu
      align="start"
      verbs={connections.data.map((connection) => ({
        key: String(connection.id),
        label: `@${connection.login}${connection.status === "active" ? "" : ` (${connection.status})`}`,
        icon: GitHubMark,
        mark: <ForgeFace login={connection.login} provider="github" size="xs" />,
        checked: connection.id === current.id,
        run: () => goToAccount(connection.id),
      }))}
      trigger={
        <button
          type="button"
          aria-label={`Account @${current.login}, switch account`}
          className="inline-flex h-4 items-center gap-1 rounded-sm text-foreground focus-ring hover:underline"
        >
          @{current.login}
          <ChevronDown aria-hidden className="size-3" />
        </button>
      }
    />
  )

  const facts = current ? (
    <>
      {!active && <Fact className="text-warning">Sign-in ended</Fact>}
      {accountFacts(current.login, counts).map((fact, index) =>
        index === 0 && accountPicker ? (
          <Fact key="account">{accountPicker}</Fact>
        ) : (
          <Fact key={fact} className={index === 0 ? "font-mono" : "numeric"}>
            {fact}
          </Fact>
        ),
      )}
    </>
  ) : configured && connections.data ? (
    <Fact>Not connected</Fact>
  ) : setup.data && !configured ? (
    <Fact>Not set up</Fact>
  ) : undefined

  const loading = !setup.data || (configured && !connections.data && !connections.error)
  const command =
    active && control ? (
      <Button size="sm" onClick={() => setCreateOpen(true)} aria-label="New repository">
        <Plus className="size-3.5" />
        New
      </Button>
    ) : loading ? (
      true
    ) : undefined

  const identity = (
    <IdentityHeader
      label="About GitHub"
      slot="github-identity"
      loading={loading}
      mark="github"
      fallback={GitHubMark}
      title="GitHub"
      facts={facts}
      command={command}
      menu={
        <>
          {menu.length > 0 && (
            <VerbMenu verbs={menu} glyphs={false} label="More actions for GitHub" />
          )}
          <span className="max-sm:hidden">
            <WorkspaceHelp compact />
          </span>
        </>
      }
    />
  )

  const connectHelp = (
    <ConnectHelp
      origin={origin}
      setup={setup.data}
      admin={admin}
      onCode={() => {
        setHelpOpen(false)
        setDeviceOpen(true)
      }}
      onAdded={setup.refresh}
    />
  )

  let body: React.ReactNode
  if (setup.error && !setup.data) {
    body = <ErrorState error={setup.error} what="the GitHub setup" onRetry={setup.refresh} />
  } else if (!setup.data) {
    body = <HomeSkeleton />
  } else if (!configured) {
    body = (
      <EmptyState
        mark={<GitHubMark aria-hidden className="size-6 text-foreground" />}
        title="GitHub is not set up"
        description={
          admin
            ? "Create this dashboard's GitHub App once; then everyone here connects their own GitHub account through it."
            : "An administrator sets it up once; then you can connect your own GitHub account here."
        }
        action={
          admin && (
            <Button size="sm" onClick={() => setSetupOpen(true)}>
              <GitHubMark className="size-4" />
              Set up
            </Button>
          )
        }
      />
    )
  } else if (connections.error && !connections.data) {
    body = (
      <ErrorState
        error={connections.error}
        what="your GitHub accounts"
        onRetry={connections.refresh}
      />
    )
  } else if (!connections.data) {
    body = <HomeSkeleton />
  } else if (!current) {
    body = (
      <EmptyState
        mark={<GitHubMark aria-hidden className="size-6 text-foreground" />}
        title="Connect GitHub"
        description="Sign in with your GitHub account to list and manage the repositories it reaches."
        action={
          !control ? undefined : registered ? (
            <Button size="sm" onClick={() => void connect()} pending={connecting}>
              <SignIn className="size-4" />
              Connect GitHub
            </Button>
          ) : (
            <div className="w-full max-w-lg text-left">{connectHelp}</div>
          )
        }
      />
    )
  } else {
    const failure = list.error ? listFailure(list.error) : undefined
    const reconnect = !active || failure?.kind === "reconnect"
    body = (
      <div className="flex min-w-0 flex-col gap-4">
        {reconnect && (
          <Notice
            title={`@${current.login} needs to sign in again`}
            tone="warning"
            icon={SignIn}
            data-slot="github-reconnect"
          >
            <span className="block">
              {(failure?.kind === "reconnect" && failure.message) ||
                current.statusMessage ||
                "GitHub no longer accepts this sign-in."}
            </span>
            {control && (
              <Button
                size="xs"
                className="mt-2"
                onClick={() => void connect()}
                pending={connecting}
              >
                Reconnect
              </Button>
            )}
          </Notice>
        )}
        {list.data?.partial.map((part) => (
          <Notice
            key={part.installationId}
            title={`Could not list ${part.account}'s repositories`}
            tone="warning"
            data-slot="github-partial"
          >
            {part.message}
          </Notice>
        ))}
        {list.data?.truncated && (
          <Notice title="Only the first 3,000 repositories are listed" tone="warning">
            Narrow the installation&apos;s repositories on GitHub to see the rest here.
          </Notice>
        )}
        {active && !reconnect && (
          <HomeViews
            tab={tab}
            setTab={setTab}
            connection={current}
            counts={counts}
            overview={
              <Overview
                connection={current}
                profile={profile.data}
                profileLoading={!profile.data && !profile.error}
                repos={repos}
                listLoading={!list.data && !list.error}
                listError={
                  list.error && !list.data ? (
                    <ListError error={list.error} failure={failure} onRetry={refreshList} />
                  ) : undefined
                }
                listed={listed}
                events={events.data?.events}
                eventsLoading={!events.data && !events.error}
                local={local.data?.repos}
                control={control}
                onClone={setCloning}
                installUrl={app?.installUrl}
                refreshingAccess={refreshingAccess}
                onRefreshAccess={() => void refreshAccess(current)}
              />
            }
            repositories={
              <RepositoryList
                list={list.data}
                failure={failure}
                error={list.error}
                onRetry={refreshList}
                filters={filters}
                set={set}
                clear={clear}
                counts={counts}
                owners={owners}
                languages={languages}
                shown={shown}
                shownRows={shownRows}
                onMore={() => setRows({ key: filterKey, count: shownRows + GITHUB_PAGE_ROWS })}
                narrowed={narrowed}
                connection={current}
                local={local.data?.repos}
                control={control}
                onNew={() => setCreateOpen(true)}
                onClone={setCloning}
                onRefresh={refreshList}
                refreshing={list.loading}
              />
            }
            pulls={<InboxView kind="pull" connection={current.id} listed={listed} />}
            issues={<InboxView kind="issue" connection={current.id} listed={listed} />}
          />
        )}
      </div>
    )
  }

  return (
    <Workspace
      name="GitHub"
      stateKey="github.list"
      refresh={refreshAll}
      escape={() => {
        if (!narrowed) return false
        clear()
        return true
      }}
    >
      <Page>
        <PageContext title="GitHub" />
        {identity}
        {body}
        {dialog}
        <SetupDialog open={setupOpen} onOpenChange={setSetupOpen} />
        <DeviceSignIn
          open={deviceOpen}
          onOpenChange={setDeviceOpen}
          settingsUrl={app?.settingsUrl}
          onConnected={(connection) => {
            setDeviceOpen(false)
            connections.refresh()
            goToAccount(connection.id)
          }}
        />
        <Modal open={helpOpen} onOpenChange={setHelpOpen} title="Connect GitHub" size="md">
          {connectHelp}
        </Modal>
        {current && active && (
          <CreateRepositoryDialog
            open={createOpen}
            onOpenChange={setCreateOpen}
            connection={current.id}
            login={current.login}
            onCreated={refreshList}
          />
        )}
        {current && cloning && (
          <CloneToServerDialog
            open
            onOpenChange={(open) => !open && setCloning(null)}
            connection={current.id}
            repository={cloning}
            onCloned={() => local.refresh()}
          />
        )}
      </Page>
    </Workspace>
  )
}

/** The page before its first read, in its loaded shape: the route's Suspense fallback. */
export function GitHubListSkeleton() {
  return (
    <Page>
      <PageContext title="GitHub" />
      <IdentityHeader
        label="About GitHub"
        slot="github-identity"
        loading
        mark="github"
        fallback={GitHubMark}
        title="GitHub"
        command
        menu={false}
      />
      <HomeSkeleton />
    </Page>
  )
}

/**
 * Why the web sign-in cannot start from this address, and the two ways on:
 * the code sign-in, which works from anywhere, or adding this address to the
 * App's callback URLs on GitHub (an administrator), then telling the
 * dashboard it was added.
 */
function ConnectHelp({
  origin,
  setup,
  admin,
  onCode,
  onAdded,
}: {
  origin: string
  setup?: GitHubSetup
  admin: boolean
  onCode: () => void
  onAdded: () => void
}) {
  const [adding, setAdding] = useState(false)
  const registeredUrls = setup?.app?.callbackUrls ?? []
  const add = async () => {
    setAdding(true)
    try {
      await post("/github/app/callback-urls", { origin })
      notify.success("This address can sign in now")
      onAdded()
    } catch (error) {
      notify.error("Could not add this address", error)
    } finally {
      setAdding(false)
    }
  }
  return (
    <div data-slot="github-connect-help" className="space-y-3">
      <p className="text-body">
        GitHub only sends a sign-in back to the addresses registered on the App, and{" "}
        <span className="font-mono">{origin}</span> is not one of them.
      </p>
      {registeredUrls.length > 0 && (
        <ul
          aria-label="Registered addresses"
          className="space-y-0.5 font-mono text-xs text-muted-foreground"
        >
          {registeredUrls.map((url) => (
            <li key={url} className="truncate">
              {url}
            </li>
          ))}
        </ul>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" onClick={onCode}>
          <GitHubMark className="size-4" />
          Sign in with a code
        </Button>
        {admin && setup?.app && (
          <>
            <Button size="sm" variant="outline" asChild>
              <a href={setup.app.settingsUrl} target="_blank" rel="noreferrer">
                <External className="size-3.5" />
                App settings
              </a>
            </Button>
            <Button size="sm" variant="outline" onClick={() => void add()} pending={adding}>
              <LinkGlyph className="size-3.5" />
              Add this address
            </Button>
          </>
        )}
      </div>
      {admin && setup?.app && (
        <p className="text-hint text-muted-foreground">
          Add <span className="font-mono text-foreground">{githubCallbackUrl(origin)}</span> under
          Callback URL in the App settings first.
        </p>
      )}
    </div>
  )
}

/**
 * The repository list's first read failed: a spent rate limit says when it
 * lifts (the Overview's recent repositories and the Repositories view say
 * it alike), anything else is the ordinary failed read.
 */
function ListError({
  error,
  failure,
  onRetry,
}: {
  error: Error
  failure?: ReturnType<typeof listFailure>
  onRetry: () => void
}) {
  if (failure?.kind === "rate") {
    return (
      <EmptyState
        data-slot="github-rate-limited"
        icon={Clock}
        title="GitHub asks to wait"
        description={`${failure.message}${failure.until ? ` Try again after ${clockTime(failure.until)}.` : ""}`}
        action={
          <Button size="sm" variant="outline" onClick={onRetry}>
            Try again
          </Button>
        }
      />
    )
  }
  return <ErrorState error={error} what="the repositories" onRetry={onRetry} />
}

function RepositoryList({
  list,
  failure,
  error,
  onRetry,
  filters,
  set,
  clear,
  counts,
  owners,
  languages,
  shown,
  shownRows,
  onMore,
  narrowed,
  connection,
  local,
  control,
  onNew,
  onClone,
  onRefresh,
  refreshing,
}: {
  list?: GitHubRepositoryList
  failure?: ReturnType<typeof listFailure>
  error?: Error
  onRetry: () => void
  filters: GitHubListFilters
  set: (change: Partial<GitHubListFilters>) => void
  clear: () => void
  counts?: Record<GitHubListState, number>
  owners: { login: string; count: number }[]
  languages: { name: string; count: number }[]
  shown: GitHubRepository[]
  shownRows: number
  onMore: () => void
  narrowed: boolean
  connection: GitHubConnection
  local?: GitRepo[]
  control: boolean
  onNew: () => void
  onClone: (repo: GitHubRepository) => void
  onRefresh: () => void
  refreshing: boolean
}) {
  if (error && !list) return <ListError error={error} failure={failure} onRetry={onRetry} />
  if (!list) {
    return (
      <>
        <ListToolbar
          search={
            <SearchInput
              disabled
              placeholder="Search repositories"
              aria-label="Search repositories"
            />
          }
        />
        <RepoCardSkeleton />
      </>
    )
  }
  if (list.repositories.length === 0) {
    return (
      <EmptyState
        mark={<GitHubMark aria-hidden className="size-6 text-foreground" />}
        title="No repositories yet"
        description={`Nothing the App can reach for @${connection.login}. Create one, or install the App on more repositories.`}
        action={
          control && (
            <Button size="sm" onClick={onNew}>
              <Plus className="size-4" />
              New
            </Button>
          )
        }
      />
    )
  }
  const page = shown.slice(0, shownRows)
  return (
    <>
      <ListToolbar
        search={
          <SearchInput
            value={filters.query}
            onChange={(event) => set({ query: event.target.value })}
            placeholder="Search repositories"
            aria-label="Search repositories"
            clearable
          />
        }
        sort={
          <>
            {owners.length > 1 && (
              <Select
                value={filters.owner || ANY}
                onValueChange={(value) => set({ owner: value === ANY ? "" : value })}
              >
                <SelectTrigger size="sm" className="w-36" aria-label="Owner">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={ANY}>Every owner</SelectItem>
                  {owners.map((owner) => (
                    <SelectItem key={owner.login} value={owner.login} className="font-mono">
                      {owner.login}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
            {languages.length > 1 && (
              <Select
                value={filters.language || ANY}
                onValueChange={(value) => set({ language: value === ANY ? "" : value })}
              >
                <SelectTrigger size="sm" className="w-36" aria-label="Language">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={ANY}>Every language</SelectItem>
                  {languages.map((language) => (
                    <SelectItem key={language.name} value={language.name}>
                      {language.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
            <Select
              value={filters.sort}
              onValueChange={(value) => set({ sort: value as GitHubListSort })}
            >
              <SelectTrigger size="sm" className="w-40" aria-label="Sort repositories">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {(Object.keys(GITHUB_SORT_LABEL) as GitHubListSort[]).map((key) => (
                  <SelectItem key={key} value={key}>
                    {GITHUB_SORT_LABEL[key]}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </>
        }
        actions={
          <IconAction
            label="Refresh from GitHub"
            variant="outline"
            size="icon-sm"
            onClick={onRefresh}
            disabled={refreshing}
          >
            <RefreshClockwise className="size-4" />
          </IconAction>
        }
        chips={
          <ChipStrip aria-label="Which repositories are listed">
            {STATES.filter(
              ({ key }) => key === "all" || key === filters.state || (counts?.[key] ?? 0) > 0,
            ).map(({ key, label }) => (
              <FilterChip
                key={key}
                selected={filters.state === key}
                onClick={() => set({ state: key })}
              >
                {label}
                <ChipCount>{counts?.[key] ?? 0}</ChipCount>
              </FilterChip>
            ))}
          </ChipStrip>
        }
      />
      {shown.length === 0 ? (
        <EmptyNote action={narrowed ? { label: "Clear filters", onClick: clear } : undefined}>
          No repository matches.
        </EmptyNote>
      ) : (
        <div className="flex min-w-0 flex-col gap-3">
          <ul aria-label={`Repositories of @${connection.login}`} className={REPO_CARD_GRID}>
            {page.map((repo, index) => (
              <RepoCard
                // The full name, which one account's list holds once.
                key={repo.fullName}
                repo={repo}
                connection={connection.id}
                checkouts={localCheckoutsOf(local, repo.fullName)}
                control={control}
                onClone={onClone}
                index={index}
              />
            ))}
          </ul>
          {shown.length > page.length && (
            <div className="flex items-center justify-center gap-3">
              <Button size="sm" variant="outline" onClick={onMore}>
                Show more
              </Button>
              <span className="numeric text-hint text-muted-foreground">
                {page.length} of {shown.length}
              </span>
            </div>
          )}
        </div>
      )}
    </>
  )
}

/** The views' strip and the one view on screen; the strip holds the page's place while it is read. */
function HomeViews({
  tab,
  setTab,
  connection,
  counts,
  overview,
  repositories,
  pulls,
  issues,
}: {
  tab: HomeTab
  setTab: (next: string) => void
  connection: GitHubConnection
  counts?: Record<GitHubListState, number>
  overview: React.ReactNode
  repositories: React.ReactNode
  pulls: React.ReactNode
  issues: React.ReactNode
}) {
  const views: TabView<HomeTab>[] = HOME_TABS.map((key) => ({
    key,
    label: HOME_TAB_LABEL[key],
    count: key === "repositories" ? counts?.all : undefined,
  }))
  return (
    <Tabs value={tab} onValueChange={setTab} className="gap-0">
      <ViewTabList label={`Views of @${connection.login}`} views={views} value={tab} sticky />
      <TabsContent value="overview" className="pt-6">
        {tab === "overview" && overview}
      </TabsContent>
      <TabsContent value="repositories" className="flex flex-col gap-4 pt-6">
        {tab === "repositories" && repositories}
      </TabsContent>
      <TabsContent value="pulls" className="pt-6">
        {tab === "pulls" && pulls}
      </TabsContent>
      <TabsContent value="issues" className="pt-6">
        {tab === "issues" && issues}
      </TabsContent>
    </Tabs>
  )
}

/** The address of one of the page's views, on the account it is on. */
function viewHref(connection: number, tab: HomeTab) {
  const query = new URLSearchParams({ account: String(connection) })
  if (tab !== "overview") query.set("tab", tab)
  return `/github?${query}`
}

/**
 * The profile column and the main column, at their widths, from a laptop up:
 * the profile and what the App reaches stand one over the other in the left
 * column, and the main column runs down beside both. On a phone it is one
 * column, the profile first and what the App reaches last.
 */
const HOME_GRID =
  "grid min-w-0 gap-8 lg:grid-cols-[16.5rem_minmax(0,1fr)] lg:grid-rows-[auto_1fr] lg:gap-x-12"

/**
 * The account's home: its profile in the left column, and in the main one
 * its year, the repositories it pushed to last, the pull requests and issues
 * that are its to look at, and what it did lately. On a phone the columns
 * become one, the profile a compact block at the top.
 */
function Overview({
  connection,
  profile,
  profileLoading,
  repos,
  listLoading,
  listError,
  listed,
  events,
  eventsLoading,
  local,
  control,
  onClone,
  installUrl,
  refreshingAccess,
  onRefreshAccess,
}: {
  connection: GitHubConnection
  profile?: GitHubProfile
  profileLoading: boolean
  repos: GitHubRepository[]
  listLoading: boolean
  /** The list's failed first read, said where the recent repositories would stand. */
  listError?: React.ReactNode
  listed: Set<string>
  events?: GitHubEventList["events"]
  eventsLoading: boolean
  local?: GitRepo[]
  control: boolean
  onClone: (repo: GitHubRepository) => void
  installUrl?: string
  refreshingAccess: boolean
  onRefreshAccess: () => void
}) {
  const recent = recentRepositories(repos)
  return (
    <div className={HOME_GRID} data-slot="github-overview">
      <ProfileColumn connection={connection} profile={profile} className="lg:col-start-1" />
      <AccessList
        connection={connection}
        installUrl={installUrl}
        refreshing={refreshingAccess}
        onRefresh={onRefreshAccess}
        className="order-last self-start lg:order-none lg:col-start-1 lg:row-start-2"
      />
      <div
        data-slot="github-overview-main"
        className="flex min-w-0 flex-col gap-10 lg:col-start-2 lg:row-span-2 lg:row-start-1"
      >
        <ContributionCalendar
          contributions={profile?.contributions}
          message={profile?.contributionsMessage}
          loading={profileLoading}
        />
        <Section
          title="Recently pushed"
          link={
            repos.length > recent.length
              ? { href: viewHref(connection.id, "repositories"), label: "All repositories" }
              : undefined
          }
        >
          {listError ? (
            listError
          ) : listLoading ? (
            <RepoCardSkeleton count={4} />
          ) : recent.length === 0 ? (
            <EmptyNote>Nothing pushed yet.</EmptyNote>
          ) : (
            <ul aria-label="Recently pushed" className={REPO_CARD_GRID}>
              {recent.map((repo, index) => (
                <RepoCard
                  key={repo.fullName}
                  repo={repo}
                  connection={connection.id}
                  checkouts={localCheckoutsOf(local, repo.fullName)}
                  control={control}
                  onClone={onClone}
                  index={index}
                />
              ))}
            </ul>
          )}
        </Section>
        <InboxPreview
          title="Your pull requests"
          kind="pull"
          filter="created"
          connection={connection.id}
          listed={listed}
          href={viewHref(connection.id, "pulls")}
        />
        <InboxPreview
          title="Review requests"
          kind="pull"
          filter="review"
          connection={connection.id}
          listed={listed}
          href={viewHref(connection.id, "pulls")}
        />
        <InboxPreview
          title="Assigned issues"
          kind="issue"
          filter="assigned"
          connection={connection.id}
          listed={listed}
          href={viewHref(connection.id, "issues")}
        />
        <Section title="Public activity">
          <ActivityFeed
            events={events}
            loading={eventsLoading}
            listed={listed}
            connection={connection.id}
          />
        </Section>
      </div>
    </div>
  )
}

/**
 * The connected page before its reads land, in the Overview's shape: the
 * views' strip, the profile column and the calendar's height, so the page
 * does not jump when they arrive (§11).
 */
function HomeSkeleton() {
  return (
    <div aria-hidden className="flex min-w-0 flex-col gap-6">
      <Skeleton className="h-10 w-full max-w-md rounded-md" />
      <div className={HOME_GRID}>
        <div className="flex items-center gap-4 lg:flex-col lg:items-start">
          <Skeleton className="size-16 rounded-full sm:size-20 lg:size-28" />
          <div className="space-y-2">
            <Skeleton className="h-6 w-36" />
            <Skeleton className="h-5 w-24" />
          </div>
        </div>
        <div className="flex min-w-0 flex-col gap-10">
          <Skeleton className="h-36 w-full rounded-lg" />
          <RepoCardSkeleton count={4} />
        </div>
      </div>
    </div>
  )
}
