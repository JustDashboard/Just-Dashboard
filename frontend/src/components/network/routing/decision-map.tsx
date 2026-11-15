"use client"

import { createRef, useMemo, useRef, useState, type RefObject } from "react"
import { Route as RouteGlyph, StopCircle } from "@/components/icons"
import type { NetworkRouting } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductGlyph } from "@/components/product-logo"
import { WireMark, WireNode } from "@/components/deploy/wire"
import { ChipStrip, FilterChip } from "@/components/tabs"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { addressFamily, decisionRules, decisionTables, type RouteFamily } from "./decision-reading"
import { parseTarget, replyHighlight, ruleSelectors, ruleThen } from "./route-reading"
import { AnswerLine, TargetField, type RouteLookup, type RouteQuestion } from "./route-lookup"

/**
 * How the kernel picks a route, drawn as the decision it is: the policy rules
 * on the left in the order they are asked — priority first — and the tables
 * they send a packet to on the right, each with how many routes it holds.
 * Two columns wired to each other, which is not the shape `WireFlow` draws
 * (things through one hub), so it is laid by hand on `deploy/wire`.
 *
 * Its head is the question: the family, and the address a packet is going
 * to. With nothing asked, the rule and table behind this browser's replies
 * carry the moving wire. The table is the one the kernel reports answering;
 * the rule is the one the server's model evaluated to that table, named only
 * when model and kernel agree, and without that answer the older inference
 * from routes is drawn and labelled as such. With an address asked about, the
 * table the kernel names for it is lit instead, and the rule only where one
 * rule alone leads to that table. A rule that discards (blackhole, prohibit,
 * unreachable) ends in a mark of its own rather than a table. A rule the
 * dashboard made has its priority in the brand's blue, the colour of where
 * the reader is (§3); Tailscale's take its logo.
 */
export function DecisionMap({
  routing,
  question,
  onQuestion,
  lookup,
}: {
  routing: NetworkRouting
  question: RouteQuestion
  onQuestion: (next: RouteQuestion) => void
  lookup: RouteLookup
}) {
  const container = useRef<HTMLDivElement>(null)
  const [focus, setFocus] = useState<string | null>(null)
  const asked = question.target.trim()
  // The picture follows the family of what is asked about, and of this
  // browser with nothing asked; a chip pressed holds until the question changes.
  const [picked, setPicked] = useState<{ family: RouteFamily; asked: string }>()
  const family =
    picked?.asked === asked
      ? picked.family
      : (parseTarget(asked)?.family ?? addressFamily(routing.clientPath.address))
  const rules = useMemo(() => decisionRules(routing, family), [routing, family])
  const tables = useMemo(() => decisionTables(routing, rules, family), [routing, rules, family])

  const answer = lookup.state === "answer" && lookup.result.family === family ? lookup : undefined
  const reply = asked ? undefined : replyHighlight(routing, family)
  // Only a rule can send a packet to a table, so where one rule alone looks
  // in the table the kernel named, that rule is the one that matched.
  const towards = answer
    ? rules.filter((r) => r.action === "lookup" && r.table === answer.result.table)
    : []
  const answering = answer
    ? { rule: towards.length === 1 ? towards[0] : undefined, table: answer.result.table }
    : reply
      ? { rule: rules.find((r) => r.priority === reply.rulePriority), table: reply.table }
      : undefined
  const ruleLabel = answer
    ? "the one rule that looks there"
    : reply?.basis === "kernel"
      ? "your replies (evaluated)"
      : "your replies (inferred)"
  const tableLabel = answer
    ? `answers ${answer.target} (kernel)`
    : reply?.basis === "inferred"
      ? "answers you (inferred)"
      : "answers you (kernel)"

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
  const pick = (next: RouteFamily) => {
    setPicked({ family: next, asked })
    setFocus(null)
  }

  return (
    <section aria-label="How a packet is routed" data-slot="decision-map" className="min-w-0">
      <div className="mb-3 flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-3">
        <ChipStrip aria-label="Route diagram family">
          <FilterChip selected={family === "inet"} onClick={() => pick("inet")}>
            IPv4
          </FilterChip>
          <FilterChip selected={family === "inet6"} onClick={() => pick("inet6")}>
            IPv6
          </FilterChip>
        </ChipStrip>
        <TargetField question={question} onChange={onQuestion} className="max-sm:w-full" />
      </div>
      <AnswerLine routing={routing} question={question} lookup={lookup} family={family} />
      <div ref={container} className="relative mt-6 pb-2">
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
          const carries = answering?.rule !== undefined && answering.rule === rule
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
        <div className={GRID}>
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
                const carries = answering?.rule !== undefined && answering.rule === rule
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
                          {carries ? ` · ${ruleLabel}` : ""}
                        </span>
                      }
                      title={
                        <span className="font-mono text-xs">
                          {ruleSelectors(rule).join(" ") || "everything"}
                        </span>
                      }
                      hint={`then ${ruleThen(rule)}`}
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
                          {answers ? ` · ${tableLabel}` : ""}
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
    </section>
  )
}

