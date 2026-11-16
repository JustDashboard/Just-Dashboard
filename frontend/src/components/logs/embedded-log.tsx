"use client"

import { Logs } from "@/components/icons"
import { get } from "@/lib/api"
import { useBelow } from "@/lib/breakpoints"
import { bytes, relativeTime } from "@/lib/format"
import { fieldPredicates } from "@/lib/log/filter"
import { lensFor } from "@/lib/log/lenses"
import { usePoll } from "@/hooks/use-poll"
import { ChoiceList, ChoiceRow } from "@/components/choice-row"
import { ErrorState } from "@/components/state"
import { LogSection } from "@/components/logs/log-section"
import type { LogAsk } from "@/components/logs/logs-model"
import {
  ServiceLogs,
  type ServiceLogSource,
  type ServiceLogsProps,
} from "@/components/logs/service-logs"
import { KIND_TAG, kindIcon } from "@/components/logs/source-rail"
import { ProductLogo } from "@/components/product-logo"

/**
 * The Logs page opened on one source, narrowed the way the section was asked
 * to narrow it: a reading pressed on the page's grid carries its question
 * along, in the address the Logs page reads a link from.
 */
export function logsPageHref(sourceId: string, ask?: LogAsk): string {
  const params = new URLSearchParams({ source: sourceId })
  if (ask?.q) params.set("q", ask.q)
  if (ask?.levels?.length) params.set("levels", ask.levels.join(","))
  for (const predicate of fieldPredicates(ask?.fields ?? {}) ?? []) params.append("f", predicate)
  return `/logs?${params}`
}

/**
 * What one source is, in the words a card has room for: the kind, then how
 * big and how fresh it is where the server measured it, or where it lives.
 */
export function sourceSummary(source: Omit<ServiceLogSource, "id">): string {
  const parts: string[] = []
  const kind = KIND_TAG[source.kind] ?? source.kind
  if (kind) parts.push(kind)
  if (source.size !== undefined && source.size > 0) {
    parts.push(bytes(source.size))
    if (source.modified) parts.push(`written ${relativeTime(source.modified)}`)
  } else if (source.path) {
    parts.push(source.path)
  } else if (source.detail) {
    parts.push(source.detail)
  }
  return parts.join(" · ")
}

export type EmbeddedLogProps = Omit<
  ServiceLogsProps,
  "sources" | "size" | "paneClassName" | "layout" | "flush" | "className"
> & {
  /** The section's title: "Engine log", "Cron log". */
  title: string
  /** The logs the page offers, or undefined while it is still finding out. */
  sources: ServiceLogSource[] | undefined
  /**
   * The page's own reason there is nothing to read, said in place of the
   * pane: a firewall that is not logging, a host with no cron log.
   */
  instead?: React.ReactNode
  /** The page could not find out which logs there are. */
  error?: Error
  onRetry?: () => void
  /** The section, for a page that brings it into view. */
  ref?: React.Ref<HTMLElement>
}

/**
 * A service's log at the foot of the page about it: the engine's, the cron
 * log, a firewall's, the links'. One section — a title and a hairline over
 * the workbench well (`ServiceLogs size="section"`) — where five files each
 * drew the same frame at a height of their own.
 *
 * On a phone it is not a console. A pane with its own toolbar, chips and
 * scroller under a long page is a scrolling box inside a scrolling page,
 * which a phone never draws (D12): each source is a card instead, its kind
 * and size in words, that opens the Logs page on it — one pane, the whole
 * screen, with its own bar. A narrowing the page asked for goes with it.
 *
 * With no sources and nothing to say instead, the section is not drawn.
 */
export function EmbeddedLog({
  title,
  sources,
  instead,
  error,
  onRetry,
  ref,
  ...logs
}: EmbeddedLogProps) {
  const phone = useBelow("md")
  const ready = Boolean(instead) || Boolean(error) || sources !== undefined
  if (ready && !instead && !error && sources?.length === 0) return null
  return (
    <LogSection title={title} ready={ready} ref={ref}>
      {instead ? (
        instead
      ) : error && !sources ? (
        <ErrorState error={error} onRetry={onRetry} />
      ) : !sources ? null : phone ? (
        <ChoiceList aria-label={title}>
          {sources.map((source) => (
            <SourceCard key={source.id} source={source} ask={logs.ask} fallback={title} />
          ))}
        </ChoiceList>
      ) : (
        <ServiceLogs {...logs} sources={sources} size="section" />
      )}
    </LogSection>
  )
}

/**
 * One source as a card into the Logs page. A source the page has not
 * described is asked about once, for its size and its last write; a server
 * that does not answer leaves the card with what the page knew.
 */
function SourceCard({
  source,
  ask,
  fallback,
}: {
  source: ServiceLogSource
  ask?: LogAsk
  /** The section's title, for a source the server described without a name. */
  fallback: string
}) {
  const described = usePoll(
    (signal) => get<Partial<ServiceLogSource>>("/logs/source", { source: source.id }, signal),
    0,
    [source.id],
    { enabled: !source.described },
  )
  const facts = { ...described.data, ...stripUndefined(source) } as ServiceLogSource
  const product = source.product ?? lensFor(source.lens)?.product
  const name = source.label || source.path?.slice(source.path.lastIndexOf("/") + 1) || fallback
  return (
    <ChoiceRow
      leading={<ProductLogo id={product} size="sm" fallback={kindIcon(source.kind) ?? Logs} />}
      title={name}
      verb={`Open ${name} in Logs`}
      description={sourceSummary(facts) || undefined}
      // A source with no id cannot be named in the address; the Logs page
      // still opens, on the source it would have opened anyway.
      href={source.id ? logsPageHref(source.id, ask) : "/logs"}
    />
  )
}

/** The page's facts over the server's, without an undefined one hiding a measured one. */
function stripUndefined<T extends object>(value: T): Partial<T> {
  return Object.fromEntries(Object.entries(value).filter(([, v]) => v !== undefined)) as Partial<T>
}
