"use client"

import { TextShimmer } from "@/components/ui/text-shimmer"
import { ProductLogo, ProductLogos } from "@/components/product-logo"
import { WireHost } from "@/components/wire/wire"
import { WireFlow, type WireFlowNode, type WireLine } from "@/components/wire/wire-flow"
import { ShieldCheck } from "@/components/icons"
import {
  DRIFT_DOMAINS,
  driftCounts,
  driftWorst,
  type DriftDomain,
  type DriftReport,
  type DriftRow,
} from "@/lib/network/drift"
import { cn } from "@/lib/utils"
import { ShortDigest } from "@/components/network/drift/marks"

/**
 * The line a domain's wire takes from the worst thing in it: red where
 * another owner holds a name this dashboard owns, amber where something
 * differs or is gone, plain where every comparison matches or one could not
 * be made, and dashed where there is nothing to compare. It runs while an
 * inspection is in flight, because that is when something travels down it.
 */
export function domainLine(rows: DriftRow[], inspecting = false): WireLine {
  const worst = driftWorst(rows.filter((row) => row.status !== "not_required"))
  if (!worst) return { state: "idle" }
  if (worst === "conflict") return { state: "danger", moving: inspecting }
  if (worst === "drift" || worst === "missing") return { state: "warning", moving: inspecting }
  return { state: "up", moving: inspecting }
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
 * as the hub, wired out to the files rendered from it, the kernel objects it
 * creates, the boot unit that restores them and the blocklist sets it loads —
 * each drawn as the product that reads it (netfilter's flame for the nftables
 * files and chains, systemd's brackets for the units, Linux for the kernel's
 * own devices, routes and settings, a list as its publisher).
 *
 * It is a `WireFlow`, as every other Network picture is, with nothing on the
 * start side: it had its own grid, a beam per node and a phone layout chosen
 * in script after mount. The wires are the readings the four grey tiles were,
 * said per domain (`domainLine`); pressing a domain narrows the comparisons
 * below to it.
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
  const saved = rows.filter((row) => row.domain === "saved")
  const spec = saved.find((row) => row.kind === "spec")
  const nodes: WireFlowNode[] = driftTargets(rows).map(({ domain, label }) => {
    const own = rows.filter((row) => row.domain === domain)
    const products = domainProducts(own)
    return {
      id: domain,
      mark:
        products.length > 1 ? (
          <ProductLogos ids={products} size="md" />
        ) : (
          <ProductLogo id={products[0]} fallback={ShieldCheck} />
        ),
      title: <span className={cn(selected === domain && "text-brand")}>{label}</span>,
      name: label,
      label: `Show ${label.toLowerCase()} in the comparisons`,
      hint: domainWords(own),
      onPress: () => onDomain(domain),
      line: domainLine(own, inspecting),
    }
  })

  return (
    <WireFlow
      start={{ title: "Saved", nodes: [] }}
      hub={{
        mark: <WireHost />,
        eyebrow: "Saved",
        title: (
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
        ),
        hint: (
          <span className="inline-flex flex-wrap items-center justify-center gap-x-1.5">
            {inspecting ? (
              <TextShimmer>Inspecting the host</TextShimmer>
            ) : (
              <>
                <ShortDigest value={report.savedGeneration} />
                {report.change && <span>· journal {report.change.phase.replaceAll("_", " ")}</span>}
              </>
            )}
          </span>
        ),
      }}
      end={{ title: "Where it is written", nodes, heading: "narrow" }}
      className={NO_START}
    />
  )
}

/**
 * The picture has nothing on its start side. Wide that is an empty column the
 * hub sits after; stacked it would be a "Saved" heading over nothing, over the
 * hub that says "Saved" itself, so the empty column is not drawn there.
 */
const NO_START = "max-lg:[&_section:first-of-type]:hidden"

/** The picture before the first inspection lands, at the shape it will have. */
export function DriftPictureWaiting() {
  return (
    <WireFlow
      loading
      start={{ title: "Saved", nodes: [] }}
      hub={{
        mark: <WireHost />,
        eyebrow: "Saved",
        loadingLines: { title: "spec.json" },
      }}
      end={{
        title: "Where it is written",
        nodes: [],
        heading: "narrow",
        loadingRows: 3,
        loadingLines: { eyebrow: false },
      }}
      className={NO_START}
    />
  )
}

/** The domains the saved configuration is written to; blocklists only where there are some. */
function driftTargets(rows: DriftRow[]) {
  return DRIFT_DOMAINS.filter((d) => d.domain !== "saved").filter(
    (d) => d.domain !== "blocklists" || rows.some((row) => row.domain === "blocklists"),
  )
}
