"use client"

import { useRef, type RefObject } from "react"
import { describeClient, networkOf, type NetworkKind } from "@/lib/clients"
import { relativeTime } from "@/lib/format"
import type { SessionInfo } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useMediaQuery } from "@/hooks/use-mobile"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { WireHost, WireMark, WireNode } from "@/components/deploy/wire"
import { useNow } from "@/components/deploy/vocabulary"
import { NETWORK_GLYPH } from "@/components/client-mark"
import { ProductGlyph } from "@/components/product-logo"
import {
  activeNow,
  byNetwork,
  sessionGuard,
  sessionLine,
  type Guard,
  type Line,
} from "@/components/account/access"
import { SessionState, SessionWireMark } from "@/components/account/marks"

/** How many sessions the picture draws before it counts the rest. */
const SHOWN = 6

/**
 * Each network's hue on its node: Tailscale in its own blue, the open
 * internet in violet — neither red nor amber, because arriving from the
 * internet is a fact about a session rather than a verdict on it.
 */
export const NETWORK_HUE: Record<NetworkKind, string> = {
  tailscale: "var(--tag-blue)",
  local: "var(--tag-cyan)",
  server: "var(--tag-slate)",
  internet: "var(--tag-violet)",
}

/** What each network is to the server, as the word over its node — Configuration's "Tailnet". */
const NETWORK_EYEBROW: Record<NetworkKind, string> = {
  tailscale: "Tailnet",
  local: "Network",
  server: "Loopback",
  internet: "Network",
}

export const GUARD_WORDS: Record<Guard, string> = {
  code: "authenticator code",
  recovery: "recovery code",
  password: "password only",
  pending: "owes a code",
  unknown: "method not recorded",
}

/**
 * Where this account is signed in, drawn as the path each session takes to
 * the server: the browsers and programs on the left as themselves, the
 * networks they came in on in the middle — the tailnet as Tailscale, the rest
 * as the place they are — and this server on the right. Configuration's
 * request path, for every session at once, and drawn at its scale: every
 * node is a full-size tile with a word over its name and a line of detail
 * under it, the address in mono.
 *
 * The page is read to answer "is every one of these me", and the picture
 * answers it before a word is read: a session open now pulses down its
 * wire, one let in on a password alone is amber all the way in, and one
 * still owing its code is dashed. A network's wire into the server is as
 * busy as the busiest session on it.
 */
