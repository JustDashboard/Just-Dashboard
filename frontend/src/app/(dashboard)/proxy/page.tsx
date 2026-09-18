"use client"

import { useMemo } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowRight, Globe, ShieldCheck } from "@/components/icons"
import { ApiError, get } from "@/lib/api"
import type { Certificate, CertbotState, Listener, StreamStatus, VHost } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { Page, PageHeader, PageState } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { StatGrid, StatLink, StatTile } from "@/components/stat-tile"
import { StatusDot, Status } from "@/components/status-dot"
import { FindingList } from "@/components/finding-list"
import { EmptyState } from "@/components/state"
import { Skeleton } from "@/components/ui/skeleton"
import { useProxy } from "@/components/proxy/proxy-context"
import { EngineActions, EngineFacts, useEngineUnit } from "@/components/proxy/engine"
import { foldProxyFindings } from "@/components/proxy/attention"

/**
 * The section's landing page, read the way the host overview is read: what
 * the engine is, four figures, what needs doing, and the sites.
 *
 * It used to open on a "Reverse proxy" tile and a list that named every
 * exposed socket as needing attention. The engine is a fact, so it is in the
 * facts row; and the attention list is folded from the conditions somebody
 * would actually act on, with the same three-part shape the host's health
 * findings use.
 */
