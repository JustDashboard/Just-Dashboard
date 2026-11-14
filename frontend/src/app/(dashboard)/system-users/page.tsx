"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { useEffect, useMemo, useRef, useState } from "react"
import {
  ArrowRight,
  Clock,
  FolderOpen,
  Key,
  LockClosed,
  LockOpen,
  SettingsSliders,
  ShieldCheck,
  Terminal,
  Trash,
  UserPlus,
  Users,
} from "@/components/icons"
import { Page, PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelToolbar } from "@/components/panel"
import { Address } from "@/components/security/marks"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { platformProduct, ProductGlyph } from "@/components/product-logo"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow, GroupRule } from "@/components/flow"
import { DimActions } from "@/components/icon-action"
import { EmptyState } from "@/components/state"
import { Status } from "@/components/status-dot"
import {
  accountFindings,
  accountHref,
  latestSignIn,
  reachesRoot,
  rootSummary,
  signInSummary,
  type RootSummary,
} from "@/components/system-users/access"
import { ChoiceRowsWaiting } from "@/components/system-users/group-parts"
import { AccountMark, FaceRun, groupProduct } from "@/components/system-users/marks"
import { AdminOnly, ReadFailed } from "@/components/system-users/read-state"
import { RootPicture } from "@/components/system-users/root-picture"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { VerbMenu, type Verb } from "@/components/verbs"
import { useMetrics } from "@/hooks/use-metrics"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import { get, post } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { BulkResults, SystemUser } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useSessionState, useViewState } from "@/lib/view-state"

type Scope = "all" | "signin" | "root" | "disabled"

const ROOT_INK: Record<RootSummary["tone"], string> = {
  danger: "text-destructive",
  warning: "text-warning",
  default: "text-foreground",
  muted: "text-muted-foreground",
}

/** A search box is for a list too long to read; two accounts are not one. */
const SEARCH_FROM = 8

/**
 * The host's accounts: who can be root as a picture, what to change as a line
 * each, then every account as a row to open.
 *
 * It opened on three report blocks (last sign-ins on an axis, who can become
 * root, whose keys open the host) and the operator asked for what is not needed
 * to go: on a small server all three said "nobody", "never" and "none". What
 * the blocks were for is the list of findings under the line, drawn only when
 * there is something to change, and every figure in them is a column of the
 * rows. The picture came back in 0.8.7 as one thing: the ways to root, which is
 * the question a table of seven accounts answers one row at a time.
 *
 * An account is a row to open, not a table row read down a column: it is one
 * line at every width, its readings in columns from `xl` and on two lines of
 * their own under the name below it, so no account is taller than another.
 *
 * Root is in the list however the system-accounts switch is set. It is a system
 * account by number and the most important account by every other measure, and
 * leaving it out is how this page once said "0 have sign-in routes" over a host
 * where root signs in with a password.
 */
export default function SystemUsersPage() {
  return (
    <AdminOnly title="System users">
      <SystemUsers />
    </AdminOnly>
  )
}

