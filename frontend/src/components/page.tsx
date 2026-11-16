import Link from "next/link"
import { cn } from "@/lib/utils"
import { ArrowLeft } from "@/components/icons"
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
 *
 * `fill="workspace"` is the workbench's frame (Files, Git, the terminal, the
 * map, the boards): the same definite height, the whole width, no gap between
 * its parts, and an inset only from `lg`, where the shell has room around it.
 * Those pages each spelled that with their own overrides — twelve spellings,
 * so their insets drifted by 4 to 8px and a loading branch framed differently
 * from its loaded one. `bleed` drops the inset too, for a remote screen that
 * runs edge to edge.
 *
 * `rise` is the page arriving (the `rise` animation), said once here rather
 * than as `className="animate-rise"` on each page.
 *
 * The gutter is named `--page-inset` (20, 32 from `md`) on the page element,
 * so what bleeds to the screen's edge inside it (a `ChipStrip` on a phone)
 * bleeds by the page's own gutter rather than a copy of the number. The
 * padding is said again at `md` as it was with `md:px-8`, so a page that
 * overrides only the base padding (`p-0 lg:px-3`) keeps the same inset from
 * `md` that it had.
 */
export function Page({
  className,
  fill,
  bleed,
  rise,
  register = "reading",
  ...props
}: React.ComponentProps<"div"> & {
  /**
   * `"xl"` holds the page to the window only where its columns sit side by
   * side — a flow page decided in one view (§17 pass 8) that stacks and
   * scrolls like any other page below that width. `"workspace"` is a
   * workbench's frame (above).
   */
  fill?: boolean | "xl" | "workspace"
  /** With `fill="workspace"`: no inset at any width. */
  bleed?: boolean
  /** The page rises into place as it arrives. */
  rise?: boolean
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
        "group/page mx-auto flex w-full min-w-0 flex-col",
        fill === "workspace"
          ? cn("h-full min-h-0 max-w-none overflow-hidden", !bleed && "lg:p-3")
          : "max-w-[1440px] gap-6 px-(--page-inset) py-6 [--page-inset:1.25rem] md:gap-8 md:px-(--page-inset) md:py-8 md:[--page-inset:2rem]",
        fill === true && "h-full min-h-0 overflow-hidden",
        fill === "xl" && "xl:h-full xl:min-h-0 xl:overflow-hidden",
        rise && "animate-rise",
        className,
      )}
      {...props}
    />
  )
}

/**
 * The way back to the list a page sits under: "← Containers". One drawing for
 * every page, where 24 files each kept the same link in a constant of their
 * own. `PageContext` draws it from `back`; a flow's header and `PageState`
 * take the same prop.
 */
export function BackLink({ href, children }: { href: string; children: React.ReactNode }) {
  return (
    <Link
      href={href}
      className="inline-flex min-w-0 items-center gap-1 rounded-sm focus-ring hover:underline"
    >
      <ArrowLeft aria-hidden className="size-3 shrink-0" />{" "}
      <span className="truncate">{children}</span>
    </Link>
  )
}

/** Where a page's back link leads, and the list's name it says. */
export type Back = { href: string; label: string }

/**
 * The accessible name and contextual controls of a reading page.
 *
 * The rail identifies the page visually. A repeated title above the content
 * used a full row without adding context, so only a linked parent and controls
 * are drawn here. The h1 still names the page for assistive technology, and
 * the same words name its tab where the nav cannot: on an item's own page the
 * address says only "Containers", and the heading says which one
 * (`components/shell/tab-title.tsx`).
 *
 * An item page one level under a list carries `back`, the one way back; a
 * pane a row opened inside a workspace is `PaneBack`'s, which is a switch of
 * panes and not a link. `actions` hold the walk (Alt+arrows) and the help;
 * a thing's verbs go on its identity line (`IdentityVerbs`), so on a phone the
 * row under the shell is the way back and nothing that wraps.
 */
export function PageContext({
  back,
  eyebrow,
  title,
  actions,
  className,
}: {
  back?: Back
  /**
   * @deprecated use `back`. A link passed here is drawn as before; words
   * alone are not drawn (they named a section the rail already names) and
   * say so in development, so the page pass removes them.
   */
  eyebrow?: React.ReactNode
  title: React.ReactNode
  actions?: React.ReactNode
  className?: string
}) {
  if (typeof eyebrow === "string") droppedEyebrow(eyebrow)
  const link = back ? (
    <BackLink href={back.href}>{back.label}</BackLink>
  ) : typeof eyebrow === "string" ? null : (
    eyebrow
  )

  return (
    <>
      <h1 className="sr-only">{title}</h1>
      <TabHeading title={title} />
      {(link || actions) && (
        <div
          data-slot="page-context"
          className={cn(
            "flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-2",
            className,
          )}
        >
          {link && (
            <div className="flex min-w-0 items-center text-body text-muted-foreground">{link}</div>
          )}
          {actions && (
            <div className={cn("flex max-w-full flex-wrap items-center gap-2", link && "ml-auto")}>
              {actions}
            </div>
          )}
        </div>
      )}
    </>
  )
}

const said = new Set<string>()

/** A string eyebrow is not drawn; in development it says so, once per string. */
function droppedEyebrow(words: string) {
  if (process.env.NODE_ENV === "production" || said.has(words)) return
  said.add(words)
  console.warn(
    `PageContext: the eyebrow "${words}" is not drawn. Pass back={{ href, label }} for a way back, or remove it.`,
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
  back,
  eyebrow,
  title,
  error,
  onRetry,
  skeleton,
}: {
  back?: Back
  /** @deprecated use `back`; see `PageContext`. */
  eyebrow?: React.ReactNode
  title: React.ReactNode
  /** When set, the page failed; otherwise it is still loading. */
  error?: Error
  onRetry?: () => void
  skeleton?: React.ReactNode
}) {
  return (
    <Page>
      <PageContext back={back} eyebrow={eyebrow} title={title} />
      {error ? <ErrorState error={error} onRetry={onRetry} /> : (skeleton ?? <LoadingPanel />)}
    </Page>
  )
}
