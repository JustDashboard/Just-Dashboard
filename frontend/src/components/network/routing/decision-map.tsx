"use client"

import { createRef, useMemo, useRef, useState, type RefObject } from "react"
import { Route as RouteGlyph, StopCircle } from "@/components/icons"
import type { NetworkRouting, NetworkRule } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductGlyph } from "@/components/product-logo"
import { WireMark, WireNode } from "@/components/deploy/wire"
import { AnimatedBeam } from "@/components/ui/animated-beam"

/** The rules the kernel writes for itself, which every host has and nobody reads twice. */
const QUIET = new Set([0])

/**
 * How the kernel picks a route, drawn as the decision it is: the policy rules
 * on the left in the order they are asked — priority first — and the tables
 * they send a packet to on the right, each with how many routes it holds.
 *
 * The rule and table that answer this browser's replies carry a moving wire,
 * so "why does my traffic leave through tailscale0" is answered by following
 * the one line that moves. A rule that discards (blackhole, prohibit,
 * unreachable) ends in a mark of its own rather than a table. A rule the
 * dashboard made has its priority in the brand's blue, the colour of where
 * the reader is (§3); Tailscale's take its logo.
 */
export function DecisionMap({ routing }: { routing: NetworkRouting }) {
  const container = useRef<HTMLDivElement>(null)
  const [focus, setFocus] = useState<string | null>(null)
  const rules = routing.rules.filter((r) => r.family === "inet" && !QUIET.has(r.priority))
  // Every table a rule sends to, in the order the rules first name them; one
  // that holds nothing (the `default` table, usually) is still drawn, empty,
  // because a rule pointing at it is still asked.
  const tables = useMemo(() => {
    const out: NetworkRouting["tables"] = []
    for (const r of rules) {
      if (r.action !== "lookup" || r.table === undefined || out.some((t) => t.id === r.table))
        continue
      out.push(
        routing.tables.find((t) => t.id === r.table) ?? {
          id: r.table,
          name: r.tableName ?? String(r.table),
          routes: [],
        },
      )
    }
    return out
  }, [routing.tables, rules])
  const answering = answeringTable(routing)

  const ids = [
    ...rules.map((r) => `r:${r.priority}:${r.id}`),
    ...tables.map((t) => `t:${t.id}`),
    "drop",
  ].join("\n")
  const refs = useMemo(() => {
    const map = new Map<string, RefObject<HTMLDivElement | null>>()
    for (const id of ids.split("\n")) if (id) map.set(id, createRef<HTMLDivElement>())
    return map
  }, [ids])
  const watch = (id: string) => ({
    onPointerEnter: () => setFocus(id),
    onPointerLeave: () => setFocus((held) => (held === id ? null : held)),
  })
  const discards = rules.some((r) => r.action !== "lookup" && r.action !== "goto")
  const port = 22

  return (
    <div className="relative animate-rise py-4">
      <div aria-hidden className="wire-grid pointer-events-none absolute -inset-x-4 inset-y-0" />
      <div ref={container} className="relative">
        {rules.map((rule, index) => {
          const from = refs.get(`r:${rule.priority}:${rule.id}`)
          const to =
            rule.action === "lookup" && rule.table !== undefined
              ? refs.get(`t:${rule.table}`)
              : rule.action === "goto"
                ? undefined
                : refs.get("drop")
          if (!from || !to) return null
          const lit = focus === null || focus === `r:${rule.priority}:${rule.id}`
          const carries = answering?.rule === rule
          return (
            <AnimatedBeam
              key={`${rule.priority}:${rule.id}`}
              containerRef={container}
              fromRef={from}
              toRef={to}
              shape="s"
              startXOffset={port}
              endXOffset={-port}
              still={!carries}
              tone={rule.action === "lookup" ? "default" : "danger"}
              duration={2.6}
              delay={index * 0.2}
              className={cn("transition-opacity max-lg:hidden", !lit && "opacity-15")}
            />
          )
        })}
        <div className="relative mx-auto grid max-w-4xl gap-y-8 lg:grid-cols-[minmax(0,1.1fr)_clamp(3rem,10vw,9rem)_minmax(0,1fr)] lg:items-center">
          <section aria-label="Policy rules, in the order they are asked" className="min-w-0">
            <p className="eyebrow mb-4">Asked in this order</p>
            <ol className="flex flex-col gap-4">
              {rules.map((rule) => {
                const id = `r:${rule.priority}:${rule.id}`
                const carries = answering?.rule === rule
                return (
                  <li
                    key={id}
                    {...watch(id)}
                    className={cn(
                      "min-w-0 transition-opacity",
                      focus !== null && focus !== id && "opacity-40",
                    )}
                  >
                    <WireNode
                      nodeRef={refs.get(id)}
                      align="end"
                      mark={
                        <WireMark tone="logo" shape="square" size="sm">
                          {rule.owner === "tailscale" ? (
                            <ProductGlyph id="tailscale" />
                          ) : (
                            <span
                              className={cn(
                                "numeric font-mono text-micro",
                                rule.managed && "text-brand",
                              )}
                            >
                              {short(rule.priority)}
                            </span>
                          )}
                        </WireMark>
                      }
                      eyebrow={
                        <span className={cn(carries && "text-brand")}>
                          {rule.priority}
                          {carries ? " · your replies" : ""}
                        </span>
                      }
                      title={<span className="font-mono text-xs">{selector(rule)}</span>}
                      hint={
                        rule.action === "lookup"
                          ? `then ${rule.tableName ?? rule.table}`
                          : rule.action === "goto"
                            ? "jump further down"
                            : rule.action
                      }
                    />
                  </li>
                )
              })}
            </ol>
          </section>
          <div aria-hidden className="max-lg:hidden" />
          <section aria-label="Routing tables" className="min-w-0">
            <p className="eyebrow mb-4">Tables</p>
            <ol className="flex flex-col gap-5">
              {tables.map((table) => {
                const id = `t:${table.id}`
                const answers = answering?.table === table.id
                const tailnet = table.id === 52
                return (
                  <li key={id} className="min-w-0">
                    <WireNode
                      nodeRef={refs.get(id)}
                      mark={
                        <WireMark tone="logo" shape="square" size="md">
                          {tailnet ? <ProductGlyph id="tailscale" /> : <RouteGlyph aria-hidden />}
                        </WireMark>
                      }
                      eyebrow={
                        <span className={cn(answers && "text-brand")}>
                          table {table.id}
                          {answers ? " · answers you" : ""}
                        </span>
                      }
                      title={table.name}
                      hint={`${table.routes.length} route${table.routes.length === 1 ? "" : "s"}${defaultOf(table.routes)}`}
                    />
                  </li>
                )
              })}
              {discards && (
                <li className="min-w-0">
                  <WireNode
                    nodeRef={refs.get("drop")}
                    mark={
                      <WireMark tone="danger" shape="square" size="md">
                        <StopCircle aria-hidden />
                      </WireMark>
                    }
                    eyebrow="Discarded"
                    title="Nowhere"
                    hint="blackhole, prohibit or unreachable"
                  />
                </li>
              )}
            </ol>
          </section>
        </div>
      </div>
    </div>
  )
}

