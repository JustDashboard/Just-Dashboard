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
 * The spring is overdamped, so a count of two never shows three on the way.
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
  const springValue = useSpring(motionValue, { damping: 60, stiffness: 100 })
  const isInView = useInView(ref, { once: true, margin: "0px" })

  useEffect(() => {
    if (!isInView) return
    const timer = setTimeout(() => motionValue.set(value), delay * 1000)
    return () => clearTimeout(timer)
  }, [motionValue, isInView, delay, value])

  useEffect(
    () =>
      springValue.on("change", (latest) => {
        if (ref.current) {
          ref.current.textContent = Intl.NumberFormat("en-US", {
            minimumFractionDigits: decimalPlaces,
            maximumFractionDigits: decimalPlaces,
          }).format(Number(latest.toFixed(decimalPlaces)))
        }
      }),
    [springValue, decimalPlaces],
  )

  return (
    <span ref={ref} className={cn("numeric inline-block", className)} {...props}>
      {reduced ? value : startValue}
    </span>
  )
}
