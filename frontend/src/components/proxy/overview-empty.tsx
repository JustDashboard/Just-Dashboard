/**
 * What a block says when it has nothing to read: what is missing, why, and
 * the one way to change that where there is one. The overview's blocks each
 * had their own grey sentence at their own size — one at 13px, one at 11px —
 * so four empties on a quiet host read as four different kinds of nothing.
 */
export function OverviewEmpty({
  icon: Icon,
  title,
  children,
  action,
}: {
  icon: React.ComponentType<{ className?: string }>
  title: string
  children: React.ReactNode
  action?: React.ReactNode
}) {
  return (
    <div data-slot="overview-empty" className="flex min-w-0 animate-rise gap-3 py-1">
      <Icon aria-hidden className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
      <div className="min-w-0 space-y-1.5">
        <p className="text-body font-medium">{title}</p>
        <p className="max-w-sm text-xs leading-relaxed text-pretty text-muted-foreground">
          {children}
        </p>
        {action && <div className="pt-1.5">{action}</div>}
      </div>
    </div>
  )
}
