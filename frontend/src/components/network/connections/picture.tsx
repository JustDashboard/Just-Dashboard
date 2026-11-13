"use client"

import { useEffect, useMemo, useState } from "react"
import { Servers } from "@/components/icons"
import type { NetworkKind } from "@/lib/clients"
import type { Connections } from "@/lib/types"
import { cn } from "@/lib/utils"
import { NETWORK_GLYPH } from "@/components/client-mark"
import { ProductGlyph, hasProductLogo, processProduct } from "@/components/product-logo"
import { WireHost, WireMark } from "@/components/deploy/wire"
import { Sparkline } from "@/components/metrics/sparkline"
import { WireFlow, type WireFlowNode } from "@/components/wire-flow"
import { beat, foldPeers, type Place, type Program } from "@/components/network/connections/model"

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`

/**
 * Who is connected, drawn on the shared picture (`wire-flow.tsx`): where the
 * callers are on the left — the internet, the tailnet, the local networks,
 * each with how many addresses and sockets and the busiest two addresses —
 * this server in the middle, and on the right the programs they reached, each
 * as the product it is with the ports it answered on. A wire moves while its
 * side holds an established connection, quicker the more it holds. Loopback
 * never left the machine and is not drawn.
 *
 * The list under it is the same peers one address at a time; this is their
 * shape at once, which is what an incident asks first: is this the internet,
 * and what is it hitting? So the internet is the list's "From the internet"
 * chip, and a program narrows the list to the addresses that reached it.
 *
 * The figures that four tiles once carried live on its nodes: the server's
 * sockets, listening and loopback on the hub, each place's addresses on the
 * place, and under each the counts of every read since the page opened as a
 * line — the backend keeps no history of connections, and a count that is
 * only ever "now" cannot show that the burst was ten minutes ago.
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

  return (
    <WireFlow
      className="animate-rise"
      phone="marks"
      loading={loading && !data}
      start={{
        title: "Where the callers are",
        nodes: places.map((place) =>
          placeNode(place, history.places[place.kind] ?? [], {
            pressed: place.kind === "internet" && internetOnly,
            onPress: place.kind === "internet" ? onInternet : undefined,
          }),
        ),
        loadingRows: 3,
        empty: "Nobody from outside is connected",
      }}
      hub={{
        mark: <WireHost />,
        eyebrow: "This server",
        title: data ? plural(data.total, "socket") : undefined,
        hint: data && (
          <>
            <span className="numeric block">
              {data.listening} listening · {data.loopback} on loopback
            </span>
            <Trend values={history.total} label="Sockets since the page opened" align="centre" />
          </>
        ),
      }}
      end={{
        title: "What they reached",
        nodes: programs.map((program) => programNode(program, onProgram)),
        max: 5,
        more: { words: (hidden) => `${hidden} more programs` },
        empty: "Nothing reached",
      }}
    />
  )
}

function placeNode(
  place: Place,
  values: number[],
  press: { pressed: boolean; onPress?: () => void },
): WireFlowNode {
  const Glyph = NETWORK_GLYPH[place.kind]
  return {
    id: `place:${place.kind}`,
    mark: (
      <WireMark tone="logo" shape="square" size="md">
        {place.kind === "tailscale" ? <ProductGlyph id="tailscale" /> : <Glyph aria-hidden />}
      </WireMark>
    ),
    eyebrow: `${plural(place.peers.length, "address", "addresses")} · ${plural(place.sockets, "socket")}`,
    title: place.title,
    name: place.title,
    label: press.onPress
      ? press.pressed
        ? "Show every address"
        : "Show only the addresses from the internet"
      : undefined,
    onPress: press.onPress,
    hint: (
      <>
        <span className="block truncate font-mono">
          {place.peers
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
    line: { state: "up", moving: place.active > 0, duration: beat(place.active) },
  }
}

function programNode(program: Program, onProgram: (name: string) => void): WireFlowNode {
  const product = processProduct(program.name)
  return {
    id: `program:${program.key}`,
    mark: (
      <WireMark tone="logo" shape="square" size="md">
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
