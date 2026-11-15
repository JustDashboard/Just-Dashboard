"use client"

import Link from "next/link"
import {
  ArrowLeftRight,
  Bridge,
  Connection,
  DotMark,
  FirewallCheck,
  Globe,
  Hub,
  ListOrdered,
  Location,
  NetworkDevice,
  Route,
  Router,
  Shield,
  type Icon,
} from "@/components/icons"
import { cn } from "@/lib/utils"
import { WireMark, WireNode } from "@/components/deploy/wire"
import { ProductGlyph } from "@/components/product-logo"
import { evidenceCounts } from "@/lib/network-investigator"
import type { PathEvidence, PathResult } from "@/lib/network-investigator-types"

type Basis = PathEvidence["basis"]

/**
 * The four kinds of evidence a hop can rest on, each with its own colour so
 * the chain says at a glance how much of it was seen, predicted, measured or
 * not known. Unknown has no hue: it is the absence of one. None of them is
 * the green of success: a measurement is green's colour only if it worked,
 * and a refused connection is as much a measurement as an accepted one.
 */
const KIND: Record<Basis, { word: string; hue?: string }> = {
  observed: { word: "Snapshot", hue: "var(--chart-2)" },
  modeled: { word: "Model", hue: "var(--chart-4)" },
  measured: { word: "Measured", hue: "var(--chart-3)" },
  unknown: { word: "Unknown" },
}
const KINDS: Basis[] = ["observed", "modeled", "measured", "unknown"]

// Each layer wears the glyph of the page that owns it, and a product's own
// logo where one product is the owner.
const GLYPH: Record<string, Icon> = {
  dns: Globe,
  rules: ListOrdered,
  route: Route,
  firewall: FirewallCheck,
  "docker-user": FirewallCheck,
  nat: ArrowLeftRight,
  tunnel: NetworkDevice,
  proxy: Hub,
  owner: Router,
  listener: Router,
  probe: Connection,
  gateway: Bridge,
  provider: Shield,
  external: Location,
}
const PRODUCT: Record<string, string> = {
  dnat: "docker",
  publication: "docker",
  stream: "nginx",
  stream_traversal: "nginx",
}

// Runs saved before the investigator named the Ports page by its address
// still carry the old one, which is not a page.
const ownerHref = (path: string) => (path === "/ports" ? "/proxy/ports" : path)

/**
 * How one connection would travel, as a chain of wired hops: DNS, the policy
 * rules, the kernel route, the firewall, NAT, the interface, a proxy, the
 * owner at the far end and, when asked for, one measured connection. Each hop
 * is a mark in the colour of what it rests on — a snapshot read now, the
 * model's prediction, a measurement, or nothing known — so a path that is
 * mostly predicted never reads as a path that was walked. The line between two
 * hops is dashed where either end is unknown.
 *
 * It was a row of four tiles counting those kinds and a numbered list; the
 * counts are now the key to the colours, and the list is the chain.
 */
export function PathReport({ result }: { result: PathResult }) {
  const counts = evidenceCounts(result.evidence)
  const scope = result.scope
  return (
    <section aria-label="Connection path report" className="min-w-0 space-y-6">
      <header className="min-w-0 space-y-1.5">
        <h2 className="text-title font-medium break-words">
          {scope.source} → {scope.target}
        </h2>
        <p className="font-mono text-hint break-all text-muted-foreground">
          {scope.family === "inet" ? "IPv4" : "IPv6"} · {scope.protocol.toUpperCase()} /{" "}
          {scope.port} ·{" "}
          {scope.sourceAddress ||
            (scope.vantage === "published_port" ? "any outside address" : "source unknown")}{" "}
          → {scope.address || "destination unresolved"}
          {scope.mark && ` · mark ${scope.mark}`}
        </p>
        <p className="text-body">{result.comparison}</p>
        <p className="text-hint text-muted-foreground">
          Collected{" "}
          <time dateTime={result.endedAt}>{new Date(result.endedAt).toLocaleString()}</time>
        </p>
      </header>

      <ul aria-label="Evidence kinds" className="flex flex-wrap gap-x-5 gap-y-1.5 text-hint">
        {KINDS.map((basis) => (
          <li key={basis} className="flex items-center gap-1.5">
            <span
              aria-hidden
              className={cn(
                "size-2 rounded-full",
                !KIND[basis].hue && "border border-border-strong bg-control",
              )}
              style={KIND[basis].hue ? { background: KIND[basis].hue } : undefined}
            />
            <span>{KIND[basis].word}</span>{" "}
            <span className="numeric text-muted-foreground">{counts[basis]}</span>
          </li>
        ))}
      </ul>

      <ol aria-label="Hops" className="min-w-0">
        {result.evidence.map((item, index) => {
          const next = result.evidence[index + 1]
          return (
            <Hop
              key={item.id}
              item={item}
              line={
                next
                  ? item.basis === "unknown" || next.basis === "unknown"
                    ? "dashed"
                    : "solid"
                  : undefined
              }
            />
          )
        })}
      </ol>

      {scope.limitations.length > 0 && (
        <div className="min-w-0 space-y-2 border-t border-hairline pt-4">
          <h3 className="text-body font-medium">Scope and remaining unknowns</h3>
          <ul className="list-disc space-y-1 pl-4 text-hint text-muted-foreground">
            {scope.limitations.map((limit) => (
              <li key={limit}>{limit}</li>
            ))}
          </ul>
        </div>
      )}
    </section>
  )
}

