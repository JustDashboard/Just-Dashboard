"use client"

import { motion, useReducedMotion } from "motion/react"

import { cn } from "@/lib/utils"

/**
 * A word with a band of light passing along it: the name of the thing that
 * is happening right now. Motion Primitives' `text-shimmer`, from its
 * registry, with the two colours taken from the palette — the resting ink is
 * the muted foreground and the band is the foreground — so it reads as the
 * same text, lit, rather than as a different colour of text. It says *live*
 * (design system §11) for a label the way `StatusDot`'s halo does for a dot.
 * Reduced motion sets the word in the resting ink and leaves it.
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
  const reduced = useReducedMotion()
  if (reduced) return <span className={cn("text-muted-foreground", className)}>{children}</span>
  const band = children.length * spread
  return (
    <motion.span
      className={cn(
        "relative inline-block bg-[length:250%_100%,auto] bg-clip-text [background-repeat:no-repeat,padding-box] text-transparent",
        className,
      )}
      initial={{ backgroundPosition: "100% center" }}
      animate={{ backgroundPosition: "0% center" }}
      transition={{ repeat: Infinity, duration, ease: "linear" }}
      style={{
        backgroundImage: `linear-gradient(90deg, #0000 calc(50% - ${band}px), var(--foreground), #0000 calc(50% + ${band}px)), linear-gradient(var(--muted-foreground), var(--muted-foreground))`,
      }}
    >
      {children}
    </motion.span>
  )
}
