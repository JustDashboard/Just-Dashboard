"use client"

import { useMemo } from "react"
import Link from "next/link"
import { Globe, Router, Servers, ShieldCheck } from "@/components/icons"
import { get } from "@/lib/api"
import type { Certificate, Listener, VHost } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { Page, PageHeader, PageState, Section } from "@/components/page"
import { Panel, PanelBody } from "@/components/panel"
import { StatGrid, StatTile } from "@/components/stat-tile"
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
      <PageHeader
        eyebrow="Network"
        title="Proxy & TLS"
      />

      <StatGrid columns={4}>
        <StatTile
          label="Reverse proxy"
          icon={Servers}
          value={engine}
          hint={status?.certbot ? "certbot available" : "no certbot"}
          tone={status?.nginx || status?.caddy ? "default" : "warning"}
        />
        <Link href="/proxy/sites" className="block min-w-0">
          <StatTile
            className="h-full transition-colors hover:border-rule-brand"
            label="Sites"
            icon={Globe}
            value={hosts.length}
            hint={`${onTls} on TLS`}
          />
        </Link>
        <Link href="/proxy/certificates" className="block min-w-0">
          <StatTile
            className="h-full transition-colors hover:border-rule-brand"
            label="Certificates"
            icon={ShieldCheck}
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
        </Link>
        <Link href="/proxy/ports" className="block min-w-0">
          <StatTile
            className="h-full transition-colors hover:border-rule-brand"
            label="Exposed ports"
            icon={Router}
            value={exposed.length}
            hint={exposed.length ? "reachable off the machine" : "all on loopback"}
            tone={exposed.length ? "warning" : "success"}
          />
        </Link>
      </StatGrid>

      {(badCerts.length > 0 || exposed.length > 0) && (
        <Section title="Needs attention">
          <Panel>
            <PanelBody flush>
              <ul className="divide-y divide-hairline">
                {badCerts.map((cert) => (
                  <li key={`cert-${cert.path || cert.name}`}>
                    <Link
                      href="/proxy/certificates"
                      className="flex min-w-0 items-center justify-between gap-3 px-4 py-2.5 hover:bg-row-hover"
                    >
                      <span className="flex min-w-0 items-center gap-2.5">
                        <ShieldCheck className="size-3.5 shrink-0 text-muted-foreground" />
                        <span className="truncate text-body font-medium">{cert.name}</span>
                      </span>
                      <ExpiryStatus cert={cert} />
                    </Link>
                  </li>
                ))}
                {exposed.map((listener, i) => (
                  <li key={`port-${listener.port}-${i}`}>
                    <Link
                      href="/proxy/ports"
                      className="flex min-w-0 items-center justify-between gap-3 px-4 py-2.5 hover:bg-row-hover"
                    >
                      <span className="flex min-w-0 items-center gap-2.5">
                        <StatusDot tone="warning" />
                        <span className="truncate text-body font-medium">
                          {listener.process || "unknown"}
                        </span>
                      </span>
                      <span className="numeric shrink-0 font-mono text-hint text-muted-foreground">
                        :{listener.port} {listener.protocol}
                      </span>
                    </Link>
                  </li>
                ))}
              </ul>
            </PanelBody>
          </Panel>
        </Section>
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
