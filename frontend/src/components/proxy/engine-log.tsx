"use client"

import { useMemo } from "react"
import { get } from "@/lib/api"
import type { LogSource } from "@/lib/types"
import { dockerSource, fileSource, journalSource } from "@/lib/log-sources"
import { usePoll } from "@/hooks/use-poll"
import { ServiceLogs, type ServiceLogSource } from "@/components/logs/service-logs"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { LoadingRows } from "@/components/state"
import type { ProxyStatus } from "@/components/proxy/proxy-context"

/**
 * Where the engine itself writes, by what it is: host nginx's own two files
 * and its unit's journal; a Caddy on the host, its unit's journal; the shared
 * Docker Caddy ingress, its container's output — beside nginx's on a host
 * with both, where the ingress is what answers on ports 80 and 443. First the
 * one a failure lands in. `ingress` is whether the container the status names
 * exists: before one is provisioned, the status names the one it would be.
 */
export function engineLogSources(status: ProxyStatus, ingress: boolean): ServiceLogSource[] {
  const container: ServiceLogSource[] =
    ingress && status.ingressContainer
      ? [
          {
            id: dockerSource(status.ingressContainer),
            label: status.ingressContainer,
            kind: "docker",
            lens: "caddy",
            product: "caddy",
          },
        ]
      : []
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
      ...container,
    ]
  }
  if (status.caddy && !status.ingressContainer) {
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
  return container
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
  // One question about the ingress before it is offered: a container that
  // is not there would be a picker entry that answers with an error.
  const name = status.ingressContainer
  const probe = usePoll(
    (signal) => get<LogSource>("/logs/source", { source: dockerSource(name ?? "") }, signal),
    0,
    [name],
    { enabled: Boolean(name) },
  )
  const settled = !name || probe.data !== undefined || probe.error !== undefined
  const ingress = Boolean(probe.data)
  const sources = useMemo(() => engineLogSources(status, ingress), [status, ingress])
  // nginx's own logs are there to read while the ingress is asked about; a
  // pane whose only log is the ingress waits for the answer.
  if (!status.nginx && !settled) {
    return (
      <Panel plain>
        <PanelHeader title="Engine log" />
        <PanelBody flush className="pt-3">
          <LoadingRows rows={4} />
        </PanelBody>
      </Panel>
    )
  }
  if (sources.length === 0) return null
  return (
    <Panel plain>
      <PanelHeader title="Engine log" />
      <PanelBody flush className="pt-3">
        <ServiceLogs
          sources={sources}
          storageKey="proxy.engine"
          pickerLabel="Engine log"
          paneClassName="h-[min(90vh,64rem)] min-h-[40rem]"
        />
      </PanelBody>
    </Panel>
  )
}
