import { cn } from "@/lib/utils"
import { Input } from "@/components/ui/input"
import { MagnifyingGlass } from "@/components/icons"
import { ErrorState, LoadingPanel } from "@/components/state"

/**
 * The frame every page renders into.
 *
 * One measure, one gutter, one vertical rhythm, set here rather than by each
 * page picking its own `gap-4`/`gap-6`/`space-y-4`. `min-w-0` is load-bearing
 * on both the frame and its children: a wide table's intrinsic width would
 * otherwise widen the flex column and take the whole shell sideways with it,
 * instead of scrolling inside the panel that owns it.
 *
 * `fill` is for the pages whose content *is* the viewport — the terminal and
 * the log stream — where the pane has to take the remaining height rather than
 * the page growing past the bottom of the window.
 *
 * It has to be a *definite* height, not `min-h-full`, and that distinction is
 * the whole bug it exists to prevent. A minimum is a floor the box may exceed,
 * so the page stayed content-sized: every `min-h-0 flex-1` beneath it then
 * measured against a parent that grows to fit, which is the opposite of what
 * those classes are asking for. A log pane sized itself to eight thousand
 * lines, and a terminal ratcheted — xterm's fit addon reads the box, the box
 * came from xterm's own rows, so the pane could grow but never shrink back.
 * `h-full` resolves against the shell's scroll container, which does have a
 * definite height, and `overflow-hidden` keeps that promise: a fill page is
 * exactly the space it was handed, and the scrolling happens inside the pane
 * that owns the content.
 */
export function Page({
  className,
  fill,
  ...props
}: React.ComponentProps<"div"> & { fill?: boolean }) {
  return (
    <div
      data-slot="page"
      className={cn(
        "mx-auto flex w-full max-w-[1440px] min-w-0 flex-col gap-6 px-5 py-6 md:gap-8 md:px-8 md:py-8",
        fill && "h-full min-h-0 overflow-hidden",
        className,
      )}
      {...props}
    />
  )
}

/**
 * The title band at the top of a page: where it sits in the product, what it
 * is called, and what you can do to it.
 *
 * The eyebrow repeats the nav group rather than the page name, so the band
 * answers "where am I" without restating the sidebar item directly above it.
 *
 * There is no description. Every page carried a sentence under its heading
 * explaining what the page was, which is a caption for a title the reader has
 * already read and understood — it pushed the first real row of every page a
 * line and a half down the screen and was never looked at twice. What a page
 * actually needs said goes in a `Notice`, where it is a fact rather than a
 * subtitle; what it does not need said goes nowhere.
 */
export function PageHeader({
  eyebrow,
  title,
  actions,
  className,
}: {
  eyebrow?: React.ReactNode
  title: React.ReactNode
  actions?: React.ReactNode
  className?: string
}) {
  return (
    <div
      data-slot="page-header"
      className={cn("flex min-w-0 flex-wrap items-end justify-between gap-x-6 gap-y-3", className)}
    >
      <div className="min-w-0 space-y-1.5">
        {eyebrow && <p className="eyebrow">{eyebrow}</p>}
        {/* The largest type on the page, by a clear step: the title is the one
            thing that has to be found without reading, and at 20px it sat two
            pixels from the panel titles it was meant to rank above. */}
        <h1 className="truncate text-2xl leading-tight font-semibold tracking-tight">{title}</h1>
      </div>
      {actions && (
        <div className="flex max-w-full shrink-0 flex-wrap items-center gap-2">{actions}</div>
      )}
    </div>
  )
}

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

/** A row of filters and actions that stands on its own, outside a panel. */
export function Toolbar({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("flex min-w-0 flex-wrap items-center gap-2", className)} {...props} />
}

/**
 * The filter box, which appeared in six pages as the same three elements
 * assembled slightly differently each time — a different width, a different
 * icon offset, sometimes no icon at all.
 */
