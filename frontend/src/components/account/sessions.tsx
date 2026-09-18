"use client"

import { DesktopDevice } from "@/components/icons"
import { del, get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { calendarDate, relativeTime } from "@/lib/format"
import type { SessionInfo } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { rowReveal } from "@/components/icon-action"
import { Panel, PanelBody } from "@/components/panel"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { cn } from "@/lib/utils"

/**
 * A user agent as a person would say it: "Chrome on macOS", "curl". The raw
 * string is kept as the cell's title for the one time it matters, but a table
 * of sessions is read to answer "is that my phone or somebody else", and a
 * 120-character token string does not answer it.
 */
export function describeClient(ua: string) {
  const browser = /Edg\//.test(ua)
    ? "Edge"
    : /OPR\//.test(ua)
      ? "Opera"
      : /Firefox\//.test(ua)
        ? "Firefox"
        : /Chrome\//.test(ua)
          ? "Chrome"
          : /Safari\//.test(ua)
            ? "Safari"
            : /curl\//.test(ua)
              ? "curl"
              : ua.split(/[\s/]/)[0] || "Unknown client"
  const os = /Windows/.test(ua)
    ? "Windows"
    : /iPhone|iPad/.test(ua)
      ? "iOS"
      : /Android/.test(ua)
        ? "Android"
        : /Mac OS X|Macintosh/.test(ua)
          ? "macOS"
          : /Linux/.test(ua)
            ? "Linux"
            : ""
  return os ? `${browser} on ${os}` : browser
}

export function useSessions() {
  return usePoll((signal) => get<SessionInfo[]>("/account/sessions", undefined, signal), 20000)
}

export function SessionsTable({ sessions }: { sessions: ReturnType<typeof useSessions> }) {
  const { data, error, loading, refresh } = sessions
  if (loading && !data) return <LoadingPanel />
  if (error) return <ErrorState error={error} />

  return (
    <Panel plain>
      <PanelBody flush>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-full">Device</TableHead>
              <TableHead>Address</TableHead>
              <TableHead>Signed in</TableHead>
              <TableHead>Last seen</TableHead>
              <TableHead className="w-px" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {data?.map((session) => (
              <TableRow key={session.id} className="group">
                <TableCell>
                  <span className="flex min-w-0 items-center gap-2">
                    <span className="truncate text-body font-medium" title={session.userAgent}>
                      {describeClient(session.userAgent)}
                    </span>
                    {session.current && <Tag tone="success">this device</Tag>}
                  </span>
                </TableCell>
                <TableCell className="font-mono">{session.ip}</TableCell>
                <TableCell className="text-muted-foreground">{calendarDate(session.createdAt)}</TableCell>
                <TableCell className="text-muted-foreground">
                  {relativeTime(session.lastSeenAt)}
                </TableCell>
                <TableCell>
                  {!session.current && (
                    <Button
                      size="xs"
                      variant="ghost"
                      className={cn("text-destructive", rowReveal())}
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
                  )}
                </TableCell>
              </TableRow>
            ))}
            {data?.length === 0 && (
              <TableRow>
                <TableCell colSpan={5} className="p-0">
                  <EmptyState icon={DesktopDevice} title="No sessions" />
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </PanelBody>
    </Panel>
  )
}

/** The header's one command: everywhere but here. */
export function SignOutOthersButton({
  sessions,
}: {
  sessions: ReturnType<typeof useSessions>
}) {
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
            res.revoked === 1 ? "1 other session signed out" : `${res.revoked} other sessions signed out`,
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
