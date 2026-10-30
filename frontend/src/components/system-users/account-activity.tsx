"use client"

import Link from "next/link"
import { useMemo, useState } from "react"
import { ArrowRight, Notes, ShieldCheck, SignIn, Terminal, Warning } from "@/components/icons"
import { useConfirm } from "@/components/confirm-dialog"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph, ProductLogo, processProduct } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { Address } from "@/components/security/marks"
import { EmptyNote } from "@/components/state"
import { useAccount } from "@/components/system-users/account-page"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { usePoll } from "@/hooks/use-poll"
import { del, get } from "@/lib/api"
import { plural, relativeTime } from "@/lib/format"
import type { AccountActivity, AccountHistory, AuditEntry } from "@/lib/types"
import { cn } from "@/lib/utils"

/**
 * What the account is doing this minute. It draws nothing at all when the answer
 * is nothing, because most accounts on a server are not doing anything and a
 * panel saying so is one more thing to read past.
 */
export function ActivityPanel() {
  const { user, path, readOnly, query } = useAccount()
  const { confirm, dialog } = useConfirm()
  const { data, refresh } = usePoll(
    (signal) => get<AccountActivity>(`${path}/activity`, undefined, signal),
    15000,
    [path],
  )
  if (!data) return null
  const idle = data.sessions.length === 0 && data.processes === 0 && data.cronJobs === 0
  if (idle) return null

  const end = (pid: number) =>
    confirm({
      title: "End this sign-in",
      confirmLabel: "End",
      description: <p>The shell gets the same hangup a dropped connection would send it.</p>,
      action: async (typed) => {
        await del(`${path}/sessions`, { confirm: typed, query: { ...query, pid } })
        refresh()
      },
    })

  return (
    <Panel plain aria-label="Right now">
      <PanelHeader
        title="Right now"
        actions={
          <span className="flex flex-wrap items-center gap-x-4 gap-y-1 text-hint text-muted-foreground">
            {data.processes > 0 && (
              <Link
                href={`/processes?user=${encodeURIComponent(user.username)}`}
                className="inline-flex items-center gap-1.5 rounded-sm focus-ring hover:text-foreground"
              >
                {data.busiest.map((process) => {
                  const product = processProduct(process.name)
                  return product ? <ProductGlyph key={process.pid} id={product} /> : null
                })}
                <span className="numeric">{plural(data.processes, "process", "processes")}</span>
              </Link>
            )}
            {data.cronJobs > 0 && (
              <Link
                href="/processes/scheduled"
                className="numeric rounded-sm focus-ring hover:text-foreground"
              >
                {plural(data.cronJobs, "scheduled job")}
              </Link>
            )}
          </span>
        }
      />
      {data.sessions.length > 0 && (
        <PanelBody flush>
          <RowList className="animate-rise">
            {data.sessions.map((session, index) => (
              <Row
                key={session.pid ?? `${session.tty}-${index}`}
                leading={<ProductLogo size="sm" fallback={Terminal} />}
                title={
                  <span className="flex flex-wrap items-center gap-x-2">
                    {session.from ? <Address ip={session.from} /> : "On this machine"}
                    <span className="font-mono text-hint text-muted-foreground">{session.tty}</span>
                  </span>
                }
                subtitle={[
                  session.loginTime && `Since ${relativeTime(session.loginTime)}`,
                  session.idle && `idle ${session.idle}`,
                ]
                  .filter(Boolean)
                  .join(" · ")}
                trailing={
                  !readOnly &&
                  session.pid && (
                    <Button size="xs" variant="outline" onClick={() => end(session.pid as number)}>
                      End
                    </Button>
                  )
                }
              />
            ))}
          </RowList>
        </PanelBody>
      )}
      {dialog}
    </Panel>
  )
}

type Kind = "signin" | "failed" | "sudo" | "dashboard"

type HistoryEvent = {
  id: string
  at: number
  kind: Kind
  title: React.ReactNode
  detail?: React.ReactNode
  bad?: boolean
}

const KIND_MARK = {
  signin: SignIn,
  failed: Warning,
  sudo: ShieldCheck,
  dashboard: Notes,
} as const

const KIND_LABEL: Record<Kind, string> = {
  signin: "Sign-ins",
  failed: "Refused",
  sudo: "sudo",
  dashboard: "Dashboard",
}

/** The dashboard's own audit words for what it did to an account. */
const AUDIT_WORDS: Record<string, string> = {
  "system.user.create": "Account created",
  "system.user.update": "Account changed",
  "system.user.password.set": "Password set",
  "system.user.password.remove": "Password removed",
  "system.user.sudo.set": "sudo rule changed",
  "system.user.sudo.rule.remove": "sudo rule removed",
  "system.user.sshkey.add": "SSH key added",
  "system.user.sshkey.update": "SSH key changed",
  "system.user.sshkey.remove": "SSH key revoked",
  "system.user.sshkey.import": "SSH keys imported",
  "system.user.sshkey.generate": "SSH key generated",
  "system.user.sshkey.copy": "SSH key given to another account",
  "system.user.limits.set": "Limits changed",
  "system.user.session.end": "A sign-in was ended",
  "system.user.sessions.end": "Everything it was running was ended",
  "system.user.failed-attempts.reset": "Failed attempts cleared",
}

