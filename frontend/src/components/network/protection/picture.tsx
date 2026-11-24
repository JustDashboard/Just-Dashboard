"use client"

import { useState } from "react"
import { flag } from "@/lib/countries"
import { plural } from "@/lib/format"
import type { ProtectionBlocklist, ProtectionLimit, ProtectionView } from "@/lib/types"
import { cn } from "@/lib/utils"
import { Check, Globe, ListUnordered, LockOpen, Plus, ShieldCheck } from "@/components/icons"
import { WireHost, WireMark, WirePlaceholder } from "@/components/wire/wire"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { WireFlow, type WireFlowNode, type WireLine } from "@/components/wire/wire-flow"
import { fitColumn } from "@/components/wire/wire-flow-model"
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
 * The nodes each side of the picture holds at most: three lists and "N more",
 * and a limit or two over the exceptions and the trusted set. As a column a
 * side the picture is held to this height whatever the server has (`FRAME`),
 * so the lists under it start where they will while the first read is still
 * out.
 */
export const PICTURE_ROWS = LISTS_DRAWN + 1

// Four rows of a mark beside three lines, their gaps and the picture's own
// padding, where the picture is a column a side (`WireFlow`'s `data-form`):
// four of 49.75px, three gaps of 20 and 32 of padding are 291px, so the frame
// is the next quarter rem over that. At 18rem it was three pixels short of
// four rows, and a server with fewer drew a shorter picture. A
// phone's picture is rows of marks, one height already. Where the sides are
// rows of nodes the frame is left off: four nodes are one row or two by the
// room the page has, and the frame stood a third taller than two rows and
// twice as tall as one. The waiting picture there is as tall as the drawn one
// because it draws as many nodes (`pictureRows`), each on the lines of a real
// one.
const FRAME =
  "lg:data-[form=column]:grid lg:data-[form=column]:min-h-[18.25rem] lg:data-[form=column]:items-center"

/**
 * The half of a node's line that is how its entry is set: the networks a list
 * holds, a limit's rate. Beside the mark it leads the line. In a row of nodes
 * (`WireFlow`'s `data-form`) a node is about 120px wide and the line was cut
 * inside what the entry had counted, so there the line is the count alone: the
 * setting is the line's `title` and in the entry's row under the picture.
 */
const SETTING = "lg:in-data-[form=rows]:hidden"

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
 * As a column a side it is one height in every state: four rows a side at
 * most, and room for four whatever the server has, so nothing under it moves
 * when the read lands or a list is added. On a wide page it is rows of nodes
 * (`WireFlow`'s `rows`) and as short as they are: the read lands on the nodes
 * the last visit drew, and a fourth list or a second limit may begin a second
 * row. On a phone it is rows of marks: the lists under it name every node
 * again.
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
          loading
          phone="marks"
          className={FRAME}
          start={{ title: "Refused", nodes: [], loadingRows: rows?.start ?? 3 }}
          end={{ title: "Gets through", nodes: [], loadingRows: rows?.end ?? 4 }}
          // Both lines the hub says under its name, on a phone too: its rows
          // of marks stand where the drawn picture's will.
          hub={{ mark: <WireHost />, eyebrow: "This server", loadingLines: { hint: 2 } }}
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

  const refused: WireFlowNode[] = view.blocklists.map((list) => {
    const held = `${list.count.toLocaleString()} networks`
    const dropped = `${compact(list.packets)} dropped`
    const counted = !list.error && list.enabled
    return {
      id: `list:${list.id}`,
      mark: (size) => <ListWireMark list={list} size={size} />,
      eyebrow: KIND_WORD[list.kind],
      title: (
        <span
          className={cn("block truncate", !list.enabled && "text-muted-foreground")}
          title={list.name}
        >
          {list.name}
        </span>
      ),
      name: list.name,
      label: `Open ${list.name}`,
      hint: (
        <span
          className={cn("numeric block truncate", list.error && "text-destructive")}
          title={counted ? `${held} · ${dropped}` : undefined}
        >
          {list.error ? (
            "fetch failed"
          ) : list.enabled ? (
            <>
              <span className={SETTING}>{held} · </span>
              {dropped}
            </>
          ) : (
            "switched off"
          )}
        </span>
      ),
      onPress: () => onOpenList(list),
      line: wire(list.enabled, `list:${list.id}`, Boolean(list.error)),
    }
  })
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
  const through: WireFlowNode[] = shown.map((limit) => {
    const rate = limitShort(limit)
    const turned = `${compact(limit.packets)} refused`
    return {
      id: `limit:${limit.id}`,
      mark: (size) => <LimitWireMark limit={limit} size={size} />,
      // Its own leading: the mono face on the eyebrow's line made that line a
      // pixel taller than a waiting node's, and a row of nodes with it.
      eyebrow: (
        <span className="font-mono leading-none tracking-normal normal-case">
          {portsWord(limit)}
        </span>
      ),
      title: (
        <span
          className={cn("block truncate", !limit.enabled && "text-muted-foreground")}
          title={limit.name}
        >
          {limit.name}
        </span>
      ),
      name: limit.name,
      label: `Open ${limit.name}`,
      hint: (
        <span
          className="numeric block truncate"
          title={limit.enabled ? [rate, turned].filter(Boolean).join(" · ") : undefined}
        >
          {limit.enabled ? (
            <>
              {rate && <span className={SETTING}>{rate} · </span>}
              {turned}
            </>
          ) : (
            "switched off"
          )}
        </span>
      ),
      onPress: () => onOpenLimit(limit),
      line: wire(limit.enabled, `limit:${limit.id}`),
    }
  })
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

/** The rows each side of the picture holds for so many lists and limits, for the grey one. */
export function pictureRows(lists: number, limits: number) {
  return {
    start: Math.max(1, Math.min(lists, PICTURE_ROWS)),
    end: Math.max(1, Math.min(limits, LIMITS_DRAWN + 1)) + 2,
  }
}
