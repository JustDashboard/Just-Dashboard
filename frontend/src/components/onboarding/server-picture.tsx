"use client"

import { createRef, useRef, type RefObject } from "react"
import { Plus } from "@/components/icons"
import { get } from "@/lib/api"
import type { HostInfo } from "@/lib/types"
import { PURPOSES, type PurposeKey } from "@/lib/sidebar-sections"
import { cn } from "@/lib/utils"
import { usePoll } from "@/hooks/use-poll"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { WireHost, WireMark, WirePlaceholder } from "@/components/deploy/wire"
import { ProductGlyph, platformProduct } from "@/components/product-logo"

/**
 * This server, wired to what it is for: the rack every wiring picture draws
 * for the machine, named and marked with its distribution, and a line with a
 * pulse running down it to each purpose chosen, drawn as the product it
 * mostly is. It answers the cards beside it as they are pressed, so the first
 * question has something on the screen that changes besides a tick.
 *
 * Nothing chosen is the dashed ring of a thing not added yet, as on every
 * picture with a place still empty.
 */
export function ServerPicture({
  purposes,
  className,
}: {
  purposes: readonly PurposeKey[]
  className?: string
}) {
  const host = usePoll((signal) => get<HostInfo>("/system/host", undefined, signal), 0)
  const container = useRef<HTMLDivElement>(null)
  const hub = useRef<HTMLDivElement>(null)
  const pending = useRef<HTMLDivElement>(null)
  const marks = useRef<Partial<Record<PurposeKey, RefObject<HTMLDivElement | null>>>>({})
  const markOf = (key: PurposeKey) => (marks.current[key] ??= createRef<HTMLDivElement>())
  const chosen = PURPOSES.filter((purpose) => purposes.includes(purpose.key))
  const platform = platformProduct(host.data?.platform)
  // The row is centred, so a mark leaving the middle moves the ones after it;
  // every line is drawn afresh for each set rather than to where a mark was.
  const layout = chosen.map((purpose) => purpose.key).join()

  return (
    <div data-slot="server-picture" className={cn("relative min-w-0 py-6", className)}>
      <div aria-hidden className="wire-grid pointer-events-none absolute inset-0" />
      <div ref={container} className="relative flex flex-col items-center gap-12">
        {/* The lines are the container's own children: a mark's row rises
            into place on a transform, and a line drawn inside it would be
            positioned against the row rather than against the picture. */}
        {chosen.length === 0 ? (
          <AnimatedBeam containerRef={container} fromRef={hub} toRef={pending} still dashed />
        ) : (
          chosen.map((purpose, index) => (
            <AnimatedBeam
              key={`${purpose.key}:${layout}`}
              containerRef={container}
              fromRef={hub}
              toRef={markOf(purpose.key)}
              duration={2.4}
              delay={index * 0.35}
            />
          ))
        )}
        <div className="flex flex-col items-center gap-2.5">
          <div ref={hub} className="relative z-10 flex">
            <WireHost />
          </div>
          <p className="flex items-center gap-1.5 text-body font-medium">
            {platform && <ProductGlyph id={platform} />}
            {host.data?.hostname ?? "This server"}
          </p>
        </div>
        <ul
          aria-label="What this server is for"
          className="flex max-w-[20rem] flex-wrap justify-center gap-x-5 gap-y-6"
        >
          {chosen.length === 0 ? (
            <li className="flex animate-rise flex-col items-center">
              <div ref={pending} className="relative z-10 flex">
                <WirePlaceholder size="md" fallback={Plus} />
              </div>
            </li>
          ) : (
            chosen.map((purpose) => (
              <li
                key={purpose.key}
                className="flex w-20 animate-rise flex-col items-center gap-2 text-center"
              >
                <div ref={markOf(purpose.key)} className="relative z-10 flex">
                  <WireMark tone="logo" size="md">
                    <ProductGlyph id={purpose.products[0]} />
                  </WireMark>
                </div>
                <span className="text-hint leading-tight">{purpose.title}</span>
              </li>
            ))
          )}
        </ul>
      </div>
    </div>
  )
}
