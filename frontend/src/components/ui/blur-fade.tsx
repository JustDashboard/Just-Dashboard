"use client"

import { motion } from "motion/react"
import { useReducedMotion } from "@/components/shell/motion-pause"

import { cn } from "@/lib/utils"

/**
 * A block that arrives: from a few pixels away and out of focus, into place.
 * Magic UI's `blur-fade`, from the shadcn registry, trimmed to the one use it
 * has here — a grid of cards landing one after another, so a page of twelve
 * projects reads as arriving rather than appearing. It is `animate-rise`
 * with a stagger; reduced motion puts the children in place at once.
 */
export function BlurFade({
  children,
  className,
  delay = 0,
  duration = 0.32,
  offset = 6,
}: {
  children: React.ReactNode
  className?: string
  delay?: number
  duration?: number
  offset?: number
}) {
  // One element whichever the answer: Pause animations turned on under a
  // grid that has landed must not draw the cards again from nothing.
  const reduced = useReducedMotion()
  return (
    <motion.div
      data-slot="blur-fade"
      initial={reduced ? false : { y: offset, opacity: 0, filter: "blur(4px)" }}
      animate={{ y: 0, opacity: 1, filter: "blur(0px)" }}
      transition={{ delay, duration, ease: [0.16, 1, 0.3, 1] }}
      className={cn("min-w-0", className)}
    >
      {children}
    </motion.div>
  )
}
