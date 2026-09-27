"use client"

import { useMemo } from "react"
import { dockerSource, fileSource, journalSource } from "@/lib/log-sources"
import { ServiceLogs, type ServiceLogSource } from "@/components/logs/service-logs"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import type { ProxyStatus } from "@/components/proxy/proxy-context"

/**
 * Where the engine itself writes, by what it is: host nginx's own two files
 * and its unit's journal; a Caddy on the host, its unit's journal; the shared
 * Docker Caddy ingress, its container's output. First the one a failure
 * lands in.
 */
export function engineLogSources(status: ProxyStatus): ServiceLogSource[] {
  if (status.nginx) {
    return [
      {
        id: fileSource("/var/log/nginx/error.log"),
        label: "error.log",
        kind: "nginx",
        path: "/var/log/nginx/error.log",
        lens: "nginx-error",
        product: "nginx-static",
      },
      {
        id: fileSource("/var/log/nginx/access.log"),
        label: "access.log",
        kind: "nginx",
        path: "/var/log/nginx/access.log",
        lens: "http-access",
        product: "nginx-static",
      },
      {
        id: journalSource("nginx.service"),
        label: "nginx.service",
        kind: "journal",
        lens: "nginx-error",
        product: "nginx-static",
      },
    ]
  }
  if (status.caddy && status.ingressContainer) {
    return [
      {
        id: dockerSource(status.ingressContainer),
        label: status.ingressContainer,
        kind: "docker",
        lens: "caddy",
        product: "caddy",
      },
    ]
  }
  if (status.caddy) {
    return [
      {
        id: journalSource("caddy.service"),
        label: "caddy.service",
        kind: "journal",
        lens: "caddy",
        product: "caddy",
      },
    ]
  }
  return []
}

/**
 * The engine's own log, on the page about the engine: what nginx said when
 * the reload was refused, which upstream stopped answering, what the ingress
 * logged as it renewed a certificate. It used to be a unit behind "Service
 * details" and a file on the Logs page, and neither was where the question —
 * why is the proxy unhappy — gets asked. The page already opens on four
 * figures, so the log's own readings stay in its quick views and Insights.
 */
export function EngineLog({ status }: { status: ProxyStatus }) {
  const sources = useMemo(() => engineLogSources(status), [status])
  if (sources.length === 0) return null
  return (
    <Panel plain>
      <PanelHeader title="Engine log" />
      <PanelBody flush className="pt-3">
        <ServiceLogs
          sources={sources}
          storageKey="proxy.engine"
          pickerLabel="Engine log"
          paneClassName="h-[min(70vh,40rem)] min-h-96"
        />
      </PanelBody>
    </Panel>
  )
}
