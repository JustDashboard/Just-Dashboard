"use client"

import { NetworkReadWarning } from "@/components/network/read-warning"
import { useState } from "react"
import { get } from "@/lib/api"
import { plural } from "@/lib/format"
import type { DNSView, HostRecords } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageContext, Section } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
import { ErrorState, LoadingPanel } from "@/components/state"
import { NumberTicker } from "@/components/ui/number-ticker"
import { Adblock } from "@/components/network/dns/adblock"
import { HostRecordsEditor } from "@/components/network/dns/host-records"
import { LookupRace } from "@/components/network/dns/lookup-race"
import { ResolverChain, viaStub } from "@/components/network/dns/resolver-chain"
import { encryptionOf, splitServer } from "@/components/network/dns/resolvers"
import { UpstreamEditor } from "@/components/network/dns/upstreams"

/**
 * Who answers this server's lookups, and how.
 *
 * The page opens on the chain a name goes down — the programs that ask, the
 * stub or resolv.conf they ask, each upstream drawn as the provider it is
 * with whether the way to it is encrypted, and what else answers on port 53 —
 * because "why did that name resolve there" is the question a DNS page is
 * opened with. Under it the four readings the resolver keeps (the cache, the
 * transactions, what DNSSEC found, and whether the upstreams are held to
 * TLS), then the upstreams as the thing you change: public resolvers as
 * cards, the three answers DNS over TLS has, DNSSEC and the domains, with
 * Apply asking first because a wrong upstream stops every lookup on the host.
 * Then the ad-blocker the page found or the two ways to get one, the host's
 * own records, and the race that asks one name of every resolver at once.
 *
 * The chain is read every ten seconds. A wire into the stub carries while the
 * resolver's transaction count rose over the last interval; the wire to the
 * server in use carries because resolved says it is the one in use. Nothing
 * is drawn live that is not read live.
 */
export default function NetworkDNSPage() {
  const dns = usePoll<DNSView>((signal) => get("/network/dns/", undefined, signal), 10_000)
  const hosts = usePoll<HostRecords>(
    (signal) => get("/network/dns/hosts", undefined, signal),
    30_000,
  )

  // Whether the transaction count rose since the last read. Derived while
  // rendering from the count seen last time, so there is no effect and no ref
  // written mid-render.
  const transactions = dns.data?.resolved.statistics?.transactions
  const [seen, setSeen] = useState<{ count?: number; asking: boolean }>({ asking: false })
  if (transactions !== seen.count) {
    setSeen({
      count: transactions,
      asking: seen.count !== undefined && transactions !== undefined && transactions > seen.count,
    })
  }

  if (!dns.data) {
    return (
      <Page className="animate-rise">
        <PageContext eyebrow="Network" title="DNS" />
        {dns.error ? <ErrorState error={dns.error} onRetry={dns.refresh} /> : <LoadingPanel />}
      </Page>
    )
  }
  const view = dns.data
  const { resolved, resolvConf } = view
  // Resolved must be running for the dashboard's drop-in to be read by anything.
  const readOnly = !resolved.active
    ? resolved.installed
      ? "systemd-resolved is installed but not running, so a drop-in written here would be read by nothing."
      : `systemd-resolved is not running here; ${resolvConf.path} is managed by ${resolvConf.managedBy ?? (resolvConf.mode === "static" ? "a plain file" : "something else")}, so the upstreams are changed there rather than on this page.`
    : undefined

  return (
    <Page className="animate-rise">
      <PageContext eyebrow="Network" title="DNS" />
      {dns.data && <NetworkReadWarning error={dns.error} refresh={dns.refresh} />}

      <Panel plain>
        <PanelHeader
          title="Resolver chain"
          actions={
            <span className="text-hint text-muted-foreground">
              {viaStub(view) ? "systemd-resolved stub" : `${resolvConf.path} · ${resolvConf.mode}`}
            </span>
          }
        />
        <PanelBody>
          <ResolverChain view={view} asking={seen.asking} />
        </PanelBody>
      </Panel>

      <Readings view={view} />

      <div id="dns-upstreams" className="scroll-mt-6">
        <Section
          title="Upstreams"
          actions={
            <span className="text-hint text-muted-foreground">
              {view.managed.exists ? (
                <>
                  set here in <span className="font-mono">{view.managed.path}</span>
                </>
              ) : (
                "the host's own settings"
              )}
            </span>
          }
        >
          <UpstreamEditor view={view} readOnly={readOnly} onChanged={dns.refresh} />
        </Section>
      </div>

      <Section title="Ad-blocking">
        <Panel plain>
          <PanelBody className="py-0 group-data-[plain]/panel:py-0">
            <Adblock adblock={view.adblock} />
          </PanelBody>
        </Panel>
      </Section>

      {hosts.data ? (
        <HostRecordsEditor records={hosts.data} onSaved={hosts.refresh} />
      ) : (
        <Section title="Host records">
          {hosts.error ? (
            <ErrorState error={hosts.error} onRetry={hosts.refresh} />
          ) : (
            <LoadingPanel />
          )}
        </Section>
      )}

      <Section title="Resolve a name">
        <LookupRace />
      </Section>
    </Page>
  )
}

