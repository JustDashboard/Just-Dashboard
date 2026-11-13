"use client"

import { useState } from "react"
import { flag } from "@/lib/countries"
import { plural } from "@/lib/format"
import type { ProtectionBlocklist, ProtectionLimit, ProtectionView } from "@/lib/types"
import { cn } from "@/lib/utils"
import { Check, Globe, ListUnordered, LockOpen, Plus, ShieldCheck } from "@/components/icons"
import { WireHost, WireMark, WirePlaceholder } from "@/components/deploy/wire"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { Skeleton } from "@/components/ui/skeleton"
import { WireFlow, type WireFlowNode, type WireLine } from "@/components/wire-flow"
import { fitColumn } from "@/components/wire-flow-model"
import { compact, grown } from "@/components/network/gateway/reading"
import {
  LIMITS_DRAWN,
  LISTS_DRAWN,
  limitProduct,
  limitShort,
  portsWord,
  protectionCounters,
  protectionTotals,
} from "@/components/network/protection/reading"

type MarkSize = "lg" | "md" | "sm"

/**
 * The rows each side of the picture holds at most: three lists and "N more",
 * and a limit or two over the exceptions and the trusted set. The picture is
 * held to this height whatever the server has (`FRAME`), so the lists under
 * it start where they will while the first read is still out.
 */
export const PICTURE_ROWS = LISTS_DRAWN + 1

// Four rows of a mark beside three lines, their gaps and the picture's own
// padding. Wide only: a phone's picture is rows of marks, one height already.
// The same frame holds the grey picture (`grey`), whose nodes are drawn on
// the lines of real ones.
const FRAME = "lg:grid lg:min-h-[18rem] lg:items-center"

/** A list's mark on a wire: its first country's flag, else the glyph of its kind. */
export function ListWireMark({
  list,
  size,
}: {
  list: Pick<ProtectionBlocklist, "kind" | "countries">
  size: MarkSize
}) {
  return (
    <WireMark tone="logo" shape="square" size={size}>
      {list.kind === "country" && list.countries[0] ? (
        <span aria-hidden className={cn("leading-none", size === "sm" ? "text-base" : "text-xl")}>
          {flag(list.countries[0])}
        </span>
      ) : list.kind === "feed" ? (
        <Globe aria-hidden />
      ) : (
        <ListUnordered aria-hidden />
      )}
    </WireMark>
  )
}

/** A limit's mark on a wire: the product that listens on its port, else a shield. */
function LimitWireMark({ limit, size }: { limit: ProtectionLimit; size: MarkSize }) {
  const product = limitProduct(limit)
  return (
    <WireMark tone="logo" shape="square" size={size}>
      {hasProductLogo(product) ? <ProductGlyph id={product} /> : <ShieldCheck aria-hidden />}
    </WireMark>
  )
}

const KIND_WORD: Record<ProtectionBlocklist["kind"], string> = {
  country: "Countries",
  feed: "Feed",
  manual: "Typed",
}

/**
 * Which lists, limits and exceptions counted a packet since the page last
 * read them: the only "alive" a firewall counter can give, as the gateway's
 * picture reads it (`gateway/use-growth`). Kept as state and compared while
 * rendering, so a poll that brought the same counters says nothing moved.
 */
function useMoved(view: ProtectionView | undefined): Set<string> {
  const [seen, setSeen] = useState<{
    view: ProtectionView | undefined
    counters: Record<string, number>
    moved: Set<string>
  }>({ view: undefined, counters: {}, moved: new Set() })
  if (!view || seen.view === view) return seen.moved
  const next = protectionCounters(view)
  const moved = grown(seen.counters, next)
  setSeen({ view, counters: next, moved })
  return moved
}

/**
 * What this server refuses and what it lets through, in the wiring every
 * picture in the product speaks (`WireFlow`).
 *
 * Left is who is refused: each blocklist as its flag or its kind, with the
 * networks it holds and what it dropped. The middle is this server, saying
 * whether its filter is in the kernel and how full the connection table is —
 * the table filling up is what a flood is aiming for, so it takes its level's
 * colour. Right is what gets through: each limited port as the product that
 * listens there, with its rate and what it refused, then the exceptions and
 * the addresses nothing may refuse.
 *
 * A wire carries a pulse when its counter grew since the last read, runs
 * still when it did not, and is dashed while its entry is switched off or the
 * table is not loaded. A kind with nothing made yet stands as a dashed ring
 * that opens the form which makes one.
 *
 * It is one height in every state: four rows a side at most, and room for
 * four whatever the server has, so nothing under it moves when the read lands
 * or a list is added. On a phone it is rows of marks: the lists under it name
 * every node again.
 */
