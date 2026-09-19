"use client"

import { useEffect, useId, useState, type RefObject } from "react"
import { motion, useReducedMotion } from "motion/react"

import { cn } from "@/lib/utils"

/**
 * A line drawn between two elements, with a pulse of light travelling along
 * it. Magic UI's `animated-beam`, brought in through the shadcn registry and
 * then made this product's: the path takes the border token, the pulse runs
 * from the brand blue to the lit `--signal`, and `still` draws the line with
 * no pulse for a link that exists but carries nothing yet.
 *
 * It measures the two ends against `containerRef` and redraws when the
 * container resizes, so the ends can be laid out by CSS and the line follows.
 * Reduced motion draws the still line: the root `prefers-reduced-motion` rule
 * only reaches CSS animations, and this one is driven from JavaScript.
 */
export function AnimatedBeam({
  className,
  containerRef,
  fromRef,
  toRef,
  curvature = 0,
  reverse = false,
  still = false,
  dashed = false,
  tone = "default",
  duration = 3,
  delay = 0,
  pathWidth = 1.5,
  repeatDelay = 0.6,
  startXOffset = 0,
  startYOffset = 0,
  endXOffset = 0,
  endYOffset = 0,
}: {
  className?: string
  containerRef: RefObject<HTMLElement | null>
  fromRef: RefObject<HTMLElement | null>
  toRef: RefObject<HTMLElement | null>
  /** How far the midpoint bows above (positive) or below the straight line. */
  curvature?: number
  /** The pulse runs from `toRef` back to `fromRef`. */
  reverse?: boolean
  /** A line with nothing moving on it. */
  still?: boolean
  /** A link that is not there yet. */
  dashed?: boolean
  /** What a still line says: nothing, that it carried, or that it broke. */
  tone?: "default" | "success" | "danger"
  duration?: number
  delay?: number
  pathWidth?: number
  repeatDelay?: number
  startXOffset?: number
  startYOffset?: number
  endXOffset?: number
  endYOffset?: number
}) {
  const id = useId()
  const reduced = useReducedMotion()
  const [pathD, setPathD] = useState("")
  const [size, setSize] = useState({ width: 0, height: 0 })

  const gradient = reverse
    ? { x1: ["90%", "-10%"], x2: ["100%", "0%"], y1: ["0%", "0%"], y2: ["0%", "0%"] }
    : { x1: ["10%", "110%"], x2: ["0%", "100%"], y1: ["0%", "0%"], y2: ["0%", "0%"] }

  useEffect(() => {
    const update = () => {
      const container = containerRef.current
      const from = fromRef.current
      const to = toRef.current
      if (!container || !from || !to) return
      const box = container.getBoundingClientRect()
      const a = from.getBoundingClientRect()
      const b = to.getBoundingClientRect()
      setSize({ width: box.width, height: box.height })
      const startX = a.left - box.left + a.width / 2 + startXOffset
      const startY = a.top - box.top + a.height / 2 + startYOffset
      const endX = b.left - box.left + b.width / 2 + endXOffset
      const endY = b.top - box.top + b.height / 2 + endYOffset
      const controlY = startY - curvature
      setPathD(`M ${startX},${startY} Q ${(startX + endX) / 2},${controlY} ${endX},${endY}`)
    }
    const observer = new ResizeObserver(update)
    if (containerRef.current) observer.observe(containerRef.current)
    update()
    return () => observer.disconnect()
  }, [containerRef, fromRef, toRef, curvature, startXOffset, startYOffset, endXOffset, endYOffset])

  const quiet = still || reduced

  return (
    <svg
      fill="none"
      width={size.width}
      height={size.height}
      xmlns="http://www.w3.org/2000/svg"
      className={cn("pointer-events-none absolute top-0 left-0 transform-gpu", className)}
      viewBox={`0 0 ${size.width} ${size.height}`}
      aria-hidden
    >
      <path
        d={pathD}
        strokeWidth={pathWidth}
        strokeLinecap="round"
        strokeDasharray={dashed ? "2 6" : undefined}
        className={cn(
          tone === "success" && "stroke-success",
          tone === "danger" && "stroke-destructive",
          tone === "default" && (quiet ? "stroke-border-strong" : "stroke-border"),
        )}
      />
      {!quiet && (
        <>
          <path d={pathD} strokeWidth={pathWidth} stroke={`url(#${id})`} strokeLinecap="round" />
          <defs>
            <motion.linearGradient
              className="transform-gpu"
              id={id}
              gradientUnits="userSpaceOnUse"
              initial={{ x1: "0%", x2: "0%", y1: "0%", y2: "0%" }}
              animate={gradient}
              transition={{
                delay,
                duration,
                ease: [0.16, 1, 0.3, 1],
                repeat: Infinity,
                repeatDelay,
              }}
            >
              <stop style={{ stopColor: "var(--brand)" }} stopOpacity="0" />
              <stop style={{ stopColor: "var(--brand)" }} />
              <stop offset="32.5%" style={{ stopColor: "var(--signal)" }} />
              <stop offset="100%" style={{ stopColor: "var(--signal)" }} stopOpacity="0" />
            </motion.linearGradient>
          </defs>
        </>
      )}
    </svg>
  )
}
