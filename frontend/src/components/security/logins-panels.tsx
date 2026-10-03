"use client"

import { useEffect, useMemo, useState } from "react"
import { Workspace, WorkspaceHelp } from "@/components/workspace/workspace"
import { useFilterHistory } from "@/components/workspace/history"
import { useHeldList } from "@/components/workspace/held-list"
import { useRouter } from "next/navigation"
import { ClockRewind, Logout, Users } from "@/components/icons"
import { get, post, ApiError } from "@/lib/api"
import { notify } from "@/lib/toast"
import { relativeTime, timestamp } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { AttackSummary, LoginRecord, LoginSession } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { PageContext, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyNote, EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { IconAction, DimActions } from "@/components/icon-action"
import { addressVerbs, blockAddress } from "@/components/security/address-verbs"
import { Address, PeerIdentity } from "@/components/security/marks"
import { InitialsMark } from "@/components/account/user-avatar"
import { ProductLogo } from "@/components/product-logo"
import { Meter } from "@/components/meter"
import { Status } from "@/components/status-dot"
import { VerbActions } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

/**
 * The three halves of "who has been on this machine": a snapshot of the
 * interactive logins right now, who has been trying and failing — the
 * question that actually matters on an exposed host — and who got in, which
 * the host has been keeping in wtmp all along. A person is drawn as their
 * initials in the hue the users list gives them, an address as the network it
 * is on, and an attacker's attempts against the most persistent one's as a
 * meter, so the row worth blocking is found before its figure is read.
 */
export function LoginsPanels() {
  const { can } = useAuth()
  const admin = can("system.admin")
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

  const list = sessions.data ?? []
  const remote = list.filter((s) => s.isSsh).length
  const latest = history.data?.find((r) => r.kind === "login" && r.loginTime)
  const attacks = attackers.data

  return (
    <Workspace
      name="Logins"
      memory
      openItems={false}
      refresh={() => {
        sessions.refresh()
        history.refresh()
        attackers.refresh()
        window.dispatchEvent(new Event("jd:logins-refresh"))
      }}
    >
      <PageContext eyebrow="Security" title="Logins" actions={<WorkspaceHelp />} />

      <StatGrid columns={4}>
        <StatTile
          label="Logged in now"
          value={sessions.data ? list.length : "—"}
          hint={remote > 0 ? `${remote} over ssh` : "nobody holds a shell over ssh"}
        />
        <StatTile
          label="Last login"
          value={latest?.loginTime ? relativeTime(latest.loginTime) : "—"}
          hint={latest ? `${latest.user} from ${latest.from || "the console"}` : "no record read"}
        />
        <StatTile
          label="Failed attempts"
          value={attacks ? `${attacks.capped ? "≥" : ""}${attacks.attempts}` : "—"}
          tone={attacks && attacks.attempts >= 2000 ? "warning" : "default"}
          hint={
            !admin
              ? "admin only"
              : attacks
                ? `in the last ${attacks.windowHours >= 48 ? `${Math.round(attacks.windowHours / 24)} days` : `${attacks.windowHours} hours`}`
                : attackers.error
                  ? "no record on this host"
                  : "counting"
          }
        />
        <StatTile
          label="Attacking addresses"
          value={attacks ? attacks.addresses : "—"}
          tone={attacks && attacks.addresses > 0 ? "warning" : "default"}
          hint={
            !admin
              ? "admin only"
              : attacks?.attackers[0]
                ? `${attacks.attackers[0].address} most persistent`
                : "none inside the window"
          }
        />
      </StatGrid>

      <CurrentSessions poll={sessions} />
      <div className={cn("grid min-w-0 items-start gap-6", admin && "2xl:grid-cols-2")}>
        {admin && <AttackersPanel poll={attackers} />}
        <LoginHistoryPanel history={history} />
      </div>
    </Workspace>
  )
}

function CurrentSessions({ poll }: { poll: ReturnType<typeof usePoll<LoginSession[]>> }) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const { data, error, loading, refresh } = poll
  const sessions = data ?? []

  return (
    <>
      <Panel>
        <PanelHeader
          title="Interactive logins"
          actions={
            <span className="numeric text-hint text-muted-foreground">
              {sessions.length} open {sessions.length === 1 ? "session" : "sessions"}
            </span>
          }
        />
        <PanelBody flush>
          {loading && !data ? (
            <LoadingPanel rows={3} className="mt-3" />
          ) : error && !data ? (
            <ErrorState error={error} className="mt-3" />
          ) : sessions.length === 0 ? (
            <EmptyState
              icon={Users}
              title="No interactive logins"
              description="Nobody holds a shell on this host right now."
              className="mt-3"
            />
          ) : (
            <div className="min-w-0 group-data-[plain]/panel:-mx-4">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Account</TableHead>
                    <TableHead className="w-full">Connection</TableHead>
                    <TableHead>Session</TableHead>
                    <TableHead className="w-px">
                      <span className="sr-only">Actions</span>
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {sessions.map((session, i) => (
                    <TableRow key={`${session.user}-${session.tty}-${i}`} className="group">
                      <TableCell className="py-4">
                        <div className="flex items-center gap-3">
                          <InitialsMark name={session.user} />
                          <div className="space-y-1">
                            <span className="block text-body font-medium">{session.user}</span>
                            <span className="font-mono text-hint text-muted-foreground">
                              {session.tty} · {session.isSsh ? "SSH" : "local"}
                            </span>
                          </div>
                        </div>
                      </TableCell>
                      <TableCell>
                        <Address ip={session.from || "local"} />
                      </TableCell>
                      <TableCell>
                        <div className="space-y-1.5">
                          <span
                            className="block text-body"
                            title={session.loginTime ? timestamp(session.loginTime) : undefined}
                          >
                            {session.loginTime ? relativeTime(session.loginTime) : "—"}
                          </span>
                          <span className="block text-hint text-muted-foreground">
                            Idle: {session.idle || "—"}
                          </span>
                        </div>
                      </TableCell>
                      <TableCell>
                        {can("system.admin") && session.pid ? (
                          <DimActions className="justify-end">
                            <IconAction
                              label={`Disconnect ${session.user}`}
                              className="text-destructive"
                              onClick={() =>
                                confirm({
                                  title: `Disconnect ${session.user}`,
                                  confirmLabel: "Disconnect",
                                  description: (
                                    <p>
                                      The session on <b>{session.tty}</b> from{" "}
                                      <b>{session.from || "local"}</b> is hung up. Anything it is
                                      running in the foreground stops with it — a long job that was
                                      not started under tmux or nohup will not survive.
                                    </p>
                                  ),
                                  action: async () => {
                                    await post(`/ssh-sessions/${session.pid}/disconnect`, {})
                                    refresh()
                                  },
                                })
                              }
                            >
                              <Logout />
                            </IconAction>
                          </DimActions>
                        ) : null}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
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
 * host, with which account names — "root, admin, ubuntu" is a scanner, a
 * single real account name is somebody who knows the machine — and the block
 * that answers it is one press away.
 */
function AttackersPanel({ poll }: { poll: ReturnType<typeof usePoll<AttackSummary>> }) {
  const router = useRouter()
  const [blocking, setBlocking] = useState<string | null>(null)
  const { data, error, loading, refresh } = poll
  const unavailable = error instanceof ApiError && error.code === "login_history_unavailable"
  const most = Math.max(1, ...(data?.attackers ?? []).map((a) => a.attempts))

  const block = async (ip: string) => {
    setBlocking(ip)
    try {
      await blockAddress(ip, "failed logins")
      notify.success(`${ip} blocked at the firewall`, {
        description: "The deny rule sits in front of every allow and does not expire.",
      })
      refresh()
    } catch (err) {
      notify.error("Could not add the rule", err)
    } finally {
      setBlocking(null)
    }
  }

  return (
    <Panel>
      <PanelHeader
        title="Attackers"
        actions={
          data ? (
            <span className="numeric text-hint text-muted-foreground">
              {data.capped ? "at least " : ""}
              {data.attempts} attempts
              {data.since ? ` since ${relativeTime(data.since)}` : ""}
            </span>
          ) : undefined
        }
      />
      <PanelBody flush>
        {unavailable ? (
          <Notice
            tone="default"
            title="This host cannot read its failed-login record"
            className="mt-3"
          >
            <code className="font-mono">lastb</code> reads btmp and comes from{" "}
            <code className="font-mono">util-linux-extra</code>, which minimal cloud images leave
            out. Install it and this fills in.
          </Notice>
        ) : error && !data ? (
          <ErrorState error={error} className="mt-3" />
        ) : loading && !data ? (
          <LoadingPanel rows={4} className="mt-3" />
        ) : !data?.attackers.length ? (
          <EmptyState
            icon={ClockRewind}
            title="No failed attempts inside the window"
            description={
              data
                ? `Nothing in the last ${Math.round(data.windowHours / 24)} days, which on a host with a public SSH port is worth a second look at the record itself.`
                : undefined
            }
            className="mt-3"
          />
        ) : (
          <div className="min-w-0 group-data-[plain]/panel:-mx-4">
            <Table containerClassName="max-h-[28rem]">
              <TableHeader className={stickyTableHeader}>
                <TableRow>
                  <TableHead>Address</TableHead>
                  <TableHead className="w-full">Attempts</TableHead>
                  <TableHead>
                    <span className="sr-only">Actions</span>
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.attackers.map((attacker) => (
                  <TableRow key={attacker.address} className="group">
                    <TableCell className="py-4">
                      <PeerIdentity ip={attacker.address} />
                      <span className="mt-2 block text-hint text-muted-foreground">
                        {attacker.users.length
                          ? `Tried ${attacker.users.join(", ")}`
                          : "No accounts recorded"}
                      </span>
                    </TableCell>
                    <TableCell>
                      <div className="min-w-24 space-y-2">
                        <div className="flex items-center justify-between gap-4">
                          <span
                            className={cn(
                              "numeric text-body font-medium",
                              attacker.attempts >= 50 && "text-destructive",
                            )}
                          >
                            {attacker.attempts}
                          </span>
                          <span
                            className="text-hint text-muted-foreground"
                            title={`Last: ${timestamp(attacker.last)}`}
                          >
                            {relativeTime(attacker.last)}
                          </span>
                        </div>
                        <Meter
                          value={(attacker.attempts / most) * 100}
                          tone={attacker.attempts >= 50 ? "danger" : "default"}
                          size="thin"
                          label={`${attacker.attempts} attempts`}
                        />
                        <span className="block text-hint text-muted-foreground">
                          Since {relativeTime(attacker.first)}
                        </span>
                      </div>
                    </TableCell>
                    <TableCell>
                      <VerbActions
                        dim
                        className="justify-end"
                        verbs={addressVerbs({
                          ip: attacker.address,
                          block: () => void block(attacker.address),
                          blocking: blocking === attacker.address,
                          navigate: (href) => router.push(href),
                        })}
                      />
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </PanelBody>
    </Panel>
  )
}

/**
 * Recent logins, read from the host rather than remembered by the dashboard.
 *
 * Failed attempts are a separate request behind system.admin: btmp records
 * whatever was typed at a login prompt, and what people type at a login prompt
 * is sometimes their password in the username field.
 */
function LoginHistoryPanel({ history }: { history: ReturnType<typeof usePoll<LoginRecord[]>> }) {
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
  const { data, error, loading } = showFailed ? failedRecords : history

  const unavailable = error instanceof ApiError && error.code === "login_history_unavailable"

  const matches = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return data ?? []
    return (data ?? []).filter((r) => `${r.user} ${r.tty} ${r.from}`.toLowerCase().includes(q))
  }, [data, query])
  const held = useHeldList(
    data ? matches : undefined,
    (record) => `${record.user}:${record.tty}:${record.loginTime}:${record.from}`,
    JSON.stringify(filters),
  )
  const shown = held.rows

  return (
    <Panel>
      <PanelHeader
        title={showFailed ? "Failed login attempts" : "Recent logins"}
        actions={
          data && data.length > 0 ? (
            <span className="numeric text-hint text-muted-foreground">{data.length} records</span>
          ) : undefined
        }
      />
      {!unavailable && !error && (
        <PanelToolbar>
          {held.pending > 0 && (
            <Button size="xs" aria-label={`Show ${held.pending} new logins`} onClick={held.reveal}>
              <span role="status">{held.pending} new logins</span> · Show
            </Button>
          )}
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
      <PanelBody flush>
        {unavailable ? (
          <Notice tone="default" title="This host cannot read its login record" className="mt-3">
            <div className="space-y-1.5">
              <p>
                <code className="font-mono">last</code> and <code className="font-mono">lastb</code>{" "}
                are what read wtmp and btmp, and they come from{" "}
                <code className="font-mono">util-linux-extra</code> — which minimal cloud images
                leave out. Install it and this fills in; the records themselves have been there all
                along.
              </p>
              <p>
                Until then this page has no answer, which is not the same as a host nobody has tried
                to log in to.
              </p>
            </div>
          </Notice>
        ) : error && !data ? (
          <ErrorState error={error} className="mt-3" />
        ) : loading && !data ? (
          <LoadingPanel className="mt-3" />
        ) : shown.length === 0 ? (
          data?.length ? (
            <EmptyNote>Nothing matches &ldquo;{query.trim()}&rdquo;.</EmptyNote>
          ) : (
            <EmptyState
              icon={ClockRewind}
              title={showFailed ? "No failed attempts recorded" : "No logins recorded"}
              className="mt-3"
            />
          )
        ) : (
          <div className="min-w-0 group-data-[plain]/panel:-mx-4">
            <Table containerClassName="max-h-[28rem]">
              <TableHeader className={stickyTableHeader}>
                <TableRow>
                  <TableHead>Account</TableHead>
                  <TableHead className="w-full">Origin</TableHead>
                  <TableHead>When</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {shown.map((record, i) => (
                  <TableRow
                    key={`${record.user}-${record.loginTime ?? i}-${i}`}
                    data-workspace-item={`${record.user}:${record.tty}:${record.loginTime}:${record.from}`}
                    data-workspace-name={record.user}
                    tabIndex={0}
                    className="focus-ring-inset"
                  >
                    <TableCell className="py-4">
                      <div className="flex items-center gap-3">
                        {record.kind === "login" ? (
                          <InitialsMark name={record.user} />
                        ) : (
                          <ProductLogo id="linux" size="sm" />
                        )}
                        <div className="space-y-1">
                          <span className="block text-body font-medium">{record.user}</span>
                          <span className="block font-mono text-hint text-muted-foreground">
                            {record.kind === "login" ? record.tty || "—" : record.kind}
                          </span>
                        </div>
                      </div>
                    </TableCell>
                    <TableCell className="whitespace-normal">
                      <Address ip={record.from || "local"} />
                    </TableCell>
                    <TableCell>
                      <div className="space-y-1.5">
                        <span
                          className="block text-body"
                          title={record.loginTime ? timestamp(record.loginTime) : undefined}
                        >
                          {record.loginTime ? relativeTime(record.loginTime) : "—"}
                        </span>
                        {record.active ? (
                          <Status state="active" label="still open" />
                        ) : (
                          <span className="block text-hint text-muted-foreground">
                            {record.duration ?? record.ended ?? "—"}
                          </span>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </PanelBody>
    </Panel>
  )
}
