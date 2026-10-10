"use client"

import { createRef, useMemo, useRef, useState, type RefObject } from "react"
import { Servers } from "@/components/icons"
import { networkOf, type NetworkKind } from "@/lib/clients"
import type { Connections } from "@/lib/types"
import { cn } from "@/lib/utils"
import { NETWORK_GLYPH } from "@/components/client-mark"
import { ProductGlyph, hasProductLogo, processProduct } from "@/components/product-logo"
import { WireHost, WireMark, WireNode } from "@/components/deploy/wire"
import { AnimatedBeam } from "@/components/ui/animated-beam"

type Peer = Connections["peers"][number]

/** The order the places are drawn in, and what each is called. */
const PLACES: { kind: NetworkKind; title: string }[] = [
  { kind: "internet", title: "The internet" },
  { kind: "tailscale", title: "Tailnet" },
  { kind: "local", title: "Local networks" },
]

type Group = { kind: NetworkKind; title: string; peers: Peer[]; sockets: number; active: number }
type Service = { key: string; name: string; ports: number[]; sockets: number; active: number }

/**
 * Who is connected, drawn the way the Overview draws the machine: where the
 * callers are on the left — the internet, the tailnet, the local networks,
 * each with how many addresses and sockets and the busiest few addresses —
 * this server in the middle, and on the right the programs they reached,
 * each as the product it is with the ports it answered on. A wire moves while
 * its side holds an established connection, and pointing at a node steps the
 * others back. Loopback never left the machine and is not drawn.
 *
 * The table under it is the same peers one address at a time, with their
 * verbs; this is the shape of them at once, which is what an incident asks
 * first: is this the internet, and what is it hitting?
 */
export function ConnectionsMap({ data }: { data: Connections }) {
  const container = useRef<HTMLDivElement>(null)
  const host = useRef<HTMLDivElement>(null)
  const [focus, setFocus] = useState<string | null>(null)

  const { groups, services } = useMemo(() => fold(data.peers), [data.peers])
  const ids = [...groups.map((g) => `g:${g.kind}`), ...services.map((s) => `s:${s.key}`)].join("\n")
  const refs = useMemo(() => {
    const map = new Map<string, RefObject<HTMLDivElement | null>>()
    for (const id of ids.split("\n")) if (id) map.set(id, createRef<HTMLDivElement>())
    return map
  }, [ids])
  const watch = (id: string) => ({
    onPointerEnter: () => setFocus(id),
    onPointerLeave: () => setFocus((held) => (held === id ? null : held)),
    onFocus: () => setFocus(id),
    onBlur: () => setFocus((held) => (held === id ? null : held)),
  })
  const port = 26
  if (groups.length === 0 && services.length === 0) return null

  return (
    <div className="relative animate-rise py-4">
      <div aria-hidden className="wire-grid pointer-events-none absolute -inset-x-4 inset-y-0" />
      <div ref={container} className="relative">
        {groups.map((g, index) => {
          const ref = refs.get(`g:${g.kind}`)
          if (!ref) return null
          const lit = focus === null || focus === `g:${g.kind}`
          return (
            <AnimatedBeam
              key={g.kind}
              containerRef={container}
              fromRef={ref}
              toRef={host}
              shape="s"
              startXOffset={port}
              endXOffset={-port}
              still={g.active === 0}
              duration={beat(g.active)}
              delay={index * 0.4}
              className={cn("transition-opacity max-lg:hidden", !lit && "opacity-15")}
            />
          )
        })}
        {services.map((s, index) => {
          const ref = refs.get(`s:${s.key}`)
          if (!ref) return null
          const lit = focus === null || focus === `s:${s.key}`
          return (
            <AnimatedBeam
              key={s.key}
              containerRef={container}
              fromRef={host}
              toRef={ref}
              shape="s"
              startXOffset={port}
              endXOffset={-port}
              still={s.active === 0}
              duration={beat(s.active)}
              delay={(index % 5) * 0.35}
              className={cn("transition-opacity max-lg:hidden", !lit && "opacity-15")}
            />
          )
        })}

        <div className="relative grid gap-y-8 lg:grid-cols-[minmax(0,1fr)_clamp(2.5rem,6vw,6rem)_minmax(0,0.8fr)_clamp(2.5rem,6vw,6rem)_minmax(0,1fr)] lg:items-center">
          <section aria-label="Where the callers are" className="min-w-0 lg:text-right">
            <p className="eyebrow mb-4 lg:hidden">Callers</p>
            <ol className="flex flex-col gap-5">
              {groups.map((g) => {
                const Glyph = NETWORK_GLYPH[g.kind]
                const id = `g:${g.kind}`
                return (
                  <li
                    key={g.kind}
                    {...watch(id)}
                    className={cn(
                      "min-w-0 transition-opacity",
                      focus !== null && focus !== id && "opacity-40",
                    )}
                  >
                    <WireNode
                      nodeRef={refs.get(id)}
                      align="end"
                      mark={
                        <WireMark tone="logo" shape="square" size="md">
                          {g.kind === "tailscale" ? (
                            <ProductGlyph id="tailscale" />
                          ) : (
                            <Glyph aria-hidden />
                          )}
                        </WireMark>
                      }
                      eyebrow={`${g.peers.length} address${g.peers.length === 1 ? "" : "es"} · ${g.sockets} socket${g.sockets === 1 ? "" : "s"}`}
                      title={g.title}
                      hint={
                        <span className="block truncate font-mono">
                          {g.peers
                            .slice(0, 2)
                            .map((p) => p.address)
                            .join(", ")}
                          {g.peers.length > 2 && (
                            <span className="ml-1.5 font-sans">+{g.peers.length - 2}</span>
                          )}
                        </span>
                      }
                    />
                  </li>
                )
              })}
            </ol>
          </section>

          <div aria-hidden className="max-lg:hidden" />
          <div className="flex min-w-0 justify-center">
            <WireNode
              nodeRef={host}
              align="center"
              mark={<WireHost />}
              eyebrow="This server"
              title={`${data.total} socket${data.total === 1 ? "" : "s"}`}
              hint={`${data.listening} listening · ${data.loopback} on loopback`}
            />
          </div>
          <div aria-hidden className="max-lg:hidden" />

          <section aria-label="What they reached" className="min-w-0">
            <p className="eyebrow mb-4 lg:hidden">Reached</p>
            <ol className="flex flex-col gap-5">
              {services.map((s) => {
                const id = `s:${s.key}`
                const product = processProduct(s.name)
                return (
                  <li
                    key={s.key}
                    {...watch(id)}
                    className={cn(
                      "min-w-0 transition-opacity",
                      focus !== null && focus !== id && "opacity-40",
                    )}
                  >
                    <WireNode
                      nodeRef={refs.get(id)}
                      mark={
                        <WireMark tone="logo" shape="square" size="md">
                          {hasProductLogo(product) ? (
                            <ProductGlyph id={product} />
                          ) : (
                            <Servers aria-hidden />
                          )}
                        </WireMark>
                      }
                      eyebrow={`${s.sockets} socket${s.sockets === 1 ? "" : "s"} · ${s.active} active`}
                      title={s.name}
                      hint={
                        <span className="block truncate font-mono">
                          {s.ports.slice(0, 4).map((p, i) => (
                            <span key={p}>
                              {i > 0 && <span className="text-muted-foreground">, </span>}
                              <span className="text-muted-foreground">:</span>
                              <span className="text-[var(--tag-pink)]">{p}</span>
                            </span>
                          ))}
                          {s.ports.length > 4 && (
                            <span className="ml-1 font-sans">+{s.ports.length - 4}</span>
                          )}
                        </span>
                      }
                    />
                  </li>
                )
              })}
            </ol>
          </section>
        </div>
      </div>
    </div>
  )
}

