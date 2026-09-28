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
 *
 * `once` sends a single pulse and then draws the line still — §11's *once*,
 * for the picture of one thing that just happened (a test message leaving for
 * its channel) rather than of a link that carries all the time. Re-key the
 * beam to send another.
 *
 * `shape="s"` draws the line as a node editor does: it leaves the first end
 * level, bends across the middle and arrives level at the other, so a column
 * of things wired to a column of other things reads as ports and wires
 * rather than as strings pulled tight between them. The default is the arc
 * the picture of a request's path uses, bowed by `curvature`.
 */
export function AnimatedBeam({
  className,
  containerRef,
  fromRef,
  toRef,
  curvature = 0,
  shape = "arc",
  reverse = false,
  still = false,
  once = false,
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
  /** An arc bowed by `curvature`, or a wire that leaves and arrives level. */
  shape?: "arc" | "s"
  /** The pulse runs from `toRef` back to `fromRef`. */
  reverse?: boolean
  /** A line with nothing moving on it. */
  still?: boolean
  /** One pulse, then the still line. */
  once?: boolean
  /** A link that is not there yet. */
  dashed?: boolean
  /**
   * What a still line says: nothing, that it carried, that it broke — or that
   * it works but should not be relied on, a route around the proxy.
   */
  tone?: "default" | "success" | "warning" | "danger"
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
  const [spent, setSpent] = useState(false)

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
      if (shape === "s") {
        const midX = (startX + endX) / 2
        setPathD(`M ${startX},${startY} C ${midX},${startY} ${midX},${endY} ${endX},${endY}`)
        return
      }
      const controlY = startY - curvature
      setPathD(`M ${startX},${startY} Q ${(startX + endX) / 2},${controlY} ${endX},${endY}`)
    }
    const observer = new ResizeObserver(update)
    if (containerRef.current) observer.observe(containerRef.current)
    update()
    return () => observer.disconnect()
  }, [
    containerRef,
    fromRef,
    toRef,
    curvature,
    shape,
    startXOffset,
    startYOffset,
    endXOffset,
    endYOffset,
  ])

  const quiet = still || reduced || spent

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
          tone === "warning" && "stroke-warning",
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
                repeat: once ? 0 : Infinity,
                repeatDelay,
              }}
              onAnimationComplete={once ? () => setSpent(true) : undefined}
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
