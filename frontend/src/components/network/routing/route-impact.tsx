"use client"

import type { NetworkImpactItem, NetworkRouteImpact } from "@/lib/types"
import { plural } from "@/lib/format"
import { Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Warning } from "@/components/icons"

const KIND_WORD: Record<string, string> = {
  client: "your connection",
  anchor: "way out",
  tunnel: "tunnel",
  docker: "Docker",
  forward: "port forward",
  nat: "NAT",
  allowlist: "allowlist",
  connection: "connection",
}

/**
 * What a route or rule would do to the addresses this host is known to
 * depend on, modeled before anything is applied. Changed and undecidable
 * addresses are listed; the rest are counted, and what the model cannot see
 * is said under it.
 */
export function RouteImpactList({ impact, title }: { impact: NetworkRouteImpact; title: string }) {
  const changed = impact.affected.length
  return (
    <section aria-label={title} className="space-y-2">
      <p className="text-body font-medium">
        {title}{" "}
        <span className="numeric text-hint font-normal text-muted-foreground">
          {plural(changed, "address")} change · {plural(impact.unknown.length, "address")} undecided
          · {impact.unaffected} unchanged
        </span>
      </p>
      {impact.clientAffected && (
        <Notice tone="danger" icon={Warning} title="Your connection to the dashboard is affected">
          The server checks your reply path again once the change is applied and takes it back if it
          moved; the model expects it would.
        </Notice>
      )}
      {changed + impact.unknown.length > 0 && (
        <ul className="divide-y divide-hairline rounded-lg border border-hairline">
          {[...impact.affected, ...impact.unknown].map((item, index) => (
            <ImpactRow
              key={`${item.kind}:${item.address}:${index}`}
              item={item}
              unknown={index >= changed}
            />
          ))}
        </ul>
      )}
      <ul className="space-y-1 text-hint text-muted-foreground">
        {impact.limits.map((limit) => (
          <li key={limit}>{limit}</li>
        ))}
      </ul>
    </section>
  )
}

function ImpactRow({ item, unknown }: { item: NetworkImpactItem; unknown: boolean }) {
  return (
    <li className="grid min-w-0 gap-1 px-3 py-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)]">
      <div className="min-w-0">
        <span className="flex flex-wrap items-center gap-2">
          <Tag tone={item.kind === "client" ? "danger" : "default"}>
            {KIND_WORD[item.kind] ?? item.kind}
          </Tag>
          <span className="font-mono text-xs break-all">{item.address}</span>
        </span>
        <span className="mt-0.5 block text-hint text-muted-foreground">{item.name}</span>
      </div>
      <div className="min-w-0 text-xs">
        {unknown ? (
          <span className="text-warning">{item.reason ?? "The model cannot decide it."}</span>
        ) : (
          <span className="break-words">
            <span className="text-muted-foreground">{item.before}</span> <span aria-hidden>→</span>
            <span className="sr-only">becomes</span>{" "}
            <span className={item.after === "discarded" ? "text-destructive" : undefined}>
              {item.after}
            </span>
          </span>
        )}
      </div>
    </li>
  )
}
