"use client"

import { useMemo } from "react"
import { Globe, ShieldCheck } from "@/components/icons"
import { get } from "@/lib/api"
import type { Certificate, Listener, VHost } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageHeader, PageState } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Row, RowList } from "@/components/row-list"
import { StatGrid, StatLink, StatTile } from "@/components/stat-tile"
import { StatusDot } from "@/components/status-dot"
import { EmptyState } from "@/components/state"
import { useProxy } from "@/components/proxy/proxy-context"
import { ExpiryStatus } from "@/components/proxy/expiry-status"

export default function ProxyOverviewPage() {
  const { status, loading } = useProxy()

  const vhosts = usePoll<VHost[]>((signal) => get("/proxy/vhosts", undefined, signal), 30_000)
  const certs = usePoll<Certificate[]>(
    (signal) => get("/certificates/", undefined, signal),
    300_000,
  )
  const ports = usePoll<Listener[]>((signal) => get("/ports", undefined, signal), 30_000)

  const hosts = vhosts.data ?? []
  const onTls = hosts.filter((v) => v.tls).length
  const exposed = useMemo(() => (ports.data ?? []).filter((l) => l.exposed), [ports.data])
  const badCerts = useMemo(
    () => (certs.data ?? []).filter((c) => c.expired || c.expiring || c.error),
    [certs.data],
  )

  if (loading && !status) {
    return <PageState eyebrow="Network" title="Proxy & TLS" />
  }

  const engine = status?.nginx
    ? `nginx ${status.nginxVersion ?? ""}`.trim()
    : status?.caddy
      ? `Caddy ${status.caddyVersion ?? ""}`.trim()
      : "none detected"

  return (
    <Page>
      <PageHeader eyebrow="Network" title="Proxy & TLS" />

      <StatGrid columns={4}>
        <StatTile
          label="Reverse proxy"
          value={engine}
          hint={status?.certbot ? "certbot available" : "no certbot"}
          tone={status?.nginx || status?.caddy ? "default" : "warning"}
        />
        <StatLink href="/proxy/sites" label="Sites">
          <StatTile
            className="h-full transition-colors group-hover:bg-row-hover"
            label="Sites"
            value={hosts.length}
            hint={`${onTls} on TLS`}
          />
        </StatLink>
        <StatLink href="/proxy/certificates" label="Certificates">
          <StatTile
            className="h-full transition-colors group-hover:bg-row-hover"
            label="Certificates"
            value={certs.data?.length ?? "—"}
            hint={badCerts.length > 0 ? `${badCerts.length} need attention` : "all valid"}
            tone={
              badCerts.some((c) => c.expired || c.error)
                ? "danger"
                : badCerts.length
                  ? "warning"
                  : "success"
            }
          />
        </StatLink>
        <StatLink href="/proxy/ports" label="Exposed ports">
          <StatTile
            className="h-full transition-colors group-hover:bg-row-hover"
            label="Exposed ports"
            value={exposed.length}
            hint={exposed.length ? "reachable off the machine" : "all on loopback"}
            tone={exposed.length ? "warning" : "success"}
          />
        </StatLink>
      </StatGrid>

      {(badCerts.length > 0 || exposed.length > 0) && (
        <Panel plain>
          <PanelHeader title="Needs attention" />
          <PanelBody flush>
            <RowList>
              {badCerts.map((cert) => (
                <Row
                  key={`cert-${cert.path || cert.name}`}
                  href="/proxy/certificates"
                  leading={<ShieldCheck className="size-3.5 text-muted-foreground" />}
                  title={cert.name}
                  subtitle={cert.domains.join(", ")}
                  trailing={<ExpiryStatus cert={cert} />}
                />
              ))}
              {exposed.map((listener, i) => (
                <Row
                  key={`port-${listener.port}-${i}`}
                  href="/proxy/ports"
                  leading={<StatusDot tone="warning" />}
                  title={listener.process || "unknown"}
                  subtitle={listener.address}
                  mono
                  trailing={
                    <span className="numeric font-mono text-hint text-muted-foreground">
                      :{listener.port} {listener.protocol}
                    </span>
                  }
                />
              ))}
            </RowList>
          </PanelBody>
        </Panel>
      )}

      {hosts.length === 0 && !vhosts.loading && badCerts.length === 0 && exposed.length === 0 && (
        <EmptyState
          icon={Globe}
          title="Nothing configured yet"
          description="Add a site to put a domain in front of something on this machine, or issue a certificate from the Certificates tab."
        />
      )}
    </Page>
  )
}
