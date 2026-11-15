"use client"

import { useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { Servers } from "@/components/icons"
import type { NetworkKind } from "@/lib/clients"
import type { Connections } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useViewState } from "@/lib/view-state"
import { NETWORK_GLYPH } from "@/components/client-mark"
import { ProductGlyph, hasProductLogo, processProduct } from "@/components/product-logo"
import { portsHref } from "@/components/proxy/ports-list"
import { WireHost, WireMark } from "@/components/deploy/wire"
import { Sparkline } from "@/components/metrics/sparkline"
import { WireFlow, type WireFlowNode } from "@/components/wire-flow"
import {
  PLACES,
  beat,
  foldPeers,
  type Place,
  type Program,
} from "@/components/network/connections/model"

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`

/** The programs drawn before the rest fold into "N more programs". */
const PROGRAMS = 5

/**
 * Who is connected, drawn on the shared picture (`wire-flow.tsx`): where the
 * callers are on the left — the internet, the tailnet, the local networks,
 * each with how many addresses and sockets and the busiest two addresses —
 * this server in the middle, and on the right the programs they reached, each
 * as the product it is with the ports it answered on. A wire moves while its
 * side holds an established connection, quicker the more it holds. Loopback
 * never left the machine and is not drawn.
 *
 * The three places are always drawn, a quiet one with a still dashed wire:
 * the picture keeps one shape from an empty server to a busy one, and
 * "nobody on the tailnet" is said rather than left out.
 *
 * The list under it is the same peers one address at a time; this is their
 * shape at once, which is what an incident asks first: is this the internet,
 * and what is it hitting? So the internet is the list's "From the internet"
 * chip, and a program narrows the list to the addresses that reached it.
 *
 * The figures that four tiles once carried live on its nodes: what listens
 * and what never left the machine on the hub, each place's addresses on the
 * place, and under each the counts of every read since the page opened as a
 * line — the backend keeps no history of connections, and a count that is
 * only ever "now" cannot show that the burst was ten minutes ago.
 *
 * Before the first read it is drawn at the height it will have: the three
 * places and as many programs as the last visit drew, on `WireFlow`'s own
 * waiting rows with the lines a place and the hub say (`loadingLines`), so
 * the list under the picture does not move when the read lands.
 */
export function ConnectionsPicture({
  data,
  loading,
  internetOnly,
  onInternet,
  onProgram,
}: {
  data: Connections | undefined
  loading: boolean
  internetOnly: boolean
  onInternet: () => void
  onProgram: (name: string) => void
}) {
  const { places, programs } = useMemo(() => foldPeers(data?.peers ?? []), [data?.peers])
  const history = useReadings(data, places)
  // How many programs the last read drew, kept for the next visit: the
  // picture waits at the height it had.
  const [drawn, setDrawn] = useViewState("network.connections.programs", 4)
  const rows = Math.max(1, Math.min(programs.length, PROGRAMS + 1))
  useEffect(() => {
    if (data && rows !== drawn) setDrawn(rows)
  }, [data, rows, drawn, setDrawn])
  const waiting = loading && !data

  return (
    <WireFlow
      className="animate-rise"
      phone="marks"
      loading={waiting}
      start={{
        title: "Where the callers are",
        loadingRows: PLACES.length,
        // A place's line of addresses, over the box its trend is drawn in.
        loadingLines: { foot: <Trend values={[]} label="" align="end" /> },
        nodes: waiting
          ? []
          : places.map((place) =>
              placeNode(place, history.places[place.kind] ?? [], {
                pressed: place.kind === "internet" && internetOnly,
                onPress:
                  place.kind === "internet" && place.peers.length > 0 ? onInternet : undefined,
              }),
            ),
      }}
      hub={{
        mark: <WireHost />,
        eyebrow: "This server",
        // Down a phone the hub says a second line: who is on each place.
        loadingLines: { hint: { wide: 1, narrow: 2 } },
        title: data && plural(data.total, "socket"),
        hint: data && (
          <>
            <span className="numeric block">
              {/* Listening goes to the page that names each socket: this page
                  is who arrived, that one is what was open for them. */}
              <Link
                href={portsHref({})}
                aria-label="Listening ports"
                className="rounded-sm underline-offset-4 focus-ring hover:text-foreground hover:underline"
              >
                {data.listening} listening
              </Link>{" "}
              · {data.loopback} on loopback
            </span>
            {/* Down a phone the places are marks without their words, so the
                hub says who is on each. */}
            <span className="numeric block lg:hidden">
              {places
                .filter((place) => place.peers.length > 0)
                .map((place) => `${place.peers.length} ${PLACE_WORD[place.kind] ?? place.kind}`)
                .join(" · ")}
            </span>
            <Trend values={history.total} label="Sockets since the page opened" align="centre" />
          </>
        ),
      }}
      end={{
        title: "What they reached",
        loadingRows: drawn,
        nodes: waiting ? [] : programs.map((program) => programNode(program, onProgram)),
        max: PROGRAMS,
        more: { words: (hidden) => `${hidden} more programs` },
        empty: "Nothing reached",
      }}
    />
  )
}

/** A place in the phone's one line under the hub. */
const PLACE_WORD: Partial<Record<NetworkKind, string>> = {
  internet: "from the internet",
  tailscale: "on the tailnet",
  local: "local",
}

function placeNode(
  place: Place,
  values: number[],
  press: { pressed: boolean; onPress?: () => void },
): WireFlowNode {
  const Glyph = NETWORK_GLYPH[place.kind]
  const quiet = place.peers.length === 0
  return {
    id: `place:${place.kind}`,
    mark: (size) => (
      <WireMark tone={quiet ? "neutral" : "logo"} shape="square" size={size === "sm" ? "sm" : "md"}>
        {place.kind === "tailscale" ? <ProductGlyph id="tailscale" /> : <Glyph aria-hidden />}
      </WireMark>
    ),
    eyebrow: quiet
      ? "nobody"
      : `${plural(place.peers.length, "address", "addresses")} · ${plural(place.sockets, "socket")}`,
    title: <span className={cn(quiet && "font-normal text-muted-foreground")}>{place.title}</span>,
    name: place.title,
    label: press.onPress
      ? press.pressed
        ? "Show every address"
        : "Show only the addresses from the internet"
      : undefined,
    onPress: press.onPress,
    hint: (
      <>
        {/* A quiet place keeps the line its addresses would stand on. */}
        <span className="block truncate font-mono">
          {quiet
            ? "\u00a0"
            : place.peers
                .slice(0, 2)
                .map((p) => p.address)
                .join(", ")}
          {place.peers.length > 2 && (
            <span className="ml-1.5 font-sans">+{place.peers.length - 2}</span>
          )}
        </span>
        <Trend
          values={values}
          label={`Addresses on ${place.title} since the page opened`}
          align="end"
        />
      </>
    ),
    line: quiet
      ? { state: "idle" }
      : { state: "up", moving: place.active > 0, duration: beat(place.active) },
  }
}

function programNode(program: Program, onProgram: (name: string) => void): WireFlowNode {
  const product = processProduct(program.name)
  return {
    id: `program:${program.key}`,
    mark: (size) => (
      <WireMark tone="logo" shape="square" size={size === "sm" ? "sm" : "md"}>
        {hasProductLogo(product) ? <ProductGlyph id={product} /> : <Servers aria-hidden />}
      </WireMark>
    ),
    eyebrow: `${plural(program.sockets, "socket")} · ${program.active} active`,
    title: program.name,
    name: program.name,
    label: `Show the addresses connected to ${program.name}`,
    onPress: () => onProgram(program.name),
    hint: (
      <span className="block truncate font-mono">
        {program.ports.slice(0, 4).map((p, i) => (
          <span key={p}>
            {i > 0 && <span className="text-muted-foreground">, </span>}
            <span className="text-muted-foreground">:</span>
            <span className="text-[var(--tag-pink)]">{p}</span>
          </span>
        ))}
        {program.ports.length > 4 && (
          <span className="ml-1 font-sans">+{program.ports.length - 4}</span>
        )}
      </span>
    ),
    line: { state: "up", moving: program.active > 0, duration: beat(program.active) },
  }
}

/**
 * A count's reads since the page opened, at a fixed size under its node. The
 * box is drawn before there are two reads to join, so the node does not grow
 * when the line arrives.
 */
function Trend({
  values,
  label,
  align,
}: {
  values: number[]
  label: string
  /** Where the node's words stand: the left column's are set against the wires. */
  align: "centre" | "end"
}) {
  const shown = values.length >= 2 && Math.min(...values) !== Math.max(...values)
  return (
    <span
      aria-hidden={!shown}
      className={cn(
        "mt-1 block h-4 w-20 max-lg:hidden",
        align === "centre" ? "mx-auto" : "lg:ml-auto",
      )}
    >
      {shown && (
        <Sparkline values={values} width={80} height={16} className="h-4 w-20" label={label} />
      )}
    </span>
  )
}

type Readings = { total: number[]; places: Partial<Record<NetworkKind, number[]>> }

/** Every read's counts since the page opened, at most an hour of them. */
function useReadings(data: Connections | undefined, places: Place[]): Readings {
  const [history, setHistory] = useState<Readings>({ total: [], places: {} })
  useEffect(() => {
    if (!data) return
    // eslint-disable-next-line react-hooks/set-state-in-effect -- appending a read, not deriving from props
    setHistory((previous) => {
      const next: Readings = { total: [...previous.total.slice(-359), data.total], places: {} }
      for (const kind of ["internet", "tailscale", "local"] as const) {
        const count = places.find((p) => p.kind === kind)?.peers.length ?? 0
        next.places[kind] = [...(previous.places[kind] ?? []).slice(-359), count]
      }
      return next
    })
  }, [data, places])
  return history
}