/** A priority short enough for a mark: 32766 is "32k". */
function short(priority: number) {
  return priority >= 10000 ? `${Math.round(priority / 1000)}k` : String(priority)
}

/** What a rule matches, as `ip rule` would say it. */
function selector(rule: NetworkRule) {
  const parts: string[] = []
  if (rule.from) parts.push(`from ${rule.from}`)
  if (rule.to) parts.push(`to ${rule.to}`)
  if (rule.iif) parts.push(`iif ${rule.iif}`)
  if (rule.oif) parts.push(`oif ${rule.oif}`)
  if (rule.fwmark) parts.push(`fwmark ${rule.fwmark}`)
  return parts.length ? parts.join(" ") : "everything"
}

function defaultOf(routes: NetworkRouting["tables"][number]["routes"]) {
  const d = routes.find((r) => r.destination === "default" && r.family === "inet")
  if (!d) return ""
  return ` · default via ${d.gateway ?? d.device ?? "—"}`
}

/**
 * The rule and table that answer the browser's replies: the first rule with
 * no selector beyond the ones a reply cannot match that leads to a table
 * holding a route through the client path's device. A best reading of the
 * kernel's choice, drawn rather than enforced — the guard asks the kernel
 * itself.
 */
function answeringTable(routing: NetworkRouting): { rule: NetworkRule; table: number } | undefined {
  const device = routing.clientPath.device
  if (!device) return undefined
  for (const rule of routing.rules) {
    if (rule.family !== "inet" || rule.action !== "lookup" || rule.table === undefined) continue
    if (rule.fwmark || rule.iif || rule.oif) continue
    if (rule.to && rule.to !== routing.clientPath.address) continue
    if (rule.from && rule.from !== routing.clientPath.source) continue
    const table = routing.tables.find((t) => t.id === rule.table)
    if (table?.routes.some((r) => r.device === device)) return { rule, table: table.id }
  }
  return undefined
}
