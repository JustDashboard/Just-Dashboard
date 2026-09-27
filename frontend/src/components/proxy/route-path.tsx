import { cn } from "@/lib/utils"
import { ChoiceList } from "@/components/flow"
import { ArrowRight } from "@/components/icons"

/** Keep both ends visible: a route is hard to read when squeezed into one truncated sentence. */
export function RoutePath({
  source,
  destination,
  sourceLabel = "Domain",
  destinationLabel = "Upstream",
}: {
  source: React.ReactNode
  destination: React.ReactNode
  sourceLabel?: string
  destinationLabel?: string
}) {
  return (
    <dl
      data-slot="proxy-route"
      className="grid min-w-0 grid-cols-1 items-start gap-3 border-y border-hairline py-4 sm:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)]"
    >
      <div className="min-w-0 space-y-1.5">
        <dt className="text-hint text-muted-foreground">{sourceLabel}</dt>
        <dd className="font-mono text-body leading-relaxed break-all">{source}</dd>
      </div>
      <ArrowRight aria-hidden className="mt-6 hidden size-4 text-muted-foreground sm:block" />
      <div className="min-w-0 space-y-1.5">
        <dt className="text-hint text-muted-foreground">{destinationLabel}</dt>
        <dd className="font-mono text-body leading-relaxed break-all">{destination}</dd>
      </div>
    </dl>
  )
}

/** Stretch the arrival wrapper as well as the card, keeping every edge on its grid row. */
export function ProxyGrid({ className, ...props }: React.ComponentProps<"ul">) {
  return (
    <ChoiceList
      className={cn(
        "grid gap-4 space-y-0 xl:grid-cols-2 [&>li>div]:h-full [&>li>div>div]:h-full",
        className,
      )}
      {...props}
    />
  )
}
