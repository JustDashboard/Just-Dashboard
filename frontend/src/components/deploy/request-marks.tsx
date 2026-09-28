"use client"

import { Bug, DesktopDevice, Terminal } from "@/components/icons"
import { cn } from "@/lib/utils"
import { crawlerOf, describeClient, networkOf, parseAgent } from "@/lib/clients"
import { CLASS_TEXT, CLASS_TONE, methodEmphasis, statusClass, statusWord } from "@/lib/requests"
import type { SocketState } from "@/hooks/use-socket"
import { NETWORK_GLYPH } from "@/components/client-mark"
import { tokenClass } from "@/components/logs/log-text"
import { FactDot } from "@/components/metrics/host-identity"
import { ProductGlyph } from "@/components/product-logo"
import { StatusDot, type DotTone } from "@/components/status-dot"

/**
 * A request's parts, drawn once.
 *
 * The request console, the opened row, the Insights lists and the Scanners
 * notice all draw a path, a method, a status and a client, and each had its
 * own spans for them — so a path was ink in the rows and ink in a different
 * size on Insights, and the host's own Logs page drew the same nginx line in
 * colour. These take the log console's token classes (`log-text.tsx`) and the
 * request log's status map (`lib/requests.ts`), so a request reads the same
 * wherever it is drawn: the path in the path hue, a write in the method hue,
 * a code in its family's colour, an address in the address hue and the
 * browser that sent it as itself (§14).
 *
 * `plain` is the console's Colour switch turned off — the line as it was
 * written, with only a failure and a refusal still coloured, because those
 * are readings of state rather than decoration (§3).
 */

/**
 * The path in the path hue, and its query split into keys and values the way
 * a log line's logfmt pairs are. The path is one span, so `/api/checkout` is
 * one run of text for find-in-page and for a test to read.
 */
export function RequestPath({
  path,
  query,
  plain,
  className,
}: {
  path: string
  query?: string
  plain?: boolean
  className?: string
}) {
  return (
    <span className={cn("min-w-0 truncate font-mono", className)}>
      <span className={plain ? undefined : tokenClass("path")}>{path}</span>
      {query &&
        (plain ? (
          <span className="text-muted-foreground/60">?{query}</span>
        ) : (
          <QueryText query={query} />
        ))}
    </span>
  )
}

function QueryText({ query }: { query: string }) {
  const punct = tokenClass("punct")
  return (
    <>
      <span className={punct}>?</span>
      {query.split("&").map((pair, i) => {
        const at = pair.indexOf("=")
        const key = at < 0 ? pair : pair.slice(0, at)
        const value = at < 0 ? undefined : pair.slice(at + 1)
        return (
          <span key={i}>
            {i > 0 && <span className={punct}>&amp;</span>}
            <span className={tokenClass("key")}>{key}</span>
            {value !== undefined && (
              <>
                <span className={punct}>=</span>
                <span className={tokenClass(/^-?\d+(\.\d+)?$/.test(value) ? "number" : "string")}>
                  {value}
                </span>
              </>
            )}
          </span>
        )
      })}
    </>
  )
}

/** A method as a word: reads muted, writes in the method hue. */
export function MethodWord({
  method,
  plain,
  className,
}: {
  method: string
  plain?: boolean
  className?: string
}) {
  return (
    <span
      className={cn(
        "font-mono",
        plain ? "text-muted-foreground" : methodEmphasis(method),
        className,
      )}
    >
      {method}
    </span>
  )
}

/**
 * A status code in its family's colour, and the word a reader uses for it
 * when there is room: "500 Server error". Off, only the two families that
 * mean something went wrong keep a colour.
 */
export function StatusCode({
  status,
  word,
  plain,
  className,
}: {
  status: number
  word?: boolean
  plain?: boolean
  className?: string
}) {
  const klass = statusClass(status)
  const tone = CLASS_TONE[klass]
  return (
    <span className={cn("inline-flex min-w-0 items-baseline gap-1.5", className)}>
      <span
        className={cn(
          "numeric font-mono font-medium",
          plain
            ? tone === "danger"
              ? "text-destructive"
              : tone === "warning"
                ? "text-warning"
                : undefined
            : CLASS_TEXT[klass],
        )}
      >
        {status}
      </span>
      {word && (
        <span className="truncate font-sans text-muted-foreground">{statusWord(status)}</span>
      )}
    </span>
  )
}

