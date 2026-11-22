"use client"

import {
  Fragment,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react"
import { useRouter, useSearchParams } from "next/navigation"
import {
  ChevronDown,
  Clock,
  CloudDownload,
  Copy,
  External,
  GitBranch,
  GitHubMark,
  Link as LinkGlyph,
  LockClosed,
  Plus,
  RefreshClockwise,
  Servers,
  SettingsGear,
  SignIn,
  Slash,
  Star,
  Trash,
} from "@/components/icons"
import { SourceFork } from "@/components/git/glyphs"
import { ApiError, del, get, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { notify } from "@/lib/toast"
import { relativeTime } from "@/lib/format"
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
import {
  githubCallbackRegistered,
  githubCallbackUrl,
  githubRepoHref,
  localCheckoutsOf,
  localGitHref,
} from "@/lib/github"
import type {
  GitHubConnection,
  GitHubInstallation,
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
import { ChoiceList, ChoiceListSkeleton, ChoiceRow } from "@/components/choice-row"
import { DimActions, IconAction } from "@/components/icon-action"
import { EmptyNote, EmptyState, ErrorState, Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { LanguageMark } from "@/components/language-icon"
import { ForgeFace } from "@/components/git/marks"
import { VerbMenu, type VerbSpec } from "@/components/verbs"
import { useConfirm } from "@/components/confirm-dialog"
import { Modal } from "@/components/modal"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { Button } from "@/components/ui/button"
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
  rowTags,
} from "@/components/github/list-model"

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
const fresh = new Set<number>()
const askFresh = (id: number | undefined) => id !== undefined && fresh.add(id)
const takeBypass = (id: number | undefined) => id !== undefined && fresh.delete(id)

const noSubscription = () => () => {}
const browserOrigin = () => window.location.origin
const serverOrigin = () => ""

/**
 * The GitHub page: the GitHub accounts this dashboard account connected,
 * and the repositories they reach through the dashboard's repository App.
 *
 * It is a list page (`recipes/list-page.md`) and apart from Local Git on
 * purpose: Local Git is the checkouts on this server and the server's own
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
      const fresh = takeBypass(current?.id)
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
  const refreshList = useCallback(() => {
    askFresh(current?.id)
    list.refresh()
  }, [list, current?.id])

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

  const goToAccount = useCallback(
    (id: number | undefined) => router.replace(id ? `/github?account=${id}` : "/github"),
    [router],
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
      menu.push({ key: "refresh", label: "Refresh", icon: RefreshClockwise, run: refreshList })
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
          {menu.length > 0 && <VerbMenu verbs={menu} label="More actions for GitHub" />}
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
    body = <ChoiceListSkeleton rows={6} lines={2} />
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
    body = <ChoiceListSkeleton rows={6} lines={2} />
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
        {active && !reconnect && (
          <AccessStrip
            connection={current}
            installUrl={app?.installUrl}
            refreshing={refreshingAccess}
            onRefresh={() => void refreshAccess(current)}
          />
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
        )}
      </div>
    )
  }

  return (
    <Workspace
      name="GitHub"
      stateKey="github.list"
      refresh={refreshList}
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
      <ChoiceListSkeleton rows={6} lines={2} />
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
 * What the App can reach for this account: one entry per installation, the
 * account it is on, all or selected repositories, and where that is changed
 * on GitHub. One line on a wide screen; it wraps entry by entry, never inside
 * one.
 */
function AccessStrip({
  connection,
  installUrl,
  refreshing,
  onRefresh,
}: {
  connection: GitHubConnection
  installUrl?: string
  refreshing: boolean
  onRefresh: () => void
}) {
  return (
    <div
      role="group"
      aria-label="What the App can reach"
      data-slot="github-access"
      className="flex min-w-0 flex-wrap items-center gap-x-5 gap-y-2 text-xs"
    >
      {connection.installations.length === 0 ? (
        <span className="text-muted-foreground">Not installed on any account yet</span>
      ) : (
        connection.installations.map((installation) => (
          <AccessEntry key={installation.id} installation={installation} />
        ))
      )}
      <span className="ml-auto flex items-center gap-1">
        {installUrl && (
          <Button size="xs" variant="ghost" asChild>
            <a href={installUrl} target="_blank" rel="noreferrer">
              <Plus className="size-3.5" />
              Install
            </a>
          </Button>
        )}
        <Button size="xs" variant="ghost" onClick={onRefresh} pending={refreshing}>
          <RefreshClockwise className="size-3.5" />
          Refresh access
        </Button>
      </span>
    </div>
  )
}

function AccessEntry({ installation }: { installation: GitHubInstallation }) {
  const all = installation.repositorySelection === "all"
  return (
    <span data-slot="github-installation" className="inline-flex min-w-0 items-center gap-1.5">
      <ForgeFace login={installation.account.login} provider="github" size="xs" />
      <span className="font-mono text-foreground">{installation.account.login}</span>
      <span className="text-muted-foreground">
        {installation.suspended ? (
          <span className="text-warning">Suspended</span>
        ) : all ? (
          "All repositories"
        ) : (
          "Selected repositories"
        )}
      </span>
      <a
        href={installation.settingsUrl}
        target="_blank"
        rel="noreferrer"
        aria-label={`Change what ${installation.account.login} grants`}
        className="rounded-sm text-muted-foreground underline-offset-2 focus-ring hover:text-foreground hover:underline"
      >
        Change
      </a>
    </span>
  )
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
  if (error && !list) {
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
        <ChoiceListSkeleton rows={8} lines={2} />
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
          <ChoiceList aria-label={`Repositories of @${connection.login}`}>
            {page.map((repo) => (
              <RepositoryRow
                // The full name, which one account's list holds once.
                key={repo.fullName}
                repo={repo}
                connection={connection.id}
                checkouts={localCheckoutsOf(local, repo.fullName)}
                control={control}
                onClone={onClone}
              />
            ))}
          </ChoiceList>
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

/**
 * One repository: its owner's face, its name over its full name, branch and
 * description, then aligned columns — kind and role tags, whether a
 * checkout is on this server, visibility, language, stars, forks and when
 * it last moved. On a phone the columns go and the row keeps its height.
 */
function RepositoryRow({
  repo,
  connection,
  checkouts,
  control,
  onClone,
}: {
  repo: GitHubRepository
  connection: number
  checkouts: GitRepo[]
  control: boolean
  onClone: (repo: GitHubRepository) => void
}) {
  const tags = rowTags(repo)
  const checkout = checkouts[0]
  const updated = repo.pushedAt ?? repo.updatedAt
  const verbs: VerbSpec[] = [
    {
      key: "github",
      label: "Open on GitHub",
      icon: External,
      run: () => window.open(repo.htmlUrl, "_blank", "noopener"),
    },
    ...(control
      ? [{ key: "clone", label: "Clone to server…", icon: CloudDownload, run: () => onClone(repo) }]
      : []),
    ...(checkout
      ? [
          {
            key: "local",
            label: "Open in Local Git",
            icon: GitBranch,
            href: localGitHref(checkout.path),
            internal: true,
          },
        ]
      : []),
    {
      key: "copy",
      label: "Copy clone URL",
      icon: Copy,
      run: () => void copyText(repo.cloneUrl, "Clone URL copied"),
    },
  ]
  return (
    <ChoiceRow
      href={githubRepoHref(repo.fullName, { account: connection })}
      verb={`Open ${repo.fullName}`}
      workspaceItem={{ id: repo.fullName, name: repo.fullName }}
      leading={<ForgeFace login={repo.owner.login} provider="github" size="sm" />}
      title={repo.name}
      description={
        <span title={repo.description}>
          <span className="font-mono">{repo.fullName}</span>
          <span className="max-sm:hidden">
            {" · "}
            <span className="font-mono">{repo.defaultBranch}</span>
          </span>
          {repo.description && ` · ${repo.description}`}
        </span>
      }
      trailing={
        <span className="flex items-center gap-3 text-xs text-muted-foreground">
          {/* A tag is a bare word, so two side by side read as one phrase
              ("Shared with you Write") without the dot between them. */}
          <span className="hidden w-44 items-center justify-end gap-1 overflow-hidden lg:flex">
            {tags.map((tag, index) => (
              <Fragment key={tag}>
                {index > 0 && (
                  <span aria-hidden className="text-faint">
                    ·
                  </span>
                )}
                <Tag tone={tag === "Archived" ? "warning" : "default"}>{tag}</Tag>
              </Fragment>
            ))}
          </span>
          <span className="hidden w-4 justify-center sm:flex">
            {checkout && (
              <span
                role="img"
                data-slot="on-server"
                aria-label={`On this server at ${checkout.path}`}
                title={`On this server: ${checkout.path}`}
              >
                <Servers aria-hidden className="size-3.5 text-foreground" />
              </span>
            )}
          </span>
          <span className="flex w-16 justify-end max-sm:w-auto">
            {repo.visibility === "public" ? (
              <Tag className="max-sm:hidden">Public</Tag>
            ) : (
              <Tag icon={LockClosed}>{repo.visibility === "internal" ? "Internal" : "Private"}</Tag>
            )}
          </span>
          <span className="hidden w-24 xl:flex">
            {repo.language && (
              <Tag>
                <LanguageMark language={repo.language} />
              </Tag>
            )}
          </span>
          <span className="numeric hidden w-12 items-center justify-end gap-1 lg:flex">
            <Star aria-hidden className="size-3" />
            {repo.stars}
            <span className="sr-only">stars</span>
          </span>
          <span className="numeric hidden w-10 items-center justify-end gap-1 lg:flex">
            <SourceFork aria-hidden className="size-3" />
            {repo.forks}
            <span className="sr-only">forks</span>
          </span>
          <span className="hidden w-16 justify-end md:flex" title={updated}>
            {updated ? relativeTime(updated) : ""}
          </span>
        </span>
      }
      actions={
        <DimActions>
          <VerbMenu verbs={verbs} label={`Actions for ${repo.fullName}`} />
        </DimActions>
      }
    />
  )
}