/**
 * The four readings the resolver keeps: how much it answers from its cache,
 * how many lookups it has made, what DNSSEC found in them, and whether the
 * servers are held to TLS. Resolved counts since it started, so these are
 * totals, not rates, and a host whose resolver is not systemd-resolved has
 * none of them — the tiles say that instead of drawing zeros.
 */
function Readings({ view }: { view: DNSView }) {
  const { resolved } = view
  const stats = resolved.statistics
  const encryption = encryptionOf(resolved.global.dnsOverTLS)
  const named = resolved.global.servers.filter((s) => splitServer(s).tlsName).length
  const unavailable = stats ? undefined : (resolved.statisticsError ?? "no resolver statistics")
  return (
    <Section title="Resolver">
      <StatGrid columns={4}>
        <StatTile
          label="Answered from cache"
          value={stats ? <NumberTicker value={stats.hitPercent} decimalPlaces={0} /> : "—"}
          trailing={stats ? "%" : undefined}
          meter={stats?.hitPercent}
          meterLabel="Share of lookups answered from the cache"
          hint={
            stats
              ? `${stats.cacheHits.toLocaleString()} hits · ${stats.cacheMisses.toLocaleString()} misses`
              : unavailable
          }
        />
        <StatTile
          label="Lookups"
          value={stats ? <NumberTicker value={stats.transactions} /> : "—"}
          tone={stats && stats.failures > stats.transactions * 0.02 ? "warning" : "default"}
          hint={
            stats
              ? `${stats.currentTransactions} in flight · ${plural(stats.timeouts, "timeout")} · ${plural(stats.failures, "failure")}`
              : unavailable
          }
        />
        <StatTile
          label="DNSSEC"
          value={
            stats ? (
              stats.dnssecSecure + stats.dnssecBogus === 0 ? (
                "No verdicts"
              ) : (
                <>
                  <NumberTicker value={stats.dnssecSecure} />
                  {" secure"}
                </>
              )
            ) : (
              "—"
            )
          }
          tone={stats && stats.dnssecBogus > 0 ? "danger" : "default"}
          hint={
            stats
              ? `${stats.dnssecBogus.toLocaleString()} bogus · ${stats.dnssecInsecure.toLocaleString()} unsigned · setting ${resolved.global.dnssec || "default"}`
              : unavailable
          }
        />
        <StatTile
          label="Encryption"
          value={
            encryption === "required"
              ? "Required"
              : encryption === "opportunistic"
                ? "When offered"
                : "None"
          }
          tone={encryption === "plain" ? "warning" : "default"}
          hint={
            resolved.global.servers.length > 0
              ? `DNS over TLS · ${named} of ${plural(resolved.global.servers.length, "server")} named`
              : "DNS over TLS · no global servers"
          }
        />
      </StatGrid>
    </Section>
  )
}