/**
 * Who asked: the browser or program as its own mark, the tailnet as
 * Tailscale's, and the address. The title says the rest in words.
 *
 * A request with no user agent — the health probe, a script that sends none —
 * has no client to draw, so its slot draws where the address is instead, the
 * way the Clients list draws the same address: this server's own probe reads
 * as this server rather than as a hole in the line.
 */
export function ClientCell({
  ip,
  userAgent,
  plain,
  className,
}: {
  ip?: string
  userAgent?: string
  plain?: boolean
  className?: string
}) {
  const agent = userAgent ? parseAgent(userAgent) : undefined
  const crawler = userAgent ? crawlerOf(userAgent) : undefined
  const network = ip ? networkOf(ip) : undefined
  // A crawler first: Googlebot's own agent string names Chrome too.
  const product = crawler ? crawler.product : agent?.product
  const Fallback = crawler ? Bug : agent?.device === "program" ? Terminal : DesktopDevice
  const Place = network && !agent ? NETWORK_GLYPH[network.kind] : undefined
  return (
    <span
      title={[userAgent ? clientName(userAgent) : "No user agent", network?.label]
        .filter(Boolean)
        .join(" · ")}
      className={cn("flex min-w-0 items-center gap-1.5", className)}
    >
      <span aria-hidden className="flex size-3.5 shrink-0 items-center justify-center">
        {product ? (
          <ProductGlyph id={product} />
        ) : agent ? (
          <Fallback className="size-3.5 text-muted-foreground" />
        ) : network?.product ? (
          <ProductGlyph id={network.product} />
        ) : Place ? (
          <Place className="size-3.5 text-muted-foreground/60" />
        ) : null}
      </span>
      {agent && network?.product && <ProductGlyph id={network.product} />}
      {ip ? <Address ip={ip} plain={plain} /> : <span className="text-muted-foreground/60">—</span>}
    </span>
  )
}

/** What a person would call the client: "Chrome on macOS", "curl", "Googlebot". */
export function clientName(userAgent: string) {
  return crawlerOf(userAgent)?.name ?? describeClient(userAgent)
}

/**
 * An address in the address hue, as a log line draws one — except this
 * server asking itself, the health probe, which recedes: it is the one
 * client nobody is looking for.
 */
export function Address({
  ip,
  plain,
  className,
}: {
  ip: string
  plain?: boolean
  className?: string
}) {
  return (
    <span
      className={cn(
        "numeric truncate font-mono",
        networkOf(ip).kind === "server" || plain ? "text-muted-foreground/60" : tokenClass("ip"),
        className,
      )}
    >
      {ip}
    </span>
  )
}

/**
 * An address there is no point offering to block: this server talking to
 * itself, something already inside the network the firewall stands at the
 * edge of, or a peer on the tailnet. It is `networkOf` asked one question,
 * rather than a second classifier: the two disagreed, and a row drawn with
 * Tailscale's mark offered a firewall deny against a tailnet peer.
 */
export function isPrivate(ip: string) {
  return networkOf(ip).kind !== "internet"
}

/**
 * A socket's state, as the Live chip's dot and a pane's footer both draw it:
 * open is live and breathing, connecting is amber and still, and a socket
 * that closed or failed is grey and says so. One mapping, so the chip and
 * the footer under it never draw one state in two tones.
 */
export function socketReading(state: SocketState): {
  tone: DotTone
  label: string
  live: boolean
} {
  if (state === "open") return { tone: "running", label: "Live", live: true }
  if (state === "connecting") return { tone: "warning", label: "Connecting", live: false }
  return { tone: "stopped", label: "Disconnected", live: false }
}

/** The dot inside a Live chip, in `socketReading`'s tones. */
export function LiveDot({ state }: { state: SocketState }) {
  const { tone, live } = socketReading(state)
  return <StatusDot tone={tone} live={live} />
}

/**
 * A dot between the facts of a line that wraps — the alerts line, a pane's
 * footer — drawn from `sm` up. On a phone the line breaks, and a dot left at
 * the end of one reads as a stray mark rather than a separator; the gap
 * between the facts does its job there.
 */
export function WrapDot() {
  return (
    <span aria-hidden className="max-sm:hidden">
      <FactDot />
    </span>
  )
}
