"use client"

import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageContext } from "@/components/page"
import { ErrorState, LoadingPanel } from "@/components/state"
import { DockerUnreachableState } from "@/components/docker/unreachable-state"
import type { DockerPing } from "@/components/docker/unreachable"

/**
 * Docker is seven pages, not one screen of tabs. The rail lists them; this
 * layout owns exactly one thing — the reachability check — because an App
 * Router layout mounts once and every sub-page then inherits "Docker is not
 * reachable" without polling for it again.
 */
export default function DockerLayout({ children }: { children: React.ReactNode }) {
  const ping = usePoll((signal) => get<DockerPing>("/docker/ping", undefined, signal), 30_000)

  if (ping.loading) {
    return (
      <Page>
        <PageContext eyebrow="Server" title="Docker" />
        <LoadingPanel />
      </Page>
    )
  }

  // The question itself went unanswered: that is the dashboard's failure to
  // say, not a reading of Docker.
  if (ping.error && !ping.data) {
    return (
      <Page>
        <PageContext eyebrow="Server" title="Docker" />
        <ErrorState error={ping.error} onRetry={ping.refresh} retryable />
      </Page>
    )
  }

  if (!ping.data?.available) {
    return (
      <Page>
        <PageContext eyebrow="Server" title="Docker" />
        <DockerUnreachableState ping={ping.data ?? { available: false }} onCheck={ping.refresh} />
      </Page>
    )
  }

  return <>{children}</>
}
