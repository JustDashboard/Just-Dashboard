"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { Logout, RefreshClockwise, SignIn, Slash } from "@/components/icons"
import { get, post, ApiError } from "@/lib/api"
import { networkOf } from "@/lib/clients"
import { plural, relativeTime, timestamp } from "@/lib/format"
import type { MetricsState } from "@/lib/metrics-store"
import { notify } from "@/lib/toast"
import type { AttackSummary, Attacker, LoginRecord, LoginSession } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { useMetricsValue } from "@/hooks/use-metrics"
import { usePoll, type PollState } from "@/hooks/use-poll"
import { InitialsMark } from "@/components/account/user-avatar"
import { NETWORK_GLYPH } from "@/components/client-mark"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { FactDot, HostIdentity } from "@/components/metrics/host-identity"
import { deniedSources } from "@/components/network/firewall-reading"
import { PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { ProductGlyph, ProductLogo, platformProduct } from "@/components/product-logo"
import { RankList, RankSkeleton, type RankItem } from "@/components/rank-list"
import { addressVerbs, blockAddress } from "@/components/security/address-verbs"
import { LoginsPicture } from "@/components/security/logins-picture"
import {
  accountsOf,
  attemptsWords,
  idleWords,
  originsOf,
  recordKey,
  refusedBy,
  signedInWords,
  windowWords,
} from "@/components/security/logins-reading"
import { Address } from "@/components/security/marks"
import { useSecurity } from "@/components/security/security-context"
import { ErrorState } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { VerbMenu, type Verb } from "@/components/verbs"
import { useHeldList } from "@/components/workspace/held-list"
import { useFilterHistory } from "@/components/workspace/history"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"

const hostOf = (state: MetricsState) => state.host

/** The record is there and the tool that reads it is not: a missing package, not a failure. */
const noRecord = (error: Error | undefined) =>
  error instanceof ApiError && error.code === "login_history_unavailable"

/**
 * Who is on this machine, who has been, and who is trying: the open sessions
 * as a snapshot, the failed-login record folded by address (the question that
 * matters on an exposed host), and the logins the host has kept in wtmp all
 * along.
 *
 * It opens on the server and what the three reads say about it, then draws
 * them as one picture (`logins-picture.tsx`). Under it each is a list of its
 * own kind: a session is a thing you end, so it is a card with its verb in
 * the menu; the attackers are a ranking, so they are a `RankList` with their
 * figures in columns and the block in each row's menu; the record is read
 * down its columns, so it is the page's one framed table.
 */
export function LoginsPanels() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const { firewall, refreshFirewall } = useSecurity()
  const host = useMetricsValue(hostOf)
  const sessions = usePoll(
    (signal) => get<LoginSession[]>("/ssh-sessions", undefined, signal),
    10000,
  )
  const history = usePoll((signal) => get<LoginRecord[]>("/logins", { limit: 100 }, signal), 60000)
  // Failed attempts are admin-only: btmp records whatever was typed at a
  // login prompt, and what people type at a login prompt is sometimes their
  // password in the username field.
  const attackers = usePoll<AttackSummary>(
    (signal) => get("/logins/attackers", { top: 25 }, signal),
    120000,
    [],
    { enabled: admin },
  )

  const list = useMemo(() => sessions.data ?? [], [sessions.data])
  const records = useMemo(() => history.data ?? [], [history.data])
  const attacks = attackers.data
  const origins = useMemo(() => originsOf(list), [list])
  const accounts = useMemo(() => accountsOf(list, records), [list, records])
  const denied = useMemo(() => deniedSources(firewall), [firewall])
  const refused = useCallback((address: string) => refusedBy(address, denied), [denied])

  const refresh = () => {
    sessions.refresh()
    history.refresh()
    attackers.refresh()
    refreshFirewall()
    window.dispatchEvent(new Event("jd:logins-refresh"))
  }
  // The picture waits for all it draws, so it lands once rather than a column at a time.
  const waiting =
    (sessions.loading && !sessions.data && !sessions.error) ||
    (history.loading && !history.data && !history.error) ||
    (admin && attackers.loading && !attackers.data && !attackers.error)

  return (
    <Workspace name="Logins" memory openItems={false} refresh={refresh}>
      <PageContext eyebrow="Security" title="Logins" />

      <LoginsIdentity
        hostname={host?.hostname}
        platform={host?.platform}
        sessions={sessions}
        history={history}
        attackers={attackers}
        admin={admin}
        refresh={refresh}
      />

      <LoginsPicture
        origins={origins}
        accounts={accounts}
        attacks={attacks}
        refused={refused}
        unread={Boolean(sessions.error && !sessions.data)}
        loading={waiting}
      />

      <CurrentSessions poll={sessions} />
      {admin && <AttackersPanel poll={attackers} refused={refused} onBlocked={refreshFirewall} />}
      <LoginHistoryPanel history={history} />
    </Workspace>
  )
}

