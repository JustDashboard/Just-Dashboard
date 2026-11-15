import { cn } from "@/lib/utils"

/**
 * A labelled group of panels inside a page, for the pages that hold more than
 * one idea (account, certificates, the dashboard's own configuration).
 */
export function Section({
  title,
  actions,
  className,
  children,
}: {
  title: React.ReactNode
  actions?: React.ReactNode
  className?: string
  children: React.ReactNode
}) {
  return (
    <section className={cn("flex min-w-0 flex-col gap-4", className)}>
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <div className="min-w-0">
          <h2 className="flex min-w-0 items-center gap-1.5 text-base font-semibold tracking-tight">
            <span className="truncate">{title}</span>
          </h2>
        </div>
        {actions}
      </div>
      {children}
    </section>
  )
}
