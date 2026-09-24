"use client"

import { Suspense } from "react"
import { Pane, PaneHeader } from "@/components/panel"
import { CONSOLE_HEIGHT, ProjectConsole } from "@/components/deploy/project-console"
import { Skeleton } from "@/components/ui/skeleton"

export default function Page() {
  return (
    <Suspense fallback={<ConsoleSkeleton />}>
      <ProjectConsole />
    </Suspense>
  )
}

/**
 * The console's own silhouette — a pane the height the shell will be, under
 * its strip — so nothing jumps when the terminal arrives. A framed list of
 * rows was the shape of a different page.
 */
function ConsoleSkeleton() {
  return (
    <Pane aria-hidden className={CONSOLE_HEIGHT}>
      <PaneHeader>
        <Skeleton className="size-3.5 rounded-sm" />
        <Skeleton className="h-3 w-48" />
      </PaneHeader>
      <div className="min-h-0 flex-1 bg-surface-sunken" />
    </Pane>
  )
}
