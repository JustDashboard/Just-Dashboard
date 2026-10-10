"use client"

import { useState } from "react"
import Link from "next/link"
import { Globe, Route } from "@/components/icons"
import { usePoll } from "@/hooks/use-poll"
import { get, post } from "@/lib/api"
import { timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { PortsExternal } from "@/lib/types"
import { FormSection } from "@/components/form"
import { PublishedPathReport, bindingFamily } from "@/components/docker/published-path"
import type { Socket } from "@/components/proxy/ports"
import {
  externalFor,
  externalWords,
  pendingCheck,
  scopesFor,
} from "@/components/proxy/ports-reachability"
import { Hint } from "@/components/docker/explain"
import { Button } from "@/components/ui/button"

const PLACEMENT: Record<string, string> = {
  external_host: "external host",
  controlled_fixture: "controlled fixture, not off-host",
}

/**
 * Whether this port is reachable from outside the host, as enrolled external
 * sources measured it: the only evidence on this page that is not a reading
 * of the host itself. A check is started through the external-check owner and
 * measured on the source's next poll; nothing here probes the port.
 */
export function ExternalProof({ socket }: { socket: Socket }) {
  const [starting, setStarting] = useState("")
  // Once a check is queued, read again every few seconds while this sheet is
  // open, so the source's measurement appears when it arrives.
  const [following, setFollowing] = useState(false)
  const reading = usePoll<PortsExternal>(
    (signal) => get<PortsExternal>("/ports/external", undefined, signal),
    following ? 5_000 : 0,
    [socket.port],
  )
  const external = reading.data
  const evidence = externalFor(external, socket)
  const scopes = scopesFor(external, socket)
  const waiting = pendingCheck(evidence)

  const check = async (vantageId: string, scopeId: string, family: "inet" | "inet6") => {
    const key = `${vantageId}:${scopeId}:${family}`
    setStarting(key)
    try {
      await post("/network/external/checks", {
        vantageId,
        scopeId,
        family,
        port: socket.port,
        tls: false,
      })
      notify.success("Check queued — the source measures it on its next poll")
      setFollowing(true)
      reading.refresh()
    } catch (err) {
      notify.error("Could not queue the check", err)
    } finally {
      setStarting("")
    }
  }

  return (
    <FormSection
      title="From outside"
      actions={
        <Button asChild variant="outline" size="xs">
          <Link href="/network/external">
            <Globe />
            External checks
          </Link>
        </Button>
      }
    >
      {reading.error && !reading.data && (
        <p className="text-hint text-destructive">
          Retained external evidence could not be read: {reading.error.message}
        </p>
      )}
      {external?.error && <p className="text-hint text-muted-foreground">{external.error}</p>}
      {socket.protocol !== "tcp" ? (
        <Hint>External checks measure TCP; a UDP port is not measured from outside.</Hint>
      ) : (
        external && (
          <div className="space-y-3">
            {evidence.length === 0 ? (
              <p className="text-body text-muted-foreground">
                No enrolled source has measured port {socket.port}. Reachability from outside is
                unproven: this host&apos;s own reading cannot see a provider&apos;s firewall in
                front of it.
              </p>
            ) : (
              <ul aria-label="External measurements" className="divide-y divide-hairline">
                {evidence.map((e) => (
                  <li key={e.checkId} className="space-y-0.5 py-1.5">
                    <p className="text-body">{externalWords(e)}</p>
                    <p className="text-hint text-muted-foreground">
                      {e.source}
                      {e.location && ` · ${e.location}`} · {PLACEMENT[e.placement] ?? e.placement} ·{" "}
                      {e.family === "inet" ? "IPv4" : "IPv6"} · {timestamp(e.at)}
                    </p>
                  </li>
                ))}
              </ul>
            )}
            {scopes.length === 0 ? (
              <Hint>
                No enrolled source may check port {socket.port}. Enroll one with this port in its
                scope on External checks.
              </Hint>
            ) : (
              <div className="flex flex-wrap gap-2">
                {scopes.map(({ scope, family }) => {
                  const key = `${scope.vantageId}:${scope.scopeId}:${family}`
                  return (
                    <Button
                      key={key}
                      size="xs"
                      variant="outline"
                      disabled={Boolean(starting) || waiting}
                      pending={starting === key}
                      onClick={() => void check(scope.vantageId, scope.scopeId, family)}
                    >
                      Check from {scope.source} ({family === "inet" ? "IPv4" : "IPv6"})
                    </Button>
                  )
                })}
              </div>
            )}
          </div>
        )
      )}
    </FormSection>
  )
}

/**
 * The inbound path to a port Docker publishes: its publication and NAT,
 * DOCKER-USER, the forwarded leg's firewall, the gateway, provider policy and
 * any external measurement, read when asked.
 */
export function DockerPath({ socket }: { socket: Socket }) {
  const [shown, setShown] = useState(false)
  const container = socket.container
  if (!container?.published) return null
  return (
    <FormSection title="Path from outside">
      {shown ? (
        <PublishedPathReport
          container={container.id}
          binding={{
            hostPort: socket.port,
            protocol: socket.protocol,
            family: bindingFamily(socket.address),
          }}
        />
      ) : (
        <div className="space-y-2">
          <Hint>
            Docker publishes this port through its own NAT, ahead of the firewall&apos;s inbound
            rules. The path reads each layer it crosses and names the ones nothing here can see.
          </Hint>
          <Button size="xs" variant="outline" onClick={() => setShown(true)}>
            <Route />
            Trace the path
          </Button>
        </div>
      )}
    </FormSection>
  )
}
