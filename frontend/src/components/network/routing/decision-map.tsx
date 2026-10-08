"use client"

import { createRef, useMemo, useRef, useState, type RefObject } from "react"
import { Route as RouteGlyph, StopCircle } from "@/components/icons"
import type { NetworkRouting, NetworkRule } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductGlyph } from "@/components/product-logo"
import { WireMark, WireNode } from "@/components/deploy/wire"
import { ChipStrip, FilterChip } from "@/components/tabs"
import {
  addressFamily,
  decisionRules,
  decisionTables,
  inferredAnswer,
  type RouteFamily,
} from "./decision-reading"
import { RouteLookup } from "./route-lookup"
import { AnimatedBeam } from "@/components/ui/animated-beam"

/**
 * How the kernel picks a route, drawn as the decision it is: the policy rules
 * on the left in the order they are asked — priority first — and the tables
 * they send a packet to on the right, each with how many routes it holds.
 *
 * An inferred rule and table for this browser's replies carry a moving wire.
 * The separate target lookup asks the kernel; the diagram does not implement
 * every policy selector. A rule that discards (blackhole, prohibit,
 * unreachable) ends in a mark of its own rather than a table. A rule the
 * dashboard made has its priority in the brand's blue, the colour of where
 * the reader is (§3); Tailscale's take its logo.
 */
export function DecisionMap({ routing }: { routing: NetworkRouting }) {
  const container = useRef<HTMLDivElement>(null)
  const [focus, setFocus] = useState<string | null>(null)
  const [family, setFamily] = useState<RouteFamily>(() => addressFamily(routing.clientPath.address))
  const rules = useMemo(() => decisionRules(routing, family), [routing, family])
  const tables = useMemo(() => decisionTables(routing, rules, family), [routing, rules, family])
  const answering = inferredAnswer(routing, family)

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
      <div className="relative mb-5 space-y-2">
        <ChipStrip aria-label="Route diagram family">
          <FilterChip
            selected={family === "inet"}
            onClick={() => {
              setFamily("inet")
              setFocus(null)
            }}
          >
            IPv4
          </FilterChip>
          <FilterChip
            selected={family === "inet6"}
            onClick={() => {
              setFamily("inet6")
              setFocus(null)
            }}
          >
            IPv6
          </FilterChip>
        </ChipStrip>
        <p className="text-hint text-muted-foreground">
          Highlights infer which rule may answer your replies; they do not evaluate every policy
          selector.
        </p>
        <p className="text-hint text-muted-foreground">
          Browser path ({addressFamily(routing.clientPath.address) === "inet6" ? "IPv6" : "IPv4"}):{" "}
          <span className="inline-block max-w-full font-mono break-all">
            {routing.clientPath.address}
          </span>
          {routing.clientPath.device && (
            <>
              {" "}
              · via{" "}
              <span className="inline-block max-w-full font-mono break-all">
                {routing.clientPath.device}
              </span>
            </>
          )}
          {routing.clientPath.source && (
            <>
              {" "}
              · source{" "}
              <span className="inline-block max-w-full font-mono break-all">
                {routing.clientPath.source}
              </span>
            </>
          )}
          {routing.clientPath.gateway && (
            <>
              {" "}
              · gateway{" "}
              <span className="inline-block max-w-full font-mono break-all">
                {routing.clientPath.gateway}
              </span>
            </>
          )}
        </p>
      </div>
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
              {rules.length === 0 && (
                <li className="text-body text-muted-foreground">
                  No visible {family === "inet6" ? "IPv6" : "IPv4"} policy rules.
                </li>
              )}
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
                          {carries ? " · your replies (inferred)" : ""}
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
                          {answers ? " · answers you (inferred)" : ""}
                        </span>
                      }
                      title={table.name}
                      hint={`${table.routes.length} route${table.routes.length === 1 ? "" : "s"}${defaultOf(table.routes, family)}`}
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
      <RouteLookup />
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

function defaultOf(routes: NetworkRouting["tables"][number]["routes"], family: RouteFamily) {
  const d = routes.find((r) => r.destination === "default" && r.family === family)
  if (!d) return ""
  return ` · default via ${d.gateway ?? d.device ?? "—"}`
}
