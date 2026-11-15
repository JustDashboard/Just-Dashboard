"use client"

import Link from "next/link"
import { useParams, useRouter } from "next/navigation"
import { createContext, useCallback, useContext, useMemo, useState } from "react"
import { FolderOpen, LockClosed, LockOpen, Logout, Terminal, Trash } from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { DetailPage, useDetailTab, useSiblingSteps, type Step } from "@/components/detail-page"
import { Fact } from "@/components/fact-line"
import { IdentityHeader } from "@/components/identity-header"
import { Address } from "@/components/security/marks"
import { EmptyState, Notice } from "@/components/state"
import { LoadingRows } from "@/components/loading"
import { Status } from "@/components/status-dot"
import { accountHref, reachesRoot, signInSummary } from "@/components/system-users/access"
import { ActivityPanel, HistoryPanel } from "@/components/system-users/account-activity"
import { AccountRail } from "@/components/system-users/account-rail"
import { RemoveAccountDialog } from "@/components/system-users/account-removal"
import { RootPanel } from "@/components/system-users/account-root"
import { AccountSettings } from "@/components/system-users/account-settings"
import { SignInPanel } from "@/components/system-users/account-sign-in"
import { AccountMark, shellProduct } from "@/components/system-users/marks"
import { ReadFailed } from "@/components/system-users/read-state"
import { Button } from "@/components/ui/button"
import { ViewTabList, type TabView } from "@/components/tabs"
import { Tabs, TabsContent } from "@/components/ui/tabs"
import { VerbMenu, type Verb } from "@/components/verbs"
import { usePoll } from "@/hooks/use-poll"
import { ApiError, del, get, patch } from "@/lib/api"
import { bytes, plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { SystemUser, SystemUserDetail } from "@/lib/types"
import { useSessionState, useViewState } from "@/lib/view-state"
import { decodeSegment } from "@/lib/route-params"

type AccountContextValue = {
  user: SystemUserDetail
  /** The account's path, ready to have a route appended. */
  path: string
  /** Sent with every write: says a system account's editing was turned on. */
  query?: { allowSystem: true }
  readOnly: boolean
  refresh: () => void
}

const AccountContext = createContext<AccountContextValue | null>(null)

export function useAccount() {
  const value = useContext(AccountContext)
  if (!value) throw new Error("useAccount outside an account page")
  return value
}

const TABS = ["overview", "settings", "history"] as const
type Tab = (typeof TABS)[number]

type Scope = "all" | "signin" | "root" | "disabled"
const SCOPES: Record<Scope, string> = {
  all: "",
  signin: "can sign in",
  root: "reach root",
  disabled: "disabled",
}

const BACK = { href: "/system-users", label: "System users" }

/**
 * One account, as a place of its own with the list beside it.
 *
 * It was one long page until 0.8.7: how it gets in, how far it reaches, what
 * it is doing, what it has done, and three folded boxes of settings at the
 * foot, each with a Save of its own. The sections an operator opens an account
 * for stay together on the first view (a key is usually changed by somebody
 * about to look at what that key could do), and the two that are read apart
 * from them have views of their own: Settings, a form in one column saved by
 * one bar, and History. The view is in the address, so a link can open an
 * account's settings.
 *
 * From `xl` the accounts stand in a column beside it (`AccountRail`) with the
 * list's own search, filter and order, so the next account is one press away,
 * Alt+↑ and Alt+↓ walk them, and the view on screen goes with you. On a phone
 * the page is the one pane, with the way back and the same two arrows.
 *
 * Deleting is a verb and not a view, because it is rare and it opens its own
 * reading of what would go.
 */
export function AccountPage() {
  const params = useParams<{ name: string }>()
  const name = decodeSegment(params.name)
  const router = useRouter()
  const [tab, setTab] = useDetailTab(TABS, { remember: "system-users.account.tab" })
  const open = useCallback((next: string) => router.push(accountTabHref(next, tab)), [router, tab])
  const path = `/system-users/${encodeURIComponent(name)}`
  const account = usePoll((signal) => get<SystemUserDetail>(path, undefined, signal), 30000, [path])

  const rail = useRail()
  const steps = useSiblingSteps(
    rail.users.map((user) => user.username),
    name,
    open,
  )
  const frame: Frame = {
    steps,
    refresh: account.refresh,
    rail: (
      <AccountRail
        users={rail.users}
        current={name}
        currentUser={account.data}
        loading={rail.loading}
        error={rail.error}
        onRetry={rail.refresh}
        query={rail.query}
        onQuery={rail.setQuery}
        filtered={rail.filtered}
        onClearFilters={rail.clearFilters}
        caption={rail.caption}
        onOpen={open}
      />
    ),
  }

  // Keyed, so another account opens on its own state: no edit allowed on a
  // system account carries over, and its settings are read when first opened.
  return (
    <AccountMain
      key={name}
      frame={frame}
      name={name}
      path={path}
      account={account}
      onChanged={rail.refresh}
      tab={tab}
      setTab={setTab}
    />
  )
}

/** What every state of the page hands the shared frame. */
type Frame = { steps: { previous: Step; next: Step }; refresh: () => void; rail: React.ReactNode }

/** The account's detail frame: the way back, the walk and the rail (`DetailPage`). */
function AccountFrame({
  frame,
  title,
  identity,
  children,
}: {
  frame: Frame
  title: string
  identity?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <DetailPage
      name="Account"
      what="account"
      stateKey="system-users.account.page"
      refresh={frame.refresh}
      steps={frame.steps}
      back={BACK}
      title={title}
      rail={frame.rail}
      identity={identity}
    >
      {children}
    </DetailPage>
  )
}

function accountTabHref(username: string, tab: string) {
  return tab === "overview" ? accountHref(username) : `${accountHref(username)}?tab=${tab}`
}

/**
 * The accounts list, read for the rail: the same switch, filter and search
 * the list was left on (they are its session's), at its cadence and in its
 * order: root, then people, then what packages made.
 */
function useRail() {
  const [showSystem] = useViewState("system-users.show-system", false)
  const [query, setQuery] = useSessionState("system-users.query", "")
  const [scope, setScope] = useSessionState<Scope>("system-users.scope", "all")
  const list = usePoll(
    (signal) => get<SystemUser[]>("/system-users/", { system: showSystem }, signal),
    30000,
    [showSystem],
  )
  const users = useMemo(() => {
    const q = query.trim().toLowerCase()
    const matching = (list.data ?? []).filter((u) => {
      if (scope === "signin" && !(u.signIn.password.allowed || u.signIn.key.allowed)) return false
      if (scope === "root" && !reachesRoot(u)) return false
      if (scope === "disabled" && !u.disabled) return false
      if (!q) return true
      return (
        u.username.toLowerCase().includes(q) ||
        u.comment.toLowerCase().includes(q) ||
        u.groups.some((g) => g.toLowerCase().includes(q))
      )
    })
    const rank = (u: SystemUser) => (u.uid === 0 ? 0 : u.system ? 2 : 1)
    return [...matching].sort((a, b) => rank(a) - rank(b))
  }, [list.data, query, scope])
  const total = list.data?.length ?? 0
  const filtered = scope !== "all" && SCOPES[scope] !== undefined
  const caption = !list.data
    ? "Reading the accounts…"
    : `${filtered || query ? `${users.length} of ${total}` : plural(total, "account")}${filtered ? ` · ${SCOPES[scope]}` : ""}`
  return {
    users,
    loading: list.loading,
    error: list.data ? undefined : list.error,
    refresh: list.refresh,
    query,
    setQuery,
    filtered,
    clearFilters: () => setScope("all"),
    caption,
  }
}

function AccountMain({
  frame,
  name,
  path,
  account,
  onChanged,
  tab,
  setTab,
}: {
  frame: Frame
  name: string
  path: string
  account: ReturnType<typeof usePoll<SystemUserDetail>>
  onChanged: () => void
  tab: Tab
  setTab: (tab: string) => void
}) {
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const [editing, setEditing] = useState(false)
  const [removing, setRemoving] = useState(false)
  // The settings are read when their view is first opened and then stay: an
  // edit made there is still held, and its bar still up, while another view
  // is looked at.
  const [settingsOpened, setSettingsOpened] = useState(tab === "settings")
  if (tab === "settings" && !settingsOpened) setSettingsOpened(true)
  const { data: user, error, refresh: refreshAccount } = account
  const { data: disk } = usePoll(
    (signal) => get<{ bytes: number }>(`${path}/disk`, undefined, signal),
    0,
    [path],
    { enabled: Boolean(user) },
  )
  const refresh = useCallback(() => {
    refreshAccount()
    onChanged()
  }, [refreshAccount, onChanged])
  if (!user) {
    if (error) {
      const missing = error instanceof ApiError && [400, 404].includes(error.status)
      return (
        <AccountFrame frame={frame} title="Account">
          {missing ? (
            <EmptyState
              mark={<AccountMark user={{ username: name, system: false, uid: -1 }} size="lg" />}
              title={`No account named ${name || "that"} on this host`}
              description="It may have been deleted or renamed. Pick another from the list."
              action={
                <Link
                  href="/system-users"
                  className="text-body font-medium text-brand hover:underline"
                >
                  Back to the accounts
                </Link>
              }
            />
          ) : (
            <ReadFailed error={error} onRetry={refreshAccount} />
          )}
        </AccountFrame>
      )
    }
    return (
      <AccountFrame
        frame={frame}
        title={name}
        identity={
          // The line's final shape while the account is read, its command's
          // and menu's places held, so nothing under it moves when it lands.
          <IdentityHeader
            loading
            className="border-b-0 pb-0"
            logo={<AccountMark user={{ username: name, system: false, uid: -1 }} size="lg" />}
            title={<span className="font-mono">{name}</span>}
            state
            command
            menu={false}
          />
        }
      >
        <LoadingRows />
      </AccountFrame>
    )
  }

  const isDaemon = Boolean(user.system && user.uid !== 0)
  const readOnly = isDaemon && !editing
  const query = editing ? ({ allowSystem: true } as const) : undefined

  const setDisabled = (disabled: boolean) => {
    if (!disabled) {
      void (async () => {
        try {
          await patch(path, { disabled: false }, { query })
          notify.success(`${name} can sign in again`)
          refresh()
        } catch (err) {
          notify.error("Could not enable the account", err)
        }
      })()
      return
    }
    confirm({
      title: `Disable ${name}`,
      confirmLabel: "Disable",
      description: (
        <p>
          Closes every way in, SSH keys included, and ends what it is running now. Nothing is
          deleted, and enabling it brings everything back.
        </p>
      ),
      action: async () => {
        await patch(path, { disabled: true, endSessions: true }, { query })
        refresh()
      },
    })
  }

  const endSessions = () =>
    confirm({
      title: `End everything ${name} is running`,
      confirmLabel: "End sessions",
      description: <p>Signs it out everywhere and stops every process it owns.</p>,
      action: async (typed) => {
        await del(`${path}/sessions`, { confirm: typed, query })
        refresh()
      },
    })

  const terminal = () => router.push(`/terminal?as=${encodeURIComponent(name)}`)
  const files = () => router.push(`/files?path=${encodeURIComponent(user.home)}`)
  // One command stands on the line: a terminal as the account where it has a
  // shell, its files where it has none. Everything else is a word in the menu.
  const verbs: Verb[] = [
    ...(user.canLogin ? [{ key: "files", label: "Files", icon: FolderOpen, run: files }] : []),
    ...(readOnly || user.protected
      ? []
      : [
          {
            key: "disable",
            label: user.disabled ? "Enable" : "Disable",
            icon: user.disabled ? LockOpen : LockClosed,
            run: () => setDisabled(!user.disabled),
          },
          { key: "end", label: "End sessions", icon: Logout, run: endSessions },
          {
            key: "delete",
            label: "Delete",
            icon: Trash,
            danger: true,
            run: () => setRemoving(true),
          },
        ]),
  ]

  const state = signInSummary(user)
  const views: TabView<Tab>[] = [
    { key: "overview", label: "Overview" },
    { key: "settings", label: "Settings" },
    { key: "history", label: "History" },
  ]

  return (
    <AccountContext.Provider value={{ user, path, query, readOnly, refresh }}>
      <AccountFrame
        frame={frame}
        title={name}
        identity={
          <>
            <IdentityHeader
              className="animate-rise border-b-0 pb-0"
              logo={<AccountMark user={user} size="lg" />}
              title={
                <span className="flex min-w-0 flex-wrap items-baseline gap-x-2">
                  <span className="font-mono">{user.username}</span>
                  {user.comment && user.comment !== user.username && (
                    <span className="text-body font-normal text-muted-foreground">
                      {user.comment}
                    </span>
                  )}
                </span>
              }
              state={<Status tone={state.tone} label={state.label} />}
              facts={
                <>
                  <Fact className="numeric">uid {user.uid}</Fact>
                  <Fact product={shellProduct(user.shell)} mono>
                    {user.shell}
                  </Fact>
                  <Fact mono>
                    {user.home}
                    {disk && disk.bytes > 0 && (
                      <span className="numeric ml-2 font-sans">{bytes(disk.bytes)}</span>
                    )}
                  </Fact>
                  {user.lastLogin ? (
                    <Fact>
                      <span className="inline-flex min-w-0 items-center gap-1.5">
                        <span className="numeric shrink-0">{relativeTime(user.lastLogin)}</span>
                        {user.lastLoginFrom && (
                          <Address ip={user.lastLoginFrom} className="min-w-0" />
                        )}
                      </span>
                    </Fact>
                  ) : (
                    <Fact>never signed in</Fact>
                  )}
                </>
              }
              command={
                user.canLogin ? (
                  <Button size="sm" onClick={terminal}>
                    <Terminal className="size-4" />
                    Terminal
                  </Button>
                ) : (
                  <Button size="sm" onClick={files}>
                    <FolderOpen className="size-4" />
                    Files
                  </Button>
                )
              }
              menu={
                verbs.length > 0 ? <VerbMenu verbs={verbs} label={`More for ${name}`} /> : false
              }
            />
            {isDaemon && !editing && (
              <Notice title="This account belongs to the system">
                <span className="flex flex-wrap items-center justify-between gap-2">
                  A package made it and expects it as it left it, so it is read-only here.
                  <Button size="xs" variant="outline" onClick={() => setEditing(true)}>
                    Allow changes
                  </Button>
                </span>
              </Notice>
            )}
          </>
        }
      >
        <Tabs value={tab} onValueChange={setTab} className="gap-0">
          <ViewTabList label={`Views of ${name}`} views={views} value={tab} sticky />

          <TabsContent value="overview" className="pt-6">
            <div className="flex min-w-0 flex-col gap-8 pb-6">
              <div className="grid min-w-0 gap-x-10 gap-y-8 2xl:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
                <SignInPanel />
                <RootPanel />
              </div>
              <ActivityPanel />
            </div>
          </TabsContent>

          <TabsContent value="settings" forceMount hidden={tab !== "settings"} className="pt-6">
            {settingsOpened && <AccountSettings />}
          </TabsContent>

          <TabsContent value="history" className="pt-6">
            {tab === "history" && <HistoryPanel />}
          </TabsContent>
        </Tabs>
        {removing && <RemoveAccountDialog onClose={() => setRemoving(false)} />}
        {dialog}
      </AccountFrame>
    </AccountContext.Provider>
  )
}
