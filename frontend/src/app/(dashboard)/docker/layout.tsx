"use client"

import { Box, RefreshClockwise } from "@/components/icons"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageContext } from "@/components/page"
import { EmptyState, LoadingPanel } from "@/components/state"
import { Button } from "@/components/ui/button"

/**
 * Docker is seven pages, not one screen of tabs. The rail lists them; this
 * layout owns exactly one thing — the reachability check — because an App
 * Router layout mounts once and every sub-page then inherits "Docker is not
 * reachable" without polling for it again.
 */
export default function DockerLayout({ children }: { children: React.ReactNode }) {
  const ping = usePoll(
    (signal) =>
      get<{ available: boolean; error?: string; serverVersion?: string }>(
        "/docker/ping",
        undefined,
        signal,
      ),
    30_000,
  )

  if (ping.loading) {
    return (
      <Page>
        <PageContext eyebrow="Server" title="Docker" />
        <LoadingPanel />
      </Page>
    )
  }

  if (!ping.data?.available) {
    return (
      <Page>
        <PageContext eyebrow="Server" title="Docker" />
        <EmptyState
          icon={Box}
          title="Docker is not reachable"
          description={
            ping.data?.error ??
            "The dashboard could not connect to the Docker socket. Check that the daemon is running and that this process can read /var/run/docker.sock."
          }
          // The poll retries on its own half-minute timer; the button is for
          // the moment *after* fixing the daemon, when thirty seconds is a
          // long time to keep staring at a page that says no.
          action={
            <Button size="sm" variant="outline" onClick={ping.refresh}>
              <RefreshClockwise className="size-4" />
              Check again
            </Button>
          }
        />
      </Page>
    )
  }

  return <>{children}</>
}