/**
 * The line the page opens on: this server, how many are signed in and how
 * many addresses are trying as its state, then the last login and the failed
 * attempts as its facts. They were four tiles, each a count of a list under
 * them.
 */
function LoginsIdentity({
  hostname,
  platform,
  sessions,
  history,
  attackers,
  admin,
  refresh,
}: {
  hostname: string | undefined
  platform: string | undefined
  sessions: PollState<LoginSession[]>
  history: PollState<LoginRecord[]>
  attackers: PollState<AttackSummary>
  admin: boolean
  refresh: () => void
}) {
  const list = sessions.data
  const remote = list?.filter((session) => session.isSsh).length ?? 0
  const attacks = attackers.data
  const latest = history.data?.find((record) => record.kind === "login" && record.loginTime)
  return (
    <HostIdentity
      mark={platformProduct(platform)}
      fallback={SignIn}
      label="Logins on this server"
      title={<span className="truncate">{hostname || "This server"}</span>}
      state={
        list ? (
          <>
            <Status
              tone={list.length > 0 ? "running" : "stopped"}
              label={signedInWords(list.length)}
            />
            {attacks && attacks.addresses > 0 && (
              <Status
                tone="warning"
                label={`${plural(attacks.addresses, "address", "addresses")} trying`}
              />
            )}
          </>
        ) : sessions.error ? (
          <Status tone="warning" label="Sessions unread" />
        ) : (
          <Skeleton className="h-4 w-24" />
        )
      }
      facts={
        !list && !sessions.error ? (
          // Two lines on a phone, where the facts wrap to two.
          <>
            <Skeleton className="h-4 w-72 max-w-full" />
            <Skeleton className="h-4 w-40 sm:hidden" />
          </>
        ) : (
          <>
            {latest ? (
              <span className="inline-flex min-w-0 items-center gap-1.5">
                <span>last login</span>
                <span className="font-medium text-foreground">{latest.user}</span>
                <span className="numeric" title={timestamp(latest.loginTime)}>
                  {relativeTime(latest.loginTime)}
                </span>
                <span>from</span>
                <Address ip={latest.from || "the console"} className="text-foreground" />
              </span>
            ) : noRecord(history.error) ? (
              <span>no login record this server can read</span>
            ) : history.error && !history.data ? (
              <span className="text-warning">login record unread</span>
            ) : history.data ? (
              <span>no login on record</span>
            ) : (
              <span>reading the login record</span>
            )}
            {remote > 0 && (
              <>
                <FactDot />
                <span className="numeric">{remote} over ssh</span>
              </>
            )}
            <FactDot />
            {!admin ? (
              <span>failed attempts: administrators only</span>
            ) : attacks ? (
              <span className="numeric">
                <span className={cn("font-medium", attacks.attempts > 0 && "text-foreground")}>
                  {attemptsWords(attacks)}
                </span>{" "}
                failed {attacks.attempts === 1 ? "attempt" : "attempts"} in{" "}
                {windowWords(attacks.windowHours)}
              </span>
            ) : noRecord(attackers.error) ? (
              <span>no failed-login record on this server</span>
            ) : attackers.error ? (
              <span className="text-warning">failed attempts unread</span>
            ) : (
              <span>counting failed attempts</span>
            )}
          </>
        )
      }
      command={<WorkspaceHelp compact />}
      menu={
        <VerbMenu
          label="Logins actions"
          verbs={[{ key: "refresh", label: "Refresh", icon: RefreshClockwise, run: refresh }]}
        />
      }
    />
  )
}

/** A section's count, at the far end of its title line. */
function Count({ children }: { children: React.ReactNode }) {
  return <span className="numeric text-hint text-muted-foreground">{children}</span>
}

