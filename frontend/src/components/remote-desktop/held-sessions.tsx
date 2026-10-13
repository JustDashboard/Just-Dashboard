"use client"

import dynamic from "next/dynamic"
import Link from "next/link"
import { usePathname, useRouter } from "next/navigation"
import { createContext, useCallback, useContext, useMemo, useState } from "react"
import { ArrowRight, Cross } from "@/components/icons"
import { IconAction } from "@/components/icon-action"
import { ProductGlyph } from "@/components/product-logo"
import { ComputerMark } from "@/components/remote-desktop/marks"
import { LoadingPanel } from "@/components/state"
import { StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { sessionHref, type RdOS, type SessionMode } from "@/lib/remote-desktop"

// The Guacamole client draws into the page's own DOM and reads the window's
// size and keyboard; there is nothing of it to render on the server.
const RemoteSession = dynamic(
  () => import("@/components/remote-desktop/session").then((mod) => mod.RemoteSession),
  { ssr: false, loading: () => <LoadingPanel /> },
)

/** What a held session says of itself, for the dock and the rail's link. */
export type HeldState = {
  name: string
  os: RdOS
  live: boolean
  /** How the screen is drawn, in the computer's words: Screen Sharing, Remote Desktop, Stream. */
  drawn: string
  /** The round trip, said, when it is known. */
  latency?: string
}

type Held = { id: string; mode: SessionMode } & Partial<HeldState>

type HeldSessions = {
  held: Held[]
  /** The session shown last, which the rail's Remote Desktop goes back to. */
  latest?: Held
  /** Opens a computer's session, or shows the one already open; a mode of null keeps its own. */
  hold: (id: string, mode: SessionMode | null) => void
  describe: (id: string, state: HeldState) => void
  release: (id: string) => void
}

const HeldSessionsContext = createContext<HeldSessions | null>(null)

/**
 * The remote sessions open in this tab, kept for as long as the tab is.
 *
 * A session is a live connection: guacd's tunnel, or Sunshine's stream in its
 * frame. Drawn by the session page, it went with the page, so a look at
 * another page and back meant connecting again, and to a Mac signing in again.
 * The sessions are drawn here, in the shell, instead: the session page only
 * says which one it shows, and every other one stays connected, hidden, until
 * it is disconnected.
 */
export function HeldSessionsProvider({ children }: { children: React.ReactNode }) {
  const [held, setHeld] = useState<Held[]>([])
  // Kept apart from the order of `held`: moving a session in that list would
  // move its frame in the page, and a frame that moves loads again.
  const [latestId, setLatestId] = useState<string>()
  const hold = useCallback((id: string, mode: SessionMode | null) => {
    setLatestId(id)
    setHeld((sessions) => {
      const open = sessions.find((session) => session.id === id)
      if (!open) return [...sessions, { id, mode: mode ?? "control" }]
      if (!mode || open.mode === mode) return sessions
      return sessions.map((session) => (session.id === id ? { ...session, mode } : session))
    })
  }, [])
  const describe = useCallback((id: string, state: HeldState) => {
    setHeld((sessions) =>
      sessions.some(
        (session) =>
          session.id === id &&
          (Object.keys(state) as (keyof HeldState)[]).some((key) => session[key] !== state[key]),
      )
        ? sessions.map((session) => (session.id === id ? { ...session, ...state } : session))
        : sessions,
    )
  }, [])
  const release = useCallback((id: string) => {
    setHeld((sessions) => sessions.filter((session) => session.id !== id))
  }, [])
  const latest = held.find((session) => session.id === latestId) ?? held.at(-1)
  const value = useMemo(
    () => ({ held, latest, hold, describe, release }),
    [held, latest, hold, describe, release],
  )
  return <HeldSessionsContext.Provider value={value}>{children}</HeldSessionsContext.Provider>
}

export function useHeldSessions() {
  const value = useContext(HeldSessionsContext)
  if (!value) throw new Error("useHeldSessions needs HeldSessionsProvider")
  return value
}

/** Where a held session is shown, in the mode it is in. */
export function heldHref(session: Pick<Held, "id" | "mode">) {
  return `${sessionHref(session.id)}?mode=${session.mode}`
}

/**
 * Every held session, drawn after the page: the one the address names takes
 * the page's place, the rest stay mounted and hidden, connected. A hidden
 * frame keeps its document, so a stream keeps playing and a tunnel keeps its
 * screen; nothing is torn down until Disconnect.
 */
export function HeldSessionsHost() {
  const { held, hold, describe, release } = useHeldSessions()
  const pathname = usePathname()
  const router = useRouter()
  const shown = held.find((session) => pathname === sessionHref(session.id))
  return (
    <>
      {held.map((session) => {
        const visible = session === shown
        return (
          <div
            key={session.id}
            data-slot="held-session"
            data-computer={session.id}
            hidden={!visible}
            className="h-full"
          >
            <RemoteSession
              id={session.id}
              mode={session.mode}
              visible={visible}
              onMode={(next) => {
                hold(session.id, next)
                router.replace(`${sessionHref(session.id)}?mode=${next}`, { scroll: false })
              }}
              onDisconnect={() => release(session.id)}
              onState={(state) => describe(session.id, state)}
            />
          </div>
        )
      })}
      {!shown && held.length > 0 && <HeldSessionsDock held={held} onRelease={release} />}
    </>
  )
}

/**
 * The sessions still connected while another page is up: each one as its
 * computer, how it is drawn and whether it is live, one press back to it and
 * one to end it. Without it a stream would play on unseen.
 */
function HeldSessionsDock({ held, onRelease }: { held: Held[]; onRelease: (id: string) => void }) {
  return (
    <aside
      aria-label="Remote sessions"
      data-slot="held-sessions-dock"
      className="fixed right-4 bottom-4 z-40 flex w-[22rem] max-w-[calc(100vw-2rem)] animate-rise flex-col gap-1 rounded-xl border border-border-strong bg-popover/95 p-2 text-popover-foreground shadow-xl backdrop-blur"
    >
      <p className="px-2 pt-1 pb-0.5 text-hint font-medium text-muted-foreground">
        Still connected
      </p>
      {held.map((session) => {
        const name = session.name ?? "Remote session"
        const href = heldHref(session)
        return (
          <div
            key={session.id}
            data-slot="held-session-row"
            className="group flex min-w-0 items-center gap-3 rounded-xl p-2 transition-colors hover:bg-row-hover"
          >
            <Link
              href={href}
              className="flex min-w-0 flex-1 items-center gap-3 rounded-lg focus-ring"
            >
              {session.os ? (
                <ComputerMark os={session.os} size="sm" />
              ) : (
                <span className="size-9 shrink-0 rounded-xl bg-control" />
              )}
              <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                <span className="truncate text-body font-semibold">{name}</span>
                <span className="flex min-w-0 items-center gap-1.5 text-hint text-muted-foreground">
                  <StatusDot
                    tone={session.live ? "running" : "stopped"}
                    live={session.live}
                    className="size-1.5"
                  />
                  {session.mode === "stream" && <ProductGlyph id="sunshine" />}
                  <span className="truncate">
                    {session.live
                      ? [
                          session.mode === "stream" ? "Streaming" : (session.drawn ?? "Connected"),
                          session.latency,
                        ]
                          .filter(Boolean)
                          .join(" · ")
                      : "Not connected"}
                  </span>
                </span>
              </span>
            </Link>
            <Button size="sm" variant="outline" asChild>
              <Link href={href} aria-label={`Open ${name}`}>
                Open
                <ArrowRight className="size-3.5" />
              </Link>
            </Button>
            <IconAction label={`Disconnect ${name}`} onClick={() => onRelease(session.id)}>
              <Cross />
            </IconAction>
          </div>
        )
      })}
    </aside>
  )
}
