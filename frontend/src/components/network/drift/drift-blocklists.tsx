"use client"

import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { Status, type DotTone } from "@/components/status-dot"
import { ShieldCheck } from "@/components/icons"
import { plural } from "@/lib/format"
import { blocklistProduct, type DriftReport } from "@/lib/network-drift"
import { cn } from "@/lib/utils"
import { ShortDigest } from "@/components/network/drift/marks"

type List = DriftReport["blocklists"][number]

function enforcementTone(list: List): DotTone {
  if (!list.enabled) return "stopped"
  if (list.enforcement === "verified") return "running"
  if (list.enforcement === "degraded") return "warning"
  return "unknown"
}

/**
 * Each blocklist as the project that publishes it, and the three places its
 * addresses are held, in the order they travel: the downloaded cache, the
 * contents rendered from it, and the kernel's set. A link between two of
 * them is green where they hold the same generation and amber where the
 * later one has fallen behind, so a set the kernel never reloaded is the
 * amber link at the end of the row rather than a digest to compare by eye.
 */
export function DriftBlocklists({ lists }: { lists: List[] }) {
  return (
    <Panel plain aria-label="Blocklists">
      <PanelHeader
        title="Blocklists"
        actions={
          <span className="numeric text-hint text-muted-foreground">
            {plural(lists.filter((list) => list.enabled).length, "enabled list")}
          </span>
        }
      />
      <PanelBody>
        <ul className="divide-y divide-hairline">
          {lists.map((list) => (
            <li key={list.id} className="space-y-3 py-4 first:pt-1">
              <div className="flex min-w-0 items-center gap-3">
                <ProductLogo id={blocklistProduct(list.name)} size="sm" fallback={ShieldCheck} />
                <span className="min-w-0 flex-1 truncate text-body font-medium">{list.name}</span>
                <Status tone={enforcementTone(list)} label={list.enforcement} />
              </div>
              {list.enabled && (
                <div className="flex min-w-0 items-start pl-11">
                  <Stage
                    label="Cache"
                    status={list.cache.status}
                    count={list.cache.count}
                    generation={list.cache.generation}
                  />
                  <Link same={list.renderedGeneration === list.cache.generation} />
                  <Stage
                    label="Rendered"
                    generation={list.renderedGeneration}
                    against={list.cache.generation}
                  />
                  <Link same={list.runtime.generation === list.renderedGeneration} />
                  <Stage
                    label="Kernel set"
                    status={list.runtime.status}
                    count={list.runtime.count ?? undefined}
                    generation={list.runtime.generation}
                    against={list.renderedGeneration}
                  />
                </div>
              )}
              {(list.cache.error || list.runtime.error) && (
                <p className="pl-11 text-hint text-warning">
                  {list.cache.error ?? list.runtime.error}
                </p>
              )}
            </li>
          ))}
        </ul>
      </PanelBody>
    </Panel>
  )
}

function Stage({
  label,
  status,
  count,
  generation,
  against,
}: {
  label: string
  status?: string
  count?: number
  generation?: string
  against?: string
}) {
  return (
    <div className="min-w-0 shrink-0 space-y-0.5">
      <p className="text-micro font-medium tracking-[0.06em] text-muted-foreground uppercase">
        {label}
      </p>
      <ShortDigest value={generation || undefined} against={against} />
      <p className="numeric text-micro text-muted-foreground">
        {count !== undefined ? `${count.toLocaleString()} entries` : (status ?? " ")}
      </p>
    </div>
  )
}

function Link({ same }: { same: boolean }) {
  return (
    <span
      aria-hidden
      className={cn(
        "mx-2 mt-6 h-0.5 min-w-4 flex-1 rounded-full",
        same ? "bg-success/70" : "bg-warning",
      )}
    />
  )
}
