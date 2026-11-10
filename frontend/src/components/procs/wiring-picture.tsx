"use client"

import { createRef, useMemo, useRef, useState, type RefObject } from "react"
import Link from "next/link"
import { cn } from "@/lib/utils"
import { WireNode } from "@/components/deploy/wire"
import { Section } from "@/components/page"
import { AnimatedBeam } from "@/components/ui/animated-beam"

export type WireTone = "default" | "warning" | "danger"

/** One thing drawn beside the subject, with the line between them. */
export type PictureNode = {
  id: string
  mark: React.ReactNode
  eyebrow: string
  title: React.ReactNode
  hint?: React.ReactNode
  wire: { tone: WireTone; dashed: boolean; carries: boolean }
  /** Where a press on it goes. */
  open?: () => void
  href?: string
  label?: string
}

export type PictureLane = {
  title: string
  nodes: PictureNode[]
  /** Drawn in the lane's place when it has nothing. */
  empty: React.ReactNode
}

/**
 * The subject in the middle with what stands on either side of it, wired:
 * the process page draws what supervises and started a process on the left
 * and what it started and listens on the right, the service page what starts
 * a unit and what it runs. One layout, so a reader who has understood one
 * has understood the other (§ wiring pictures, `deploy/wire`).
 *
 * A node's line pulses while something flows on it (`wire.carries`), is
 * dashed while the subject is not running, and takes the warning or danger
 * hue when the node is what is wrong. Hovering a node dims the others.
 * Narrower than `lg` the lanes stack and no lines are drawn, so each node
 * says in words what it is.
 */
export function WiringPicture({
  title,
  left,
  right,
  center,
}: {
  title: string
  left: PictureLane
  right: PictureLane
  center: {
    mark: React.ReactNode
    eyebrow?: React.ReactNode
    title: React.ReactNode
    hint?: React.ReactNode
  }
}) {
  const container = useRef<HTMLDivElement>(null)
  const [focus, setFocus] = useState<string | null>(null)
  const ids = ["c", ...left.nodes.map((n) => n.id), ...right.nodes.map((n) => n.id)].join("\n")
  const refs = useMemo(() => {
    const map = new Map<string, RefObject<HTMLSpanElement | null>>()
    for (const id of ids.split("\n")) map.set(id, createRef<HTMLSpanElement>())
    return map
  }, [ids])
  const middle = refs.get("c")!
  const watch = (id: string) => ({
    onPointerEnter: () => setFocus(id),
    onPointerLeave: () => setFocus((held) => (held === id ? null : held)),
  })
  const dim = (id: string) => focus !== null && focus !== id && focus !== "c"
  const port = 26

  return (
    <Section title={title}>
      <div role="group" aria-label={title} className="relative animate-rise py-2">
        <div ref={container} className="relative">
          {left.nodes.map((node, index) => (
            <AnimatedBeam
              key={`${node.id}→c`}
              containerRef={container}
              fromRef={refs.get(node.id)!}
              toRef={middle}
              shape="s"
              startXOffset={port}
              endXOffset={-port - 4}
              still={!node.wire.carries}
              dashed={node.wire.dashed}
              tone={node.wire.tone}
              duration={2.4}
              delay={index * 0.35}
              className={cn("transition-opacity max-lg:hidden", dim(node.id) && "opacity-15")}
            />
          ))}
          {right.nodes.map((node, index) => (
            <AnimatedBeam
              key={`c→${node.id}`}
              containerRef={container}
              fromRef={middle}
              toRef={refs.get(node.id)!}
              shape="s"
              startXOffset={port + 4}
              endXOffset={-port}
              still={!node.wire.carries}
              dashed={node.wire.dashed}
              tone={node.wire.tone}
              duration={2.8}
              delay={(index % 6) * 0.35}
              className={cn("transition-opacity max-lg:hidden", dim(node.id) && "opacity-15")}
            />
          ))}
          <div className="relative grid gap-y-8 lg:grid-cols-[minmax(0,1fr)_clamp(2.5rem,5vw,5rem)_minmax(0,0.8fr)_clamp(2.5rem,5vw,5rem)_minmax(0,1fr)] lg:items-center">
            <Lane title={left.title} count={left.nodes.length} className="lg:text-right">
              {left.nodes.length === 0 ? (
                <li>{left.empty}</li>
              ) : (
                left.nodes.map((node) => (
                  <PictureItem
                    key={node.id}
                    node={node}
                    align="end"
                    markRef={refs.get(node.id)!}
                    dimmed={dim(node.id)}
                    {...watch(node.id)}
                  />
                ))
              )}
            </Lane>
            <div aria-hidden className="hidden lg:block" />
            <div className="min-w-0 lg:flex lg:justify-center lg:pb-16">
              <WireNode
                align="center"
                mark={
                  <span ref={middle} className="relative flex" {...watch("c")}>
                    {center.mark}
                  </span>
                }
                eyebrow={center.eyebrow}
                title={center.title}
                hint={center.hint}
              />
            </div>
            <div aria-hidden className="hidden lg:block" />
            <Lane title={right.title} count={right.nodes.length}>
              {right.nodes.length === 0 ? (
                <li>{right.empty}</li>
              ) : (
                right.nodes.map((node) => (
                  <PictureItem
                    key={node.id}
                    node={node}
                    markRef={refs.get(node.id)!}
                    dimmed={dim(node.id)}
                    {...watch(node.id)}
                  />
                ))
              )}
            </Lane>
          </div>
        </div>
      </div>
    </Section>
  )
}

function PictureItem({
  node,
  align,
  markRef,
  dimmed,
  onPointerEnter,
  onPointerLeave,
}: {
  node: PictureNode
  align?: "start" | "end"
  markRef: RefObject<HTMLSpanElement | null>
  dimmed: boolean
  onPointerEnter: () => void
  onPointerLeave: () => void
}) {
  const body = (
    <WireNode
      align={align}
      mark={
        <span ref={markRef} className="flex">
          {node.mark}
        </span>
      }
      eyebrow={node.eyebrow}
      title={node.title}
      hint={node.hint}
    />
  )
  const face =
    "-m-1.5 block w-[calc(100%+0.75rem)] rounded-lg p-1.5 text-left focus-ring-inset transition-colors hover:bg-row-hover"
  return (
    <li
      onPointerEnter={onPointerEnter}
      onPointerLeave={onPointerLeave}
      className={cn("min-w-0 transition-opacity", dimmed && "opacity-40")}
    >
      {node.href ? (
        <Link
          href={node.href}
          aria-label={node.label}
          className={cn(face, align === "end" && "lg:text-right")}
        >
          {body}
        </Link>
      ) : node.open ? (
        <button
          type="button"
          onClick={node.open}
          aria-label={node.label}
          className={cn(face, align === "end" && "lg:text-right")}
        >
          {body}
        </button>
      ) : (
        body
      )}
    </li>
  )
}

function Lane({
  title,
  count,
  className,
  children,
}: {
  title: string
  count: number
  className?: string
  children: React.ReactNode
}) {
  return (
    <div className="min-w-0 self-stretch lg:flex lg:flex-col lg:justify-center">
      <p className={cn("eyebrow mb-4", className)}>
        {title}
        {count > 0 && (
          <span className="numeric ml-1.5 font-medium tracking-normal text-muted-foreground/70">
            {count}
          </span>
        )}
      </p>
      <ol className="flex flex-col gap-5" aria-label={title}>
        {children}
      </ol>
    </div>
  )
}