function Hop({ item, line }: { item: PathEvidence; line?: "solid" | "dashed" }) {
  const kind = KIND[item.basis]
  const Glyph = GLYPH[item.id] ?? DotMark
  const product = PRODUCT[item.id]
  const state = item.state.replaceAll("_", " ")
  const limitations = item.limitations.filter(Boolean)
  return (
    <li
      id={`evidence-${item.id}`}
      data-basis={item.basis}
      className="relative min-w-0 scroll-mt-6 pb-7 last:pb-0"
    >
      {/* The wire to the next hop: from under this mark to the top of the
          next one, on the marks' own centre line. */}
      {line && (
        <span
          aria-hidden
          data-slot="hop-wire"
          data-line={line}
          className={cn(
            "absolute top-11 bottom-0 left-[1.375rem] -ml-px w-0 border-l-2 border-border-strong",
            line === "dashed" && "border-dashed",
          )}
        />
      )}
      <WireNode
        mark={
          <span data-slot="hop-mark" className="flex">
            {/* The wire stops at the mark, so it needs no ground of its own
                to hide one, and the report is drawn on a pane, a sheet and a
                page alike. */}
            <WireMark size="md" hue={kind.hue} className="bg-transparent">
              {product ? (
                <ProductGlyph id={product} />
              ) : (
                <Glyph aria-hidden className={cn(!kind.hue && "text-muted-foreground")} />
              )}
            </WireMark>
          </span>
        }
        eyebrow={
          <>
            <span style={kind.hue ? { color: kind.hue } : undefined}>{kind.word}</span>
            {state !== item.basis && state !== kind.word.toLowerCase() && ` · ${state}`}
          </>
        }
        title={<h3 className="text-body font-medium">{item.title}</h3>}
      />
      <div className="mt-1.5 min-w-0 space-y-2 pl-[3.625rem]">
        <p className="text-body">
          {item.summary || "This owner did not provide evidence for this layer."}
        </p>
        <p className="text-hint text-muted-foreground">
          {item.owner} · {item.scope}
          {item.ownerPath && (
            <>
              {" · "}
              <Link
                href={ownerHref(item.ownerPath)}
                className="text-foreground underline underline-offset-4"
              >
                Open owner
              </Link>
            </>
          )}
        </p>
        {item.facts.length > 0 && (
          <dl className="grid gap-x-6 gap-y-1.5 sm:grid-cols-2">
            {item.facts.map((fact, index) => (
              <div key={`${fact.label}:${index}`} className="min-w-0">
                <dt className="text-hint text-muted-foreground">{fact.label}</dt>
                <dd className="text-body break-all">{fact.value || "Unknown"}</dd>
              </div>
            ))}
          </dl>
        )}
        {limitations.length > 0 && (
          <ul className="list-disc space-y-1 pl-4 text-hint text-muted-foreground">
            {limitations.map((limit, index) => (
              <li key={index}>{limit}</li>
            ))}
          </ul>
        )}
      </div>
    </li>
  )
}
