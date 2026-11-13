"use client"

import Link from "next/link"
import { createRef, useMemo, useRef, useState, type RefObject } from "react"
import { ArrowLeft, Globe, NetworkDevice, Router } from "@/components/icons"
import { cn } from "@/lib/utils"
import { plural } from "@/lib/format"
import { ProductGlyph, hasProductLogo } from "@/components/product-logo"
import { DashboardMark } from "@/components/backups/marks"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { WireHost, WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import {
  LANE_WORDS,
  laneFilter,
  listenerProduct,
  portHref,
  reachPicture,
  type PictureOwner,
  type PictureTone,
  type ReachGroup,
  type ReachLane,
  type Socket,
} from "@/components/proxy/ports"

const LANE_PRODUCT: Partial<Record<ReachLane, string>> = {
  tailnet: "tailscale",
  vpn: "wireguard",
  docker: "docker",
}

const MARK_TONE: Record<PictureTone, "danger" | "warning" | "logo"> = {
  danger: "danger",
  warning: "warning",
  default: "logo",
}

const LANE_SHORT: Record<ReachLane, string> = {
  internet: "the internet",
  tailnet: "the tailnet",
  vpn: "a VPN",
  private: "a private network",
  docker: "Docker",
  local: "this server",
}

/**
 * Who can reach what on this host: on the left each network a socket
 * answers on — the internet, the tailnet, a VPN, a private network, Docker's
 * bridges, this server alone — and on the right the programs listening
 * there, each wired to the networks that reach it. A wire is red where the
 * posture calls the exposure critical (a database the internet reaches),
 * amber where it warns, and it carries a pulse only while somebody is
 * connected: both ends are up and something is crossing.
 *
 * Pressing a network narrows the list under the picture to it, as its chip
 * does; a program's ports open their own pages. Pointing at either steps
 * back every wire but its own. Below `lg` the lanes stack, the wires are
 * not drawn, and each program says in words who reaches it.
 */
export function PortsPicture({
  sockets,
  all,
  reach,
  onReach,
}: {
  /** The sockets to draw: the page's whole list, not the filtered rows. */
  sockets: Socket[]
  /** Every row, for the keys that name a page where two rows share a port. */
  all: Socket[]
  reach: ReachGroup | "all"
  onReach: (reach: ReachGroup | "all") => void
}) {
  const model = useMemo(() => reachPicture(sockets, 6), [sockets])
  const container = useRef<HTMLDivElement>(null)
  const [focus, setFocus] = useState<string | null>(null)
  const ids = [
    ...model.lanes.map((l) => `l:${l.lane}`),
    ...model.owners.map((o) => `o:${o.key}`),
  ].join("\n")
  const refs = useMemo(() => {
    const out = new Map<string, RefObject<HTMLSpanElement | null>>()
    for (const id of ids.split("\n")) if (id) out.set(id, createRef<HTMLSpanElement>())
    return out
  }, [ids])

  const joined = (a: string, b: string) =>
    model.wires.some(
      (w) =>
        (a === `l:${w.lane}` && b === `o:${w.owner}`) ||
        (b === `l:${w.lane}` && a === `o:${w.owner}`),
    )
  const lit = (id: string) => focus === null || focus === id || joined(focus, id)
  const watch = (id: string) => ({
    onPointerEnter: () => setFocus(id),
    onPointerLeave: () => setFocus((held) => (held === id ? null : held)),
    onFocus: () => setFocus(id),
    onBlur: () => setFocus((held) => (held === id ? null : held)),
  })

  return (
    <div data-slot="ports-picture" className="relative animate-rise pt-6 pb-2">
      <div ref={container} className="relative">
        {model.wires.map((wire, index) => {
          const from = refs.get(`l:${wire.lane}`)
          const to = refs.get(`o:${wire.owner}`)
          if (!from || !to) return null
          const on = focus === null || focus === `l:${wire.lane}` || focus === `o:${wire.owner}`
          return (
            <AnimatedBeam
              key={`${wire.lane}→${wire.owner}`}
              containerRef={container}
              fromRef={from}
              toRef={to}
              shape="s"
              startXOffset={22}
              endXOffset={-18}
              still={!wire.busy}
              tone={wire.tone}
              duration={3}
              delay={(index % 6) * 0.35}
              className={cn("transition-opacity max-lg:hidden", !on && "opacity-15")}
            />
          )
        })}

        <div className="relative grid gap-y-8 lg:grid-cols-[minmax(0,1fr)_clamp(4rem,14vw,14rem)_minmax(0,1fr)] lg:items-center">
          <Lane title="Who can reach it" count={model.lanes.length} className="lg:text-right">
            {model.lanes.map(({ lane, sockets: count, tone }) => {
              const filter = laneFilter(lane)
              const pressed = reach === filter
              return (
                <li
                  key={lane}
                  {...watch(`l:${lane}`)}
                  className={cn("min-w-0 transition-opacity", !lit(`l:${lane}`) && "opacity-40")}
                >
                  <WireNode
                    align="end"
                    mark={
                      <span ref={refs.get(`l:${lane}`)} className="flex">
                        <LaneMark lane={lane} tone={tone} />
                      </span>
                    }
                    title={
                      <button
                        type="button"
                        aria-pressed={pressed}
                        onClick={() => onReach(pressed ? "all" : filter)}
                        className={cn(
                          "rounded-sm text-left focus-ring hover:underline lg:text-right",
                          pressed && "underline underline-offset-4",
                        )}
                      >
                        {LANE_WORDS[lane]}
                      </button>
                    }
                    hint={<span className="numeric">{plural(count, "socket")}</span>}
                  />
                </li>
              )
            })}
          </Lane>

          <div aria-hidden className="max-lg:hidden" />

          <Lane title="What listens" count={model.owners.length + model.hidden} dense>
            {model.owners.map((owner) => (
              <li
                key={owner.key}
                {...watch(`o:${owner.key}`)}
                className={cn("min-w-0 transition-opacity", !lit(`o:${owner.key}`) && "opacity-40")}
              >
                <OwnerNode owner={owner} all={all} nodeRef={refs.get(`o:${owner.key}`)} />
              </li>
            ))}
            {model.hidden > 0 && (
              <li className="min-w-0">
                <WireNode
                  mark={
                    <WirePlaceholder size="sm">
                      <Router />
                    </WirePlaceholder>
                  }
                  title={
                    <span className="text-muted-foreground">
                      {plural(model.hidden, "more program")} in the list
                    </span>
                  }
                />
              </li>
            )}
          </Lane>
        </div>
      </div>
    </div>
  )
}

function LaneMark({ lane, tone }: { lane: ReachLane; tone: PictureTone }) {
  if (lane === "local") return <WireHost size="md" />
  // A network's own logo where nothing on it is wrong; tinted, its glyph
  // takes the tint's colour, which a logo's artwork would not.
  const product = tone === "default" ? LANE_PRODUCT[lane] : undefined
  return (
    <WireMark tone={MARK_TONE[tone]} size="md">
      {product && hasProductLogo(product) ? (
        <ProductGlyph id={product} />
      ) : lane === "internet" ? (
        <Globe aria-hidden />
      ) : (
        <NetworkDevice aria-hidden />
      )}
    </WireMark>
  )
}

/** A program as the product it is, with the ports it holds, each opening its own page. */
function OwnerNode({
  owner,
  all,
  nodeRef,
}: {
  owner: PictureOwner
  all: Socket[]
  nodeRef?: RefObject<HTMLSpanElement | null>
}) {
  const first = owner.sockets[0]
  const product = listenerProduct(first)
  const shown = owner.sockets.slice(0, 4)
  return (
    <WireNode
      mark={
        <span ref={nodeRef} className="flex">
          {first.self ? (
            <DashboardMark />
          ) : (
            <WireMark tone="logo" size="sm">
              {product && hasProductLogo(product) ? (
                <ProductGlyph id={product} />
              ) : (
                <Router aria-hidden />
              )}
            </WireMark>
          )}
        </span>
      }
      title={
        <span
          className={cn(
            "block truncate",
            owner.tone === "danger" && "text-destructive",
            owner.tone === "warning" && "text-warning",
          )}
        >
          {owner.title}
        </span>
      }
      hint={
        <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
          {shown.map((socket) => (
            <Link
              key={`${socket.protocol}-${socket.address}-${socket.port}`}
              href={portHref(socket, all)}
              className="numeric rounded-sm font-mono focus-ring hover:text-foreground hover:underline"
            >
              :{socket.port}
              {socket.protocol === "udp" && <span className="text-micro uppercase"> udp</span>}
            </Link>
          ))}
          {owner.sockets.length > shown.length && (
            <span className="numeric">+{owner.sockets.length - shown.length}</span>
          )}
          {/* Below `lg` there are no wires, so the line says who reaches it. */}
          <span className="inline-flex min-w-0 items-center gap-1 lg:hidden">
            <ArrowLeft aria-hidden className="size-3 shrink-0" />
            <span className="truncate">{owner.lanes.map((l) => LANE_SHORT[l]).join(", ")}</span>
          </span>
        </span>
      }
    />
  )
}

function Lane({
  title,
  count,
  className,
  dense,
  children,
}: {
  title: string
  count: number
  className?: string
  /** Smaller marks, closer together: the programs outnumber the networks. */
  dense?: boolean
  children: React.ReactNode
}) {
  return (
    <div className="min-w-0 self-stretch lg:flex lg:flex-col lg:justify-center lg:py-4">
      <div className="lg:relative">
        <p className={cn("eyebrow mb-4 lg:absolute lg:inset-x-0 lg:bottom-full", className)}>
          {title}
          {count > 0 && (
            <span className="numeric ml-1.5 font-medium tracking-normal text-muted-foreground/70">
              {count}
            </span>
          )}
        </p>
        <ol className={cn("flex flex-col", dense ? "gap-3" : "gap-4")} aria-label={title}>
          {children}
        </ol>
      </div>
    </div>
  )
}
