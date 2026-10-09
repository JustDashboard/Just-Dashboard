"use client"

import type { NetworkEgressIdentity } from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Status } from "@/components/status-dot"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { SCOPE_WORD, identityVerdict } from "@/components/network/topology-reading"

/**
 * How this server leaves in each family, as the kernel answers it, and what
 * that says about the address the internet sees. The NIC's source and the
 * provider's identity are two columns because they are two facts: a private
 * source is translated to an address this host never sees, and a public one
 * may still sit behind a provider's NAT or firewall. Forwarding is per family
 * beside it, since a gateway can route one and not the other.
 */
export function EgressIdentityTable({ identity }: { identity: NetworkEgressIdentity[] }) {
  return (
    <Panel>
      <PanelHeader title="Ways out" />
      <PanelBody flush>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Family</TableHead>
              <TableHead>Leaves through</TableHead>
              <TableHead>Sends from</TableHead>
              <TableHead>The internet sees</TableHead>
              <TableHead>Forwarding</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {identity.map((id) => (
              <TableRow key={id.family} data-family={id.family}>
                <TableCell className="font-medium">
                  {id.family === "inet6" ? "IPv6" : "IPv4"}
                </TableCell>
                <TableCell>
                  {id.device ? (
                    <span className="font-mono">
                      {id.device}
                      {id.gateway && (
                        <span className="text-muted-foreground"> via {id.gateway}</span>
                      )}
                    </span>
                  ) : (
                    <span className="text-muted-foreground">
                      {id.public === "no_route" ? "no route" : "unknown"}
                    </span>
                  )}
                </TableCell>
                <TableCell>
                  {id.source ? (
                    <span>
                      <span className="font-mono">{id.source}</span>
                      <span className="text-muted-foreground"> · {SCOPE_WORD[id.sourceScope]}</span>
                    </span>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </TableCell>
                <TableCell className="max-w-[28rem] whitespace-normal" title={id.detail}>
                  <Status
                    tone={
                      id.public === "nic"
                        ? "running"
                        : id.public === "unknown"
                          ? "warning"
                          : id.public === "no_route"
                            ? "stopped"
                            : "notice"
                    }
                    label={identityVerdict(id)}
                  />
                  <p className="mt-1 text-hint text-muted-foreground">{id.error ?? id.detail}</p>
                </TableCell>
                <TableCell>
                  {id.forwardingError ? (
                    <span className="text-warning" title={id.forwardingError}>
                      unreadable
                    </span>
                  ) : id.forwarding ? (
                    "on"
                  ) : (
                    <span className="text-muted-foreground">off</span>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </PanelBody>
    </Panel>
  )
}
