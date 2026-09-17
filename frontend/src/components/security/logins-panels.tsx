"use client"

import { useMemo, useState } from "react"
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
import { PageHeader, SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { EmptyNote, EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { IconAction, RowActions } from "@/components/icon-action"
import { addressVerbs, blockAddress } from "@/components/security/address-verbs"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { VerbActions } from "@/components/verbs"
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
 * the host has been keeping in wtmp all along.
 */
export function LoginsPanels() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const sessions = usePoll(
    (signal) => get<LoginSession[]>("/ssh-sessions", undefined, signal),
    10000,
  )
  const history = usePoll(
    (signal) => get<LoginRecord[]>("/logins", { limit: 100 }, signal),
    60000,
  )
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
    <>
      <PageHeader
        eyebrow="Security"
        title="Logins"
        actions={
          sessions.data && (
            <Status
              verdict={remote > 0 ? "notice" : "ok"}
              label={
                list.length === 0
                  ? "nobody logged in"
                  : `${list.length} session${list.length === 1 ? "" : "s"}${remote > 0 ? `, ${remote} over ssh` : ""}`
              }
            />
          )
        }
      />

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
      {admin && <AttackersPanel poll={attackers} />}
      <LoginHistoryPanel history={history} />
    </>
  )
}

function CurrentSessions({ poll }: { poll: ReturnType<typeof usePoll<LoginSession[]>> }) {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const { data, error, loading, refresh } = poll
  const sessions = data ?? []

  return (
    <>
      <Panel plain>
        <PanelHeader title="Interactive logins" />
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
            <div className="-mx-4 min-w-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>User</TableHead>
                    <TableHead>Type</TableHead>
                    <TableHead className="hidden sm:table-cell">Terminal</TableHead>
                    <TableHead className="hidden md:table-cell">Logged in</TableHead>
                    <TableHead className="hidden lg:table-cell">Idle</TableHead>
                    <TableHead className="w-full">From</TableHead>
                    <TableHead className="w-px" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {sessions.map((session, i) => (
                    <TableRow key={`${session.user}-${session.tty}-${i}`} className="group">
                      <TableCell className="text-body font-medium">{session.user}</TableCell>
                      <TableCell>
                        <Tag>{session.isSsh ? "ssh" : "local"}</Tag>
                      </TableCell>
                      <TableCell className="hidden font-mono sm:table-cell">{session.tty}</TableCell>
                      <TableCell className="hidden whitespace-nowrap text-muted-foreground md:table-cell">
                        {session.loginTime ? timestamp(session.loginTime) : "—"}
                      </TableCell>
                      <TableCell className="numeric hidden text-muted-foreground lg:table-cell">
                        {session.idle ?? "—"}
                      </TableCell>
                      <TableCell className="font-mono">{session.from || "local"}</TableCell>
                      <TableCell>
                        {can("system.admin") && session.pid ? (
                          <RowActions className="justify-end">
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
                          </RowActions>
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
    <Panel plain>
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
          <Notice tone="default" title="This host cannot read its failed-login record" className="mt-3">
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
          <div className="-mx-4 min-w-0">
            <Table containerClassName="max-h-[28rem]">
              <TableHeader className={stickyTableHeader}>
                <TableRow>
                  <TableHead>Address</TableHead>
                  <TableHead>Attempts</TableHead>
                  <TableHead className="hidden w-full sm:table-cell">Accounts tried</TableHead>
                  <TableHead className="hidden md:table-cell">First</TableHead>
                  <TableHead className="hidden sm:table-cell">Last</TableHead>
                  <TableHead className="w-px" />
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.attackers.map((attacker) => (
                  <TableRow key={attacker.address} className="group">
                    <TableCell className="font-mono">{attacker.address}</TableCell>
                    <TableCell>
                      <span
                        className={cn(
                          "numeric text-xs font-medium",
                          attacker.attempts >= 50 ? "text-destructive" : "text-muted-foreground",
                        )}
                      >
                        {attacker.attempts}
                      </span>
                    </TableCell>
                    <TableCell className="hidden sm:table-cell">
                      <span className="flex flex-wrap gap-1">
                        {attacker.users.map((user) => (
                          <Tag key={user} mono>
                            {user}
                          </Tag>
                        ))}
                        {attacker.users.length === 0 && (
                          <span className="text-muted-foreground">—</span>
                        )}
                      </span>
                    </TableCell>
                    <TableCell className="hidden whitespace-nowrap text-muted-foreground md:table-cell">
                      {relativeTime(attacker.first)}
                    </TableCell>
                    <TableCell className="hidden whitespace-nowrap text-muted-foreground sm:table-cell">
                      {relativeTime(attacker.last)}
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
  const [failed, setFailed] = useState(false)
  const [query, setQuery] = useState("")
  const admin = can("system.admin")
  const showFailed = failed && admin

  const failedRecords = usePoll(
    (signal) => get<LoginRecord[]>("/logins/failed", { limit: 100 }, signal),
    60000,
    [],
    { enabled: showFailed },
  )
  const { data, error, loading } = showFailed ? failedRecords : history

  const unavailable = error instanceof ApiError && error.code === "login_history_unavailable"

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return data ?? []
    return (data ?? []).filter((r) => `${r.user} ${r.tty} ${r.from}`.toLowerCase().includes(q))
  }, [data, query])

  return (
    <Panel plain>
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
                <code className="font-mono">last</code> and{" "}
                <code className="font-mono">lastb</code> are what read wtmp and btmp, and they
                come from <code className="font-mono">util-linux-extra</code> — which minimal
                cloud images leave out. Install it and this fills in; the records themselves have
                been there all along.
              </p>
              <p>
                Until then this page has no answer, which is not the same as a host nobody has
                tried to log in to.
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
          <div className="-mx-4 min-w-0">
            <Table containerClassName="max-h-[28rem]">
              <TableHeader className={stickyTableHeader}>
                <TableRow>
                  <TableHead>User</TableHead>
                  <TableHead className="hidden sm:table-cell">Terminal</TableHead>
                  <TableHead>When</TableHead>
                  <TableHead className="hidden md:table-cell">Lasted</TableHead>
                  <TableHead className="w-full">From</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {shown.map((record, i) => (
                  <TableRow key={`${record.user}-${record.loginTime ?? i}-${i}`}>
                    <TableCell className="text-body font-medium">
                      <span className="flex items-center gap-2">
                        {record.user}
                        {record.kind !== "login" && (
                          <Tag>{record.kind === "boot" ? "boot" : "shutdown"}</Tag>
                        )}
                      </span>
                    </TableCell>
                    <TableCell className="hidden font-mono sm:table-cell">
                      {record.tty || "—"}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-muted-foreground">
                      {record.loginTime ? timestamp(record.loginTime) : "—"}
                    </TableCell>
                    <TableCell className="numeric hidden text-muted-foreground md:table-cell">
                      {record.active ? (
                        <Status state="active" label="still open" />
                      ) : (
                        (record.duration ?? record.ended ?? "—")
                      )}
                    </TableCell>
                    <TableCell className="font-mono">{record.from || "local"}</TableCell>
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
