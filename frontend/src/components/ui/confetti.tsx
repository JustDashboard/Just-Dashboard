"use client"

import { useCallback, useEffect, useImperativeHandle, useRef, type Ref } from "react"
import confetti from "canvas-confetti"
import type { CreateTypes, Options } from "canvas-confetti"

export type ConfettiRef = { fire: (options?: Options) => void }

/**
 * A burst of paper. Magic UI's `confetti`, from the shadcn registry, cut
 * down to a canvas and a `fire` handle: the one thing it does here is
 * celebrate a release that went live while somebody was watching it build,
 * and it is never fired on arrival at a page. The pieces are the palette's
 * own colours, read at the moment of firing so they follow the tokens.
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
    const colors = ["--brand", "--signal", "--success", "--foreground"].map((name) =>
      style.getPropertyValue(name).trim(),
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
