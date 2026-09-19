"use client"

import { motion, useReducedMotion } from "motion/react"

import { cn } from "@/lib/utils"

/**
 * A light travelling around the edge of its container. Magic UI's
 * `border-beam`, from the shadcn registry, in this product's colours: the
 * beam runs from the brand blue to the lit `--signal`, over the container's
 * own border. It says one thing — work is happening inside this frame — and
 * is drawn nowhere else. The container must be `relative`.
 */
export function BorderBeam({
  className,
  size = 64,
  duration = 5,
  delay = 0,
  reverse = false,
  borderWidth = 1,
}: {
  className?: string
  size?: number
  duration?: number
  delay?: number
  reverse?: boolean
  borderWidth?: number
}) {
  const reduced = useReducedMotion()
  if (reduced) return null
  return (
    <div
      aria-hidden
      className="pointer-events-none absolute inset-0 rounded-[inherit] border-(length:--border-beam-width) border-transparent mask-[linear-gradient(transparent,transparent),linear-gradient(#000,#000)] mask-intersect [mask-clip:padding-box,border-box]"
      style={{ "--border-beam-width": `${borderWidth}px` } as React.CSSProperties}
    >
      <motion.div
        className={cn(
          "absolute aspect-square bg-linear-to-l from-brand via-signal to-transparent",
          className,
        )}
        style={{ width: size, offsetPath: `rect(0 auto auto 0 round ${size}px)` }}
        initial={{ offsetDistance: "0%" }}
        animate={{ offsetDistance: reverse ? ["100%", "0%"] : ["0%", "100%"] }}
        transition={{ repeat: Infinity, ease: "linear", duration, delay: -delay }}
      />
    </div>
  )
}
