import { cn } from "@/lib/utils"

/**
 * A name and what it reads, one to a line under a hairline — the shape the shared fields, the
 * keys and the update policy all take, so a person comparing what leaves this server with what
 * the site pinned is reading one kind of list. `mono` is for strings compared character by
 * character, the fingerprints; `after` takes the row's full width under it, for a list the
 * reading counts.
 */
export function ReadingList({ className, ...props }: React.ComponentProps<"dl">) {
  return <dl className={cn("divide-y divide-hairline", className)} {...props} />
}

export function Reading({
  label,
  mono,
  after,
  children,
}: {
  label: React.ReactNode
  mono?: boolean
  after?: React.ReactNode
  children: React.ReactNode
}) {
  return (
    <div className="grid grid-cols-[minmax(0,8rem)_minmax(0,1fr)] gap-x-4 py-1.5 text-body">
      <dt className="truncate text-muted-foreground">{label}</dt>
      <dd
        className={cn(
          "min-w-0 break-words tabular-nums",
          mono && "font-mono text-xs leading-relaxed",
        )}
      >
        {children}
      </dd>
      {after && <dd className="col-span-2 min-w-0">{after}</dd>}
    </div>
  )
}
