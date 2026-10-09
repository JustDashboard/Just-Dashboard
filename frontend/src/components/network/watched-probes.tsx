"use client"

import { useState } from "react"
import Link from "next/link"
import { Connection, Trash } from "@/components/icons"
import { ApiError, del, get, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { isProbe, probeStatus, type WatchedEndpoint } from "@/lib/watched-probes"
import { usePoll } from "@/hooks/use-poll"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ErrorState } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { CheckTrend } from "@/components/proxy/watched-domains"

/**
 * Network probes kept on the watch list: the TLS watch's own schedule,
 * history and alerts, asked only whether the endpoint takes a connection.
 * There is no second monitor; a probe is a watched endpoint of its own kind.
 */
export function WatchedProbes({ admin }: { admin: boolean }) {
  const watched = usePoll(
    (signal) => get<WatchedEndpoint[]>("/certificates/watched", undefined, signal),
    60_000,
  )
  const [busy, setBusy] = useState<"check" | number | null>(null)
  const probes = (watched.data ?? []).filter(isProbe)

  const checkNow = async () => {
    setBusy("check")
    try {
      await post("/certificates/watched/check")
      watched.refresh()
    } catch (err) {
      notify.error("The check did not finish", err)
    } finally {
      setBusy(null)
    }
  }
  const stop = async (row: WatchedEndpoint) => {
    setBusy(row.id)
    try {
      await del(`/certificates/watched/${row.id}`)
      watched.refresh()
    } catch (err) {
      notify.error(`Could not stop watching ${row.domain}:${row.port}`, err)
    } finally {
      setBusy(null)
    }
  }

  return (
    <Panel plain aria-label="Watched probes">
      <PanelHeader
        title="Watched probes"
        actions={
          <>
            <Button asChild variant="ghost" size="sm">
              <Link href="/proxy/certificates">Watch list</Link>
            </Button>
            {admin && probes.length > 0 && (
              <Button size="sm" variant="outline" onClick={checkNow} pending={busy === "check"}>
                Check now
              </Button>
            )}
          </>
        }
      />
      <PanelBody>
        {watched.error && !watched.data ? (
          <ErrorState error={watched.error} onRetry={watched.refresh} />
        ) : probes.length === 0 ? (
          <p className="text-body text-muted-foreground">
            No probe is watched. Run a TCP port check on the Tools page and watch it to have the
            server connect on the watch list&rsquo;s schedule, keep each connect time and alert when
            it stops answering.
          </p>
        ) : (
          <ul className="divide-y divide-hairline">
            {probes.map((row) => {
              const status = probeStatus(row.probe)
              return (
                <li
                  key={row.id}
                  className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 py-2.5"
                >
                  <Connection className="size-4 shrink-0 text-muted-foreground" />
                  <span className="min-w-0 font-mono text-body break-all">
                    {row.domain}:{row.port}
                    {row.ip && <span className="text-muted-foreground"> via {row.ip}</span>}
                  </span>
                  <Status tone={status.tone} label={status.label} />
                  <span className="text-hint text-muted-foreground">
                    {row.checkedAt
                      ? `checked ${relativeTime(row.checkedAt)}`
                      : "waiting for its first check"}
                  </span>
                  <CheckTrend id={row.id} checkedAt={row.checkedAt} probe />
                  {row.probe?.error && (
                    <span className="w-full text-hint break-all text-destructive">
                      {row.probe.error}
                    </span>
                  )}
                  {admin && (
                    <Button
                      size="xs"
                      variant="ghost"
                      className="ml-auto"
                      aria-label={`Stop watching ${row.domain}:${row.port}`}
                      onClick={() => void stop(row)}
                      pending={busy === row.id}
                    >
                      <Trash className="size-3.5" />
                      Stop
                    </Button>
                  )}
                </li>
              )
            })}
          </ul>
        )}
      </PanelBody>
    </Panel>
  )
}

/**
 * Watch the endpoint a port check reached: the server then connects on the
 * watch list's schedule, through the same monitor that checks certificates.
 */
export function WatchProbeButton({ target, port }: { target: string; port: number }) {
  const [busy, setBusy] = useState(false)
  const watch = async () => {
    setBusy(true)
    try {
      const row = await post<WatchedEndpoint>("/certificates/watched", {
        domain: target,
        port,
        kind: "tcp",
      })
      notify.success(`Watching ${row.domain}:${row.port}`, {
        description: "The server connects on the watch list's schedule and keeps each result.",
      })
    } catch (err) {
      if (err instanceof ApiError && err.code === "already_watched") {
        notify.info(`${target}:${port} is already watched`, { description: err.message })
      } else {
        notify.error("Could not watch it", err)
      }
    } finally {
      setBusy(false)
    }
  }
  return (
    <Button size="sm" variant="outline" onClick={watch} pending={busy} disabled={!target || !port}>
      Watch on a schedule
    </Button>
  )
}
