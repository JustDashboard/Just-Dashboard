"use client"

import { motion, useReducedMotion } from "motion/react"

import { cn } from "@/lib/utils"

/**
 * A block that arrives: from a few pixels away and out of focus, into place.
 * Magic UI's `blur-fade`, from the shadcn registry, trimmed to the one use it
 * has here — a grid of cards landing one after another, so a page of twelve
 * projects reads as arriving rather than appearing. It is `animate-rise`
 * with a stagger; reduced motion renders the children as they are.
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
  const reduced = useReducedMotion()
  if (reduced) return <div className={className}>{children}</div>
  return (
    <motion.div
      initial={{ y: offset, opacity: 0, filter: "blur(4px)" }}
      animate={{ y: 0, opacity: 1, filter: "blur(0px)" }}
      transition={{ delay, duration, ease: [0.16, 1, 0.3, 1] }}
      className={cn("min-w-0", className)}
    >
      {children}
    </motion.div>
  )
}
