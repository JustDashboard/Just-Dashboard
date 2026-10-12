"use client"

import { createRef, useRef, type RefObject } from "react"
import { cn } from "@/lib/utils"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { WireHost, WireMark } from "@/components/deploy/wire"
import { ProductGlyph } from "@/components/product-logo"

// One product for each thing the first-run question offers, in the order the
// purposes are asked, so the picture at sign-in is the answer's vocabulary.
const PRODUCTS = [
  "github",
  "docker",
  "postgres",
  "redis",
  "caddy",
  "wireguard",
  "lets-encrypt",
  "terminal",
] as const

/**
 * The machine the reader is about to sign in to, wired to what it runs — the
 * rack every wiring picture draws for a server, ringed by the products the
 * dashboard drives, with a pulse running out to each. It is the picture the
 * first-run question then fills in with the reader's own answer, so sign-in and
 * the screen after it are one drawing rather than two pages.
 *
 * Nothing in it is read from the server: the page is drawn before anybody has
 * signed in, and the products are the dashboard's, not this host's.
 *
 * `orbit` is the square picture beside the form on a wide screen; `row` is the
 * strip a phone has room for, the server in the middle and the products either
 * side of it on one line.
 */
export function ProductOrbit({
  layout = "orbit",
  className,
}: {
  layout?: "orbit" | "row"
  className?: string
}) {
  const container = useRef<HTMLDivElement>(null)
  const hub = useRef<HTMLDivElement>(null)
  const marks = useRef<Partial<Record<string, RefObject<HTMLDivElement | null>>>>({})
  const markOf = (id: string) => (marks.current[id] ??= createRef<HTMLDivElement>())

  if (layout === "row") {
    // Three a side: as many as a 320px phone holds at the small mark's size.
    const left = PRODUCTS.slice(0, 3)
    const right = PRODUCTS.slice(3, 6)
    return (
      <div
        ref={container}
        aria-hidden
        className={cn("relative flex items-center justify-between", className)}
      >
        {[...left, ...right].map((id, index) => (
          <AnimatedBeam
            key={id}
            containerRef={container}
            fromRef={hub}
            toRef={markOf(id)}
            reverse={index < left.length}
            duration={2.8}
            delay={index * 0.3}
          />
        ))}
        {left.map((id) => (
          <Mark key={id} id={id} markRef={markOf(id)} size="sm" />
        ))}
        <div ref={hub} className="relative z-10 flex">
          <WireHost size="md" />
        </div>
        {right.map((id) => (
          <Mark key={id} id={id} markRef={markOf(id)} size="sm" />
        ))}
      </div>
    )
  }

  return (
    <div ref={container} aria-hidden className={cn("relative aspect-square w-full", className)}>
      <span className="absolute inset-[12%] rounded-full border border-dashed border-hairline" />
      {PRODUCTS.map((id, index) => (
        <AnimatedBeam
          key={id}
          containerRef={container}
          fromRef={hub}
          toRef={markOf(id)}
          // Every pulse leaves the server: the beam's light runs left to right
          // unless reversed, so the marks on the left half run it back.
          reverse={Math.cos(angleOf(index)) < -0.01}
          duration={3.2}
          delay={index * 0.4}
        />
      ))}
      <div
        ref={hub}
        className="absolute top-1/2 left-1/2 z-10 flex -translate-x-1/2 -translate-y-1/2"
      >
        <WireHost />
      </div>
      {PRODUCTS.map((id, index) => (
        <div
          key={id}
          className="absolute z-10 flex -translate-x-1/2 -translate-y-1/2"
          style={{
            left: `${50 + 38 * Math.cos(angleOf(index))}%`,
            top: `${50 + 38 * Math.sin(angleOf(index))}%`,
          }}
        >
          <Mark id={id} markRef={markOf(id)} size="md" />
        </div>
      ))}
    </div>
  )
}

/** Clockwise from twelve o'clock. */
function angleOf(index: number) {
  return (index / PRODUCTS.length) * 2 * Math.PI - Math.PI / 2
}

function Mark({
  id,
  markRef,
  size,
}: {
  id: string
  markRef: RefObject<HTMLDivElement | null>
  size: "sm" | "md"
}) {
  return (
    <div ref={markRef} className="relative z-10 flex">
      <WireMark tone="logo" size={size}>
        <ProductGlyph id={id} />
      </WireMark>
    </div>
  )
}