/** One line in a section that has nothing to list, at the height of the row that would stand there. */
function Nothing({ children }: { children: React.ReactNode }) {
  return (
    <p className="flex min-h-14.5 items-center rounded-xl border border-dashed px-4 text-body text-muted-foreground">
      {children}
    </p>
  )
}

function CurrentSessions({ poll }: { poll: PollState<LoginSession[]> }) {
  const { can } = useAuth()
  // The route is behind both: ending a session is destructive as well as an
  // administrator's, and a session narrowed to one of them was offered a
  // verb the server refused.
  const mayEnd = can("system.admin") && can("destructive")
  const { confirm, dialog } = useConfirm()
  const { data, error, loading, refresh } = poll
  const sessions = data ?? []

  const end = (session: LoginSession) =>
    confirm({
      title: `End ${session.user}'s session`,
      confirmLabel: "End session",
      description: (
        <p>
          The session on <b>{session.tty}</b> from <b>{session.from || "the console"}</b> is hung
          up. Anything it is running in the foreground stops with it: a long job that was not
          started under tmux or nohup will not survive.
        </p>
      ),
      action: async () => {
        await post(`/ssh-sessions/${session.pid}/disconnect`, {})
        refresh()
      },
    })

  return (
    <>
      <Panel plain id="sessions" className="scroll-mt-6">
        <PanelHeader title="Signed in" actions={data && <Count>{sessions.length}</Count>} />
        <PanelBody>
          {loading && !data && !error ? (
            <Skeleton className="h-14.5 rounded-xl" />
          ) : error && !data ? (
            <ErrorState error={error} onRetry={refresh} retryable />
          ) : sessions.length === 0 ? (
            <Nothing>Nobody holds a shell on this server.</Nothing>
          ) : (
            <ChoiceList aria-label="Open sessions">
              {sessions.map((session, i) => (
                <ChoiceRow
                  key={`${session.user}-${session.tty}-${i}`}
                  // A session opens nothing: it is a card for its verb.
                  disabled
                  leading={<InitialsMark name={session.user} size="md" className="rounded-lg" />}
                  title={session.user}
                  verb={session.user}
                  description={
                    <span className="flex min-w-0 items-center gap-1.5">
                      <Address ip={session.from || "console"} />
                      <span className="shrink-0 font-mono">
                        · {session.tty} · {session.isSsh ? "ssh" : "local"}
                      </span>
                    </span>
                  }
                  trailing={
                    <span className="flex flex-col items-end text-right leading-snug">
                      <span
                        className="numeric text-body"
                        title={session.loginTime ? timestamp(session.loginTime) : undefined}
                      >
                        {session.loginTime ? relativeTime(session.loginTime) : "—"}
                      </span>
                      <span className="text-hint text-muted-foreground">
                        {idleWords(session.idle)}
                      </span>
                    </span>
                  }
                  actions={
                    !mayEnd ? undefined : session.pid ? (
                      <VerbMenu
                        label={`Actions for ${session.user} on ${session.tty}`}
                        verbs={[
                          {
                            key: "end",
                            label: "End session",
                            icon: Logout,
                            danger: true,
                            run: () => end(session),
                          },
                        ]}
                      />
                    ) : (
                      // A session the host gave no process for cannot be
                      // ended; its figures still stand under its neighbours'.
                      <span aria-hidden className="size-7" />
                    )
                  }
                />
              ))}
            </ChoiceList>
          )}
        </PanelBody>
      </Panel>
      {dialog}
    </>
  )
}

/**
 * Who is trying, folded from the failed-login record.
 *
 * The failed listing is five hundred lines of the same three addresses, and a
 * list that long answers nothing. Folded by address it becomes the one
 * question worth asking of btmp: which addresses are working through this
 * host, with which account names ("root, admin, ubuntu" is a scanner, a
 * single real account name is somebody who knows the machine), ranked by how
 * hard each is trying with its share of all of it, and the block that answers
 * it in the row's menu. An address the firewall already turns away says so
 * and is not offered a second rule.
 */
