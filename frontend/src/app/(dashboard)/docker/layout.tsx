"use client"

import { Box } from "@/components/icons"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageHeader } from "@/components/page"
import { SectionNav } from "@/components/tabs"
import { EmptyState, LoadingPanel } from "@/components/state"

/**
 * Docker is six pages, not one screen of tabs. The sidebar entry expands to
 * them; this strip is the switcher for when it is collapsed to the icon rail,
 * and the faster target on a wide screen.
 *
 * The layout owns exactly one thing beyond the strip — the reachability check —
 * because an App Router layout mounts once and every sub-page then inherits
 * "Docker is not reachable" without polling for it again.
 */
const TABS = [
  { title: "Overview", href: "/docker" },
  { title: "Containers", href: "/docker/containers" },
  { title: "Stacks", href: "/docker/stacks" },
  { title: "Images", href: "/docker/images" },
  { title: "Volumes", href: "/docker/volumes" },
  { title: "Networks", href: "/docker/networks" },
  { title: "Events", href: "/docker/events" },
]

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
        <PageHeader eyebrow="Server" title="Docker" />
        <LoadingPanel />
      </Page>
    )
  }

  if (!ping.data?.available) {
    return (
      <Page>
        <PageHeader eyebrow="Server" title="Docker" />
        <EmptyState
          icon={Box}
          title="Docker is not reachable"
          description={
            ping.data?.error ??
            "The dashboard could not connect to the Docker socket. Check that the daemon is running and that this process can read /var/run/docker.sock."
          }
        />
      </Page>
    )
  }

  return (
    <>
      <SectionNav tabs={TABS} />
      {children}
    </>
  )
}