/** A wire's pulse for how many connections it carries: quicker the more there are. */
function beat(active: number) {
  if (active <= 1) return 3.6
  return Math.max(1.4, 3.6 - Math.log2(active) * 0.4)
}

/** The peers folded by where they are and by what they reached. Pure, so it is testable. */
export function fold(peers: Peer[]): { groups: Group[]; services: Service[] } {
  const byKind = new Map<NetworkKind, Group>()
  const byService = new Map<string, Service>()
  for (const peer of peers) {
    const kind = networkOf(peer.address).kind
    if (kind === "server") continue
    const place = PLACES.find((p) => p.kind === kind)
    const group =
      byKind.get(kind) ??
      ({ kind, title: place?.title ?? kind, peers: [], sockets: 0, active: 0 } satisfies Group)
    group.peers.push(peer)
    group.sockets += peer.count
    group.active += peer.established
    byKind.set(kind, group)

    const name = peer.processes[0] || peer.service || "Unknown program"
    const service = byService.get(name) ?? { key: name, name, ports: [], sockets: 0, active: 0 }
    for (const p of peer.ports) if (!service.ports.includes(p)) service.ports.push(p)
    service.sockets += peer.count
    service.active += peer.established
    byService.set(name, service)
  }
  const groups = PLACES.map((p) => byKind.get(p.kind)).filter((g): g is Group => !!g)
  for (const g of groups) g.peers.sort((a, b) => b.count - a.count)
  const services = [...byService.values()]
    .map((s) => ({ ...s, ports: s.ports.sort((a, b) => a - b) }))
    .sort((a, b) => b.sockets - a.sockets)
    .slice(0, 8)
  return { groups, services }
}
