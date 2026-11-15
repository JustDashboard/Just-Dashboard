import Link from "next/link"
import { cn } from "@/lib/utils"

/**
 * A lane's heading: the eyebrow, and beside it how many the lane holds in the
 * faint ink. The count was the muted ink at 70%, a fade the palette has no
 * name for (one mode, three inks), copied into seven pictures. With `href` the
 * heading is the way to the page that lists the lane, and says so with an
 * arrow.
 */
export function WireLaneTitle({
  title,
  count,
  href,
  className,
}: {
  title: React.ReactNode
  count?: number
  href?: string
  className?: string
}) {
  const words = (
    <>
      {title}
      {/* A space, not only a margin: a link named "Sessions3" is what a
          screen reader says without one. */}
      {count !== undefined && count > 0 && (
        <>
          {" "}
          <span className="numeric ml-0.5 font-medium tracking-normal text-faint">{count}</span>
        </>
      )}
    </>
  )
  if (href)
    return (
      <p className={cn("eyebrow", className)}>
        <Link href={href} className="rounded-sm focus-ring transition-colors hover:text-foreground">
          {words}
          <span aria-hidden className="ml-1">
            →
          </span>
        </Link>
      </p>
    )
  return <p className={cn("eyebrow", className)}>{words}</p>
}

/**
 * One column of a hand-laid wiring picture: its heading and the list of its
 * nodes. Seven pictures carried this as a local `Lane`, four byte for byte,
 * and two with a fix the others never got: the heading hung above the list
 * wide (`float`), because in the list's box it pushed the marks half its
 * height below the hub's and every wire bent to meet them.
 *
 * It is for a picture `WireFlow` does not draw — tiers, faces on a node, two
 * lanes a side, many to many (the stack map, a container's picture). A
 * picture of things wired through one hub is a `WireFlow`, whose columns are
 * this lane already.
 *
 * The list is an `ol` named by the title, so a spec or a screen reader finds
 * the lane by its name. `dense` closes the gap for a lane of small marks that
 * outnumbers the other side (the programs on Ports). `align="end"` sets the
 * heading against the lane's right edge from `lg`, for the start side.
 */
export function WireLane({
  title,
  count,
  float = false,
  dense = false,
  align = "start",
  className,
  children,
}: {
  title: string
  count?: number
  /** Hang the heading above the list from `lg`, so the nodes centre on the hub. */
  float?: boolean
  dense?: boolean
  align?: "start" | "end"
  className?: string
  children: React.ReactNode
}) {
  return (
    <div
      data-slot="wire-lane"
      className={cn(
        "min-w-0 self-stretch lg:flex lg:flex-col lg:justify-center",
        // The hung heading's room, above and below alike so the list stays
        // on the hub's line.
        float && "lg:py-8",
        className,
      )}
    >
      <div className={cn(float && "lg:relative")}>
        <WireLaneTitle
          title={title}
          count={count}
          className={cn(
            "mb-4",
            float && "lg:absolute lg:inset-x-0 lg:bottom-full",
            align === "end" && "lg:text-right",
          )}
        />
        <ol className={cn("flex flex-col", dense ? "gap-3" : "gap-5")} aria-label={title}>
          {children}
        </ol>
      </div>
    </div>
  )
}
