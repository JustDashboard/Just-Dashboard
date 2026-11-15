"use client"

import { ChartActivity, ShieldCheck, Warning } from "@/components/icons"
import { plural } from "@/lib/format"
import type { DNSTLSReport, DNSView } from "@/lib/types"
import { WireMark } from "@/components/deploy/wire"
import { TileTrend } from "@/components/metrics/sparkline"
import { HUE } from "@/components/overview/readings"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { NoReading, ReadingLine, ReadingLines } from "@/components/reading-line"
import { GhostMark } from "@/components/network/dns/ghost"
import { LinkScopes } from "@/components/network/dns/link-scopes"
import {
  dnssecWords,
  failing,
  lookupWords,
  port53Of,
  type DNSShape,
} from "@/components/network/dns/reading"
import { Port53, ResolverChain } from "@/components/network/dns/resolver-chain"
import { ResolverOwnerPanel } from "@/components/network/dns/resolver-owner"
import { TLSCheck } from "@/components/network/dns/tls-check"

/**
 * The resolver as it is now: the chain a name goes down, what resolved has
 * counted, what answers on port 53, each link's own scope, the certificates
 * the upstreams present, and who owns the file programs read.
 *
 * Every block is drawn while the first read is out, at the height it had the
 * last time this browser saw it (`shape`), so the read lands in place. The
 * owner comes last because its length is the server's to decide: nothing
 * stands under it to be pushed.
 */
export function ResolverView({
  view,
  shape,
  asking,
  trend,
  tls,
  onTLS,
  onChanged,
}: {
  /** Undefined while the first read is out. */
  view?: DNSView
  shape: DNSShape
  /** The lookup count rose over the last interval. */
  asking: boolean
  /** Lookups made between each read and the one before, since the page opened. */
  trend: number[]
  tls?: DNSTLSReport
  onTLS: (report: DNSTLSReport) => void
  onChanged: () => void
}) {
  const listeners = view ? port53Of(view).length : undefined
  return (
    <div className="flex min-w-0 flex-col gap-6 md:gap-8">
      <section aria-label="Resolver chain" className="min-w-0">
        <ResolverChain view={view} asking={asking} shape={shape} />
      </section>

      {(view ? view.resolved.active : shape.readings) && <Readings view={view} trend={trend} />}

      <Panel plain aria-label="Answering on port 53">
        <PanelHeader
          title="Answering on port 53"
          actions={
            listeners !== undefined && (
              <span className="numeric text-hint text-muted-foreground">{listeners}</span>
            )
          }
        />
        <PanelBody>
          <Port53 view={view} shape={shape} />
        </PanelBody>
      </Panel>

      <Panel plain id="dns-links" className="scroll-mt-6">
        <PanelHeader
          title="Per-link DNS"
          actions={
            view && (
              <span className="numeric text-hint text-muted-foreground">
                {plural(view.resolved.links.length, "link")} of {view.resolved.linksTotal} with DNS
                settings
              </span>
            )
          }
        />
        <PanelBody>
          <LinkScopes view={view} rows={shape.links} onChanged={onChanged} />
        </PanelBody>
      </Panel>

      <TLSCheck report={tls} onReport={onTLS} />

      {view?.owner && <ResolverOwnerPanel owner={view.owner} />}
    </div>
  )
}

/**
 * What resolved has counted since it started: the lookups that failed or
 * timed out among those it made, with what the cache did, and the verdicts
 * DNSSEC reached. They were two of four grey tiles; said as lines, a failure
 * is a sentence in amber and a bogus answer one in red, on the marks the
 * rest of the product's readings stand on.
 */
function Readings({ view, trend }: { view?: DNSView; trend: number[] }) {
  if (!view) {
    return (
      <ReadingLines aria-hidden>
        {["Lookups", "DNSSEC"].map((name) => (
          <ReadingLine key={name} mark={<GhostMark size="md" />} name={name}>
            <NoReading>Reading…</NoReading>
          </ReadingLine>
        ))}
      </ReadingLines>
    )
  }
  const { resolved } = view
  const stats = resolved.statistics
  const unavailable = resolved.statisticsError ?? "no resolver statistics"
  const troubled = stats ? failing(stats) : false
  return (
    <ReadingLines>
      <ReadingLine
        mark={
          troubled ? (
            <WireMark size="md" tone="warning">
              <Warning aria-hidden />
            </WireMark>
          ) : (
            <WireMark size="md" hue={HUE.net}>
              <ChartActivity aria-hidden />
            </WireMark>
          )
        }
        name="Lookups"
        hint={
          stats
            ? `${stats.cacheHits.toLocaleString()} from cache · ${stats.cacheMisses.toLocaleString()} upstream · ${stats.cacheSize.toLocaleString()} held · ${stats.currentTransactions} in flight`
            : unavailable
        }
        trend={
          <TileTrend
            values={trend}
            label="Lookups between reads, since the page opened"
            color={HUE.net}
          />
        }
      >
        {stats ? (
          <span className={troubled ? "numeric text-warning" : "numeric"}>
            {lookupWords(stats)}
          </span>
        ) : (
          <NoReading>No reading</NoReading>
        )}
      </ReadingLine>
      <ReadingLine
        mark={
          stats && stats.dnssecBogus > 0 ? (
            <WireMark size="md" tone="danger">
              <Warning aria-hidden />
            </WireMark>
          ) : (
            <WireMark size="md" hue={HUE.mem}>
              <ShieldCheck aria-hidden />
            </WireMark>
          )
        }
        name="DNSSEC"
        hint={
          stats
            ? `${stats.dnssecInsecure.toLocaleString()} unsigned · ${stats.dnssecIndeterminate.toLocaleString()} indeterminate · set to ${resolved.global.dnssec || "resolved's default"}`
            : unavailable
        }
      >
        {stats ? (
          <span className={stats.dnssecBogus > 0 ? "numeric text-destructive" : "numeric"}>
            {dnssecWords(stats)}
          </span>
        ) : (
          <NoReading>No reading</NoReading>
        )}
      </ReadingLine>
    </ReadingLines>
  )
}
