"use client"

import { createRef, useRef, type RefObject } from "react"
import { Servers } from "@/components/icons"
import { get } from "@/lib/api"
import type { HostInfo } from "@/lib/types"
import { PURPOSES, type PurposeKey } from "@/lib/sidebar-sections"
import { cn } from "@/lib/utils"
import { useIsMobile } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { ProductGlyph, ProductLogo, platformProduct } from "@/components/product-logo"

/**
 * This server, wired to what it is for: the outcome of the first-run
 * question, drawn on its own screen and at the size of one.
 *
 * The server stands in the middle, named and marked with its distribution,
 * and each purpose chosen is a card around it — the three products it means
 * and its name — with a pulse running out to it. It used to be a picture in a
 * 22rem column beside the cards that decided it, at a size where six purposes
 * were six 44px tiles and a caption each; the answer reads better as the
 * thing the reader arrives at than as a side panel they glance at.
 *
 * Wide, the purposes ring the server on an ellipse; on a phone they sit in two
 * columns under it.
 */
export function ServerPicture({
  purposes,
  className,
}: {
  purposes: readonly PurposeKey[]
  className?: string
}) {
  const host = usePoll((signal) => get<HostInfo>("/system/host", undefined, signal), 0)
  const narrow = useIsMobile()
  const container = useRef<HTMLDivElement>(null)
  const hub = useRef<HTMLDivElement>(null)
  const nodes = useRef<Partial<Record<PurposeKey, RefObject<HTMLDivElement | null>>>>({})
  const nodeOf = (key: PurposeKey) => (nodes.current[key] ??= createRef<HTMLDivElement>())
  const chosen = PURPOSES.filter((purpose) => purposes.includes(purpose.key))
  const platform = platformProduct(host.data?.platform)
  const angles = ring(chosen.length)
  // Two cards either side of the server do not need the width six do.
  const rx = chosen.length <= 2 ? 28 : 36

  const server = (
    <div ref={hub} className="relative z-10 flex flex-col items-center">
      <span className="flex rounded-[1.375rem] border border-rule-brand bg-background p-1">
        <span className="flex size-20 items-center justify-center rounded-[1.125rem] bg-wash-brand text-brand">
          <Servers aria-hidden className="size-9" />
        </span>
      </span>
      {/* Hung off the mark rather than stacked with it, so the mark is the
          point every line leaves from: under it on the ring, where no line
          runs straight down, and over it on a phone, where every line does. */}
      <p
        className={cn(
          "absolute flex items-center gap-1.5 text-title font-semibold whitespace-nowrap",
          narrow ? "bottom-full mb-3" : "top-full mt-3",
        )}
      >
        {platform && <ProductGlyph id={platform} className="size-4" />}
        {host.data?.hostname ?? "This server"}
      </p>
    </div>
  )

  return (
    <div
      ref={container}
      data-slot="server-picture"
      className={cn(
        "relative min-w-0",
        // Capped by the window's height as well, so the command under the
        // picture is on the screen at a laptop's 800px.
        narrow
          ? "flex flex-col items-center gap-14 pt-9"
          : "mx-auto aspect-[2/1] max-h-[52svh] w-full max-w-4xl",
        className,
      )}
    >
      {!narrow && (
        <span
          aria-hidden
          className="absolute inset-y-[16%] rounded-[50%] border border-dashed border-hairline"
          style={{ left: `${50 - rx}%`, right: `${50 - rx}%` }}
        />
      )}
      {/* The lines are the picture's own children: a card rises into place on
          a transform, and a line drawn inside it would be positioned against
          the card rather than against the picture. */}
      {chosen.map((purpose, index) => (
        <AnimatedBeam
          key={`${purpose.key}:${chosen.length}:${narrow}`}
          containerRef={container}
          fromRef={hub}
          toRef={nodeOf(purpose.key)}
          reverse={!narrow && Math.cos(angles[index]) < -0.01}
          pathWidth={2}
          duration={2.8}
          delay={index * 0.3}
        />
      ))}
      {narrow ? (
        server
      ) : (
        <div className="absolute top-1/2 left-1/2 z-10 -translate-x-1/2 -translate-y-1/2">
          {server}
        </div>
      )}
      <ul
        aria-label="What this server is for"
        className={cn(narrow && "grid w-full grid-cols-2 gap-3")}
      >
        {chosen.map((purpose, index) => (
          <li
            key={purpose.key}
            // Positioned either way, so a card paints over the line that ends at it.
            className={cn(
              "relative z-10 animate-rise",
              !narrow && "absolute -translate-x-1/2 -translate-y-1/2",
            )}
            style={
              narrow
                ? undefined
                : {
                    left: `${50 + rx * Math.cos(angles[index])}%`,
                    top: `${50 + 34 * Math.sin(angles[index])}%`,
                  }
            }
          >
            <div
              ref={nodeOf(purpose.key)}
              className="flex min-w-0 flex-col items-center gap-3 rounded-xl border bg-card px-4 py-3.5 text-center sm:px-5"
            >
              <span className="flex gap-1.5">
                {purpose.products.map((product) => (
                  <ProductLogo key={product} id={product} size={narrow ? "sm" : "md"} />
                ))}
              </span>
              <span className="text-body leading-tight font-semibold whitespace-nowrap">
                {purpose.title}
              </span>
            </div>
          </li>
        ))}
      </ul>
    </div>
  )
}

/**
 * Where each card sits around the server, in radians, clockwise from three
 * o'clock. An odd count starts at twelve; an even one is turned half a step,
 * so two sit either side and four on the diagonals. Either way no card is
 * straight below the server, where its name hangs.
 */
function ring(count: number) {
  const step = (2 * Math.PI) / Math.max(count, 1)
  const start = count % 2 === 0 ? -Math.PI / 2 + step / 2 : -Math.PI / 2
  return Array.from({ length: count }, (_, index) => start + index * step)
}