function SystemUsers() {
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const { host } = useMetrics()
  const wide = useMediaQuery("(min-width: 1280px)")
  const [showSystem, setShowSystem] = useViewState("system-users.show-system", false)
  const [query, setQuery] = useSessionState("system-users.query", "")
  const [scope, setScope] = useSessionState<Scope>("system-users.scope", "all")
  // How many findings there were and how many accounts reached root at the
  // last read, so both wait at the height they will have and the list under
  // them does not move when the read lands.
  const [expected, setExpected] = useViewState("system-users.shape", { findings: 0, root: 3 })
  const [selected, setSelected] = useState<string[]>([])
  const [acting, setActing] = useState(false)
  const list = useRef<HTMLElement>(null)
  const { data, error, loading, refresh } = usePoll(
    (signal) => get<SystemUser[]>("/system-users/", { system: showSystem }, signal),
    30000,
    [showSystem],
  )

  const figures = useMemo(() => {
    const users = data ?? []
    return {
      people: users.filter((u) => !u.system).length,
      signIn: users.filter((u) => u.signIn.password.allowed || u.signIn.key.allowed).length,
      root: users.filter(reachesRoot).length,
      becomeRoot: users.filter((u) => u.uid !== 0 && reachesRoot(u)).length,
      disabled: users.filter((u) => u.disabled).length,
      latest: latestSignIn(users),
      findings: accountFindings(users),
    }
  }, [data])
  const shape = { findings: figures.findings.length, root: figures.becomeRoot }
  useEffect(() => {
    if (data) setExpected({ findings: shape.findings, root: shape.root })
  }, [data, shape.findings, shape.root, setExpected])

  const rows = useMemo(() => {
    const q = query.trim().toLowerCase()
    return (data ?? []).filter((u) => {
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
  }, [data, query, scope])

  // Root and people lead, and the daemons are a shelf of their own under them:
  // forty package accounts are not read the way three people are.
  const accounts = rows
    .filter((u) => !u.system || u.uid === 0)
    .sort((a, b) => Number(b.uid === 0) - Number(a.uid === 0))
  const daemons = rows.filter((u) => u.system && u.uid !== 0)
  const selectable = accounts.filter((u) => !u.system).map((u) => u.username)
  const chosen = selected.filter((name) => selectable.includes(name))

  const bulk = async (action: string, extra?: object) => {
    // One at a time: a second press while the first is on its way would lock
    // or end the same accounts twice.
    if (acting) return
    setActing(true)
    try {
      const { results } = await post<BulkResults>("/system-accounts/bulk", {
        action,
        usernames: chosen,
        ...extra,
      })
      reportBulk(results)
      setSelected([])
      refresh()
    } finally {
      setActing(false)
    }
  }
  const bulkNow = (action: string) =>
    void bulk(action).catch((err) => notify.error("That did not go through", err))

  const bulkDisable = () =>
    confirm({
      title: `Disable ${plural(chosen.length, "account")}`,
      confirmLabel: "Disable",
      description: (
        <p>
          Closes every way in for {chosen.join(", ")}, SSH keys included, and ends what they are
          running. Nothing is deleted, and enabling them brings everything back.
        </p>
      ),
      action: () => bulk("disable", { endSessions: true }),
    })

  const bulkEnd = () =>
    confirm({
      title: `End everything ${chosen.join(", ")} ${chosen.length === 1 ? "is" : "are"} running`,
      confirmLabel: "End sessions",
      description: <p>Signs them out everywhere and stops every process they own.</p>,
      action: () => bulk("end-sessions"),
    })

  const bulkDelete = () =>
    confirm({
      title: `Delete ${plural(chosen.length, "account")}`,
      confirmLabel: "Delete",
      description: (
        <p className="text-destructive">
          Removes {chosen.join(", ")} and ends what they are running. Their home directories stay on
          the server. To remove a home as well, delete the account from its own page.
        </p>
      ),
      action: async (typed) => {
        const { results } = await post<BulkResults>(
          "/system-accounts/bulk/delete",
          { usernames: chosen, endSessions: true },
          { confirm: typed },
        )
        reportBulk(results)
        setSelected([])
        refresh()
      },
    })

  const toggle = (name: string) =>
    setSelected((now) => (now.includes(name) ? now.filter((n) => n !== name) : [...now, name]))

  // A press on the picture's "more" narrows the rows to those accounts and
  // brings them into view.
  const narrow = (next: Scope) => {
    setScope(next)
    list.current?.scrollIntoView({ behavior: "smooth", block: "start" })
  }

  const row = (user: SystemUser) => (
    <AccountRow
      key={user.username}
      user={user}
      wide={wide}
      selecting={selectable.length > 0}
      selected={chosen.includes(user.username)}
      onToggle={user.system ? undefined : () => toggle(user.username)}
      go={(href) => router.push(href)}
    />
  )

  const menu: Verb[] = [
    {
      key: "groups",
      label: "Groups",
      icon: Users,
      run: () => router.push("/system-users/groups"),
    },
    {
      key: "defaults",
      label: "Account defaults",
      icon: SettingsSliders,
      run: () => router.push("/system-users/defaults"),
    },
  ]

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Advanced" title="System users" />

      <HostIdentity
        mark={platformProduct(host?.platform)}
        fallback={Users}
        title={host?.hostname ?? "Accounts"}
        state={
          data ? (
            <Status
              tone={figures.signIn > 0 ? "running" : "unknown"}
              label={`${figures.signIn} can sign in`}
            />
          ) : undefined
        }
        facts={
          data ? (
            <>
              <span className="numeric">{plural(figures.people, "person", "people")}</span>
              <FactDot />
              <span className="numeric">{figures.root} reach root</span>
              {figures.latest && (
                <>
                  <FactDot />
                  <span className="inline-flex min-w-0 items-center gap-1.5">
                    <span className="shrink-0 text-foreground">{figures.latest.username}</span>
                    <span className="numeric shrink-0">
                      {relativeTime(figures.latest.lastLogin)}
                    </span>
                    {figures.latest.lastLoginFrom && (
                      <Address ip={figures.latest.lastLoginFrom} className="min-w-0" />
                    )}
                  </span>
                </>
              )}
            </>
          ) : error ? (
            <span>Not read</span>
          ) : (
            <TextShimmer>Reading the accounts…</TextShimmer>
          )
        }
        command={
          <Button size="sm" asChild>
            <Link href="/system-users/new">
              <UserPlus className="size-4" />
              New account
            </Link>
          </Button>
        }
        menu={<VerbMenu verbs={menu} label="Groups and defaults" />}
      />

      {!data && !error && expected.findings > 0 && (
        <ul aria-hidden className="-mt-3 divide-y divide-hairline md:-mt-5">
          {Array.from({ length: expected.findings }, (_, index) => (
            <li key={index} className="flex h-[38px] items-center">
              <span className="h-3 w-64 max-w-full animate-pulse rounded-md bg-muted" />
            </li>
          ))}
        </ul>
      )}
      {figures.findings.length > 0 && (
        <ul data-slot="account-findings" className="-mt-3 divide-y divide-hairline md:-mt-5">
          {figures.findings.map((finding) => (
            <li
              key={finding.id}
              // A set height, so the waiting lines are as tall as these.
              className="flex min-h-[38px] min-w-0 flex-wrap items-center gap-x-3 gap-y-1 py-2"
            >
              <Status tone={finding.tone} label={finding.title} className="font-medium" />
              {finding.detail && (
                <span className="min-w-0 text-hint text-muted-foreground">{finding.detail}</span>
              )}
              <Link
                href={finding.href}
                className="ml-auto inline-flex shrink-0 items-center gap-1 rounded-sm text-hint font-medium text-foreground focus-ring hover:underline"
              >
                {finding.action}
                <ArrowRight className="size-3" />
              </Link>
            </li>
          ))}
        </ul>
      )}

      {!(error && !data) && (
        <RootPicture
          users={data}
          loading={!data}
          expected={expected.root}
          onMore={() => narrow("root")}
        />
      )}

      {error && !data ? (
        <ReadFailed error={error} onRetry={refresh} />
      ) : (
        /* A plain block: each row owns its edge, and a frame around them is one
         more box saying nothing (§7). */
        <Panel plain ref={list} className="scroll-mt-6">
          <PanelToolbar>
            {chosen.length > 0 ? (
              <div data-slot="bulk-bar" className="flex min-w-0 flex-wrap items-center gap-2">
                <span className="numeric text-body font-medium">{chosen.length} selected</span>
                <Button
                  size="xs"
                  variant="outline"
                  disabled={acting}
                  onClick={() => bulkNow("lock")}
                >
                  <LockClosed className="size-3" />
                  Lock passwords
                </Button>
                <Button size="xs" variant="outline" disabled={acting} onClick={bulkDisable}>
                  <LockClosed className="size-3" />
                  Disable
                </Button>
                <Button
                  size="xs"
                  variant="outline"
                  disabled={acting}
                  onClick={() => bulkNow("enable")}
                >
                  <LockOpen className="size-3" />
                  Enable
                </Button>
                <Button size="xs" variant="outline" disabled={acting} onClick={bulkEnd}>
                  End sessions
                </Button>
                <Button
                  size="xs"
                  variant="outline"
                  className="text-destructive"
                  disabled={acting}
                  onClick={bulkDelete}
                >
                  <Trash className="size-3" />
                  Delete
                </Button>
                <Button size="xs" variant="ghost" onClick={() => setSelected([])}>
                  Clear
                </Button>
              </div>
            ) : (
              <>
                {(data?.length ?? 0) >= SEARCH_FROM && (
                  <SearchInput
                    value={query}
                    onChange={(event) => setQuery(event.target.value)}
                    placeholder="Name or group"
                    aria-label="Search the accounts"
                    containerClassName="sm:w-56"
                  />
                )}
                <ChipStrip>
                  <FilterChip selected={scope === "all"} onClick={() => setScope("all")}>
                    All {data && <ChipCount>{data.length}</ChipCount>}
                  </FilterChip>
                  <FilterChip selected={scope === "signin"} onClick={() => setScope("signin")}>
                    Can sign in {data && <ChipCount>{figures.signIn}</ChipCount>}
                  </FilterChip>
                  <FilterChip selected={scope === "root"} onClick={() => setScope("root")}>
                    Reach root {data && <ChipCount>{figures.root}</ChipCount>}
                  </FilterChip>
                  {figures.disabled > 0 && (
                    <FilterChip
                      selected={scope === "disabled"}
                      onClick={() => setScope("disabled")}
                    >
                      Disabled <ChipCount>{figures.disabled}</ChipCount>
                    </FilterChip>
                  )}
                </ChipStrip>
                <FilterChip
                  className="sm:ml-auto"
                  selected={showSystem}
                  onClick={() => setShowSystem(!showSystem)}
                >
                  System accounts
                </FilterChip>
              </>
            )}
          </PanelToolbar>
          <PanelBody flush className="pt-3">
            {loading && !data && <ChoiceRowsWaiting rows={5} />}
            {data && rows.length === 0 && (
              <EmptyState
                mark={<FaceRun names={["ada", "lin", "sam"]} max={3} />}
                title="No accounts match"
                description="Clear the search or pick All."
                action={
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => {
                      setQuery("")
                      setScope("all")
                    }}
                  >
                    Show all accounts
                  </Button>
                }
              />
            )}
            {data && rows.length > 0 && (
              <div className="flex min-w-0 flex-col gap-2">
                {accounts.length > 0 && (
                  <ChoiceList aria-label="Accounts">{accounts.map(row)}</ChoiceList>
                )}
                {daemons.length > 0 && (
                  <>
                    <div className="pt-4 pb-1">
                      <GroupRule label="System accounts" count={daemons.length} />
                    </div>
                    <ChoiceList aria-label="System accounts">{daemons.map(row)}</ChoiceList>
                  </>
                )}
              </div>
            )}
          </PanelBody>
        </Panel>
      )}
      {dialog}
    </Page>
  )
}

