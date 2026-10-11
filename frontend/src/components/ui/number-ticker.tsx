"use client"

import { useEffect, useRef, type ComponentPropsWithoutRef } from "react"
import { useInView, useMotionValue, useReducedMotion, useSpring } from "motion/react"

import { cn } from "@/lib/utils"

/**
 * A figure that counts up to its value when it arrives. Magic UI's
 * `number-ticker`, from the shadcn registry, with its own colours and tracking
 * removed so it inherits the tile it sits in — it is a figure, and the tile
 * already says how a figure is set.
 *
 * The spring is critically damped, the quickest that does not overshoot, so a
 * count of two never shows three on the way. It settles in under a second. It
 * was overdamped (damping 60) and took about three and a half, which on a live
 * figure read every two seconds meant it never stopped: the Overview rewrote
 * its readings and laid the page out again on every frame while idle.
 * Reduced motion writes the value at once.
 */
export function NumberTicker({
  value,
  startValue = 0,
  delay = 0,
  className,
  decimalPlaces = 0,
  ...props
}: ComponentPropsWithoutRef<"span"> & {
  value: number
  startValue?: number
  delay?: number
  decimalPlaces?: number
}) {
  const ref = useRef<HTMLSpanElement>(null)
  const reduced = useReducedMotion()
  const motionValue = useMotionValue(reduced ? value : startValue)
  const springValue = useSpring(motionValue, { damping: 20, stiffness: 100 })
  const isInView = useInView(ref, { once: true, margin: "0px" })

  useEffect(() => {
    if (!isInView) return
    const timer = setTimeout(() => motionValue.set(value), delay * 1000)
    return () => clearTimeout(timer)
  }, [motionValue, isInView, delay, value])

  // Written only when the figure changes at the precision shown: every write
  // is a DOM mutation the browser answers with style and layout.
  useEffect(
    () =>
      springValue.on("change", (latest) => {
        const node = ref.current
        if (!node) return
        const next = figure(latest, decimalPlaces)
        if (node.textContent !== next) node.textContent = next
      }),
    [springValue, decimalPlaces],
  )

  return (
    <span ref={ref} className={cn("numeric inline-block", className)} {...props}>
      {/* With reduced motion the spring never moves, so this is the figure
          the reader sees — and so is a start that is already the value: at
          the precision asked for, never the raw double. */}
      {figure(reduced ? value : startValue, decimalPlaces)}
    </span>
  )
}

/** Built once per precision rather than on every frame of every ticker. */
const formatters = new Map<number, Intl.NumberFormat>()

function figure(value: number, decimalPlaces: number) {
  let format = formatters.get(decimalPlaces)
  if (!format) {
    format = new Intl.NumberFormat("en-US", {
      minimumFractionDigits: decimalPlaces,
      maximumFractionDigits: decimalPlaces,
    })
    formatters.set(decimalPlaces, format)
  }
  return format.format(Number(value.toFixed(decimalPlaces)))
}
