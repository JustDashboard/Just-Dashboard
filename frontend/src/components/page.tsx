import { cn } from "@/lib/utils"
import { ErrorState } from "@/components/state"
import { LoadingPanel } from "@/components/loading"
import { TabHeading } from "@/components/shell/tab-title"

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
  register = "reading",
  ...props
}: React.ComponentProps<"div"> & {
  /**
   * `"xl"` holds the page to the window only where its columns sit side by
   * side — a flow page decided in one view (§17 pass 8) that stacks and
   * scrolls like any other page below that width.
   */
  fill?: boolean | "xl"
  /**
   * Which register this page is drawn in (§16).
   *
   * `reading` is the product: a page that reports state, drawn flat, where
   * the figures are the loud thing. `flow` is a page where the reader is
   * deciding something in sequence — it composes `components/flow.tsx`
   * instead of bare panels and rows, and it is the only place the depth,
   * the lit border and the mandatory command face are allowed.
   *
   * It is stamped on the page element rather than inferred, so which register
   * a page is in is one grep, a page cannot be half in each by accident, and
   * a browser test can assert that only the flow pages carry the flow
   * affordances.
   */
  register?: "reading" | "flow"
}) {
  return (
    <div
      data-slot="page"
      data-register={register}
      className={cn(
        "group/page mx-auto flex w-full max-w-[1440px] min-w-0 flex-col gap-6 px-5 py-6 md:gap-8 md:px-8 md:py-8",
        fill === true && "h-full min-h-0 overflow-hidden",
        fill === "xl" && "xl:h-full xl:min-h-0 xl:overflow-hidden",
        className,
      )}
      {...props}
    />
  )
}

/**
 * The accessible name and contextual controls of a reading page.
 *
 * The rail identifies the page visually. A repeated title above the content
 * used a full row without adding context, so only a linked parent and controls
 * are drawn here. The h1 still names the page for assistive technology, and
 * the same words name its tab where the nav cannot: on an item's own page the
 * address says only "Containers", and the heading says which one
 * (`components/shell/tab-title.tsx`).
 */
export function PageContext({
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
  const back = typeof eyebrow === "string" ? null : eyebrow

  return (
    <>
      <h1 className="sr-only">{title}</h1>
      <TabHeading title={title} />
      {(back || actions) && (
        <div
          data-slot="page-context"
          className={cn(
            "flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-2",
            className,
          )}
        >
          {back && <div className="flex items-center text-body text-muted-foreground">{back}</div>}
          {actions && (
            <div className={cn("flex max-w-full flex-wrap items-center gap-2", back && "ml-auto")}>
              {actions}
            </div>
          )}
        </div>
      )}
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
      <PageContext eyebrow={eyebrow} title={title} />
      {error ? <ErrorState error={error} onRetry={onRetry} /> : (skeleton ?? <LoadingPanel />)}
    </Page>
  )
}