function reportBulk(results: BulkResults["results"]) {
  const failed = results.filter((r) => r.error)
  if (failed.length === 0) {
    notify.success(`Done for ${plural(results.length, "account")}`)
    return
  }
  notify.error(
    `${failed.length} of ${plural(results.length, "account")} refused`,
    new Error(failed.map((r) => `${r.username}: ${r.error}`).join("; ")),
  )
}

/** How the account gets in, as marks and words on one line. */
function SignInReading({ user }: { user: SystemUser }) {
  const signIn = signInSummary(user)
  return (
    <span
      data-slot="account-sign-in"
      className="flex min-w-0 items-center gap-x-3 overflow-hidden whitespace-nowrap"
      title={signIn.detail}
    >
      {signIn.password || signIn.key ? (
        <>
          {signIn.password && (
            <span
              className={cn(
                "inline-flex shrink-0 items-center gap-1",
                user.noPassword ? "text-destructive" : "text-success",
              )}
            >
              <LockOpen aria-hidden className="size-3.5" />
              {user.noPassword ? "No password" : "Password"}
            </span>
          )}
          {signIn.key && (
            <span className="inline-flex shrink-0 items-center gap-1 text-success">
              <Key aria-hidden className="size-3.5" />
              <span className="numeric">
                {user.sshKeyCount > 0 ? plural(user.sshKeyCount, "key") : "Unread keys"}
              </span>
            </span>
          )}
        </>
      ) : (
        <Status tone={signIn.tone} label={signIn.label} className="font-normal" />
      )}
    </span>
  )
}

