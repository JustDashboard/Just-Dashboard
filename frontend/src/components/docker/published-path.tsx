"use client"

import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import type { PathResult } from "@/lib/network-investigator-types"
import { PathReport } from "@/components/network/path-report"
import { SidePanel } from "@/components/side-panel"
import { ErrorState, LoadingRows } from "@/components/state"
import { Button } from "@/components/ui/button"

/** One published binding, as the inbound path is asked for it. */
export type PublishedBinding = {
  hostPort: number
  protocol: string
  family: "inet" | "inet6"
}

/** The family a binding was published in: Docker publishes each family as its own binding. */
export function bindingFamily(hostIp: string | undefined, ipv6?: boolean): "inet" | "inet6" {
  return ipv6 || (hostIp ?? "").includes(":") ? "inet6" : "inet"
}

/**
 * The inbound path to one published port — Docker's publication and NAT,
 * DOCKER-USER, the forwarded leg's firewall, the dashboard's gateway, the
 * proxy, provider policy and retained external measurements — read when
 * asked. It sends nothing to the port.
 */
export function PublishedPathReport({
  container,
  binding,
}: {
  container: string
  binding: PublishedBinding
}) {
  const query = { protocol: binding.protocol, family: binding.family }
  const { data, error, loading, refresh } = usePoll<PathResult>(
    (signal) =>
      get<PathResult>(
        `/docker/containers/${encodeURIComponent(container)}/published/${binding.hostPort}`,
        query,
        signal,
      ),
    0,
    [container, binding.hostPort, binding.protocol, binding.family],
  )
  if (loading && !data) return <LoadingRows rows={3} />
  return (
    <div className="space-y-4">
      {error && <ErrorState error={error} onRetry={refresh} />}
      {data && (
        <>
          <PathReport result={data} />
          <Button size="xs" variant="outline" onClick={refresh}>
            Read the path again
          </Button>
        </>
      )}
    </div>
  )
}

/** The path in its own sheet, opened from a container's published port. */
export function PublishedPathSheet({
  container,
  binding,
  onClose,
}: {
  container: string
  binding: PublishedBinding | null
  onClose: () => void
}) {
  return (
    <SidePanel
      open={binding !== null}
      onOpenChange={(open) => !open && onClose()}
      width="lg"
      initialFocus="body"
      title={binding ? `Path to host port ${binding.hostPort}` : "Path"}
      description="The inbound path to a published port, layer by layer, with what is not visible."
    >
      {binding && <PublishedPathReport container={container} binding={binding} />}
    </SidePanel>
  )
}
