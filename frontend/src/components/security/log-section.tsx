"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { useRouter } from "next/navigation"
import { ApiError, get } from "@/lib/api"
import { networkOf } from "@/lib/clients"
import { fileSource } from "@/lib/log-sources"
import { notify } from "@/lib/toast"
import type { LogLine } from "@/lib/types"
import type { LensReading } from "@/lib/log-lenses"
import { usePoll, type PollState } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import {
  ServiceLogs,
  type ServiceLogSource,
  type ServiceLogsProps,
} from "@/components/logs/service-logs"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ErrorState, LoadingRows } from "@/components/state"
import { addressVerbs, blockAddress } from "@/components/security/address-verbs"
import { hostLogSource, type HostLogPlan, type HostLogProbe } from "@/components/security/host-logs"
import type { Verb } from "@/components/verbs"

/**
 * Each of the files, asked after at once. A refusal is an answer rather
 * than a failure: 404 is a file this host does not keep, and 400 a path the
 * server will not read — outside `JD_LOG_ROOTS` — which is worth saying in
 * words, since the reader who set the roots can change them.
 */
function probeLogFiles(paths: string[], signal: AbortSignal) {
  return Promise.all(
    paths.map(async (path): Promise<HostLogProbe> => {
      try {
        const source = await get<ServiceLogSource>(
          "/logs/source",
          { source: fileSource(path) },
          signal,
        )
        return { path, source }
      } catch (err) {
        if (!(err instanceof ApiError) || (err.status !== 400 && err.status !== 404)) throw err
        return { path, refused: err.status === 404 ? "missing" : "outside" }
      }
    }),
  )
}

/** Which of these files the dashboard may read on this host, asked once per visit. */
export function useLogFiles(paths: string[], enabled = true) {
  return usePoll((signal) => probeLogFiles(paths, signal), 0, [paths.join("\n")], { enabled })
}

/** The log a Security page reads (`host-logs.ts`), once its files have answered. */
export function useHostLog(plan: HostLogPlan, enabled = true) {
  return usePoll(
    async (signal) =>
      hostLogSource(
        plan,
        await probeLogFiles(
          plan.files.map((file) => file.path),
          signal,
        ),
      ),
    0,
    [plan],
    { enabled },
  )
}

/**
 * The verbs for the address a log line names — an attacker in the auth log,
 * a banned address in fail2ban's — which are the section's verbs for any
 * address (`address-verbs.tsx`): the block inline, the lookups behind the
 * menu. Only a public address gets them: a tailnet peer or a container on
 * the bridge is not something the firewall at the edge stands in front of,
 * and "who owns 10.0.0.4" has no answer.
 *
 * The block is offered only on the events a deny answers — a failed
 * password, a strike, a rate limit — never on a login, an allowed packet or
 * an address fail2ban was told to ignore. The rule is permanent, and the
 * server refuses only the address the dashboard is read from, not the one
 * its operator signs in to SSH from, so one press on the wrong line locks
 * that operator out.
 */
export function useAddressLineVerbs({
  comment,
  onBlocked,
  blockOn,
  remote,
}: {
  /** Written on the rule, which is all that says why the address is refused a month later. */
  comment: string
  onBlocked?: () => void
  blockOn: readonly string[]
  /** Whether the line's address is the far end at all: an outbound packet's source is this host. */
  remote?: (line: LogLine) => boolean
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
      if (!ip || networkOf(ip).kind !== "internet" || (remote && !remote(line))) return []
      return addressVerbs({
        ip,
        block: admin && blockOn.includes(line.event ?? "") ? () => void block(ip) : undefined,
        blocking: blocking === ip,
        navigate: router.push,
      })
    },
    [admin, blockOn, remote, block, blocking, router],
  )
}

/**
 * The page's own reading tiles as presses into its log section: a figure in
 * the grid opens the lines it counts, narrowed in the pane further down, the
 * way every reading is a press away from its lines. Each press is an ask of
 * its own, so the same figure pressed again asks again.
 */
export function useReadingPress() {
  const [ask, setAsk] = useState<ServiceLogsProps["ask"]>()
  const press = useCallback(
    (reading: LensReading) =>
      setAsk((prev) => ({
        key: String(Number(prev?.key ?? 0) + 1),
        fields: reading.fields ?? {},
        levels: reading.levels ?? [],
      })),
    [],
  )
  return [ask, press] as const
}

/**
 * A service's log as a section of its Security page: a title and a hairline
 * over the pane, the way every other block in the section is drawn — the pane
 * is the one frame, because it owns its scroll (§7). What stands in for it is
 * said in the section's words: its files still being asked after, or the
 * page's own reason the pane would be empty (`instead`) — a firewall that is
 * not logging has nothing to show, and the control that changes that is on
 * the same page.
 */
export function HostLogSection({
  title,
  log,
  storageKey,
  instead,
  lineVerbs,
  ask,
}: {
  title: string
  /** From `useHostLog`. */
  log: PollState<ServiceLogSource>
  /** Where the pane keeps its reading for the tab. */
  storageKey: string
  instead?: React.ReactNode
  lineVerbs?: (line: LogLine) => Verb[]
  /** A reading pressed in the page's grid (`useReadingPress`). */
  ask?: ServiceLogsProps["ask"]
}) {
  // The grid is at the top of the page and the log at the bottom: a press
  // that narrowed lines out of sight would look like a press that did nothing.
  const ref = useRef<HTMLElement>(null)
  const asked = ask?.key
  useEffect(() => {
    if (asked) ref.current?.scrollIntoView({ block: "start" })
  }, [asked])
  return (
    <Panel plain ref={ref}>
      <PanelHeader title={title} />
      <PanelBody flush className="pt-3">
        {instead ? (
          instead
        ) : log.error && !log.data ? (
          <ErrorState error={log.error} onRetry={log.refresh} />
        ) : !log.data ? (
          <LoadingRows rows={6} />
        ) : (
          <ServiceLogs
            sources={[log.data]}
            storageKey={storageKey}
            lineVerbs={lineVerbs}
            ask={ask}
            paneClassName="h-[min(75vh,40rem)] min-h-80"
          />
        )}
      </PanelBody>
    </Panel>
  )
}