/** How far the account reaches, the strongest way first, then the groups that are root by another name. */
function RootReading({ user, none }: { user: SystemUser; none: string }) {
  const root = rootSummary(user)
  return (
    <span
      data-slot="account-root"
      className="flex min-w-0 items-center gap-x-3 overflow-hidden whitespace-nowrap"
    >
      {root.label !== "None" && root.label !== "Through a group" && (
        <span className={cn("inline-flex shrink-0 items-center gap-1", ROOT_INK[root.tone])}>
          <ShieldCheck aria-hidden className="size-3.5" />
          {root.label}
        </span>
      )}
      {root.groups.map((group) => (
        <span key={group} className="inline-flex shrink-0 items-center gap-1 text-warning">
          {groupProduct(group) ? (
            <ProductGlyph id={groupProduct(group) as string} />
          ) : (
            <ShieldCheck aria-hidden className="size-3.5" />
          )}
          {group}
        </span>
      ))}
      {root.label === "None" && <span className="text-muted-foreground/60">{none}</span>}
    </span>
  )
}

/**
 * One account on one line: who it is, its name over its number, and how it
 * gets in, how far it reaches and when it last signed in, each in a column of
 * its own from `xl`. Narrower there is no width for three columns beside a
 * name, so the first two take a line each under it (every account has both,
 * so every card is the same height) and the last sign-in ends the first.
 */
