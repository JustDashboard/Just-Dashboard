"use client"

import Link from "next/link"
import {
  Bug,
  ChevronDown,
  ChevronUp,
  ClockRewind,
  Copy,
  Cross,
  Filter,
  Globe,
  LockClosed,
  LockOpen,
  MoreHorizontal,
  Slash,
  Terminal,
  TerminalWindow,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import { bytes, relativeTime, timestamp } from "@/lib/format"
import { copyText } from "@/lib/clipboard"
import { crawlerOf, networkOf, parseAgent, refererProduct } from "@/lib/clients"
import type { RequestEntry, RequestSummary } from "@/lib/types"
import { latency, latencyBracket, latencyTone, requestURI, statusWord } from "@/lib/requests"
import { ClientMark, NETWORK_GLYPH } from "@/components/client-mark"
import { IconAction } from "@/components/icon-action"
import { PaneHeader, PaneSection } from "@/components/panel"
import { PaneBack } from "@/components/phone-panes"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { VerbMenu, type Verb } from "@/components/verbs"
import type { RequestBlockState } from "@/components/deploy/request-blocks"
import { curlOf } from "@/components/deploy/request-console"
import {
  Address,
  MethodWord,
  RequestPath,
  StatusCode,
  clientName,
  isPrivate,
} from "@/components/deploy/request-marks"

/** Who sent a request, as the page says it: a person's browser, a crawler, a program. */
export type Sender = "person" | "crawler" | "program" | "unknown"

/**
 * The same call the server makes for `who=people`: a browser's agent that is
 * no crawler. A program (curl, a Go client, a monitor) and a crawler are both
 * "bots" to the filter; here they are told apart, because the inspector names
 * which.
 */
export function senderOf(entry: RequestEntry): Sender {
  const ua = entry.userAgent
  if (!ua) return "unknown"
  if (crawlerOf(ua)) return "crawler"
  const agent = parseAgent(ua)
  if (agent.device === "program" || !/mozilla\//i.test(ua)) return "program"
  return "person"
}

const SENDER: Record<Sender, { label: string; tone: "default" | "warning" | "success" }> = {
  person: { label: "Person", tone: "success" },
  crawler: { label: "Crawler", tone: "default" },
  program: { label: "Program", tone: "default" },
  unknown: { label: "No agent", tone: "default" },
}

/**
 * One request, beside the list it was picked from.
 *
 * It used to open under its own row, which grew the row by two hundred pixels
 * and pushed every request after it down the pane; the next row the reader
 * wanted moved as they reached for it. Here the list keeps its shape and the
 * request is read in a column of its own, in the order the questions come:
 * what it was and how it went, who asked, where it came from and over what,
 * and then what to do next. Under those, what the page knows was written
 * while it was in flight — the container's lines, the proxy's word on a
 * failure — so "why was this a 502" is answered here rather than on another
 * page.
 *
 * The arrows walk the list the inspector was opened from, newest at the top,
 * as ↑ and ↓ do while it is open; Escape closes it. On a phone it is the
 * pane, with the way back where a phone's is.
 */
export function RequestInspector({
  entry,
  summary,
  latencyKnown,
  phone,
  onClose,
  onNewer,
  onOlder,
  onFilterPath,
  onFilterClient,
  onBlock,
  blockState,
  blockUnavailable,
  onEventsAround,
  onOutput,
  trace,
  inline,
}: {
  entry: RequestEntry
  summary: RequestSummary
  latencyKnown: boolean
  /** Drawn as the phone's pane: the way back in place of the close. */
  phone?: boolean
  onClose: () => void
  /** The request above it in the list, when there is one. */
  onNewer?: () => void
  /** The request below it in the list, when there is one. */
  onOlder?: () => void
  onFilterPath?: (path: string) => void
  onFilterClient?: (ip: string) => void
  onBlock?: (ip: string) => void
  blockState?: (ip: string) => RequestBlockState
  blockUnavailable?: string
  onEventsAround?: (entry: RequestEntry) => void
  onOutput?: () => void
  trace?: { href: string; label: string }
  inline?: React.ReactNode
}) {
  const ip = entry.remoteIp
  const network = ip ? networkOf(ip) : undefined
  const Place = network ? NETWORK_GLYPH[network.kind] : Globe
  const scanner = ip ? summary.scanners.some((s) => s.value === ip) : false
  const sender = senderOf(entry)
  const crawler = entry.userAgent ? crawlerOf(entry.userAgent) : undefined
  const agent = entry.userAgent ? parseAgent(entry.userAgent) : undefined
  const referer = entry.referer ? refererProduct(entry.referer) : undefined
  const bracket = latencyBracket(entry.durationMs, summary.latency)
  const slow = latencyTone(entry.durationMs) === "warning"
  const Lock = entry.tls ? LockClosed : LockOpen
  const curl = curlOf(entry)
  const blockable = Boolean(ip && onBlock && !isPrivate(ip))

  const verbs: Verb[] = [
    {
      key: "json",
      label: "Copy as JSON",
      icon: Copy,
      run: () => void copyText(JSON.stringify(entry, null, 2), "Request copied"),
    },
    ...(curl
      ? [
          {
            key: "curl",
            label: "Copy as curl",
            icon: Terminal,
            run: () => void copyText(curl, "Copied as curl"),
          },
        ]
      : []),
    ...(ip && blockable && onBlock
      ? [
          {
            key: "block",
            // A deny takes a round trip, and a verb that sits still while it
            // runs is pressed twice — which writes the rule twice (§13).
            label:
              blockState?.(ip) === "saved"
                ? "Deny rule saved"
                : blockState?.(ip) === "blocking"
                  ? "Blocking…"
                  : "Block this address",
            icon: Slash,
            danger: true,
            disabled: Boolean(blockUnavailable) || Boolean(blockState?.(ip)),
            progressive: "Blocking…",
            run: () => onBlock(ip),
          },
        ]
      : []),
  ]

  const facts: [string, React.ReactNode, string?][] = [
    ["When", timestamp(entry.time)],
    ["Host", entry.host ? <span className="font-mono">{entry.host}</span> : "—", entry.host],
    [
      "Over",
      <span key="p" className="inline-flex min-w-0 items-center gap-1.5">
        <Lock
          aria-hidden
          className={cn("size-3 shrink-0", entry.tls ? "text-muted-foreground" : "text-warning")}
        />
        <span className="truncate">
          {[entry.proto, entry.tls ? "TLS" : "plaintext"].filter(Boolean).join(" · ")}
        </span>
      </span>,
    ],
    ["Sent", entry.size ? bytes(entry.size) : "nothing"],
  ]
  if (entry.release)
    facts.push([
      "Release",
      <span key="release" className="font-mono">
        {entry.release}
      </span>,
    ])
  if (entry.user) facts.push(["Tailnet login", entry.user, entry.user])
  if (entry.edgeRule) facts.push(["Edge rule", entry.edgeRule, entry.edgeRule])
  if (entry.traceId)
    facts.push([
      "Trace",
      <span key="trace" className="flex min-w-0 items-baseline gap-2">
        <span className="truncate font-mono">{entry.traceId}</span>
        {trace && (
          <a
            href={trace.href}
            target="_blank"
            rel="noreferrer noopener"
            className="shrink-0 rounded-sm font-medium text-foreground focus-ring hover:underline"
          >
            {trace.label}
          </a>
        )}
      </span>,
      entry.traceId,
    ])

  return (
    <section
      aria-label="Request"
      data-slot="request-inspector"
      className="flex min-h-0 min-w-0 flex-1 flex-col bg-card"
    >
      <PaneHeader className="gap-1 pr-1">
        {phone && <PaneBack label="Back to the requests" until="lg" onClick={onClose} />}
        <h2 className="min-w-0 flex-1 truncate text-xs font-medium">
          Request
          <span className="font-normal text-muted-foreground"> · {relativeTime(entry.time)}</span>
        </h2>
        <IconAction label="Newer request" tooltip="Newer · ↑" onClick={onNewer} disabled={!onNewer}>
          <ChevronUp />
        </IconAction>
        <IconAction label="Older request" tooltip="Older · ↓" onClick={onOlder} disabled={!onOlder}>
          <ChevronDown />
        </IconAction>
        {!phone && (
          <IconAction label="Close the request" tooltip="Close · Esc" onClick={onClose}>
            <Cross />
          </IconAction>
        )}
      </PaneHeader>

      <div className="min-h-0 flex-1 overflow-y-auto">
        {/* What it was and how it went: the answer in its family's colour
            and words, the time it took against the window, then the request
            line whole — the one place a long path is not cut. */}
        <div className="flex flex-col gap-2.5 border-b border-hairline px-4 py-4">
          <div className="flex items-baseline justify-between gap-3">
            <StatusCode status={entry.status} className="text-title" />
            {latencyKnown && entry.durationMs !== undefined && (
              <span
                className={cn(
                  "numeric shrink-0 text-title font-semibold",
                  slow ? "text-warning" : "text-foreground",
                )}
              >
                {latency(entry.durationMs)}
              </span>
            )}
          </div>
          <p className="flex items-baseline justify-between gap-3 text-hint text-muted-foreground">
            <span>{statusWord(entry.status)}</span>
            {bracket && <span className="text-right">{bracket}</span>}
          </p>
          <p
            className="font-mono text-xs leading-5 break-all"
            title={`${entry.method} ${requestURI(entry)}`}
          >
            <MethodWord method={entry.method} className="mr-2" />
            <RequestPath
              path={entry.path}
              query={entry.query}
              className="inline whitespace-normal"
            />
          </p>
        </div>

        <PaneSection
          title="Who"
          aside={
            <span className="flex items-center gap-1.5">
              {scanner && <Tag tone="warning">scanner</Tag>}
              <Tag tone={SENDER[sender].tone}>{SENDER[sender].label}</Tag>
            </span>
          }
        >
          <div className="flex min-w-0 items-center gap-2.5">
            {crawler ? (
              <ProductLogo id={crawler.product} size="sm" fallback={Bug} />
            ) : entry.userAgent ? (
              <ClientMark userAgent={entry.userAgent} />
            ) : (
              <ProductLogo id={network?.product} size="sm" fallback={Place} />
            )}
            <span className="flex min-w-0 flex-col">
              <span className="truncate text-body font-medium">
                {entry.user ?? (entry.userAgent ? clientName(entry.userAgent) : "No user agent")}
              </span>
              {agent && agent.device !== "program" && (
                <span className="truncate text-hint text-muted-foreground capitalize">
                  {agent.device}
                </span>
              )}
            </span>
          </div>
          {ip && (
            <div className="flex min-w-0 items-center gap-2 text-xs">
              {network?.product ? (
                <ProductGlyph id={network.product} />
              ) : (
                <Place aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
              )}
              <Address ip={ip} />
              {network && <span className="truncate text-muted-foreground">{network.label}</span>}
            </div>
          )}
          {entry.referer && (
            <div className="flex min-w-0 items-center gap-2 text-xs" title={entry.referer}>
              {referer ? (
                <ProductGlyph id={referer} />
              ) : (
                <Globe aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
              )}
              <span className="shrink-0 text-muted-foreground">came from</span>
              <span className="truncate">{entry.referer}</span>
            </div>
          )}
          {entry.userAgent && (
            <p className="font-mono text-hint leading-4 break-all text-muted-foreground">
              {entry.userAgent}
            </p>
          )}
        </PaneSection>

        <PaneSection title="Details">
          <dl className="grid grid-cols-[5.5rem_minmax(0,1fr)] gap-x-3 gap-y-1.5 text-xs">
            {facts.map(([label, value, title]) => (
              <div key={label} className="contents">
                <dt className="text-hint leading-4 text-muted-foreground">{label}</dt>
                <dd
                  className="min-w-0 truncate"
                  title={title ?? (typeof value === "string" ? value : undefined)}
                >
                  {value}
                </dd>
              </div>
            ))}
          </dl>
        </PaneSection>

        <PaneSection title="Follow it">
          <div className="flex flex-col gap-1.5">
            {/* The questions a request raises, each one press, all on this
                page: what the container printed then, what happened to it
                then, and everything else to the same path or from the same
                address. */}
            {onOutput && (
              <Button size="sm" variant="outline" className="justify-start" onClick={onOutput}>
                <TerminalWindow className="size-3.5" />
                Open in Output
              </Button>
            )}
            {onEventsAround && (
              <Button
                size="sm"
                variant="outline"
                className="justify-start"
                onClick={() => onEventsAround(entry)}
              >
                <ClockRewind className="size-3.5" />
                Container events around this moment
              </Button>
            )}
            {onFilterPath && (
              <Button
                size="sm"
                variant="outline"
                className="justify-start"
                onClick={() => onFilterPath(entry.path)}
              >
                <Filter className="size-3.5" />
                Show every request to this path
              </Button>
            )}
            {ip && onFilterClient && (
              <Button
                size="sm"
                variant="outline"
                className="justify-start"
                onClick={() => onFilterClient(ip)}
              >
                <Filter className="size-3.5" />
                Show every request from this client
              </Button>
            )}
            <VerbMenu
              verbs={verbs}
              align="start"
              label="More actions for this request"
              trigger={
                <Button
                  size="sm"
                  variant="ghost"
                  className="justify-start text-muted-foreground"
                  aria-label="More actions for this request"
                >
                  <MoreHorizontal className="size-3.5" />
                  Copy or block
                </Button>
              }
            />
          </div>
          {blockable && blockUnavailable && (
            <p className="text-hint text-muted-foreground">
              {blockUnavailable}{" "}
              <Link href="/network/firewall" className="text-link hover:underline">
                Open Firewall
              </Link>
            </p>
          )}
        </PaneSection>

        {inline && <div className="px-4 pb-4 [&>*:first-child]:mt-3">{inline}</div>}
      </div>
    </section>
  )
}
