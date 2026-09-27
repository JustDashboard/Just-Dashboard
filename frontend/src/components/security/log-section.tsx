"use client"

import { useCallback, useState } from "react"
import { useRouter } from "next/navigation"
import { Logs } from "@/components/icons"
import { get } from "@/lib/api"
import { networkOf } from "@/lib/clients"
import { notify } from "@/lib/toast"
import type { LogLine, LogSourceIndex } from "@/lib/types"
import { usePoll, type PollState } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { ServiceLogs, type ServiceLogSource } from "@/components/logs/service-logs"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyState, ErrorState, LoadingRows } from "@/components/state"
import { addressVerbs, blockAddress } from "@/components/security/address-verbs"
import type { Verb } from "@/components/verbs"

/**
 * What this host can read, asked once per visit: which of the files a
 * Security page reads exist, and whether the journal is there to fall back
 * on (`host-logs.ts`). It is the logs page's own inventory, so a file the
 * reader could not open there is not offered here either.
 */
export function useHostLogs(enabled = true) {
  return usePoll<LogSourceIndex>((signal) => get("/logs/sources", undefined, signal), 0, [], {
    enabled,
  })
}

/**
 * The verbs for the address a log line names — an attacker in the auth log,
 * a banned address in fail2ban's — which are the section's verbs for any
 * address (`address-verbs.tsx`): the block inline, the lookups behind the
 * menu. Only a public address gets them: a tailnet peer or a container on
 * the bridge is not something the firewall at the edge stands in front of,
 * and "who owns 10.0.0.4" has no answer. `withBlock` leaves the block off a
 * line where it would repeat what already happened — the firewall's own drop.
 */
export function useAddressLineVerbs({
  comment,
  onBlocked,
  withBlock,
}: {
  /** Written on the rule, which is all that says why the address is refused a month later. */
  comment: string
  onBlocked?: () => void
  withBlock?: (line: LogLine) => boolean
}) {
  const { can } = useAuth()
  const router = useRouter()
  const [blocking, setBlocking] = useState<string | null>(null)
  const admin = can("system.admin")

  const block = useCallback(
    async (ip: string) => {
      setBlocking(ip)
      try {
        await blockAddress(ip, comment)
        notify.success(`${ip} blocked at the firewall`, {
          description: "The deny rule sits in front of every allow and does not expire.",
        })
        onBlocked?.()
      } catch (err) {
        notify.error("Could not add the rule", err)
      } finally {
        setBlocking(null)
      }
    },
    [comment, onBlocked],
  )

  return useCallback(
    (line: LogLine): Verb[] => {
      const ip = line.attrs?.client
      if (!ip || networkOf(ip).kind !== "internet") return []
      return addressVerbs({
        ip,
        block: admin && (withBlock?.(line) ?? true) ? () => void block(ip) : undefined,
        blocking: blocking === ip,
        navigate: router.push,
      })
    },
    [admin, withBlock, block, blocking, router],
  )
}

/**
 * A service's log as a section of its Security page: a title and a hairline
 * over the pane, the way every other block in the section is drawn — the pane
 * is the one frame, because it owns its scroll (§7). What stands in for it is
 * said in the section's words: the inventory still arriving, the host keeping
 * no such log, or the page's own reason the pane would be empty (`instead`) —
 * a firewall that is not logging has nothing to show, and the control that
 * changes that is on the same page.
 */
export function HostLogSection({
  title,
  logs,
  source,
  storageKey,
  missing,
  instead,
  lineVerbs,
}: {
  title: string
  logs: PollState<LogSourceIndex>
  /** From `host-logs.ts`: undefined until the inventory answers, null when the host has none. */
  source: ServiceLogSource | null | undefined
  /** Where the pane keeps its reading for the tab. */
  storageKey: string
  /** Why there is nothing to read, for a host with none of the files and no journal. */
  missing: { title: string; description: string }
  instead?: React.ReactNode
  lineVerbs?: (line: LogLine) => Verb[]
}) {
  return (
    <Panel plain>
      <PanelHeader title={title} />
      <PanelBody flush className="pt-3">
        {instead ? (
          instead
        ) : logs.error && !logs.data ? (
          <ErrorState error={logs.error} onRetry={logs.refresh} />
        ) : source === undefined ? (
          <LoadingRows rows={6} />
        ) : source === null ? (
          <EmptyState icon={Logs} title={missing.title} description={missing.description} />
        ) : (
          <ServiceLogs
            sources={[source]}
            storageKey={storageKey}
            lineVerbs={lineVerbs}
            paneClassName="h-[min(75vh,40rem)] min-h-80"
          />
        )}
      </PanelBody>
    </Panel>
  )
}