const GRID =
  "relative mx-auto grid max-w-4xl gap-y-8 lg:grid-cols-[minmax(0,1.1fr)_clamp(3rem,10vw,9rem)_minmax(0,1fr)] lg:items-center"

/**
 * The picture before the first read: its head and as many rules and tables as
 * it drew last time, grey, each on the lines a node's words stand on, so the
 * tables under it are where they will be when the read lands.
 */
export function DecisionMapWaiting({ rules, tables }: { rules: number; tables: number }) {
  return (
    <section aria-label="How a packet is routed" aria-busy data-slot="decision-map">
      <div className="mb-3 flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-3">
        <div className="flex items-center gap-1.5">
          <Bar className="h-7 w-12 rounded-md" />
          <Bar className="h-7 w-12 rounded-md" />
        </div>
        <Bar className="h-10 w-full rounded-md sm:h-8 sm:w-[22.375rem]" />
      </div>
      <p className="flex min-h-10 items-start text-xs sm:min-h-5">
        <Bar className="mt-1 h-2.5 w-72 max-w-full" />
      </p>
      <div className="relative mt-6 pb-2">
        <div className={GRID}>
          <div className="min-w-0">
            <p className="eyebrow mb-4">Asked in this order</p>
            <ol className="flex flex-col gap-4">
              {Array.from({ length: rules }, (_, i) => (
                <li key={i}>
                  <WireNode align="end" mark={<Ghost size="sm" />} {...WORDS} />
                </li>
              ))}
            </ol>
          </div>
          <div aria-hidden className="max-lg:hidden" />
          <div className="min-w-0">
            <p className="eyebrow mb-4">Tables</p>
            <ol className="flex flex-col gap-5">
              {Array.from({ length: tables }, (_, i) => (
                <li key={i}>
                  <WireNode mark={<Ghost size="md" />} {...WORDS} />
                </li>
              ))}
            </ol>
          </div>
        </div>
      </div>
    </section>
  )
}

/** A grey bar inside a line of text: a span, so it may stand in a paragraph. */
function Bar({ className }: { className?: string }) {
  return (
    <span
      data-slot="skeleton"
      className={cn("inline-block animate-pulse rounded-sm bg-muted align-middle", className)}
    />
  )
}

function Ghost({ size }: { size: "sm" | "md" }) {
  return (
    <span
      data-slot="skeleton"
      className={cn(
        "block animate-pulse rounded-xl bg-muted",
        size === "sm" ? "size-9" : "size-11",
      )}
    />
  )
}

// A node's three lines, each a bar on the line the words will take.
const WORDS = {
  eyebrow: <Bar className="h-2 w-16" />,
  title: <Bar className="h-2.5 w-36" />,
  hint: <Bar className="h-2 w-24" />,
}

/** A priority short enough for a mark: 32766 is "32k". */
function short(priority: number) {
  return priority >= 10000 ? `${Math.round(priority / 1000)}k` : String(priority)
}

function defaultOf(routes: NetworkRouting["tables"][number]["routes"], family: RouteFamily) {
  const d = routes.find((r) => r.destination === "default" && r.family === family)
  if (!d) return ""
  return ` · default via ${d.gateway ?? d.device ?? "—"}`
}
