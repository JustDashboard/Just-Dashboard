"use client"

import { useState } from "react"
import Link from "next/link"
import { Box, Play, RefreshClockwise } from "@/components/icons"
import { get, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { SystemdUnitDetail } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import { ProductLogos, hasProductLogo } from "@/components/product-logo"
import { EmptyState, LoadingPanel } from "@/components/state"
import { Button } from "@/components/ui/button"
import { DOCKER_UNIT, dockerUnreachable, type DockerPing } from "@/components/docker/unreachable"

/**
 * What every Docker page shows while the daemon cannot be asked: which of
 * the faults it is, where that one is fixed, and Start where starting the
 * unit is the fix. The client's own sentence is one press away, never the
 * description.
 *
 * systemd is asked about the daemon's unit before anything is drawn, so the
 * block does not open on "not answering" and change its title, and grow a
 * button, a moment later.
 */
export function DockerUnreachableState({
  ping,
  onCheck,
}: {
  ping: DockerPing
  onCheck: () => void
}) {
  const { can } = useAuth()
  const unit = usePoll(
    (signal) =>
      get<SystemdUnitDetail>(`/systemd/${encodeURIComponent(DOCKER_UNIT)}`, undefined, signal),
    15_000,
  )
  const [starting, setStarting] = useState(false)

  if (unit.loading && !unit.data && !unit.error) return <LoadingPanel />

  // A server with no systemd, or an account that may not read units, leaves
  // the unit unread: the block then says only what the client's error does.
  const state = dockerUnreachable(ping, unit.error ? undefined : unit.data?.unit)
  const start = async () => {
    setStarting(true)
    try {
      await post(`/systemd/${encodeURIComponent(DOCKER_UNIT)}/start`)
      unit.refresh()
      onCheck()
    } catch (err) {
      notify.error("Could not start Docker", err)
    } finally {
      setStarting(false)
    }
  }

  return (
    <EmptyState
      icon={Box}
      mark={hasProductLogo("docker") ? <ProductLogos ids={["docker"]} /> : undefined}
      title={state.title}
      description={state.description}
      action={
        <div className="flex max-w-full min-w-0 flex-col items-center gap-3">
          <div className="flex flex-wrap items-center justify-center gap-2">
            {state.startable && can("service.control") && (
              <Button size="sm" onClick={() => void start()} pending={starting} disabled={starting}>
                <Play className="size-4" />
                Start Docker
              </Button>
            )}
            {/* The poll asks again on its own half-minute timer; the button is
                for the moment after the fix, when thirty seconds is a long
                time to look at a page that says no. */}
            <Button size="sm" variant="outline" onClick={onCheck}>
              <RefreshClockwise className="size-4" />
              Check again
            </Button>
            {(state.fault === "stopped" || state.fault === "elsewhere") && (
              <Button size="sm" variant="ghost" asChild>
                <Link href={`/processes/services/${encodeURIComponent(DOCKER_UNIT)}`}>
                  Service details
                </Link>
              </Button>
            )}
          </div>
          {state.raw && (
            <details className="w-full max-w-md min-w-0 text-left text-xs">
              <summary className="cursor-pointer text-center text-muted-foreground hover:text-foreground">
                What the server said
              </summary>
              <pre className="mt-1 overflow-x-auto rounded-sm border border-hairline bg-surface-header/40 p-2 font-mono text-micro break-words whitespace-pre-wrap">
                {state.raw}
              </pre>
            </details>
          )}
        </div>
      }
    />
  )
}