const SHOWN = 12

/**
 * Who got in, who was refused, what they ran as root and what the dashboard did,
 * as one line of time. The server keeps these in four different places, and the
 * question is one question: what happened to this account.
 */
export function HistoryPanel() {
  const { user, path } = useAccount()
  const [kind, setKind] = useState<Kind | "all">("all")
  const [more, setMore] = useState(false)
  const { data } = usePoll(
    (signal) => get<AccountHistory>(`${path}/history`, undefined, signal),
    60000,
    [path],
  )
  const { data: audit } = usePoll(
    (signal) =>
      get<{ entries: AuditEntry[] }>(
        "/audit/",
        { action_prefix: "system.user", limit: 300 },
        signal,
      ),
    60000,
    [],
  )

  const events = useMemo<HistoryEvent[]>(() => {
    const out: HistoryEvent[] = []
    for (const login of data?.logins ?? []) {
      if (!login.loginTime) continue
      out.push({
        id: `in-${login.loginTime}-${login.tty}`,
        at: new Date(login.loginTime).getTime(),
        kind: "signin",
        title: login.active ? "Signed in, still here" : "Signed in",
        detail: login.from && <Address ip={login.from} />,
      })
    }
    for (const failed of data?.failed ?? []) {
      if (!failed.loginTime) continue
      out.push({
        id: `no-${failed.loginTime}-${failed.tty}`,
        at: new Date(failed.loginTime).getTime(),
        kind: "failed",
        title: "Sign-in refused",
        detail: failed.from && <Address ip={failed.from} />,
        bad: true,
      })
    }
    for (const event of data?.sudo ?? []) {
      out.push({
        id: `sudo-${event.at}-${event.command}`,
        at: new Date(event.at).getTime(),
        kind: "sudo",
        title: event.failed ? `sudo refused: ${event.reason}` : "Ran as " + event.runAs,
        detail: <span className="truncate font-mono">{event.command}</span>,
        bad: event.failed,
      })
    }
    for (const entry of audit?.entries ?? []) {
      if (!entry.target.split(",").includes(user.username)) continue
      out.push({
        id: `audit-${entry.id}`,
        at: new Date(entry.ts).getTime(),
        kind: "dashboard",
        title: AUDIT_WORDS[entry.action] ?? entry.action.replace(/^system\.user\./, ""),
        detail: entry.username && `by ${entry.username}`,
        bad: !entry.success,
      })
    }
    return out.filter((e) => !Number.isNaN(e.at)).sort((a, b) => b.at - a.at)
  }, [data, audit, user.username])

  const counts = (name: Kind) => events.filter((e) => e.kind === name).length
  const present = (Object.keys(KIND_LABEL) as Kind[]).filter((name) => counts(name) > 0)
  const filtered = kind === "all" ? events : events.filter((e) => e.kind === kind)
  const shown = more ? filtered : filtered.slice(0, SHOWN)

  return (
    <Panel plain aria-label="History">
      <PanelHeader title="History">
        {present.length > 1 && (
          <ChipStrip aria-label="Show" className="ml-auto">
            <FilterChip selected={kind === "all"} onClick={() => setKind("all")}>
              All <ChipCount>{events.length}</ChipCount>
            </FilterChip>
            {present.map((name) => (
              <FilterChip key={name} selected={kind === name} onClick={() => setKind(name)}>
                {KIND_LABEL[name]} <ChipCount>{counts(name)}</ChipCount>
              </FilterChip>
            ))}
          </ChipStrip>
        )}
      </PanelHeader>
      <PanelBody flush>
        {shown.length === 0 ? (
          <EmptyNote className="px-0 py-3 text-left">Nothing on record yet.</EmptyNote>
        ) : (
          <ol className="animate-rise">
            {shown.map((event) => {
              const Mark = KIND_MARK[event.kind]
              return (
                <li
                  key={event.id}
                  data-slot="history-row"
                  className="flex min-w-0 items-center gap-3 border-b border-hairline py-2 text-body last:border-b-0"
                >
                  <Mark
                    aria-hidden
                    className={cn(
                      "size-3.5 shrink-0",
                      event.bad ? "text-warning" : "text-muted-foreground",
                    )}
                  />
                  <span className={cn("shrink-0", event.bad && "text-warning")}>{event.title}</span>
                  <span className="flex min-w-0 flex-1 items-center text-hint text-muted-foreground">
                    {event.detail}
                  </span>
                  <span className="numeric shrink-0 text-hint text-muted-foreground">
                    {relativeTime(new Date(event.at).toISOString())}
                  </span>
                </li>
              )
            })}
          </ol>
        )}
        {filtered.length > SHOWN && (
          <button
            type="button"
            onClick={() => setMore((value) => !value)}
            className="mt-2 inline-flex items-center gap-1 rounded-sm text-hint font-medium text-muted-foreground focus-ring hover:text-foreground"
          >
            {more ? "Fewer" : `${filtered.length - SHOWN} more`}
            <ArrowRight className={cn("size-3", more && "rotate-90")} />
          </button>
        )}
      </PanelBody>
    </Panel>
  )
}