export function SearchInput({
  dense,
  trailing,
  className,
  containerClassName,
  ...props
}: React.ComponentProps<typeof Input> & {
  /** The filter box inside a pane's own chrome, where 32px is too tall. */
  dense?: boolean
  /** Controls pinned inside the box — a clear button, a regex toggle. */
  trailing?: React.ReactNode
  containerClassName?: string
}) {
  return (
    <div className={cn("relative flex w-full items-center sm:w-72", containerClassName)}>
      <MagnifyingGlass
        className={cn(
          "pointer-events-none absolute top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground",
          dense ? "left-2" : "left-2.5",
        )}
      />
      <Input
        className={cn(dense ? "h-7 pl-7 text-xs" : "h-8 pl-8 text-body", className)}
        {...props}
      />
      {trailing && <div className="absolute right-1 flex items-center gap-0.5">{trailing}</div>}
    </div>
  )
}

/**
 * One figure with its name, for a strip of them under a chart or in a card
 * header. The name goes above the number, small and quiet: a column of these
 * scans as a table of values rather than a paragraph of labels.
 */
export function Metric({
  label,
  value,
  hint,
  className,
}: {
  label: React.ReactNode
  value: React.ReactNode
  hint?: React.ReactNode
  className?: string
}) {
  return (
    <div className={cn("min-w-0", className)}>
      <p className="eyebrow truncate">{label}</p>
      <p className="numeric mt-0.5 truncate text-sm font-medium">{value}</p>
      {hint && <p className="truncate text-hint text-muted-foreground">{hint}</p>}
    </div>
  )
}

/** A horizontal run of Metrics, separated by rules rather than by gap alone. */
export function MetricStrip({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      className={cn(
        "flex flex-wrap gap-x-6 gap-y-3 [&>*]:min-w-0 [&>*+*]:border-l [&>*+*]:border-hairline [&>*+*]:pl-6",
        className,
      )}
      {...props}
    />
  )
}

/**
 * A table cell's primary column, rendered as the row's way into its detail
 * view — default text colour, underline only on hover.
 *
 * `Button`'s `variant="link"` doesn't fit here: it colours the text primary
 * and always underlines, which reads as navigation rather than "this is the
 * name of the thing the row is about". Thirteen tables across the app were
 * each retyping the same three classes for exactly this button before this
 * existed.
 */
export function RowLink({
  onClick,
  mono,
  className,
  children,
}: {
  onClick: () => void
  mono?: boolean
  className?: string
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "truncate text-left text-body font-medium hover:underline",
        mono && "font-mono text-xs",
        className,
      )}
    >
      {children}
    </button>
  )
}

/**
 * A label/value pair in a stacked list — the replacement for the `<dl>` grids
 * that each card used to hand-roll with its own column widths.
 */
export function DetailList({ className, ...props }: React.ComponentProps<"dl">) {
  return (
    <dl
      className={cn("grid grid-cols-[minmax(0,auto)_minmax(0,1fr)] gap-x-4 gap-y-1.5", className)}
      {...props}
    />
  )
}

export function Detail({
  label,
  children,
  className,
}: {
  label: React.ReactNode
  children: React.ReactNode
  className?: string
}) {
  return (
    <>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className={cn("min-w-0 text-xs", className)}>{children}</dd>
    </>
  )
}

/**
 * A page that has not got its data yet, or cannot get it.
 *
 * Five pages wrote the frame two or three times — once for the error branch,
 * once for the skeleton, once for the real thing — and Overview shows why that
 * is not merely repetitive: its loading branch titled the page "Overview" and
 * its loaded branch titled it with the hostname, so the heading visibly changed
 * under the reader a second after they arrived. A page's identity does not
 * depend on whether its data has landed.
 *
 * `skeleton` takes the silhouette of what is coming rather than a spinner,
 * because the point of a placeholder is that the page does not jump when the
 * content arrives. Pages whose shape is a stack of panels can leave it out and
 * get `LoadingPanel`.
 */
export function PageState({
  eyebrow,
  title,
  error,
  onRetry,
  skeleton,
}: {
  eyebrow?: React.ReactNode
  title: React.ReactNode
  /** When set, the page failed; otherwise it is still loading. */
  error?: Error
  onRetry?: () => void
  skeleton?: React.ReactNode
}) {
  return (
    <Page>
      <PageHeader eyebrow={eyebrow} title={title} />
      {error ? <ErrorState error={error} onRetry={onRetry} /> : (skeleton ?? <LoadingPanel />)}
    </Page>
  )
}