export function SessionMap({ sessions }: { sessions: SessionInfo[] }) {
  const wide = useMediaQuery("(min-width: 1024px)")
  const now = useNow(15_000)
  const container = useRef<HTMLDivElement>(null)
  const server = useRef<HTMLDivElement>(null)
  const tailscale = useRef<HTMLDivElement>(null)
  const local = useRef<HTMLDivElement>(null)
  const loopback = useRef<HTMLDivElement>(null)
  const internet = useRef<HTMLDivElement>(null)
  const networkRef: Record<NetworkKind, RefObject<HTMLDivElement | null>> = {
    tailscale,
    local,
    server: loopback,
    internet,
  }

  const ordered = [...sessions].sort(
    (a, b) =>
      Number(b.current) - Number(a.current) ||
      Number(activeNow(b, now)) - Number(activeNow(a, now)) ||
      b.lastSeenAt.localeCompare(a.lastSeenAt),
  )
  const shown = ordered.slice(0, SHOWN)
  const networks = byNetwork(ordered)
  const open = ordered.filter((s) => activeNow(s, now)).length

  const left = shown.map((session) => (
    <SessionNode
      key={session.id}
      session={session}
      containerRef={container}
      networkRef={networkRef[networkOf(session.ip).kind]}
      compact={!wide}
      line={sessionLine(session, now)}
      now={now}
    />
  ))

  const middle = networks.map((network) => {
    const Glyph = NETWORK_GLYPH[network.kind]
    const lines = network.sessions.map((s) => sessionLine(s, now))
    const line: Line = {
      still: lines.every((l) => l.still),
      tone: lines.some((l) => l.tone === "warning") ? "warning" : "default",
    }
    const mark = (
      <WireMark tone="logo" size={wide ? "lg" : "md"}>
        {network.product ? (
          <ProductGlyph id={network.product} />
        ) : (
          <Glyph aria-hidden style={{ color: NETWORK_HUE[network.kind] }} />
        )}
      </WireMark>
    )
    return (
      <div key={network.kind} className="min-w-0">
        <AnimatedBeam
          containerRef={container}
          fromRef={networkRef[network.kind]}
          toRef={server}
          shape={wide ? "s" : "arc"}
          still={line.still}
          tone={line.tone}
          duration={2.4}
          delay={0.5}
        />
        {wide ? (
          <WireNode
            nodeRef={networkRef[network.kind]}
            align="center"
            mark={mark}
            eyebrow={NETWORK_EYEBROW[network.kind]}
            title={<span className="inline-block first-letter:uppercase">{network.label}</span>}
            hint={`${network.sessions.length} ${network.sessions.length === 1 ? "session" : "sessions"}`}
          />
        ) : (
          <div ref={networkRef[network.kind]} className="relative z-10 flex">
            {mark}
            <span className="sr-only">{`${network.label}: ${network.sessions.length} sessions`}</span>
          </div>
        )}
      </div>
    )
  })

  const hint = open > 0 ? `${open} open now` : "none open now"

  return (
    <div ref={container} className="relative w-full py-4">
      {wide ? (
        <div className="relative grid grid-cols-[minmax(0,1.1fr)_minmax(2rem,0.35fr)_auto_minmax(2rem,0.35fr)_minmax(0,0.9fr)] items-center">
          <div role="group" aria-label="Sessions" className="flex flex-col items-end gap-6">
            {left}
            {ordered.length > SHOWN && (
              <p className="text-hint text-muted-foreground">
                {ordered.length - SHOWN} more in the list below
              </p>
            )}
          </div>
          <div aria-hidden />
          <div role="group" aria-label="Networks" className="flex flex-col gap-20 pb-16">
            {middle}
          </div>
          <div aria-hidden />
          <WireNode
            nodeRef={server}
            mark={<WireHost />}
            eyebrow="This server"
            title={`${ordered.length} ${ordered.length === 1 ? "session" : "sessions"} signed in`}
            hint={<span className={open > 0 ? "text-success" : undefined}>{hint}</span>}
          />
        </div>
      ) : (
        <div className="relative flex flex-col items-center text-center">
          <div role="group" aria-label="Sessions" className="flex flex-wrap justify-center gap-4">
            {left}
          </div>
          <div role="group" aria-label="Networks" className="mt-10 flex justify-center gap-8">
            {middle}
          </div>
          <div ref={server} className="relative z-10 mt-10 flex">
            <WireHost />
          </div>
          <p className="mt-2 text-hint text-muted-foreground">
            {ordered.length} signed in · {hint}
          </p>
        </div>
      )}
    </div>
  )
}

function SessionNode({
  session,
  containerRef,
  networkRef,
  compact,
  line,
  now,
}: {
  session: SessionInfo
  containerRef: RefObject<HTMLDivElement | null>
  networkRef: RefObject<HTMLDivElement | null>
  compact: boolean
  line: Line
  now: number
}) {
  const node = useRef<HTMLDivElement>(null)
  const guard = sessionGuard(session)
  const state = session.current
    ? "this device"
    : activeNow(session, now)
      ? "active now"
      : `seen ${relativeTime(session.lastSeenAt)}`
  const beam = (
    <AnimatedBeam
      containerRef={containerRef}
      fromRef={node}
      toRef={networkRef}
      shape={compact ? "arc" : "s"}
      still={line.still}
      dashed={line.dashed}
      tone={line.tone}
      duration={2.4}
    />
  )
  if (compact)
    return (
      <div className="min-w-0">
        {beam}
        <div ref={node} className="relative z-10 flex">
          <SessionWireMark userAgent={session.userAgent} />
        </div>
        <span className="sr-only">{`${describeClient(session.userAgent)}: ${state}`}</span>
      </div>
    )
  return (
    <div className="min-w-0">
      {beam}
      <WireNode
        nodeRef={node}
        align="end"
        mark={<SessionWireMark userAgent={session.userAgent} size="lg" />}
        eyebrow={<SessionState session={session} now={now} />}
        title={describeClient(session.userAgent)}
        hint={
          <>
            <span className="numeric font-mono">{session.ip}</span>
            {" · "}
            <span className={cn(line.tone === "warning" && "text-warning")}>
              {GUARD_WORDS[guard]}
            </span>
          </>
        }
      />
    </div>
  )
}
