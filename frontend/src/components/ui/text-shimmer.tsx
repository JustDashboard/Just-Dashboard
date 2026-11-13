"use client"

import { cn } from "@/lib/utils"

/**
 * A word with a band of light passing along it: the name of the thing that
 * is happening right now. Motion Primitives' `text-shimmer`, from its
 * registry, with the two colours taken from the palette — the resting ink is
 * the muted foreground and the band is the foreground — so it reads as the
 * same text, lit, rather than as a different colour of text. It says *live*
 * (design system §11) for a label the way `StatusDot`'s halo does for a dot.
 * Reduced motion sets the word in the resting ink and leaves it.
 *
 * The band moves on a CSS animation (`jd-shimmer`), not a JavaScript frame
 * loop. It is the word's own background, clipped to its letters, so the
 * browser still paints the word for each frame; that is one short run of
 * text, for as long as the wait it names.
 */
export function TextShimmer({
  children,
  className,
  duration = 2,
  spread = 2,
}: {
  children: string
  className?: string
  duration?: number
  spread?: number
}) {
  const band = children.length * spread
  return (
    <span
      className={cn(
        // Reduced motion: no band, the word in its resting ink.
        "relative inline-block bg-[length:250%_100%,auto] bg-clip-text [background-repeat:no-repeat,padding-box] text-transparent motion-reduce:bg-none motion-reduce:text-muted-foreground",
        className,
      )}
      style={{
        backgroundImage: `linear-gradient(90deg, #0000 calc(50% - ${band}px), var(--foreground), #0000 calc(50% + ${band}px)), linear-gradient(var(--muted-foreground), var(--muted-foreground))`,
        backgroundPosition: "100% center",
        animation: `jd-shimmer ${duration}s linear infinite`,
      }}
    >
      {children}
    </span>
  )
}
