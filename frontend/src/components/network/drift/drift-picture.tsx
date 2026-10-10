"use client"

import { useRef, type RefObject } from "react"
import { useMediaQuery } from "@/hooks/use-mobile"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { ProductLogo, ProductLogos } from "@/components/product-logo"
import { WireHost, WireNode } from "@/components/deploy/wire"
import { ShieldCheck } from "@/components/icons"
import {
  DRIFT_DOMAINS,
  driftCounts,
  driftWorst,
  type DriftDomain,
  type DriftReport,
  type DriftRow,
} from "@/lib/network-drift"
import { cn } from "@/lib/utils"
import { DriftBlocks, ShortDigest } from "@/components/network/drift/marks"

type Line = {
  dashed?: boolean
  tone: "default" | "success" | "warning" | "danger"
}

/**
 * The colour a domain's wire takes from the worst thing in it: red where
 * another owner holds a name this dashboard owns, amber where something
 * differs or is gone, green where every comparison matches, plain where one
 * could not be made, and dashed where there is nothing to compare.
 */
function domainLine(rows: DriftRow[]): Line {
  const worst = driftWorst(rows.filter((row) => row.status !== "not_required"))
  if (!worst) return { dashed: true, tone: "default" }
  if (worst === "conflict") return { tone: "danger" }
  if (worst === "drift" || worst === "missing") return { tone: "warning" }
  if (worst === "matching") return { tone: "success" }
  return { tone: "default" }
}

function domainWords(rows: DriftRow[]) {
  const compared = rows.filter((row) => row.status !== "not_required")
  if (compared.length === 0) return "nothing to compare"
  const counts = driftCounts(compared)
  if (counts.matching === compared.length)
    return compared.length === 1 ? "matches" : `all ${compared.length} match`
  return (
    <>
      {counts.matching} of {compared.length} match
      {counts.differences > 0 && (
        <span className={counts.conflicts ? "text-destructive" : "text-warning"}>
          {" · "}
          {counts.differences} {counts.differences === 1 ? "differs" : "differ"}
        </span>
      )}
      {counts.unknown > 0 && <> · {counts.unknown} incomplete</>}
    </>
  )
}

/** The same reading in the few words a quarter of a phone's width holds. */
function shortWords(rows: DriftRow[]) {
  const compared = rows.filter((row) => row.status !== "not_required")
  if (compared.length === 0) return "nothing to compare"
  const counts = driftCounts(compared)
  if (counts.differences)
    return (
      <span className={counts.conflicts ? "text-destructive" : "text-warning"}>
        {counts.differences} {counts.differences === 1 ? "differs" : "differ"}
      </span>
    )
  if (counts.unknown) return `${counts.unknown} incomplete`
  return "all match"
}

/** The products a domain's comparisons belong to, most-drawn first and each once. */
function domainProducts(rows: DriftRow[]) {
  const counts = new Map<string, number>()
  for (const row of rows) {
    if (row.product) counts.set(row.product, (counts.get(row.product) ?? 0) + 1)
  }
  return [...counts.keys()].sort((a, b) => (counts.get(b) ?? 0) - (counts.get(a) ?? 0))
}

/**
 * Where the saved configuration is written: the dashboard's own `spec.json`
 * wired out to the files rendered from it, the kernel objects it creates, the
 * boot unit that restores them and the blocklist sets it loads — each drawn
 * as the product that reads it (netfilter's flame for the nftables files and
 * chains, systemd's brackets for the units, Linux for the kernel's own
 * devices, routes and settings, a list as its publisher).
 *
 * The wires are the readings the four grey tiles were, said per domain (see
 * `domainLine`), and every comparison is a block in the domain's strip in the
 * colour of its status, so a page with one changed file shows which domain it
 * is in before a word is read. A pulse runs down every wire while an
 * inspection is in flight, and once more as each one lands, because that is
 * when something has travelled down them (§11). Pressing a domain narrows the
 * comparisons below to it.
 */