export default function ProxyOverviewPage() {
  const { status, loading, refresh: refreshStatus } = useProxy()
  const { can } = useAuth()
  const router = useRouter()
  const admin = can("system.admin")
  const engine = useEngineUnit(status)

  const vhosts = usePoll<VHost[]>((signal) => get("/proxy/vhosts", undefined, signal), 30_000)
  const certs = usePoll<Certificate[]>(
    (signal) => get("/certificates/", undefined, signal),
    300_000,
  )
  const certbot = usePoll<CertbotState>(
    (signal) => get("/certificates/certbot", undefined, signal),
    300_000,
    [],
    { enabled: Boolean(status?.certbot) },
  )
  const streams = usePoll<StreamStatus>(
    (signal) => get("/proxy/streams/", undefined, signal),
    60_000,
  )
  const ports = usePoll<Listener[]>((signal) => get("/ports", undefined, signal), 30_000)

  const hosts = vhosts.data ?? []
  const onTls = hosts.filter((v) => v.tls).length
  const disabled = hosts.filter((v) => v.kind === "nginx" && !v.enabled && v.enabledPath).length
  const exposed = useMemo(() => (ports.data ?? []).filter((l) => l.exposed), [ports.data])
  const badCerts = useMemo(
    () => (certs.data ?? []).filter((c) => c.expired || c.expiring || c.error),
    [certs.data],
  )
  // certbot being absent is a fact about the host, not a failure to report.
  const certbotGone =
    certbot.error instanceof ApiError && certbot.error.code === "certbot_unavailable"
  const findings = useMemo(
    () =>
      foldProxyFindings({
        certs: certs.data,
        certbot: certbotGone ? null : certbot.data,
        vhosts: vhosts.data,
        streams: streams.data,
        ports: ports.data,
      }),
    [certs.data, certbot.data, certbotGone, vhosts.data, streams.data, ports.data],
  )
  const settled = !vhosts.loading && !certs.loading && !streams.loading && !ports.loading

  if (loading && !status) {
    return <PageState eyebrow="Apps" title="Proxy & TLS" />
  }
  if (!status) return null

  const hasEngine = status.nginx || status.caddy
  const refreshAll = () => {
    refreshStatus()
    engine.refresh()
    vhosts.refresh()
  }

  return (
    <Page className="animate-rise">
      <PageHeader
        eyebrow="Apps"
        title="Proxy & TLS"
        actions={
          admin &&
          hasEngine && (
            <EngineActions
              status={status}
              unitName={engine.name}
              unit={engine.unit}
              onChanged={refreshAll}
            />
          )
        }
      />

      <EngineFacts
        status={status}
        unit={engine.unit}
        fetchedAt={engine.fetchedAt}
        certbotVersion={certbot.data?.version}
        renewSource={certbot.data ? (certbot.data.renewSource ?? null) : undefined}
      />

      <StatGrid columns={4}>
        <StatLink href="/proxy/sites" label="Sites">
          <StatTile
            className="h-full transition-colors group-hover:bg-row-hover"
            label="Sites"
            value={
              <Figure settled={!vhosts.loading}>{vhosts.data ? hosts.length : undefined}</Figure>
            }
            hint={
              vhosts.data
                ? disabled > 0
                  ? `${onTls} on TLS · ${disabled} disabled`
                  : `${onTls} on TLS`
                : undefined
            }
            tone={disabled > 0 ? "warning" : "default"}
          />
        </StatLink>
        <StatLink href="/proxy/certificates" label="Certificates">
          <StatTile
            className="h-full transition-colors group-hover:bg-row-hover"
            label="Certificates"
            value={
              <Figure settled={!certs.loading}>{certs.data ? certs.data.length : undefined}</Figure>
            }
            hint={
              certs.data
                ? badCerts.length > 0
                  ? `${badCerts.length} need attention`
                  : certs.data.length > 0
                    ? "all valid"
                    : "none issued"
                : undefined
            }
            tone={
              badCerts.some((c) => c.expired || c.error)
                ? "danger"
                : badCerts.length
                  ? "warning"
                  : "default"
            }
          />
        </StatLink>
        <StatLink href="/proxy/streams" label="Streams">
          <StatTile
            className="h-full transition-colors group-hover:bg-row-hover"
            label="Streams"
            value={
              <Figure settled={!streams.loading}>
                {streams.data ? streams.data.streams.length : undefined}
              </Figure>
            }
            hint={
              streams.data
                ? streams.data.streams.length === 0
                  ? "nothing forwarded"
                  : streams.data.included
                    ? "read by nginx"
                    : "not read by nginx"
                : undefined
            }
            tone={
              streams.data && streams.data.streams.length > 0 && !streams.data.included
                ? "warning"
                : "default"
            }
          />
        </StatLink>
        <StatLink href="/proxy/ports" label="Exposed ports">
          <StatTile
            className="h-full transition-colors group-hover:bg-row-hover"
            label="Exposed ports"
            value={
              <Figure settled={!ports.loading}>{ports.data ? exposed.length : undefined}</Figure>
            }
            hint={
              ports.data
                ? exposed.length
                  ? `of ${ports.data.length} listening, off the machine`
                  : "everything on loopback"
                : undefined
            }
          />
        </StatLink>
      </StatGrid>

      {/* A titled list on the page, not a box, for the reason the host
          overview's health list is: the findings are the first thing to read
          after the figures, and a frame around them opened the page with a
          stack of containers. */}
      <Panel plain>
        <PanelHeader title="Needs attention" />
        <PanelBody>
          {!settled ? (
            <div className="space-y-2">
              <Skeleton className="h-4 w-56" />
              <Skeleton className="h-4 w-40" />
            </div>
          ) : (
            <div className="animate-rise">
              <FindingList
                findings={findings.map((f) => ({
                  ...f,
                  action: { label: `Open ${f.meta}`, onClick: () => router.push(f.href) },
                }))}
                emptyLabel="Certificates, renewal, sites, streams and exposed ports all within limits"
              />
            </div>
          )}
        </PanelBody>
      </Panel>

      <Panel plain>
        <PanelHeader
          title="Sites"
          actions={
            hosts.length > 0 && (
              <Link
                href="/proxy/sites"
                className="flex items-center gap-1 text-hint font-medium text-muted-foreground hover:text-foreground"
              >
                All sites <ArrowRight className="size-3" />
              </Link>
            )
          }
        />
        <PanelBody flush>
          {vhosts.loading ? (
            <div className="space-y-3 py-1">
              <Skeleton className="h-4 w-64" />
              <Skeleton className="h-4 w-48" />
            </div>
          ) : hosts.length === 0 ? (
            <EmptyState
              icon={Globe}
              title="Nothing configured yet"
              description={
                status.nginx
                  ? "Add a site to put a domain in front of something on this machine, or issue a certificate from the Certificates tab."
                  : "No nginx sites, Caddyfile or shared Caddy ingress was found on this host."
              }
              className="mt-2"
            />
          ) : (
            <RowList className="animate-rise">
              {hosts.slice(0, 8).map((vhost) => (
                <Row
                  key={`${vhost.kind}:${vhost.name}`}
                  href={`/proxy/sites?site=${encodeURIComponent(vhost.name)}`}
                  leading={<StatusDot state={vhost.enabled ? "running" : "stopped"} />}
                  title={vhost.name}
                  subtitle={
                    [vhost.serverNames.join(", "), vhost.upstreams[0]]
                      .filter(Boolean)
                      .join(" → ") || vhost.path
                  }
                  mono
                  trailing={
                    vhost.tls ? (
                      <Status state="active" label="TLS" icon={ShieldCheck} />
                    ) : (
                      <span className="text-xs text-muted-foreground">plain HTTP</span>
                    )
                  }
                />
              ))}
            </RowList>
          )}
        </PanelBody>
      </Panel>
    </Page>
  )
}

/**
 * A tile's figure that rises once its own poll settles — swapping the key
 * between skeleton and value remounts it — so the four fill in rather than
 * flickering from bone to number.
 */
function Figure({ settled, children }: { settled: boolean; children: React.ReactNode }) {
  if (!settled) return <Skeleton className="my-1.5 h-5 w-12" />
  return (
    <span key="figure" className="inline-block animate-rise">
      {children ?? "—"}
    </span>
  )
}
