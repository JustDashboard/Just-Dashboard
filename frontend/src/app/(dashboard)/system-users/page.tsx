"use client"

import Link from "next/link"
import { useRouter } from "next/navigation"
import { useMemo, useState } from "react"
import {
  ArrowRight,
  Key,
  LockClosed,
  LockOpen,
  SettingsSliders,
  ShieldCheck,
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
import { GroupRule } from "@/components/flow"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
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
import { AccountMark, groupProduct } from "@/components/system-users/marks"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
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
 * The host's accounts, as a table you read down and open.
 *
 * It opened on three report blocks — last sign-ins on an axis, who can become
 * root, whose keys open the host — and the operator asked for what is not needed
 * to go. On a small server all three said "nobody", "never" and "none", and every
 * figure in them is a column here: who can sign in and how, how far they can
 * reach, when they last did. What the blocks were for is the list of findings
 * under the line, which is drawn only when there is something to change.
 *
 * Root is in the list however the system-accounts switch is set. It is a system
 * account by number and the most important account by every other measure, and
 * leaving it out is how this page once said "0 have sign-in routes" over a host
 * where root signs in with a password.
 */
export default function SystemUsersPage() {
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const { host } = useMetrics()
  const wide = useMediaQuery("(min-width: 768px)")
  const [showSystem, setShowSystem] = useViewState("system-users.show-system", false)
  const [query, setQuery] = useSessionState("system-users.query", "")
  const [scope, setScope] = useSessionState<Scope>("system-users.scope", "all")
  const [selected, setSelected] = useState<string[]>([])
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
      disabled: users.filter((u) => u.disabled).length,
      latest: latestSignIn(users),
      findings: accountFindings(users),
    }
  }, [data])

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
    const { results } = await post<BulkResults>("/system-accounts/bulk", {
      action,
      usernames: chosen,
      ...extra,
    })
    reportBulk(results)
    setSelected([])
    refresh()
  }

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

  const row = (user: SystemUser) => (
    <AccountRow
      key={user.username}
      user={user}
      wide={wide}
      selected={chosen.includes(user.username)}
      onToggle={user.system ? undefined : () => toggle(user.username)}
      onOpen={() => router.push(accountHref(user.username))}
    />
  )

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Advanced" title="System users" />

      {data && (
        <HostIdentity
          className="animate-rise"
          mark={platformProduct(host?.platform)}
          fallback={Users}
          title={host?.hostname ?? "Accounts"}
          facts={
            <>
              <span className="numeric">{plural(figures.people, "person", "people")}</span>
              <FactDot />
              <span className={cn("numeric", figures.signIn > 0 && "text-success")}>
                {figures.signIn} can sign in
              </span>
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
          }
          aside={
            <div className="flex flex-wrap items-center gap-2">
              <Button size="sm" variant="outline" asChild>
                <Link href="/system-users/groups">
                  <Users className="size-4" />
                  Groups
                </Link>
              </Button>
              <Button size="sm" variant="outline" asChild>
                <Link href="/system-users/defaults">
                  <SettingsSliders className="size-4" />
                  Defaults
                </Link>
              </Button>
              <Button size="sm" asChild>
                <Link href="/system-users/new">
                  <UserPlus className="size-4" />
                  New account
                </Link>
              </Button>
            </div>
          }
        />
      )}

      {figures.findings.length > 0 && (
        <ul data-slot="account-findings" className="-mt-3 divide-y divide-hairline md:-mt-5">
          {figures.findings.map((finding) => (
            <li
              key={finding.id}
              className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 py-2.5"
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

      {/* A plain block: the table has its own hairlines, and a frame around them
          is one more box saying nothing (§7). */}
      <Panel plain>
        <PanelToolbar>
          {chosen.length > 0 ? (
            <div data-slot="bulk-bar" className="flex min-w-0 flex-wrap items-center gap-2">
              <span className="numeric text-body font-medium">{chosen.length} selected</span>
              <Button size="xs" variant="outline" onClick={() => void bulk("lock")}>
                <LockClosed className="size-3" />
                Lock passwords
              </Button>
              <Button size="xs" variant="outline" onClick={bulkDisable}>
                <LockClosed className="size-3" />
                Disable
              </Button>
              <Button size="xs" variant="outline" onClick={() => void bulk("enable")}>
                <LockOpen className="size-3" />
                Enable
              </Button>
              <Button size="xs" variant="outline" onClick={bulkEnd}>
                End sessions
              </Button>
              <Button size="xs" variant="outline" className="text-destructive" onClick={bulkDelete}>
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
                  containerClassName="sm:w-56"
                />
              )}
              {data && (
                <ChipStrip>
                  <FilterChip selected={scope === "all"} onClick={() => setScope("all")}>
                    All <ChipCount>{data.length}</ChipCount>
                  </FilterChip>
                  <FilterChip selected={scope === "signin"} onClick={() => setScope("signin")}>
                    Can sign in <ChipCount>{figures.signIn}</ChipCount>
                  </FilterChip>
                  <FilterChip selected={scope === "root"} onClick={() => setScope("root")}>
                    Reach root <ChipCount>{figures.root}</ChipCount>
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
              )}
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
        <PanelBody flush>
          {loading && !data && <LoadingPanel />}
          {error && !data && <ErrorState error={error} />}
          {data && rows.length === 0 && (
            <EmptyState
              icon={Users}
              title="No accounts match"
              description="Clear the search or pick All."
            />
          )}
          {data && rows.length > 0 && (
            <Table>
              {wide && (
                <TableHeader>
                  <TableRow>
                    <TableHead className="w-8" />
                    <TableHead>Account</TableHead>
                    <TableHead>Sign-in</TableHead>
                    <TableHead>Root access</TableHead>
                    <TableHead>Last sign-in</TableHead>
                    <TableHead className="w-8" />
                  </TableRow>
                </TableHeader>
              )}
              <TableBody>
                {accounts.map(row)}
                {daemons.length > 0 && (
                  <TableRow className="hover:bg-transparent">
                    <TableCell colSpan={wide ? 6 : 2} className="pt-5 pb-2">
                      <GroupRule label="System accounts" count={daemons.length} />
                    </TableCell>
                  </TableRow>
                )}
                {daemons.map(row)}
              </TableBody>
            </Table>
          )}
        </PanelBody>
      </Panel>
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

/**
 * One account: named readings in columns from `md`, and under the name below it,
 * chosen once rather than drawn twice. A phone has no width for four columns, and
 * a table that scrolls sideways there hid the one that says who can become root.
 */
function AccountRow({
  user,
  wide,
  selected,
  onToggle,
  onOpen,
}: {
  user: SystemUser
  wide: boolean
  selected: boolean
  onToggle?: () => void
  onOpen: () => void
}) {
  const signIn = signInSummary(user)
  const root = rootSummary(user)
  const signInCell = (
    <span className="flex flex-wrap items-center gap-x-3 gap-y-1" title={signIn.detail}>
      {signIn.password || signIn.key ? (
        <>
          {signIn.password && (
            <span
              className={cn(
                "inline-flex items-center gap-1",
                user.noPassword ? "text-destructive" : "text-success",
              )}
            >
              <LockOpen aria-hidden className="size-3.5" />
              {user.noPassword ? "No password" : "Password"}
            </span>
          )}
          {signIn.key && (
            <span className="inline-flex items-center gap-1 text-success">
              <Key aria-hidden className="size-3.5" />
              <span className="numeric">
                {user.sshKeyCount > 0 ? plural(user.sshKeyCount, "key") : "Unread keys"}
              </span>
            </span>
          )}
        </>
      ) : (
        <Status tone={signIn.tone} label={signIn.label} />
      )}
    </span>
  )
  const rootCell = (
    <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
      {root.label !== "None" && root.label !== "Through a group" && (
        <span className={cn("inline-flex items-center gap-1", ROOT_INK[root.tone])}>
          <ShieldCheck aria-hidden className="size-3.5" />
          {root.label}
        </span>
      )}
      {root.groups.map((group) => (
        <span key={group} className="inline-flex items-center gap-1 text-warning">
          {groupProduct(group) ? (
            <ProductGlyph id={groupProduct(group) as string} />
          ) : (
            <ShieldCheck aria-hidden className="size-3.5" />
          )}
          {group}
        </span>
      ))}
      {root.label === "None" && wide && <span className="text-muted-foreground/60">None</span>}
    </span>
  )
  const lastCell = user.lastLogin ? (
    <span className="flex min-w-0 items-center gap-2 text-muted-foreground">
      <span className="numeric shrink-0">{relativeTime(user.lastLogin)}</span>
      {user.lastLoginFrom && <Address ip={user.lastLoginFrom} className="min-w-0" />}
    </span>
  ) : (
    <span className="text-muted-foreground/60">Never</span>
  )
  return (
    <TableRow
      data-slot="account-row"
      onActivate={onOpen}
      className={cn(user.disabled && "opacity-60", selected && "bg-accent/40")}
    >
      <TableCell className="w-8 pr-0 align-top md:align-middle">
        {onToggle && (
          <Checkbox
            aria-label={`Select ${user.username}`}
            checked={selected}
            onCheckedChange={onToggle}
          />
        )}
      </TableCell>
      <TableCell className={cn(!wide && "whitespace-normal")}>
        <span className="flex min-w-0 items-center gap-3">
          <AccountMark user={user} />
          <span className="min-w-0">
            <span className="block truncate font-mono text-body font-medium">{user.username}</span>
            {user.comment && user.comment !== user.username && (
              <span className="block truncate text-hint text-muted-foreground">{user.comment}</span>
            )}
          </span>
        </span>
        {!wide && (
          <span className="mt-2 flex flex-col gap-1.5">
            {signInCell}
            {rootCell}
            {user.lastLogin && lastCell}
          </span>
        )}
      </TableCell>
      {wide && (
        <>
          <TableCell>{signInCell}</TableCell>
          <TableCell>{rootCell}</TableCell>
          <TableCell>{lastCell}</TableCell>
          <TableCell className="pl-0 text-right">
            <Link
              href={accountHref(user.username)}
              aria-label={`Open ${user.username}`}
              className="inline-flex rounded-sm text-muted-foreground focus-ring hover:text-foreground"
            >
              <ArrowRight className="size-3.5" />
            </Link>
          </TableCell>
        </>
      )}
    </TableRow>
  )
}