function AttackersPanel({
  poll,
  refused,
  onBlocked,
}: {
  poll: PollState<AttackSummary>
  refused: (address: string) => boolean
  onBlocked: () => void
}) {
  const router = useRouter()
  const [blocking, setBlocking] = useState<string | null>(null)
  // Shown as blocked from the moment the rule is in, not from the firewall's next read.
  const [blocked, setBlocked] = useState<string[]>([])
  const { data, error, loading, refresh } = poll

  const block = async (ip: string) => {
    if (blocking) return
    setBlocking(ip)
    try {
      await blockAddress(ip, "failed logins")
      setBlocked((held) => [...held, ip])
      notify.success(`${ip} blocked at the firewall`, {
        description: "The deny rule sits in front of every allow and does not expire.",
      })
      onBlocked()
      refresh()
    } catch (err) {
      notify.error("Could not add the rule", err)
    } finally {
      setBlocking(null)
    }
  }

  const items: RankItem[] = (data?.attackers ?? []).map((attacker) => {
    const held = refused(attacker.address) || blocked.includes(attacker.address)
    return attackerItem({
      attacker,
      total: data?.attempts ?? 0,
      held,
      verbs: addressVerbs({
        ip: attacker.address,
        block: held ? undefined : () => void block(attacker.address),
        blocking: blocking === attacker.address,
        navigate: (href) => router.push(href),
      }).map((verb) => ({ ...verb, inline: false })),
    })
  })

  return (
    <Panel plain id="attackers" className="scroll-mt-6">
      <PanelHeader
        title="Attackers"
        actions={
          data && (
            <Count>
              {data.attempts === 0
                ? `none in ${windowWords(data.windowHours)}`
                : `${attemptsWords(data)} ${data.attempts === 1 ? "attempt" : "attempts"} from ${plural(data.addresses, "address", "addresses")}${data.since ? ` · since ${relativeTime(data.since)}` : ""}`}
            </Count>
          )
        }
      />
      <PanelBody flush>
        {noRecord(error) ? (
          <NoRecord tool="lastb" record="failed-login record" />
        ) : error && !data ? (
          <ErrorState error={error} onRetry={refresh} retryable className="mt-2" />
        ) : loading && !data ? (
          <RankSkeleton rows={3} />
        ) : (
          <RankList
            items={items}
            emptyLabel={
              data
                ? `No failed attempt in the last ${windowWords(data.windowHours)}.`
                : "No failed attempt on record."
            }
          />
        )}
      </PanelBody>
    </Panel>
  )
}

/** One attacking address as a ranked row: where it is, what it tried, its verbs, its figures. */
function attackerItem({
  attacker,
  total,
  held,
  verbs,
}: {
  attacker: Attacker
  total: number
  held: boolean
  verbs: Verb[]
}): RankItem {
  const network = networkOf(attacker.address)
  const Glyph = NETWORK_GLYPH[network.kind]
  return {
    key: attacker.address,
    mark: network.product ? (
      <ProductGlyph id={network.product} />
    ) : (
      <Glyph className="size-3.5 text-muted-foreground" />
    ),
    label: attacker.address,
    hint: attacker.users.length ? `tried ${attacker.users.join(", ")}` : "no account recorded",
    title: `${attacker.address} (${network.label}): first ${timestamp(attacker.first)}, last ${timestamp(attacker.last)}`,
    trailing: (
      <span className="flex items-center gap-3">
        {held && (
          <span className="inline-flex items-center gap-1 text-hint text-muted-foreground">
            <Slash aria-hidden className="size-3" />
            blocked
          </span>
        )}
        <span
          className="numeric w-24 text-right text-hint text-muted-foreground max-sm:hidden"
          title={`First ${timestamp(attacker.first)} · last ${timestamp(attacker.last)}`}
        >
          {relativeTime(attacker.last)}
        </span>
        <VerbMenu verbs={verbs} label={`Actions for ${attacker.address}`} />
      </span>
    ),
    value: (
      <span className={cn(attacker.attempts >= 50 && !held && "text-destructive")}>
        {attacker.attempts.toLocaleString()}
      </span>
    ),
    share: total > 0 ? attacker.attempts / total : undefined,
  }
}

/**
 * The host keeps the record and has nothing to read it with: `last` and
 * `lastb` come from a package minimal cloud images leave out. Said once, with
 * where to install it, rather than as an error.
 */
