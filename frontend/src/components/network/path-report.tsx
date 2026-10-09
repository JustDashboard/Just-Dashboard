"use client"

import Link from "next/link"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { evidenceCounts } from "@/lib/network-investigator"
import type { PathEvidence, PathResult } from "@/lib/network-investigator-types"

const basisName: Record<PathEvidence["basis"], string> = {
  observed: "Observed snapshot",
  modeled: "Supported model",
  measured: "Measured response",
  unknown: "Unknown",
}

export function PathReport({ result }: { result: PathResult }) {
  const counts = evidenceCounts(result.evidence)
  return (
    <section aria-label="Connection path report" className="min-w-0 space-y-6">
      <div className="space-y-3 border-b border-hairline pb-4">
        <h2 className="text-title font-medium">
          {result.scope.source} → {result.scope.target}
        </h2>
        <p className="font-mono text-body break-all">
          {result.scope.family === "inet" ? "IPv4" : "IPv6"} · {result.scope.protocol.toUpperCase()}{" "}
          / {result.scope.port} ·{" "}
          {result.scope.sourceAddress ||
            (result.scope.vantage === "published_port"
              ? "any outside address"
              : "source unknown")}{" "}
          → {result.scope.address || "destination unresolved"}
          {result.scope.mark && ` · mark ${result.scope.mark}`}
        </p>
        <p className="text-body">{result.comparison}</p>
        <p className="text-hint text-muted-foreground">
          Collected {new Date(result.endedAt).toLocaleString()}. This report describes the displayed
          scope at that time.
        </p>
      </div>
      <StatGrid>
        <StatTile label="Observed snapshots" value={counts.observed} />
        <StatTile label="Supported models" value={counts.modeled} />
        <StatTile label="Measured responses" value={counts.measured} />
        <StatTile
          label="Unknown layers"
          value={counts.unknown}
          tone={counts.unknown ? "warning" : "default"}
        />
      </StatGrid>
      <nav aria-label="Evidence steps" className="flex flex-wrap gap-x-5 gap-y-2 text-body">
        {result.evidence.map((item) => (
          <a key={item.id} href={`#evidence-${item.id}`} className="underline underline-offset-4">
            {item.title}
          </a>
        ))}
      </nav>
      <ol className="divide-y divide-hairline">
        {result.evidence.map((item) => (
          <li
            key={item.id}
            id={`evidence-${item.id}`}
            className="min-w-0 scroll-mt-6 py-5 first:pt-0"
          >
            <div className="flex flex-wrap items-baseline justify-between gap-2">
              <h3 className="text-title font-medium">{item.title}</h3>
              <p className="text-hint text-muted-foreground">
                {basisName[item.basis]} · {item.state.replaceAll("_", " ")}
              </p>
            </div>
            <p className="mt-2 text-body">
              {item.summary || "This owner did not provide evidence for this layer."}
            </p>
            <p className="mt-1 text-hint text-muted-foreground">
              Scope: {item.scope} · Owner: {item.owner}
              {item.ownerPath && (
                <>
                  {" "}
                  ·{" "}
                  <Link href={item.ownerPath} className="underline underline-offset-4">
                    Open owner
                  </Link>
                </>
              )}
            </p>
            {item.facts.length > 0 && (
              <dl className="mt-3 grid gap-x-6 gap-y-2 sm:grid-cols-2">
                {item.facts.map((fact, index) => (
                  <div key={`${fact.label}:${index}`} className="min-w-0">
                    <dt className="text-hint text-muted-foreground">{fact.label}</dt>
                    <dd className="text-body break-all">{fact.value || "Unknown"}</dd>
                  </div>
                ))}
              </dl>
            )}
            {item.limitations.length > 0 && (
              <ul className="mt-3 list-disc space-y-1 pl-4 text-hint text-muted-foreground">
                {item.limitations.filter(Boolean).map((limit, index) => (
                  <li key={index}>{limit}</li>
                ))}
              </ul>
            )}
          </li>
        ))}
      </ol>
      <Panel plain>
        <PanelHeader title="Scope and remaining unknowns" />
        <PanelBody>
          <ul className="list-disc space-y-2 pl-4 text-body text-muted-foreground">
            {result.scope.limitations.map((limit) => (
              <li key={limit}>{limit}</li>
            ))}
          </ul>
        </PanelBody>
      </Panel>
    </section>
  )
}
