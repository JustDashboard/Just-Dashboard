"use client"

import { useMemo } from "react"
import { get } from "@/lib/api"
import type { LogSource } from "@/lib/types"
import { dockerSource, fileSource, journalSource } from "@/lib/log-sources"
import { usePoll } from "@/hooks/use-poll"
import { kindViews } from "@/components/logs/kind-views"
import { ServiceLogs, type ServiceLogSource } from "@/components/logs/service-logs"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { LoadingRows } from "@/components/state"
import type { ProxyStatus } from "@/components/proxy/proxy-context"

/** Where a host Caddy writes its requests, when its Caddyfile logs them to a file. */
export const CADDY_ACCESS_LOG = "/var/log/caddy/access.log"

/**
 * Whether Caddy runs on the host itself. The status says "Caddy" for the
 * Docker ingress too, so a host's own is the one that has a version of its
 * own to report, or the only Caddy there is.
 */
export function hostCaddy(status: ProxyStatus): boolean {
  return status.caddy && (!status.ingressContainer || Boolean(status.caddyVersion))
}

/**
 * Where the engine itself writes, by what it is: host nginx's own two files
 * and its unit's journal; a Caddy on the host, its unit's journal and, where
 * its Caddyfile logs requests to a file, that file; the shared Docker Caddy
 * ingress, its container's output — beside the others on a host with both,
 * where the ingress is what answers on ports 80 and 443. First the one a
 * failure lands in. `found` is what was asked about before it is offered:
 * whether the container the status names exists (before one is provisioned,
 * the status names the one it would be) and whether Caddy's access file is
 * there to read.
 */
export function engineLogSources(
  status: ProxyStatus,
  found: { ingress: boolean; caddyAccess: boolean },
): ServiceLogSource[] {
  const sources: ServiceLogSource[] = []
  if (status.nginx) {
    sources.push(
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
    )
  }
  if (hostCaddy(status)) {
    sources.push({
      id: journalSource("caddy.service"),
      label: "caddy.service",
      kind: "journal",
      lens: "caddy",
      product: "caddy",
    })
    if (found.caddyAccess) {
      sources.push({
        id: fileSource(CADDY_ACCESS_LOG),
        label: "access.log",
        kind: "nginx",
        path: CADDY_ACCESS_LOG,
        lens: "http-access",
        product: "caddy",
      })
    }
  }
  if (found.ingress && status.ingressContainer) {
    sources.push({
      id: dockerSource(status.ingressContainer),
      label: status.ingressContainer,
      kind: "docker",
      lens: "caddy",
      product: "caddy",
    })
  }
  return sources
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
  // One question about each source that may not be there before it is
  // offered: a container that is not there, or a file Caddy was never told
  // to write, would be a picker entry that answers with an error.
  const name = status.ingressContainer
  const container = usePoll(
    (signal) => get<LogSource>("/logs/source", { source: dockerSource(name ?? "") }, signal),
    0,
    [name],
    { enabled: Boolean(name) },
  )
  const host = hostCaddy(status)
  const access = usePoll(
    (signal) => get<LogSource>("/logs/source", { source: fileSource(CADDY_ACCESS_LOG) }, signal),
    0,
    [host],
    { enabled: host },
  )
  const settled = !name || container.data !== undefined || container.error !== undefined
  const ingress = Boolean(container.data)
  const caddyAccess = Boolean(access.data)
  const sources = useMemo(
    () => engineLogSources(status, { ingress, caddyAccess }),
    [status, ingress, caddyAccess],
  )
  // The host's own logs are there to read while the ingress is asked about;
  // a pane whose only log is the ingress waits for the answer.
  if (!status.nginx && !host && !settled) {
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
          views={kindViews}
          storageKey="proxy.engine"
          pickerLabel="Engine log"
          paneClassName="h-[min(90vh,64rem)] min-h-[40rem]"
        />
      </PanelBody>
    </Panel>
  )
}
