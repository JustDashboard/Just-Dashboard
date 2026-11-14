import { cn } from "@/lib/utils"

/** One segment of a `StripGroup`: the group draws the edge, the segment only its hover. */
export const STRIP_SEGMENT = "h-full w-8 min-w-8 rounded-none px-0 focus-ring-inset"

/**
 * Controls that are one idea, inside one edge — the same shape as the Git
 * workspace's fetch, pull and push. Each segment is a ghost the full height of
 * the box, and the hairline between two is the group's, never the segment's.
 *
 * The Files strip drew it first; the file editor's toolbar is on it too, so a
 * file opened from the listing is worked on under the same faces it was found
 * under.
 */
export function StripGroup({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      role="group"
      className={cn(
        "flex h-8 shrink-0 items-stretch overflow-hidden rounded-md border border-input bg-control [&>*+*]:border-l [&>*+*]:border-input",
        className,
      )}
      {...props}
    />
  )
}
