"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { useRouter, useSearchParams } from "next/navigation"
import {
  Plus,
  ShieldOff,
  SidebarLeftClose,
  SidebarLeftOpen,
  SidebarRightClose,
  SidebarRightOpen,
  TerminalWindow,
} from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, get, patch, post } from "@/lib/api"
import type {
  TerminalActivity,
  TerminalFolder,
  TerminalWindow as Window,
  TerminalWorkspace,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import {
  actionFor,
  formatChord,
  keymap,
  useKeymap,
  type ShortcutAction,
} from "@/lib/terminal-keymap"
import { usePanelSize } from "@/lib/panel-size"
import { useMediaQuery } from "@/hooks/use-mobile"
import { cn } from "@/lib/utils"
import { useViewState } from "@/lib/view-state"
import { useConfirm } from "@/components/confirm-dialog"
import { Page } from "@/components/page"
import { Pane, PaneHeader } from "@/components/panel"
import { XtermPane } from "@/components/xterm-pane"
import { SessionRail } from "@/components/terminal/session-rail"
import { WindowStrip } from "@/components/terminal/window-strip"
import { WorkspaceTools } from "@/components/terminal/workspace-tools"
import { ResizeHandle } from "@/components/resize-handle"
import { EmptyState, ErrorState, LoadingPanel, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

type TerminalList = {
  enabled: boolean
  login: { user: string; home: string; shell: string; error?: string }
  folders: TerminalFolder[]
  sessions: TerminalWorkspace[]
}

const RAIL = { min: 208, max: 480, base: 288 }
const TOOLS = { min: 256, max: 640, base: 336 }
const TERMINAL_MIN = 360
// Tailwind's `lg`, below which the columns stop fitting beside each other.
const STACKED = "(max-width: 1023px)"

export default function TerminalPage() {
  const { confirm, dialog } = useConfirm()
  const router = useRouter()
  // Which session, and within each session which window, was on screen. Kept
  // in the browser rather than in component state, so leaving for another
  // page and coming back lands where you were instead of on the first
  // session's first window. `view-state` draws its line at "how the page is
  // arranged" and a selection is normally on the other side of it; a terminal
  // is the exception, because here the selection *is* the arrangement — it is
  // the tab you had open, and a tab bar that forgot it would be broken.
  const [remembered, setRemembered] = useViewState<string>("terminal.session", "")
  const [rememberedWindows, setRememberedWindows] = useViewState<Record<string, string>>(
    "terminal.windows",
    {},
  )
  // Which finishes have been seen, keyed by session or `session/window` (see
  // `useFinished`); held here only to be pruned with the sessions.
  const [viewed, setViewed] = useViewState<Record<string, number>>("terminal.viewed", {})
  // What each visited window's socket last said it was doing — newer than any
  // poll, and dropped when the socket closes so the polled fields take over.
  const [activity, setActivity] = useState<Record<string, TerminalActivity>>({})
  // Windows whose socket in this browser has dropped, until it is back. The
  // listing cannot say so — to the server the PTY is alive and well — and the
  // server sends a window's state the moment a socket attaches, so the first
  // activity from a reattached socket is what clears it.
  const [dropped, setDropped] = useState<Record<string, boolean>>({})
  const [windowLists, setWindowLists] = useState<Record<string, Window[]>>({})
  const [visitedWindows, setVisitedWindows] = useState<{ id: string; sessionId: string }[]>([])
  const [showRail, setShowRail] = useViewState("terminal.rail", true)
  const [showTools, setShowTools] = useViewState("terminal.tools", true)
  // Below `lg` the rail and the tools column cover the emulator instead of
  // sitting beside it. Stacked under and over it, they left a phone's terminal
  // one line tall — the two side panels are each "the whole screen" there, so
  // only one is up at a time and picking a session puts the rail away.
  const overlay = useMediaQuery(STACKED)
  const [immersive, setImmersive] = useState(false)
  const workspaceRef = useRef<HTMLDivElement>(null)
  const focusPaneRef = useRef<(() => void) | null>(null)
  const navigationRef = useRef<Partial<Record<ShortcutAction, () => void>>>({})
  const [railWidth, setRailWidth, resetRailWidth] = usePanelSize("terminal.rail", RAIL.base)
  const [toolsWidth, setToolsWidth, resetToolsWidth] = usePanelSize("terminal.tools", TOOLS.base)
  const [rowWidth, setRowWidth] = useState(0)

  const params = useSearchParams()
  const requestedCwd = params.get("cwd")
  const requestedFolder = params.get("folder") ?? undefined
  const launched = useRef(false)
  // Five seconds rather than ten: the listing now carries whether each session
  // is working, and that is worth seeing sooner. The remembered windows of
  // sessions that no longer exist are dropped as each listing arrives, so the
  // store does not fill with the ids of shells that ended weeks ago.
  const { data, error, loading, refresh } = usePoll(async (signal) => {
    const list = await get<TerminalList>("/terminal/", undefined, signal)
    if (!signal.aborted) {
      const live = new Set(list.sessions.map((session) => session.id))
      if (Object.keys(rememberedWindows).some((id) => !live.has(id))) {
        setRememberedWindows((windows) =>
          Object.fromEntries(Object.entries(windows).filter(([id]) => live.has(id))),
        )
      }
      const owner = (key: string) => key.split("/")[0]
      if (Object.keys(viewed).some((key) => !live.has(owner(key)))) {
        setViewed((seen) =>
          Object.fromEntries(Object.entries(seen).filter(([key]) => live.has(owner(key)))),
        )
      }
    }
    return list
  }, 5000)

  const openSession = useCallback(
    async (cwd?: string, folder?: string) => {
      try {
        // No title, even for a shell opened in a chosen directory: the rail
        // follows the shell, whose prompt names the directory it is in, and a
        // title given here would pin the row to the folder it started in.
        const session = await post<{ id: string; windowId: string }>("/terminal/", {
          rows: 30,
          cols: 110,
          cwd,
          folder,
        })
        await refresh()
        setRemembered(session.id)
        setRememberedWindows((windows) => ({ ...windows, [session.id]: session.windowId }))
      } catch (err) {
        notify.error("Could not open a terminal", err)
      }
    },
    [refresh, setRemembered, setRememberedWindows],
  )

  useEffect(() => {
    if (!requestedCwd) {
      launched.current = false
      return
    }
    if (launched.current || !data) return
    launched.current = true
    const url = new URL(window.location.href)
    url.searchParams.delete("cwd")
    url.searchParams.delete("folder")
    window.history.replaceState(window.history.state, "", url.pathname + url.search + url.hash)
    void openSession(requestedCwd, requestedFolder)
  }, [requestedCwd, requestedFolder, data, openSession])

  useEffect(() => {
    const el = workspaceRef.current
    if (!el) return
    const observer = new ResizeObserver(() => setRowWidth(el.clientWidth))
    observer.observe(el)
    setRowWidth(el.clientWidth)
    return () => observer.disconnect()
  }, [loading, error, data?.enabled])

  useEffect(() => {
    const onChange = () => {
      if (!document.fullscreenElement) setImmersive(false)
    }
    document.addEventListener("fullscreenchange", onChange)
    return () => document.removeEventListener("fullscreenchange", onChange)
  }, [])

  const sessions = data?.sessions ?? []
  const active = sessions.some((session) => session.id === remembered)
    ? remembered
    : (sessions[0]?.id ?? null)
  const activeSession = sessions.find((session) => session.id === active)
  const windows = usePoll<Window[]>(
    async (signal) => {
      const list = await get<Window[]>(
        `/terminal/${encodeURIComponent(active ?? "")}/windows`,
        undefined,
        signal,
      )
      if (!signal.aborted) {
        // Keep each result with its session. usePoll retains its previous data
        // during a fetch, which otherwise shows the old session's windows.
        setWindowLists((previous) =>
          Object.fromEntries(
            sessions.map((session) => [
              session.id,
              session.id === active ? list : (previous[session.id] ?? []),
            ]),
          ),
        )
      }
      return list
    },
    5000,
    [active],
    { enabled: Boolean(active) },
  )
  const windowList = windowLists[active ?? ""] ?? []
  const activeWindow =
    windowList.find((window) => window.id === rememberedWindows[active ?? ""]) ?? windowList[0]

  // A PTY's bounded byte history cannot reconstruct a TUI screen. Keep every
  // visited emulator consuming its stream until its window or session closes.
  const openWindows = visitedWindows.filter(
    (window) =>
      sessions.some((session) => session.id === window.sessionId) &&
      windowLists[window.sessionId]?.some((item) => item.id === window.id),
  )
  if (active && activeWindow && !openWindows.some((window) => window.id === activeWindow.id)) {
    openWindows.push({ id: activeWindow.id, sessionId: active })
  }
  const droppedWindows = openWindows.filter((window) => dropped[window.id])
  const disconnectedWindows = new Set(droppedWindows.map((window) => window.id))
  const disconnectedSessions = new Set(droppedWindows.map((window) => window.sessionId))
  if (
    openWindows.length !== visitedWindows.length ||
    openWindows.some((window, index) => window !== visitedWindows[index])
  ) {
    setVisitedWindows(openWindows)
  }

  const fitPanel = (want: number, self: { min: number; max: number }, other: number) => {
    if (!rowWidth) return want
    return Math.max(self.min, Math.min(self.max, rowWidth - TERMINAL_MIN - other, want))
  }
  const toolsPx = fitPanel(toolsWidth, TOOLS, showRail ? RAIL.min : 0)
  const railPx = fitPanel(railWidth, RAIL, showTools ? toolsPx : 0)
  const currentDir = activeWindow?.cwd || activeSession?.cwd

  const act = useCallback(
    async (fn: () => Promise<unknown>, failure: string, alsoWindows = false) => {
      try {
        await fn()
        if (alsoWindows) await windows.refresh()
        await refresh()
      } catch (err) {
        notify.error(failure, err)
      }
    },
    [refresh, windows],
  )

  const toggleRail = () => {
    if (!showRail && overlay) setShowTools(false)
    setShowRail(!showRail)
  }
  const toggleTools = () => {
    if (!showTools && overlay) setShowRail(false)
    setShowTools(!showTools)
  }

  const select = (session: TerminalWorkspace) => {
    setRemembered(session.id)
    if (overlay) setShowRail(false)
    focusPaneRef.current?.()
  }
  const showWindow = (id: string) => {
    if (active) setRememberedWindows((windows) => ({ ...windows, [active]: id }))
  }
  const setMeta = (id: string, next: Record<string, unknown>) =>
    act(() => patch(`/terminal/${encodeURIComponent(id)}`, next), "Could not update that session")

  // Closing a shell asks nothing first, for the same reason the API route
  // carries no typed phrase: it is an everyday act, and a dialog in front of
  // an everyday act stops being read and starts being dismissed. Deleting a
  // folder below still asks, because nobody does that a dozen times a day.
  const closeSession = (session: TerminalWorkspace) =>
    act(async () => {
      await del(`/terminal/${encodeURIComponent(session.id)}`)
      if (active === session.id) setRemembered("")
    }, "Could not close that session")

  const deleteFolder = (folder: TerminalFolder) =>
    confirm({
      title: `Delete ${folder.name}?`,
      description:
        "Sessions in this folder will move back to All sessions. No terminal will close.",
      confirmLabel: "Delete folder",
      action: () => del(`/terminal/folders/${encodeURIComponent(folder.name)}`).then(refresh),
    })

  // A new window opens beside the one you were looking at, so it starts where
  // that shell currently is. The polled list carries a cwd up to five seconds
  // old, which is exactly long enough to miss the `cd` that prompted the new
  // window, so the live value is read first and the polled one is the
  // fallback. Both are only a request: the backend validates the directory on
  // the host and drops back to home if it has since gone.
  const openWindow = async () => {
    if (!active) return
    let cwd = activeWindow?.cwd
    if (activeWindow) {
      try {
        cwd = (await get<{ cwd: string }>(`/terminal/${encodeURIComponent(activeWindow.id)}/cwd`))
          .cwd
      } catch {
        // Tmux sessions and shells whose directory cannot be read answer with
        // an error here; the polled value, or nothing, still opens a window.
      }
    }
    try {
      const created = await post<{ id: string }>(
        `/terminal/${encodeURIComponent(active)}/windows`,
        { cwd },
      )
      await windows.refresh()
      await refresh()
      showWindow(created.id)
      focusPaneRef.current?.()
    } catch (err) {
      notify.error("Could not open a window", err)
    }
  }
  const updateWindow = (id: string, next: Record<string, unknown>, failure: string) => {
    if (!active) return
    void act(
      () =>
        patch(`/terminal/${encodeURIComponent(active)}/windows/${encodeURIComponent(id)}`, next),
      failure,
      true,
    )
  }
  const closeWindow = (id: string) => {
    if (!active || !activeSession) return
    // The last window is the session, so closing it closes the session.
    if (windowList.length === 1) {
      void closeSession(activeSession)
      return
    }
    void act(
      async () => {
        await del(`/terminal/${encodeURIComponent(active)}/windows/${encodeURIComponent(id)}`)
        if (activeWindow?.id === id) {
          const sibling = windowList.find((item) => item.id !== id)
          if (sibling) showWindow(sibling.id)
        }
      },
      "Could not close that window",
      true,
    ).then(() => focusPaneRef.current?.())
  }

  const toggleImmersive = useCallback(() => {
    if (immersive) {
      setImmersive(false)
      if (document.fullscreenElement) void document.exitFullscreen().catch(() => {})
    } else {
      setImmersive(true)
      void workspaceRef.current?.requestFullscreen?.().catch(() => {})
    }
  }, [immersive])

  const step = <T,>(items: T[], current: number, by: number): T | undefined =>
    items.length
      ? items[(((current + by) % items.length) + items.length) % items.length]
      : undefined
  const navigation: Partial<Record<ShortcutAction, () => void>> = {
    "session.new": () => void openSession(),
    "session.next": () => {
      const next = step(
        sessions,
        sessions.findIndex((item) => item.id === active),
        1,
      )
      if (next) select(next)
    },
    "session.prev": () => {
      const previous = step(
        sessions,
        sessions.findIndex((item) => item.id === active),
        -1,
      )
      if (previous) select(previous)
    },
    "window.new": () => void openWindow(),
    "window.next": () => {
      const next = step(
        windowList,
        windowList.findIndex((item) => item.id === activeWindow?.id),
        1,
      )
      if (next) showWindow(next.id)
    },
    "window.prev": () => {
      const previous = step(
        windowList,
        windowList.findIndex((item) => item.id === activeWindow?.id),
        -1,
      )
      if (previous) showWindow(previous.id)
    },
    "window.close": () => activeWindow && closeWindow(activeWindow.id),
    "workspace.rail": toggleRail,
    "workspace.tools": toggleTools,
  }
  for (const n of [1, 2, 3, 4, 5, 6, 7, 8, 9] as const) {
    navigation[`session.${n}`] = () => sessions[n - 1] && select(sessions[n - 1])
    navigation[`window.${n}`] = () => windowList[n - 1] && showWindow(windowList[n - 1].id)
  }
  useEffect(() => {
    navigationRef.current = navigation
  })
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null
      const inTerminal = Boolean(target?.closest(".xterm"))
      const nothingFocused =
        !target || target === document.body || target === document.documentElement
      if (!inTerminal && !nothingFocused) return
      const action = actionFor(event, "navigation", keymap())
      const handler = action ? navigationRef.current[action] : undefined
      if (
        !handler ||
        document.querySelector(
          "[role=dialog][data-state=open], [role=alertdialog][data-state=open]",
        )
      )
        return
      event.preventDefault()
      event.stopPropagation()
      handler()
      focusPaneRef.current?.()
    }
    window.addEventListener("keydown", onKey, { capture: true })
    return () => window.removeEventListener("keydown", onKey, { capture: true })
  }, [])

  if (loading)
    return (
      <Page fill className="px-2 py-2 md:px-3 md:py-3">
        <LoadingPanel rows={4} />
      </Page>
    )
  if (error)
    return (
      <Page className="px-2 py-2 md:px-3 md:py-3">
        <ErrorState error={error} />
      </Page>
    )
  if (!data?.enabled)
    return (
      <Page className="px-2 py-2 md:px-3 md:py-3">
        <EmptyState
          icon={TerminalWindow}
          title="The web terminal is disabled"
          description="Set JD_TERMINAL_ENABLED=true on the backend to turn it on."
        />
      </Page>
    )

  const terminalHeader = (
    <>
      <WorkspaceToggle
        active={showRail}
        onClick={toggleRail}
        label={showRail ? "Hide the sessions rail" : "Show the sessions rail"}
        action="workspace.rail"
        icon={showRail ? SidebarLeftClose : SidebarLeftOpen}
      />
      {active ? (
        <WindowStrip
          sessionId={active}
          windows={windowList}
          activeId={activeWindow?.id ?? null}
          activity={activity}
          disconnected={disconnectedWindows}
          onSelect={(id) => {
            showWindow(id)
            focusPaneRef.current?.()
          }}
          onRename={(id, name) => updateWindow(id, { name }, "Could not rename that window")}
          onReorder={(id, position) => updateWindow(id, { position }, "Could not move that window")}
          onNew={() => void openWindow()}
          onClose={closeWindow}
        />
      ) : (
        <span className="min-w-0 flex-1 truncate px-2 text-xs font-medium text-muted-foreground">
          Terminal
        </span>
      )}
      <WorkspaceToggle
        active={showTools}
        onClick={toggleTools}
        label={showTools ? "Hide files & git" : "Show files & git"}
        action="workspace.tools"
        icon={showTools ? SidebarRightClose : SidebarRightOpen}
      />
    </>
  )

  return (
    <Page fill className="gap-2 px-2 py-2 md:px-3 md:py-3">
      {data.login.error && (
        <Notice icon={ShieldOff} tone="danger" title="No account to log in as">
          {data.login.error} Set <code className="font-mono">JD_TERMINAL_USER</code> to an account
          that exists.
        </Notice>
      )}
      {/* One frame around the whole workbench. The rail, the emulator and the
          tools column are separated by a hairline each rather than by a gutter
          and three borders: three framed panes with gaps between them read as
          three boxes floating on the page, and the screen is one working
          surface. The three columns' top strips are all 40px, so their
          hairlines run across the frame as one rule. The panel toggles live
          only in the emulator's strip; a second copy inside each panel said
          the same thing twice. Below `lg` a panel covers that strip, so there
          it carries its own way out. */}
      <div
        ref={workspaceRef}
        style={{ "--jd-rail": `${railPx}px`, "--jd-tools": `${toolsPx}px` } as React.CSSProperties}
        className={cn(
          "relative flex min-h-0 min-w-0 flex-1 overflow-hidden rounded-xl border bg-card",
          immersive && "fixed inset-0 z-50 rounded-none border-0 bg-background",
        )}
      >
        {showRail && (
          <div className="absolute inset-0 z-20 flex bg-card lg:relative lg:inset-auto lg:z-auto lg:w-(--jd-rail) lg:shrink-0 lg:border-r lg:border-hairline">
            <SessionRail
              sessions={sessions}
              folders={data.folders}
              activeId={active}
              activity={activity}
              disconnected={disconnectedSessions}
              onSelect={select}
              onRename={(session, title) => setMeta(session.id, { title })}
              onTogglePinned={(session) => setMeta(session.id, { favourite: !session.favourite })}
              onSetFolder={(id, folder) => setMeta(id, { folder })}
              onClose={closeSession}
              onNew={(folder) => void openSession(undefined, folder)}
              onCreateFolder={(name) =>
                void act(() => post("/terminal/folders", { name }), "Could not create that folder")
              }
              onUpdateFolder={(name, next) =>
                void act(
                  () => patch(`/terminal/folders/${encodeURIComponent(name)}`, next),
                  "Could not update that folder",
                )
              }
              onDeleteFolder={deleteFolder}
              onHide={overlay ? () => setShowRail(false) : undefined}
            />
            <ResizeHandle
              side="left"
              label="Sessions panel width"
              value={railPx}
              min={RAIL.min}
              max={RAIL.max}
              onChange={(px, commit) => setRailWidth(px, commit)}
              onReset={resetRailWidth}
              className="absolute inset-y-0 -right-1 z-20"
            />
          </div>
        )}

        <div className="flex min-h-0 min-w-0 flex-1 flex-col">
          {openWindows.map((window) => (
            <XtermPane
              key={window.id}
              flush
              path={`/terminal/${window.id}/attach`}
              terminalSessionId={window.id}
              active={window.id === activeWindow?.id}
              headerContent={window.id === activeWindow?.id ? terminalHeader : undefined}
              cwd={window.id === activeWindow?.id ? currentDir : undefined}
              onOpenFiles={(path) => router.push(`/files?path=${encodeURIComponent(path)}`)}
              focusRef={focusPaneRef}
              className="min-h-0 flex-1"
              onActivity={(state) => {
                setActivity((prev) => ({ ...prev, [window.id]: state }))
                setDropped((prev) => (prev[window.id] ? { ...prev, [window.id]: false } : prev))
              }}
              onExit={() => {
                setDropped((prev) => ({ ...prev, [window.id]: true }))
                setActivity((prev) => {
                  if (!(window.id in prev)) return prev
                  const next = { ...prev }
                  delete next[window.id]
                  return next
                })
                void windows.refresh()
                void refresh()
              }}
              onToggleFullscreen={toggleImmersive}
              fullscreenActive={immersive}
            />
          ))}
          {!activeWindow && active && <LoadingPanel rows={4} />}
          {!activeWindow && !active && (
            <Pane flush className="flex-1">
              <PaneHeader className="h-10 gap-1 py-0">{terminalHeader}</PaneHeader>
              <EmptyState
                className="flex-1"
                icon={TerminalWindow}
                title="No sessions yet"
                description="Open a direct PTY to start working. It ends when the dashboard closes."
                action={
                  <Button size="sm" onClick={() => void openSession()}>
                    <Plus className="size-4" />
                    Open session
                  </Button>
                }
              />
            </Pane>
          )}
        </div>

        {showTools && (
          <div className="absolute inset-0 z-20 flex flex-col bg-card lg:relative lg:inset-auto lg:z-auto lg:w-(--jd-tools) lg:shrink-0 lg:border-l lg:border-hairline">
            <ResizeHandle
              side="right"
              label="Files and git panel width"
              value={toolsPx}
              min={TOOLS.min}
              max={TOOLS.max}
              onChange={(px, commit) => setToolsWidth(px, commit)}
              onReset={resetToolsWidth}
              className="absolute inset-y-0 -left-1 z-20"
            />
            <WorkspaceTools
              dir={currentDir}
              onOpenInFiles={(path) => router.push(`/files?path=${encodeURIComponent(path)}`)}
              onClose={overlay ? () => setShowTools(false) : undefined}
            />
          </div>
        )}
      </div>
      {dialog}
    </Page>
  )
}

function WorkspaceToggle({
  active,
  onClick,
  label,
  action,
  icon: Icon,
}: {
  active: boolean
  onClick: () => void
  label: string
  action?: ShortcutAction
  icon: React.ComponentType<{ className?: string }>
}) {
  const map = useKeymap()
  const chord = action ? map[action] : undefined
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          aria-label={label}
          aria-pressed={active}
          className={cn(
            "size-7 shrink-0 rounded-md p-0",
            active ? "bg-accent text-foreground" : "text-muted-foreground hover:text-foreground",
          )}
          onClick={onClick}
        >
          <Icon />
        </Button>
      </TooltipTrigger>
      <TooltipContent>{chord ? `${label} · ${formatChord(chord)}` : label}</TooltipContent>
    </Tooltip>
  )
}