export function DriftPicture({
  report,
  rows,
  inspecting,
  selected,
  onDomain,
}: {
  report: DriftReport
  rows: DriftRow[]
  inspecting: boolean
  selected?: DriftDomain
  onDomain: (domain: DriftDomain) => void
}) {
  const wide = useMediaQuery("(min-width: 1024px)")
  const container = useRef<HTMLDivElement>(null)
  const hub = useRef<HTMLDivElement>(null)
  const saved = rows.filter((row) => row.domain === "saved")
  const spec = saved.find((row) => row.kind === "spec")
  const targets = DRIFT_DOMAINS.filter((d) => d.domain !== "saved").filter(
    (d) => d.domain !== "blocklists" || rows.some((row) => row.domain === "blocklists"),
  )

  const nodes = targets.map(({ domain, label }) => (
    <DomainNode
      key={domain}
      domain={domain}
      label={label}
      rows={rows.filter((row) => row.domain === domain)}
      compact={!wide}
      selected={selected === domain}
      containerRef={container}
      hubRef={hub}
      inspecting={inspecting}
      arrival={report.checkedAt}
      onPress={() => onDomain(domain)}
    />
  ))

  const hint = (
    <span className="inline-flex flex-wrap items-center justify-center gap-x-1.5 lg:justify-start">
      {inspecting ? (
        <TextShimmer>Inspecting the host</TextShimmer>
      ) : (
        <>
          <ShortDigest value={report.savedGeneration} />
          {report.change && <span>· journal {report.change.phase.replaceAll("_", " ")}</span>}
        </>
      )}
    </span>
  )
  const title = (
    <button
      type="button"
      onClick={() => onDomain("saved")}
      aria-pressed={selected === "saved"}
      className={cn(
        "rounded-sm focus-ring hover:text-brand",
        selected === "saved" && "text-brand",
        spec && spec.status !== "matching" && "text-warning",
      )}
    >
      spec.json
    </button>
  )

  return (
    <div ref={container} className="relative min-w-0">
      <div aria-hidden className="wire-grid pointer-events-none absolute -inset-x-4 inset-y-0" />
      {wide ? (
        <div className="relative grid min-h-72 grid-cols-[auto_minmax(5rem,0.5fr)_minmax(0,1.6fr)] items-center py-4">
          <WireNode
            nodeRef={hub}
            mark={<WireHost />}
            eyebrow="Saved"
            title={title}
            hint={hint}
            align="start"
            className="lg:flex-col lg:items-start lg:gap-3"
          />
          <div aria-hidden />
          <div role="group" aria-label="Where it is written" className="flex flex-col gap-4">
            {nodes}
          </div>
        </div>
      ) : (
        <div className="relative flex flex-col items-center py-2 text-center">
          <p className="eyebrow">Saved</p>
          <p className="text-body leading-snug font-medium">{title}</p>
          <p className="text-hint leading-snug text-muted-foreground">{hint}</p>
          <div ref={hub} className="relative z-10 mt-4 flex">
            <WireHost />
          </div>
          <div
            role="group"
            aria-label="Where it is written"
            className="mt-10 grid w-full grid-cols-4 gap-x-2"
          >
            {nodes}
          </div>
        </div>
      )}
    </div>
  )
}

function DomainNode({
  domain,
  label,
  rows,
  compact,
  selected,
  containerRef,
  hubRef,
  inspecting,
  arrival,
  onPress,
}: {
  domain: DriftDomain
  label: string
  rows: DriftRow[]
  compact: boolean
  selected: boolean
  containerRef: RefObject<HTMLDivElement | null>
  hubRef: RefObject<HTMLDivElement | null>
  inspecting: boolean
  arrival: string
  onPress: () => void
}) {
  const mark = useRef<HTMLDivElement>(null)
  const line = domainLine(rows)
  const products = domainProducts(rows)
  const drawn =
    products.length > 1 ? (
      <ProductLogos ids={compact ? products.slice(0, 1) : products} size="md" />
    ) : (
      <ProductLogo id={products[0]} fallback={ShieldCheck} />
    )
  const beam = (
    <AnimatedBeam
      // A new inspection re-keys the wire, which sends its one pulse again.
      key={inspecting ? "inspecting" : arrival}
      containerRef={containerRef}
      fromRef={hubRef}
      toRef={mark}
      shape={compact ? "arc" : "s"}
      once={!inspecting}
      dashed={line.dashed}
      tone={line.tone}
      duration={inspecting ? 1.6 : 2.2}
      repeatDelay={0.2}
    />
  )
  const name = (
    <button
      type="button"
      onClick={onPress}
      aria-pressed={selected}
      aria-label={`Show ${label.toLowerCase()} in the comparisons`}
      className={cn("rounded-sm text-left focus-ring hover:text-brand", selected && "text-brand")}
    >
      {label}
    </button>
  )
  if (compact)
    return (
      <div className="flex min-w-0 flex-col items-center gap-2">
        {beam}
        <div ref={mark} className="relative z-10 flex">
          {drawn}
        </div>
        <div className="min-w-0 text-center">
          <div className="text-hint leading-snug font-medium">{name}</div>
          <div className="text-micro leading-snug text-muted-foreground">{shortWords(rows)}</div>
        </div>
      </div>
    )
  return (
    <div className="min-w-0" data-domain={domain}>
      {beam}
      <WireNode
        nodeRef={mark}
        mark={drawn}
        title={name}
        hint={domainWords(rows)}
        aside={<DriftBlocks rows={rows} className="xl:max-w-56 xl:justify-end" />}
      />
    </div>
  )
}
