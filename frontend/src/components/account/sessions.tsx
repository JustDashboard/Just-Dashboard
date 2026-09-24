"use client"

import { DesktopDevice, Terminal } from "@/components/icons"
import { del, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { plural, relativeTime } from "@/lib/format"
import { describeClient, parseAgent } from "@/lib/clients"
import type { SessionInfo } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { DimActions } from "@/components/icon-action"
import { FactDot, HostFact, HostIdentity } from "@/components/metrics/host-identity"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ClientMark, NetworkFact } from "@/components/client-mark"
import { Row, RowList } from "@/components/row-list"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"

export function useSessions() {
  return usePoll((signal) => get<SessionInfo[]>("/account/sessions", undefined, signal), 20000)
}

/**
 * How recently a session has to have been seen to be "active now". The
 * server moves `lastSeenAt` at most every thirty seconds, and an open tab
 * polls well inside that, so two minutes is a tab that is open rather than
 * one that was.
 */
const ACTIVE_MS = 2 * 60_000

function activeNow(session: SessionInfo) {
  return Date.now() - new Date(session.lastSeenAt).getTime() < ACTIVE_MS
}

function factorLabel(session: SessionInfo) {
  return session.twoFactorPassed ? "password and code" : "password only"
}

/**
 * The session this page is being read through, as one identity line: the
 * shape the Overview gives the machine and the profile gives the account.
 * It is the one row that cannot be signed out from here, and the one the
 * reader compares every other row against.
 */
export function CurrentSession({ session }: { session: SessionInfo }) {
  const agent = parseAgent(session.userAgent)
  return (
    <HostIdentity
      mark={agent.product ?? agent.osProduct}
      fallback={agent.device === "program" ? Terminal : DesktopDevice}
      title={<span title={session.userAgent}>{agent.client}</span>}
      facts={
        <>
          {agent.os && (
            <>
              <HostFact product={agent.osProduct}>{agent.os}</HostFact>
              <FactDot />
            </>
          )}
          <NetworkFact ip={session.ip} />
          <FactDot />
          <span>signed in {relativeTime(session.createdAt)}</span>
          <FactDot />
          <span>{factorLabel(session)}</span>
          <FactDot />
          <span>expires {relativeTime(session.expiresAt)}</span>
        </>
      }
      aside={<Status tone="running" label="this device" />}
    />
  )
}

/**
 * Every session but this one, most recently seen first. A reading, not a
 * destination — a session has no page of its own — so these are rows with a
 * hairline between them rather than cards with an edge (§16), each drawn as
 * the browser and system that hold it.
 */
export function OtherSessions({ sessions }: { sessions: ReturnType<typeof useSessions> }) {
  const { data, refresh } = sessions
  const others = (data ?? [])
    .filter((s) => !s.current)
    .sort((a, b) => b.lastSeenAt.localeCompare(a.lastSeenAt))

  return (
    <Panel plain>
      <PanelHeader
        title="Other sessions"
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {plural(others.length, "session")}
          </span>
        }
      />
      <PanelBody flush>
        {others.length === 0 ? (
          <EmptyNote className="py-4">Signed in nowhere else.</EmptyNote>
        ) : (
          <RowList className="animate-rise">
            {others.map((session) => (
              <Row
                key={session.id}
                className="group"
                leading={<ClientMark userAgent={session.userAgent} />}
                title={<span title={session.userAgent}>{describeClient(session.userAgent)}</span>}
                subtitle={
                  <span className="inline-flex max-w-full min-w-0 items-center gap-2">
                    <NetworkFact ip={session.ip} />
                    <FactDot />
                    <span className="shrink-0">signed in {relativeTime(session.createdAt)}</span>
                    <span className="hidden shrink-0 items-center gap-2 md:inline-flex">
                      <FactDot />
                      {factorLabel(session)}
                    </span>
                  </span>
                }
                trailing={
                  activeNow(session) ? (
                    <Status tone="running" label="active now" />
                  ) : (
                    <span className="hidden text-xs text-muted-foreground sm:inline">
                      seen {relativeTime(session.lastSeenAt)}
                    </span>
                  )
                }
              >
                <DimActions>
                  <Button
                    size="xs"
                    variant="ghost"
                    className="text-destructive"
                    aria-label={`Sign out ${describeClient(session.userAgent)} from ${session.ip}`}
                    onClick={async () => {
                      try {
                        await del(`/account/sessions/${session.id}`)
                        notify.success("Session signed out")
                        refresh()
                      } catch (err) {
                        notify.error("Could not sign that session out", err)
                      }
                    }}
                  >
                    Sign out
                  </Button>
                </DimActions>
              </Row>
            ))}
          </RowList>
        )}
      </PanelBody>
    </Panel>
  )
}

/** The sessions page's body: this device, then everywhere else. */
export function SessionsView({ sessions }: { sessions: ReturnType<typeof useSessions> }) {
  const { data, error, loading, refresh } = sessions
  if (loading && !data) return <LoadingRows rows={4} />
  if (error && !data) return <ErrorState error={error} onRetry={refresh} />
  const current = data?.find((s) => s.current)
  return (
    <>
      {current && <CurrentSession session={current} />}
      <OtherSessions sessions={sessions} />
    </>
  )
}

/** The header's one command: everywhere but here. */
export function SignOutOthersButton({ sessions }: { sessions: ReturnType<typeof useSessions> }) {
  const others = (sessions.data ?? []).filter((s) => !s.current).length
  return (
    <Button
      size="sm"
      variant="outline"
      disabled={others === 0}
      onClick={async () => {
        try {
          const res = await post<{ revoked: number }>("/account/sessions/revoke-others")
          notify.success(
            res.revoked === 1
              ? "1 other session signed out"
              : `${res.revoked} other sessions signed out`,
          )
          sessions.refresh()
        } catch (err) {
          notify.error("Could not sign the other sessions out", err)
        }
      }}
    >
      Sign out other sessions
    </Button>
  )
}