function NoRecord({ tool, record }: { tool: string; record: string }) {
  const { can } = useAuth()
  return (
    <div className="mt-2 flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2 rounded-xl border border-dashed px-4 py-3">
      <ProductLogo id="linux" size="sm" />
      <p className="min-w-0 flex-1 basis-64 text-body text-muted-foreground">
        This server cannot read its {record}: <code className="font-mono">{tool}</code> comes with{" "}
        <code className="font-mono">util-linux-extra</code>, which is not installed.
      </p>
      {can("system.admin") && (
        <Button asChild size="xs" variant="outline">
          <Link href="/packages">Packages</Link>
        </Button>
      )}
    </div>
  )
}

/**
 * Recent logins, read from the host rather than remembered by the dashboard.
 *
 * Failed attempts are a separate request behind system.admin: btmp records
 * whatever was typed at a login prompt, and what people type at a login prompt
 * is sometimes their password in the username field.
 */
function LoginHistoryPanel({ history }: { history: PollState<LoginRecord[]> }) {
  const { can } = useAuth()
  const [filters, setFilters] = useFilterHistory("security.logins.filters", { q: "", failed: "" })
  const failed = filters.failed === "1"
  const query = filters.q
  const setFailed = (failed: boolean) =>
    setFilters((previous) => ({ ...previous, failed: failed ? "1" : "" }), true)
  const setQuery = (q: string) => setFilters((previous) => ({ ...previous, q }))
  const admin = can("system.admin")
  const showFailed = failed && admin

  const failedRecords = usePoll(
    (signal) => get<LoginRecord[]>("/logins/failed", { limit: 100 }, signal),
    60000,
    [],
    { enabled: showFailed },
  )
  useEffect(() => {
    window.addEventListener("jd:logins-refresh", failedRecords.refresh)
    return () => window.removeEventListener("jd:logins-refresh", failedRecords.refresh)
  }, [failedRecords.refresh])
  const { data, error, loading, refresh } = showFailed ? failedRecords : history

  const matches = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return data ?? []
    return (data ?? []).filter((r) => `${r.user} ${r.tty} ${r.from}`.toLowerCase().includes(q))
  }, [data, query])
  const held = useHeldList(data ? matches : undefined, recordKey, JSON.stringify(filters))
  const shown = held.rows

  return (
    <Panel plain id="recent" className="scroll-mt-6">
      <PanelHeader
        title={showFailed ? "Failed login attempts" : "Recent logins"}
        actions={data && data.length > 0 && <Count>{plural(data.length, "record")}</Count>}
      />
      {!noRecord(error) && !(error && !data) && (
        <PanelToolbar className="border-b-0 pb-0">
          {admin && (
            <ToggleGroup
              type="single"
              value={showFailed ? "failed" : "ok"}
              onValueChange={(next) => next && setFailed(next === "failed")}
              variant="outline"
              size="sm"
              aria-label="Which logins to show"
            >
              <ToggleGroupItem value="ok" className="px-2.5 text-hint">
                Successful
              </ToggleGroupItem>
              <ToggleGroupItem value="failed" className="px-2.5 text-hint">
                Failed
              </ToggleGroupItem>
            </ToggleGroup>
          )}
          {held.pending > 0 && (
            <Button size="xs" aria-label={`Show ${held.pending} new logins`} onClick={held.reveal}>
              <span role="status">{held.pending} new logins</span> · Show
            </Button>
          )}
          <span className="flex-1" />
          <SearchInput
            dense
            data-workspace-search
            aria-label="Filter login records"
            placeholder="User, terminal or address"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            containerClassName="sm:w-64"
          />
        </PanelToolbar>
      )}
      <PanelBody flush className="pt-3">
        {noRecord(error) ? (
          <NoRecord
            tool={showFailed ? "lastb" : "last"}
            record={showFailed ? "failed-login record" : "login record"}
          />
        ) : error && !data ? (
          <ErrorState error={error} onRetry={refresh} retryable />
        ) : loading && !data ? (
          <RecordSkeleton />
        ) : shown.length === 0 ? (
          <Nothing>
            {data?.length
              ? `Nothing matches “${query.trim()}”.`
              : showFailed
                ? "No failed attempt on record."
                : "No login on record."}
          </Nothing>
        ) : (
          <RecordTable records={shown} failed={showFailed} />
        )}
      </PanelBody>
    </Panel>
  )
}

