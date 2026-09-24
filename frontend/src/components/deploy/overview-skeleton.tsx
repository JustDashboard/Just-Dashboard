import { Skeleton } from "@/components/ui/skeleton"

/**
 * The Overview's silhouette: the environment's title and hairline, the
 * preview's sixteen-by-ten tile beside the four nodes of the wiring, and the
 * row of four readings under them — so the first paint lands where the page
 * will, rather than as a framed table that the page then replaces. The
 * project shell draws it under its own while the project's first read is in
 * flight, and the page while a legacy link redirects.
 */
export function OverviewSkeleton() {
  return (
    <div aria-hidden className="space-y-4">
      <div className="flex min-h-12 items-center border-b border-hairline py-3">
        <Skeleton className="h-4 w-28" />
      </div>
      <div className="grid grid-cols-[minmax(0,1fr)] items-start gap-8 lg:grid-cols-2 xl:grid-cols-[minmax(0,32rem)_minmax(0,1fr)]">
        <Skeleton className="aspect-[16/11] w-full rounded-xl" />
        <div className="space-y-5">
          {Array.from({ length: 4 }).map((_, index) => (
            <div key={index} className="flex items-center gap-3.5">
              <Skeleton className="size-11 shrink-0 rounded-full" />
              <div className="min-w-0 flex-1 space-y-1.5">
                <Skeleton className="h-2.5 w-16" />
                <Skeleton className="h-3.5 w-40 max-w-full" />
                <Skeleton className="h-3 w-56 max-w-full" />
              </div>
            </div>
          ))}
        </div>
      </div>
      <div className="grid grid-cols-2 gap-5 border-t border-hairline pt-4 xl:grid-cols-4">
        {Array.from({ length: 4 }).map((_, index) => (
          <div key={index} className="space-y-2">
            <Skeleton className="h-2.5 w-16" />
            <Skeleton className="h-6 w-20" />
            <Skeleton className="h-9 w-full" />
          </div>
        ))}
      </div>
    </div>
  )
}