export function ProtectionPicture({
  view,
  rows,
  writable,
  onNewList,
  onNewLimit,
  onOpenList,
  onOpenLimit,
}: {
  /** Undefined while the first read is out: the picture is drawn grey at its height. */
  view: ProtectionView | undefined
  /** The rows each side held at the last visit, for the grey picture. */
  rows?: { start: number; end: number }
  writable: boolean
  onNewList: () => void
  onNewLimit: () => void
  onOpenList: (list: ProtectionBlocklist) => void
  onOpenLimit: (limit: ProtectionLimit) => void
}) {
  const moved = useMoved(view)
  if (!view)
    return (
      <div data-slot="protection-picture" data-loading aria-busy>
        <WireFlow
          phone="marks"
          className={FRAME}
          start={{ title: "Refused", nodes: grey("start", rows?.start ?? 3) }}
          end={{ title: "Gets through", nodes: grey("end", rows?.end ?? 4) }}
          hub={{
            mark: <WireHost />,
            eyebrow: "This server",
            title: <Skeleton className="inline-block h-3 w-24 align-middle" />,
            hint: (
              <>
                <span className="block">
                  <Skeleton className="inline-block h-2.5 w-32 align-middle" />
                </span>
                <span className="block">
                  <Skeleton className="inline-block h-2.5 w-20 align-middle" />
                </span>
              </>
            ),
          }}
        />
      </div>
    )

  const totals = protectionTotals(view)
  const table = view.conntrack
  // A wire is a promise while its entry is off or the table is not in the kernel.
  const wire = (enabled: boolean, key: string, warning = false): WireLine => {
    const live = enabled && view.loaded
    return {
      state: !live ? "idle" : warning ? "warning" : "up",
      moving: live && moved.has(key),
      duration: 2.4,
    }
  }

  const refused: WireFlowNode[] = view.blocklists.map((list) => ({
    id: `list:${list.id}`,
    mark: (size) => <ListWireMark list={list} size={size} />,
    eyebrow: KIND_WORD[list.kind],
    title: (
      <span className={cn("block truncate", !list.enabled && "text-muted-foreground")}>
        {list.name}
      </span>
    ),
    name: list.name,
    label: `Open ${list.name}`,
    hint: (
      <span className={cn("numeric block truncate", list.error && "text-destructive")}>
        {list.error
          ? "fetch failed"
          : list.enabled
            ? `${list.count.toLocaleString()} networks · ${compact(list.packets)} dropped`
            : "switched off"}
      </span>
    ),
    onPress: () => onOpenList(list),
    line: wire(list.enabled, `list:${list.id}`, Boolean(list.error)),
  }))
  if (refused.length === 0)
    refused.push({
      id: "add:list",
      placeholder: true,
      mark: (size) => (
        <WirePlaceholder size={size} fallback={Globe}>
          <Plus aria-hidden />
        </WirePlaceholder>
      ),
      title: <span className="text-muted-foreground">New blocklist</span>,
      name: "New blocklist",
      label: "New blocklist from the picture",
      hint: "countries, a feed or addresses",
      onPress: writable ? onNewList : undefined,
      line: { state: "idle" },
    })

  const { shown, hidden } = fitColumn(view.limits, LIMITS_DRAWN)
  const through: WireFlowNode[] = shown.map((limit) => ({
    id: `limit:${limit.id}`,
    mark: (size) => <LimitWireMark limit={limit} size={size} />,
    eyebrow: <span className="font-mono tracking-normal normal-case">{portsWord(limit)}</span>,
    title: (
      <span className={cn("block truncate", !limit.enabled && "text-muted-foreground")}>
        {limit.name}
      </span>
    ),
    name: limit.name,
    label: `Open ${limit.name}`,
    hint: (
      <span className="numeric block truncate">
        {limit.enabled
          ? [limitShort(limit), `${compact(limit.packets)} refused`].filter(Boolean).join(" · ")
          : "switched off"}
      </span>
    ),
    onPress: () => onOpenLimit(limit),
    line: wire(limit.enabled, `limit:${limit.id}`),
  }))
  if (hidden > 0) {
    const rest = view.limits.slice(LIMITS_DRAWN)
    through.push({
      id: "limits:more",
      mark: (size) => (
        <WireMark tone="logo" shape="square" size={size}>
          <span className="numeric text-hint">+{hidden}</span>
        </WireMark>
      ),
      eyebrow: "Limits",
      title: <span className="block truncate">{plural(hidden, "more limit")}</span>,
      name: plural(hidden, "more limit"),
      hint: (
        <span className="numeric block truncate">
          {compact(rest.reduce((n, l) => n + l.packets, 0))} refused
        </span>
      ),
      href: "#limits",
      line: wire(
        rest.some((l) => l.enabled),
        "limits:more",
      ),
    })
  }
  if (view.limits.length === 0)
    through.push({
      id: "add:limit",
      placeholder: true,
      mark: (size) => (
        <WirePlaceholder size={size} fallback={ShieldCheck}>
          <Plus aria-hidden />
        </WirePlaceholder>
      ),
      title: <span className="text-muted-foreground">New limit</span>,
      name: "New limit",
      label: "New limit from the picture",
      hint: "a rate or a ceiling on a port",
      onPress: writable ? onNewLimit : undefined,
      line: { state: "idle" },
    })
  through.push(
    {
      id: "exceptions",
      mark: (size) => (
        <WireMark tone="logo" shape="square" size={size}>
          <LockOpen aria-hidden />
        </WireMark>
      ),
      eyebrow: "Exceptions",
      title: (
        <span className={cn("numeric", totals.exceptions === 0 && "text-muted-foreground")}>
          {totals.exceptions === 0 ? "None" : plural(totals.exceptions, "network")}
        </span>
      ),
      name: "Exceptions",
      label: "Exceptions",
      hint: (
        <span className="numeric block truncate">
          {totals.exceptions === 0
            ? "nothing let past the drops"
            : `${compact(totals.letThrough)} let through`}
        </span>
      ),
      href: "#exceptions",
      line: wire(totals.exceptionsLive > 0, "exceptions"),
    },
    {
      id: "trusted",
      mark: (size) => (
        <WireMark tone={view.clientTrusted ? "success" : "warning"} shape="square" size={size}>
          <Check aria-hidden />
        </WireMark>
      ),
      eyebrow: "Never refused",
      title: <span className="numeric">{plural(view.trusted.length, "address", "addresses")}</span>,
      name: "Never refused",
      label: "Never refused",
      hint: (
        <span className={cn("block truncate", !view.clientTrusted && "text-warning")}>
          {view.clientTrusted ? "yours among them" : "yours is not among them"}
        </span>
      ),
      href: "#never-refused",
      // Trust is not counted, so its wire says only that it holds.
      line: { state: view.clientTrusted ? "up" : "warning" },
    },
  )

  return (
    <div data-slot="protection-picture">
      <WireFlow
        phone="marks"
        className={FRAME}
        start={{
          title: "Refused",
          nodes: refused,
          max: LISTS_DRAWN,
          more: { href: "#blocklists", words: (n) => plural(n, "more list") },
        }}
        end={{ title: "Gets through", nodes: through }}
        hub={{
          mark: <WireHost />,
          eyebrow: "This server",
          title: (
            <span className={cn(totals.inForce && !view.loaded && "text-warning")}>
              {!totals.inForce
                ? "Nothing in force"
                : view.loaded
                  ? "Filter in force"
                  : "Not loaded"}
            </span>
          ),
          hint: table.available ? (
            <>
              <span
                role="meter"
                aria-label="Connection table fullness"
                aria-valuemin={0}
                aria-valuemax={100}
                aria-valuenow={table.percent}
                className={cn(
                  "numeric block",
                  table.level === "warning" && "text-warning",
                  table.level === "critical" && "text-destructive",
                )}
              >
                connection table {table.percent}% full
              </span>
              <span className="numeric block">
                {table.count.toLocaleString()} of {table.max.toLocaleString()}
              </span>
            </>
          ) : (
            <>
              <span className="block">connections not tracked</span>
              {/* The second line the hint has when the table is tracked. */}
              <span className="block">&nbsp;</span>
            </>
          ),
        }}
      />
    </div>
  )
}

/**
 * The picture before its read: grey nodes on the lines of drawn ones. They
 * are nodes rather than `WireFlow`'s own loading rows because those are a
 * little taller than a node with three one-line parts, on a phone as well as
 * wide, and this picture is held to the height of the drawn one.
 */
function grey(side: string, count: number): WireFlowNode[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `${side}:grey:${i}`,
    placeholder: true,
    mark: (size) => <Skeleton className={cn("rounded-xl", size === "sm" ? "size-9" : "size-11")} />,
    eyebrow: <Skeleton className="inline-block h-2 w-14 align-middle" />,
    title: <Skeleton className="inline-block h-3 w-28 align-middle" />,
    hint: (
      <span className="block">
        <Skeleton className="inline-block h-2.5 w-24 align-middle" />
      </span>
    ),
    line: { state: "up" },
  }))
}

/** The rows each side of the picture holds for so many lists and limits, for the grey one. */
export function pictureRows(lists: number, limits: number) {
  return {
    start: Math.max(1, Math.min(lists, PICTURE_ROWS)),
    end: Math.max(1, Math.min(limits, LIMITS_DRAWN + 1)) + 2,
  }
}
