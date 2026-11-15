import { cn } from "@/lib/utils"
import { Skeleton } from "@/components/ui/skeleton"

/**
 * A bone standing in for words that have not arrived, in a line that keeps the
 * height the words will give it.
 *
 * A bare `Skeleton` in a line of text is as tall as it is told to be, and in a
 * flex line it is the whole of the line's height, so a row holding one was a
 * few pixels shorter than the row it became and everything under it moved
 * when the words landed. This one carries a character of no width beside the
 * bone, so the line box is the words' own whatever the line is laid out as.
 * Its width is the caller's (`w-40`): about what the words will take.
 */
export function Bone({ className }: { className?: string }) {
  return (
    <span aria-hidden className={cn("inline-flex max-w-full items-center align-bottom", className)}>
      <Skeleton className="h-[0.7em] w-full" />
      {"​"}
    </span>
  )
}
