"use client"

import { useEffect, useRef } from "react"

import { beamLap } from "@/lib/beam-lap"
import { cn } from "@/lib/utils"

/**
 * A light travelling around the edge of its container. Magic UI's
 * `border-beam`, from the shadcn registry, in this product's colours: the
 * beam runs from the brand blue to the lit `--signal`, over the container's
 * own border. It says one thing — work is happening inside this frame — and
 * is drawn nowhere else. The container must be `relative`.
 *
 * The lap is a transform animation the browser runs off the main thread: the
 * frame is measured, its edge is written as keyframes (`lib/beam-lap.ts`),
 * and the Web Animations API is handed them once. It was `offset-distance`
 * set from a JavaScript frame loop, which kept a page with one working card
 * on it busy for as long as the card worked. A frame that changes size is
 * measured again and the light carries on from where it was. Reduced motion
 * draws no light, in CSS, and starts no lap.
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
  const frame = useRef<HTMLDivElement>(null)
  const light = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const edge = frame.current
    const beam = light.current
    if (!edge || !beam || typeof beam.animate !== "function") return
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return
    let lap: Animation | undefined
    const run = () => {
      const { width, height } = edge.getBoundingClientRect()
      const frames = beamLap(width, height, size)
      if (frames.length === 0) return
      const reached = lap?.currentTime ?? null
      lap?.cancel()
      lap = beam.animate(frames, {
        duration: duration * 1000,
        // Negative, as it always was: `delay` is how far round the light
        // already is when it appears, so beams on one page are out of step.
        delay: -delay * 1000,
        iterations: Infinity,
        direction: reverse ? "reverse" : "normal",
      })
      if (reached !== null) lap.currentTime = reached
    }
    const observer = new ResizeObserver(run)
    observer.observe(edge)
    return () => {
      observer.disconnect()
      lap?.cancel()
    }
  }, [size, duration, delay, reverse])

  return (
    <div
      ref={frame}
      aria-hidden
      className="pointer-events-none absolute inset-0 rounded-[inherit] border-(length:--border-beam-width) border-transparent mask-[linear-gradient(transparent,transparent),linear-gradient(#000,#000)] mask-intersect [mask-clip:padding-box,border-box] motion-reduce:hidden"
      style={{ "--border-beam-width": `${borderWidth}px` } as React.CSSProperties}
    >
      {/* Its corner on the frame's own, the border's width out from the
          padding it is positioned in; until the first lap is handed over it
          waits beyond the corner, where the mask shows none of it. */}
      <div
        ref={light}
        className={cn(
          "absolute aspect-square bg-linear-to-l from-brand via-signal to-transparent",
          className,
        )}
        style={{
          width: size,
          top: -borderWidth,
          left: -borderWidth,
          transform: `translate(${-2 * size}px, ${-2 * size}px)`,
        }}
      />
    </div>
  )
}