/** The record's rows before it is read: the frame, and lines of the rows' own height. */
function RecordSkeleton() {
  return (
    <div className="overflow-hidden rounded-xl border bg-card" aria-hidden>
      {Array.from({ length: 4 }).map((_, i) => (
        <div
          key={i}
          className="flex h-12 items-center gap-3 border-b border-hairline px-4 last:border-0"
        >
          <Skeleton className="size-6 rounded-md" />
          <Skeleton className="h-3.5" style={{ width: `${18 + ((i * 7) % 12)}%` }} />
          <span className="flex-1" />
          <Skeleton className="h-3.5 w-20" />
        </div>
      ))}
    </div>
  )
}

/** The account a record names, or the machine for the lines that are its own. */
function RecordMark({ record }: { record: LoginRecord }) {
  return record.kind === "login" ? (
    <InitialsMark name={record.user} className="size-6" />
  ) : (
    <ProductLogo id="linux" size="sm" className="size-6 rounded-md [&_img]:size-4" />
  )
}

/** How a login ended: still open, how long it lasted, or how it was cut off. */
function Lasted({ record }: { record: LoginRecord }) {
  if (record.active) return <Status state="active" label="still open" />
  return <span className="text-muted-foreground">{record.duration ?? record.ended ?? "—"}</span>
}

/**
 * The record as one framed table, a line to a row: who, on which terminal,
 * from where, when, and how long it lasted. On a phone the same rows are two
 * lines each with nothing cut off: the terminal and the origin go under the
 * name. Failed attempts have no duration, so that column is theirs to lose.
 */
function RecordTable({ records, failed }: { records: LoginRecord[]; failed: boolean }) {
  return (
    <div className="overflow-hidden rounded-xl border bg-card">
      <Table containerClassName="max-h-[30rem] max-sm:hidden">
        <TableHeader className="sticky top-0 z-10 [&_tr]:border-b">
          <TableRow>
            <TableHead>Account</TableHead>
            <TableHead>Terminal</TableHead>
            <TableHead className="w-full">From</TableHead>
            <TableHead>When</TableHead>
            {!failed && <TableHead>Lasted</TableHead>}
          </TableRow>
        </TableHeader>
        <TableBody>
          {records.map((record, i) => (
            <TableRow
              key={`${recordKey(record)}-${i}`}
              data-workspace-item={recordKey(record)}
              data-workspace-name={record.user}
              tabIndex={0}
              className="h-12 focus-ring-inset"
            >
              <TableCell className="py-0">
                <span className="flex items-center gap-2.5">
                  <RecordMark record={record} />
                  <span className="text-body font-medium">{record.user}</span>
                </span>
              </TableCell>
              <TableCell className="py-0 font-mono text-muted-foreground">
                {record.kind === "login" ? record.tty || "—" : record.kind}
              </TableCell>
              <TableCell className="py-0">
                <Address ip={record.from || "console"} />
              </TableCell>
              <TableCell
                className="numeric py-0"
                title={record.loginTime ? timestamp(record.loginTime) : undefined}
              >
                {record.loginTime ? relativeTime(record.loginTime) : "—"}
              </TableCell>
              {!failed && (
                <TableCell className="py-0">
                  <Lasted record={record} />
                </TableCell>
              )}
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <ul className="divide-y divide-hairline sm:hidden" aria-label="Login records">
        {records.map((record, i) => (
          <li
            key={`${recordKey(record)}-${i}`}
            data-workspace-item={recordKey(record)}
            data-workspace-name={record.user}
            className="flex h-14 min-w-0 items-center gap-3 px-3 focus-ring-inset"
          >
            <RecordMark record={record} />
            <span className="min-w-0 flex-1">
              <span className="flex min-w-0 items-center gap-2">
                <span className="truncate text-body font-medium">{record.user}</span>
                {record.active && <StatusDot tone="running" />}
                <span className="flex-1" />
                <span className="numeric shrink-0 text-hint">
                  {record.loginTime ? relativeTime(record.loginTime) : "—"}
                </span>
              </span>
              <span className="flex min-w-0 items-center gap-2 text-hint text-muted-foreground">
                <Address ip={record.from || "console"} className="min-w-0" />
                <span className="shrink-0 font-mono">
                  {record.kind === "login" ? record.tty : record.kind}
                </span>
                <span className="flex-1" />
                {!failed && !record.active && (
                  <span className="shrink-0">{record.duration ?? record.ended ?? ""}</span>
                )}
              </span>
            </span>
          </li>
        ))}
      </ul>
    </div>
  )
}
