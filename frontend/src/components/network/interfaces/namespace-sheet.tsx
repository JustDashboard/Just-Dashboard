"use client"

import { useState } from "react"
import { errorMessage, get } from "@/lib/api"
import type { NetworkLink, NetworkNamespaceDetail, NetworkPath } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { SidePanel } from "@/components/side-panel"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Detail, DetailList } from "@/components/page"
import { Notice } from "@/components/state"
import { Tag } from "@/components/tag"
import { Field, FieldRow } from "@/components/form"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Cidr } from "@/components/network/address"
import { NetworkReadWarning } from "@/components/network/read-warning"
import { ReadingWords } from "./device-detail"

export type NamespaceTarget = { kind: "named" | "container"; name: string }

/**
 * One namespace read as the separate network it is: its devices, its own
 * routes in both families, the resolver its processes are told to use and
 * what listens inside it — and the question routing pages are opened with,
 * asked of its kernel rather than the host's. The host end of each pair that
 * leads into it opens that device's sheet. Nothing here changes it.
 */
export function NamespaceSheet({
  target,
  links,
  onOpenChange,
  onOpenDevice,
}: {
  target: NamespaceTarget | undefined
  links: NetworkLink[]
  onOpenChange: (open: boolean) => void
  onOpenDevice: (name: string) => void
}) {
  const open = !!target
  const detail = usePoll<NetworkNamespaceDetail>(
    (signal) =>
      get(
        `/network/namespaces/${encodeURIComponent(target?.name ?? "")}`,
        { kind: target?.kind },
        signal,
      ),
    30_000,
    [target?.kind, target?.name],
    { enabled: open },
  )
  const d = detail.data
  const hostEnds = target
    ? links.filter((l) =>
        target.kind === "named" ? l.peerNamespace === target.name : l.container === target.name,
      )
    : []
  return (
    <SidePanel
      open={open}
      onOpenChange={onOpenChange}
      width="lg"
      title={
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate font-mono">{target?.name}</span>
          <Tag>{target?.kind === "container" ? "container" : "namespace"}</Tag>
          {d?.managed && <Tag>made here</Tag>}
        </span>
      }
      description={`The ${target?.name ?? ""} network namespace: devices, routes, resolver and listeners`}
    >
      <div className="flex flex-col gap-6">
        {d && (
          <NetworkReadWarning
            error={detail.error}
            refresh={detail.refresh}
            lastSuccess={detail.lastSuccess}
            reading="namespace"
          />
        )}
        {!d ? (
          detail.error ? (
            <Notice tone="warning" title="The namespace could not be read">
              {detail.error.message}
            </Notice>
          ) : (
            <p className="text-body text-muted-foreground">Reading…</p>
          )
        ) : (
          <>
            {hostEnds.length > 0 && (
              <Panel plain>
                <PanelHeader title="From this host" />
                <PanelBody>
                  <ul className="flex flex-wrap gap-2">
                    {hostEnds.map((l) => (
                      <li key={l.name}>
                        <Button size="sm" variant="outline" onClick={() => onOpenDevice(l.name)}>
                          Open {l.name}
                        </Button>
                      </li>
                    ))}
                  </ul>
                </PanelBody>
              </Panel>
            )}

            <Panel plain>
              <PanelHeader title="Devices" />
              <PanelBody>
                {d.devicesRead.state !== "ok" ? (
                  <ReadingWords reading={d.devicesRead} />
                ) : d.devices.length === 0 ? (
                  <p className="text-body text-muted-foreground">Loopback only.</p>
                ) : (
                  <ul className="flex flex-col gap-1.5">
                    {d.devices.map((dev) => (
                      <li key={dev.name} className="flex min-w-0 items-center gap-3 text-body">
                        <span className="font-mono">{dev.name}</span>
                        <span className="flex min-w-0 gap-2 truncate">
                          {dev.addresses.length ? (
                            dev.addresses.map((a) => <Cidr key={a} cidr={a} />)
                          ) : (
                            <span className="text-muted-foreground">no address</span>
                          )}
                        </span>
                        <span className="ml-auto text-hint text-muted-foreground">
                          {dev.state} · MTU {dev.mtu}
                        </span>
                      </li>
                    ))}
                  </ul>
                )}
              </PanelBody>
            </Panel>

            <Panel plain>
              <PanelHeader title="Routes" />
              <PanelBody>
                {d.routesRead.state !== "ok" && (
                  <p className="mb-2">
                    <ReadingWords reading={d.routesRead} />
                  </p>
                )}
                {d.routes.length === 0 ? (
                  d.routesRead.state === "ok" && (
                    <p className="text-body text-muted-foreground">No route of its own.</p>
                  )
                ) : (
                  <Table aria-label="Namespace routes">
                    <TableHeader>
                      <TableRow>
                        <TableHead>Destination</TableHead>
                        <TableHead>Via</TableHead>
                        <TableHead>Device</TableHead>
                        <TableHead>Table</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {d.routes.map((r, i) => (
                        <TableRow key={`${r.family}-${r.destination}-${r.table ?? ""}-${i}`}>
                          <TableCell className="font-mono">
                            {r.type && r.type !== "unicast" ? `${r.type} ` : ""}
                            {r.destination}
                          </TableCell>
                          <TableCell className="font-mono">{r.gateway ?? "—"}</TableCell>
                          <TableCell className="font-mono">{r.device ?? "—"}</TableCell>
                          <TableCell>{r.table ?? "main"}</TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                )}
                <RouteQuestion target={target!} />
              </PanelBody>
            </Panel>

            <Panel plain>
              <PanelHeader title="Resolver" />
              <PanelBody>
                {d.dnsRead.state !== "ok" || !d.dns ? (
                  <ReadingWords reading={d.dnsRead} />
                ) : (
                  <DetailList>
                    <Detail label="Name servers">
                      <span className="font-mono">
                        {d.dns.nameservers.join(", ") || "none — lookups fail"}
                      </span>
                    </Detail>
                    {d.dns.search.length > 0 && (
                      <Detail label="Search">{d.dns.search.join(", ")}</Detail>
                    )}
                    {d.dns.options.length > 0 && (
                      <Detail label="Options">
                        <span className="font-mono">{d.dns.options.join(" ")}</span>
                      </Detail>
                    )}
                  </DetailList>
                )}
              </PanelBody>
            </Panel>

            <Panel plain>
              <PanelHeader title="Listening" />
              <PanelBody>
                {d.listenersRead.state !== "ok" ? (
                  <ReadingWords reading={d.listenersRead} />
                ) : d.listeners.length === 0 ? (
                  <p className="text-body text-muted-foreground">Nothing listens inside it.</p>
                ) : (
                  <ul className="flex flex-wrap gap-1.5" aria-label="Listening sockets">
                    {d.listeners.map((l) => (
                      <li key={`${l.protocol}-${l.address}-${l.port}`}>
                        <Tag mono>
                          {l.protocol} {l.address.includes(":") ? `[${l.address}]` : l.address}:
                          {l.port}
                        </Tag>
                      </li>
                    ))}
                  </ul>
                )}
              </PanelBody>
            </Panel>
          </>
        )}
      </div>
    </SidePanel>
  )
}

/**
 * How this namespace's kernel would route to an address — asked, not sent:
 * the same lookup the Routing page makes of the host, inside the namespace.
 */
function RouteQuestion({ target }: { target: NamespaceTarget }) {
  const [address, setAddress] = useState("")
  const [answer, setAnswer] = useState<{ path?: NetworkPath; error?: string }>()
  const [busy, setBusy] = useState(false)
  return (
    <form
      className="mt-4 flex flex-col gap-3 border-t border-hairline pt-4"
      onSubmit={async (event) => {
        event.preventDefault()
        if (!address.trim() || busy) return
        setBusy(true)
        try {
          const path = await get<NetworkPath>(
            `/network/namespaces/${encodeURIComponent(target.name)}/lookup`,
            { kind: target.kind, target: address.trim() },
          )
          setAnswer({ path })
        } catch (err) {
          setAnswer({ error: errorMessage(err) })
        } finally {
          setBusy(false)
        }
      }}
    >
      <FieldRow>
        <Field
          label="Route to"
          htmlFor={`${target.name}-lookup`}
          hint="A literal address; the kernel is asked, nothing is sent"
        >
          <Input
            id={`${target.name}-lookup`}
            value={address}
            placeholder="1.1.1.1 or 2606:4700:4700::1111"
            onChange={(event) => setAddress(event.target.value)}
            className="font-mono"
          />
        </Field>
      </FieldRow>
      <Button
        type="submit"
        size="sm"
        variant="outline"
        className="self-start"
        disabled={!address.trim() || busy}
        pending={busy}
      >
        Ask
      </Button>
      {answer?.error && (
        <p role="alert" className="text-body text-destructive">
          {answer.error}
        </p>
      )}
      {answer?.path && (
        <p className="text-body" aria-label="Route answer">
          {answer.path.local ? (
            "Delivered inside the namespace."
          ) : (
            <>
              Leaves through <span className="font-mono">{answer.path.device}</span>
              {answer.path.gateway && (
                <>
                  {" "}
                  via <span className="font-mono">{answer.path.gateway}</span>
                </>
              )}
              {answer.path.source && (
                <>
                  , from <span className="font-mono">{answer.path.source}</span>
                </>
              )}
              .
            </>
          )}
        </p>
      )}
    </form>
  )
}
