"use client"

import dynamic from "next/dynamic"
import Link from "next/link"
import { usePathname, useRouter } from "next/navigation"
import { createContext, useCallback, useContext, useMemo, useState } from "react"
import { Cross } from "@/components/icons"
import { IconAction } from "@/components/icon-action"
import { LoadingPanel } from "@/components/state"
import { StatusDot } from "@/components/status-dot"
import { sessionHref, type SessionMode } from "@/lib/remote-desktop"

// The Guacamole client draws into the page's own DOM and reads the window's
// size and keyboard; there is nothing of it to render on the server.
const RemoteSession = dynamic(
  () => import("@/components/remote-desktop/session").then((mod) => mod.RemoteSession),
  { ssr: false, loading: () => <LoadingPanel /> },
)

type Held = { id: string; mode: SessionMode; name?: string; live?: boolean }

type HeldSessions = {
  held: Held[]
  /** Opens a computer's session, or shows the one already open; a mode of null keeps its own. */
  hold: (id: string, mode: SessionMode | null) => void
  describe: (id: string, state: { name: string; live: boolean }) => void
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
  const hold = useCallback((id: string, mode: SessionMode | null) => {
    setHeld((sessions) => {
      const open = sessions.find((session) => session.id === id)
      if (!open) return [...sessions, { id, mode: mode ?? "control" }]
      if (!mode || open.mode === mode) return sessions
      return sessions.map((session) => (session.id === id ? { ...session, mode } : session))
    })
  }, [])
  const describe = useCallback((id: string, state: { name: string; live: boolean }) => {
    setHeld((sessions) =>
      sessions.some(
        (session) =>
          session.id === id && (session.name !== state.name || session.live !== state.live),
      )
        ? sessions.map((session) => (session.id === id ? { ...session, ...state } : session))
        : sessions,
    )
  }, [])
  const release = useCallback((id: string) => {
    setHeld((sessions) => sessions.filter((session) => session.id !== id))
  }, [])
  const value = useMemo(() => ({ held, hold, describe, release }), [held, hold, describe, release])
  return <HeldSessionsContext.Provider value={value}>{children}</HeldSessionsContext.Provider>
}

export function useHeldSessions() {
  const value = useContext(HeldSessionsContext)
  if (!value) throw new Error("useHeldSessions needs HeldSessionsProvider")
  return value
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
 * The sessions still connected while another page is up: one press back to
 * each, one to end it. Without it a stream would play on unseen.
 */
function HeldSessionsDock({ held, onRelease }: { held: Held[]; onRelease: (id: string) => void }) {
  return (
    <aside
      aria-label="Remote sessions"
      data-slot="held-sessions-dock"
      className="fixed right-4 bottom-4 z-40 flex max-w-[calc(100vw-2rem)] flex-col gap-1 rounded-xl border border-hairline bg-popover p-1.5 text-popover-foreground shadow-lg"
    >
      {held.map((session) => {
        const name = session.name ?? "Remote session"
        return (
          <div key={session.id} className="flex min-w-0 items-center gap-1">
            <Link
              href={`${sessionHref(session.id)}?mode=${session.mode}`}
              className="flex min-w-0 flex-1 items-center gap-2 rounded-md py-1.5 pr-2 pl-2.5 text-body focus-ring transition-colors hover:bg-row-hover"
            >
              <StatusDot
                tone={session.live ? "running" : "stopped"}
                live={session.live}
                className="size-1.5"
              />
              <span className="min-w-0 truncate font-medium">{name}</span>
              <span className="shrink-0 text-hint text-muted-foreground">
                {!session.live
                  ? "not connected"
                  : session.mode === "stream"
                    ? "streaming"
                    : "connected"}
              </span>
            </Link>
            <IconAction label={`Disconnect ${name}`} onClick={() => onRelease(session.id)}>
              <Cross />
            </IconAction>
          </div>
        )
      })}
    </aside>
  )
}
