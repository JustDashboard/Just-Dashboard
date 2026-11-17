import { cn } from "@/lib/utils"
import { Bone } from "@/components/bone"
import { Skeleton } from "@/components/ui/skeleton"

/**
 * What the DNS page draws where a reading has not landed: the same boxes its
 * words and marks will stand in, in grey. The page used to wait on one panel
 * and then arrive whole, 4,800px at once; each block now waits at the height
 * it will have, so the first read moves nothing.
 *
 * Every piece carries `data-slot="skeleton"`, the mark a spec waits on.
 */

/**
 * A grey bar on a line of text: a `Bone`. It sits inside the line box, so the
 * line keeps the height its words will have.
 */
export function Bar({ className }: { className?: string }) {
  return <Bone className={className} />
}

/** A mark's place, at the size a `WireMark` of that size takes. */
export function GhostMark({ size }: { size: "lg" | "md" | "sm" }) {
  return (
    <Skeleton
      className={cn(
        "flex shrink-0 rounded-xl",
        size === "lg" ? "size-14" : size === "md" ? "size-11" : "size-9",
      )}
    />
  )
}
