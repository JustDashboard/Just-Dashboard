"use client"

import { useMemo, useState } from "react"
import { ClockRewind, Logout, Users } from "@/components/icons"
import { get, post, ApiError } from "@/lib/api"
import { timestamp } from "@/lib/format"
import type { LoginRecord, LoginSession } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader, PanelToolbar } from "@/components/panel"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { IconAction, RowActions } from "@/components/icon-action"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"
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
 * The two halves of "who has been on this machine": a snapshot of the
 * interactive logins right now, and — the question that actually matters on an
 * exposed host — who got in overnight, which the host has been keeping in wtmp
 * all along.
 */
export function LoginsPanels() {
  return (
    <div className="flex min-w-0 flex-col gap-4">
      <CurrentSessions />
      <LoginHistoryPanel />
    </div>
  )
}

function CurrentSessions() {
  const { can } = useAuth()
  const { confirm, dialog } = useConfirm()
  const { data, error, loading, refresh } = usePoll(
    (signal) => get<LoginSession[]>("/ssh-sessions", undefined, signal),
    10000,
  )
  if (loading) return <LoadingPanel rows={3} />
  if (error) return <ErrorState error={error} />

  const sessions = data ?? []
  const remote = sessions.filter((s) => s.isSsh).length

  return (
    <>
      <Panel>
        <PanelHeader
          title="Interactive logins"
          actions={
            <Status
              verdict={remote > 0 ? "notice" : "ok"}
              label={
                sessions.length === 0
                  ? "nobody logged in"
                  : `${sessions.length} session${sessions.length === 1 ? "" : "s"}${remote > 0 ? `, ${remote} over ssh` : ""}`
              }
            />
          }
        />
        <PanelBody flush>
          {sessions.length === 0 ? (
            <EmptyState
              icon={Users}
              title="No interactive logins"
              description="Nobody holds a shell on this host right now."
              className="border-0"
            />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>User</TableHead>
                  <TableHead>Type</TableHead>
                  <TableHead>Terminal</TableHead>
                  <TableHead>Logged in</TableHead>
                  <TableHead>Idle</TableHead>
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
                    <TableCell className="font-mono">{session.tty}</TableCell>
                    <TableCell className="whitespace-nowrap text-muted-foreground">
                      {session.loginTime ? timestamp(session.loginTime) : "—"}
                    </TableCell>
                    <TableCell className="numeric text-muted-foreground">
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
          )}
        </PanelBody>
      </Panel>
      {dialog}
    </>
  )
}

/**
 * Recent logins, read from the host rather than remembered by the dashboard.
 *
 * Failed attempts are a separate request behind system.admin: btmp records
 * whatever was typed at a login prompt, and what people type at a login prompt
 * is sometimes their password in the username field.
 */
function LoginHistoryPanel() {
  const { can } = useAuth()
  const [failed, setFailed] = useState(false)
  const [query, setQuery] = useState("")
  const admin = can("system.admin")
  const showFailed = failed && admin

  const { data, error, loading } = usePoll(
    (signal) =>
      get<LoginRecord[]>(showFailed ? "/logins/failed" : "/logins", { limit: 100 }, signal),
    60000,
    [showFailed],
  )

  const unavailable = error instanceof ApiError && error.code === "login_history_unavailable"

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return data ?? []
    return (data ?? []).filter((r) => `${r.user} ${r.tty} ${r.from}`.toLowerCase().includes(q))
  }, [data, query])

  return (
    <Panel>
      <PanelHeader
        title={showFailed ? "Failed login attempts" : "Recent logins"}
        actions={
          data && data.length > 0 ? (
            <span className="numeric text-xs text-muted-foreground">{data.length} records</span>
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
          <div className="p-4">
            <Notice tone="default" title="This host cannot read its login record">
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
          </div>
        ) : error ? (
          <div className="p-4">
            <ErrorState error={error} />
          </div>
        ) : loading ? (
          <LoadingPanel />
        ) : shown.length === 0 ? (
          <EmptyState
            icon={ClockRewind}
            title={
              data?.length
                ? "Nothing matches"
                : showFailed
                  ? "No failed attempts recorded"
                  : "No logins recorded"
            }
            className="border-0"
          />
        ) : (
          <Table containerClassName="max-h-[28rem]">
            <TableHeader className={stickyTableHeader}>
              <TableRow>
                <TableHead>User</TableHead>
                <TableHead>Terminal</TableHead>
                <TableHead>When</TableHead>
                <TableHead>Lasted</TableHead>
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
                  <TableCell className="font-mono">{record.tty || "—"}</TableCell>
                  <TableCell className="whitespace-nowrap text-muted-foreground">
                    {record.loginTime ? timestamp(record.loginTime) : "—"}
                  </TableCell>
                  <TableCell className="numeric text-muted-foreground">
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
        )}
      </PanelBody>
    </Panel>
  )
}
