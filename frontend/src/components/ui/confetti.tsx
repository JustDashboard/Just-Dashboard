"use client"

import { useCallback, useEffect, useImperativeHandle, useRef, type Ref } from "react"
import confetti from "canvas-confetti"
import type { CreateTypes, Options } from "canvas-confetti"

export type ConfettiRef = { fire: (options?: Options) => void }

/**
 * A burst of paper. Magic UI's `confetti`, from the shadcn registry, cut
 * down to a canvas and a `fire` handle: the one thing it does here is
 * celebrate a release that went live while somebody was watching it build,
 * an update of the dashboard itself that finished while its tab was open,
 * and the account's first sidebar landing at the end of the first-run
 * question; it is never fired on arrival at a page. The pieces are the
 * palette's own colours, read at the moment of firing so they follow the
 * tokens.
 */
export function Confetti({ ref, className }: { ref: Ref<ConfettiRef>; className?: string }) {
  const canvas = useRef<HTMLCanvasElement>(null)
  const instance = useRef<CreateTypes | null>(null)

  useEffect(() => {
    if (canvas.current && !instance.current) {
      instance.current = confetti.create(canvas.current, { resize: true, useWorker: true })
    }
    return () => {
      instance.current?.reset()
      instance.current = null
    }
  }, [])

  const fire = useCallback((options: Options = {}) => {
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return
    const style = getComputedStyle(document.documentElement)
    const colors = ["--brand", "--signal", "--chart-4", "--chart-3", "--success"].map((name) =>
      hex(style.getPropertyValue(name).trim()),
    )
    void instance.current?.({
      particleCount: 90,
      spread: 70,
      startVelocity: 38,
      gravity: 0.9,
      ticks: 220,
      origin: { x: 0.5, y: 0.35 },
      colors,
      ...options,
    })
  }, [])

  useImperativeHandle(ref, () => ({ fire }), [fire])

  return <canvas ref={canvas} aria-hidden className={className} />
}

/**
 * canvas-confetti parses `#rrggbb` and nothing else, and every token is
 * `oklch()`: handed straight through, each piece fell back to a muddy brown
 * whatever the palette said. Painting the token onto one pixel and reading it
 * back is the browser's own conversion.
 */
function hex(color: string) {
  const context = document.createElement("canvas").getContext("2d", { willReadFrequently: true })!
  context.fillStyle = color
  context.fillRect(0, 0, 1, 1)
  const [r, g, b] = context.getImageData(0, 0, 1, 1).data
  return `#${[r, g, b].map((value) => value.toString(16).padStart(2, "0")).join("")}`
}
