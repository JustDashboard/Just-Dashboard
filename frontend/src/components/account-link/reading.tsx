import { cn } from "@/lib/utils"

/**
 * A name and what it reads, one to a line under a hairline — the shape the shared fields, the
 * keys and the update policy all take, so a person comparing what leaves this server with what
 * the site pinned is reading one kind of list. `mono` is for values that are literal strings from
 * the machine: a version, a fingerprint, an address.
 */
export function ReadingList({ className, ...props }: React.ComponentProps<"dl">) {
  return <dl className={cn("divide-y divide-hairline", className)} {...props} />
}

export function Reading({
  label,
  mono,
  children,
}: {
  label: React.ReactNode
  mono?: boolean
  children: React.ReactNode
}) {
  return (
    <div className="grid grid-cols-[minmax(0,9rem)_minmax(0,1fr)] gap-x-4 py-2 text-body">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className={cn("min-w-0", mono && "font-mono text-xs leading-relaxed break-words")}>
        {children}
      </dd>
    </div>
  )
}
