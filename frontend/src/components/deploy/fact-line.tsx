import { cn } from "@/lib/utils"

/**
 * Facts on one line that wraps, with a dot between neighbours and none where
 * the line breaks. A dot drawn as an item of its own is left at the end of a
 * line when the next fact wraps, where it reads as a stray mark. Here each
 * fact carries its dot in front of it, in the gap, and the line is drawn one
 * gap to the left of a frame that clips it: the dot before a line's first
 * fact falls outside the frame, whichever fact the wrap makes first.
 */
export function FactLine({ className, children, ...props }: React.ComponentProps<"span">) {
  return (
    <span className={cn("block min-w-0 overflow-hidden", className)} {...props}>
      <span className="-ml-4 flex flex-wrap items-baseline gap-y-0.5">{children}</span>
    </span>
  )
}

export function Fact({ className, ...props }: React.ComponentProps<"span">) {
  return (
    <span
      data-slot="fact"
      className={cn(
        "relative min-w-0 pl-4 before:absolute before:left-1.5 before:text-muted-foreground/40 before:content-['·']",
        className,
      )}
      {...props}
    />
  )
}