function AccountRow({
  user,
  wide,
  selecting,
  selected,
  onToggle,
  go,
}: {
  user: SystemUser
  wide: boolean
  /** The list has accounts that can be selected, so every row keeps the box's place. */
  selecting: boolean
  selected: boolean
  onToggle?: () => void
  go: (href: string) => void
}) {
  const href = accountHref(user.username)
  const last = user.lastLogin ? (
    <span className="flex min-w-0 items-center gap-2 text-muted-foreground">
      <span className="numeric shrink-0">{relativeTime(user.lastLogin)}</span>
      {wide && user.lastLoginFrom && <Address ip={user.lastLoginFrom} className="min-w-0" />}
    </span>
  ) : (
    <span className="text-muted-foreground/60">{wide ? "Never" : "Never signed in"}</span>
  )
  const verbs: Verb[] = [
    ...(user.canLogin
      ? [
          {
            key: "terminal",
            label: "Terminal",
            icon: Terminal,
            run: () => go(`/terminal?as=${encodeURIComponent(user.username)}`),
          },
        ]
      : []),
    {
      key: "files",
      label: "Files",
      icon: FolderOpen,
      run: () => go(`/files?path=${encodeURIComponent(user.home)}`),
    },
    {
      key: "settings",
      label: "Settings",
      icon: SettingsSliders,
      run: () => go(`${href}?tab=settings`),
    },
    { key: "history", label: "History", icon: Clock, run: () => go(`${href}?tab=history`) },
  ]
  return (
    <ChoiceRow
      href={href}
      verb={`Open ${user.username}`}
      className={cn(user.disabled && "opacity-60", selected && "bg-accent/40")}
      leading={
        <span className="flex items-center gap-3">
          {selecting && (
            <span className="flex w-4 justify-center">
              {onToggle && (
                <Checkbox
                  aria-label={`Select ${user.username}`}
                  checked={selected}
                  onCheckedChange={onToggle}
                />
              )}
            </span>
          )}
          <AccountMark user={user} />
        </span>
      }
      title={<span className="font-mono">{user.username}</span>}
      description={
        <>
          <span className="numeric font-mono">uid {user.uid}</span>
          {user.comment && user.comment !== user.username && ` · ${user.comment}`}
        </>
      }
      trailing={
        wide ? (
          <span className="flex min-w-0 items-center gap-5 text-xs">
            <span className="flex w-40 min-w-0">
              <SignInReading user={user} />
            </span>
            <span className="flex w-56 min-w-0">
              <RootReading user={user} none="None" />
            </span>
            <span data-slot="account-last" className="flex w-48 min-w-0">
              {last}
            </span>
          </span>
        ) : undefined
      }
      actions={
        <DimActions>
          <VerbMenu verbs={verbs} label={`More for ${user.username}`} />
        </DimActions>
      }
    >
      {!wide && (
        <div className="grid min-w-0 gap-1 text-xs">
          <div className="flex h-5 min-w-0 items-center justify-between gap-3">
            <SignInReading user={user} />
            <span className="shrink-0">{last}</span>
          </div>
          <div className="flex h-5 min-w-0 items-center">
            <RootReading user={user} none="No root access" />
          </div>
        </div>
      )}
    </ChoiceRow>
  )
}
