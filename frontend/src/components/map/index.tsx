"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { useSearchParams } from "next/navigation"
import { ReactFlowProvider } from "@xyflow/react"
import { Page, PageContext } from "@/components/page"
import { Pane, PaneHeader } from "@/components/panel"
import { ErrorState } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"
import { usePoll } from "@/hooks/use-poll"
import { get } from "@/lib/api"
import type { ServerMap } from "@/lib/types"
import { cn } from "@/lib/utils"
import { MapCanvas } from "@/components/map/canvas"
import { useMapMemory } from "@/components/map/memory"

/**
 * How often the map is read. Slower than a list page polls: the server holds
 * every source's last answer and reads each again on its own schedule, so a
 * map open all day costs about what one page of each kind would — and a
 * source still being read on arrival is asked again soon, so the map fills in
 * a few seconds after it opens rather than a poll later.
 */
const EVERY = 20_000
const FILLING = 3_000

/** The last reading, so coming back to the map draws at once while it asks again. */
let lastMap: { value: ServerMap; at: number } | null = null
if (typeof window !== "undefined") {
  window.addEventListener("jd-working-state-cleared", () => {
    lastMap = null
  })
}

async function readMap(signal: AbortSignal) {
  const value = await get<ServerMap>("/map/", undefined, signal)
  if (!value || !Array.isArray(value.kinds) || !Array.isArray(value.links)) {
    throw new Error("The server answered the map with something the page cannot use.")
  }
  lastMap = { value, at: Date.now() }
  return value
}

/**
 * The server map: every system the server runs — each deployment, Compose
 * stack and PM2 app, this dashboard itself — drawn as its own diagram of what
 * it is made of and wired to (`lib/server-systems.ts`, `canvas.tsx`).
 *
 * A reading page held to the window as a workbench (design system §16), one
 * frame around the rail of systems, a canvas and its inspector, the way a
 * database's Diagram is.
 */
export function ServerMapPage() {
  const asked = useSearchParams().get("system") ?? undefined
  const [seed] = useState(() =>
    lastMap && Date.now() - lastMap.at < 5 * 60_000 ? lastMap.value : undefined,
  )
  const [filling, setFilling] = useState(true)
  const fetchMap = useCallback(async (signal: AbortSignal) => {
    const value = await readMap(signal)
    setFilling(value.kinds.some((k) => k.state === "unread" && k.reason === "Still being read"))
    return value
  }, [])
  const read = usePoll(fetchMap, filling ? FILLING : EVERY, [], { seed })
  const memory = useMapMemory()
  const map = read.data

  // Full screen: the browser's own where it is offered, and a fixed overlay
  // where it is refused, as the diagram does.
  const root = useRef<HTMLDivElement>(null)
  const [fullscreen, setFullscreen] = useState(false)
  const [immersive, setImmersive] = useState(false)
  const toggleFullscreen = useCallback(() => {
    if (fullscreen || immersive) {
      if (document.fullscreenElement) void document.exitFullscreen().catch(() => undefined)
      setImmersive(false)
      setFullscreen(false)
      return
    }
    const el = root.current
    if (el?.requestFullscreen) {
      el.requestFullscreen().then(
        () => setFullscreen(true),
        () => setImmersive(true),
      )
    } else {
      setImmersive(true)
    }
  }, [fullscreen, immersive])
  useEffect(() => {
    const onChange = () => {
      setFullscreen(document.fullscreenElement === root.current && root.current !== null)
      if (!document.fullscreenElement) setImmersive(false)
    }
    document.addEventListener("fullscreenchange", onChange)
    return () => document.removeEventListener("fullscreenchange", onChange)
  }, [])

  const ready = map && memory.document

  return (
    <Page fill className="max-w-none gap-2 px-2 py-2 md:px-3 md:py-3">
      <PageContext title="Map" />
      <div
        ref={root}
        className={cn(
          "flex min-h-0 min-w-0 flex-1 flex-col bg-background [&:fullscreen]:p-3",
          immersive && "fixed inset-0 z-50 p-3",
        )}
      >
        <Pane className="min-h-0 flex-1">
          {ready ? (
            <ReactFlowProvider>
              <MapCanvas
                map={map}
                doc={memory.document!}
                status={memory.status}
                updatedAt={memory.updatedAt}
                update={memory.update}
                reset={memory.reset}
                refresh={read.refresh}
                full={fullscreen || immersive}
                onFullscreen={toggleFullscreen}
                initialSystem={asked}
              />
            </ReactFlowProvider>
          ) : read.error && !map ? (
            <>
              <PaneHeader className="h-11" />
              <div className="p-4">
                <ErrorState error={read.error} onRetry={read.refresh} />
              </div>
            </>
          ) : (
            <MapSkeleton />
          )}
        </Pane>
      </div>
    </Page>
  )
}

/** The coming shape: the rail of systems, and a diagram's three columns. */
function MapSkeleton() {
  return (
    <div role="status" aria-label="Reading the server" className="flex min-h-0 flex-1 flex-col">
      <PaneHeader className="h-11">
        <Skeleton className="h-7 w-64" />
      </PaneHeader>
      <div className="grid min-h-0 flex-1 lg:grid-cols-[15rem_minmax(0,1fr)]">
        <div className="space-y-2 border-r border-hairline p-3 max-lg:hidden">
          {[0, 1, 2, 3, 4, 5].map((i) => (
            <Skeleton key={i} className="h-10 w-full rounded-lg" />
          ))}
        </div>
        <div className="relative min-h-0 overflow-hidden">
          <Skeleton className="absolute top-[38%] left-[8%] h-16 w-56 rounded-l-full rounded-r-xl" />
          <Skeleton className="absolute top-[34%] left-[38%] h-40 w-72 rounded-xl" />
          <Skeleton className="absolute top-[22%] right-[8%] h-24 w-56 rounded-xl" />
          <Skeleton className="absolute top-[52%] right-[8%] h-28 w-56 rounded-[50%/16px]" />
        </div>
      </div>
    </div>
  )
}
