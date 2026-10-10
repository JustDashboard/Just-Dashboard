"use client"

import Link from "next/link"
import { bytes, plural } from "@/lib/format"
import type { NetworkFlowEdge, NetworkTopologyFlows } from "@/lib/types"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Notice } from "@/components/state"
import { Tag } from "@/components/tag"

export type FlowNode = { label: string; href?: string }

const TRANSLATION_WORD: Record<NetworkFlowEdge["translation"], string> = {
  none: "routed as is",
  masquerade: "masqueraded",
  forward: "port forward",
}

/**
 * The topology's wires as traffic actually crosses them: which node started
 * flows with which, through which translation, hop by hop. Read from the
 * kernel's connection tracking at the moment of the Overview's read — the
 * flows open now, not a capture and not a history — and limited to this
 * host: nothing here discovers a switch beyond it.
 */
export function FlowPaths({
  flows,
  nodes,
  limit = 8,
}: {
  flows: NetworkTopologyFlows
  nodes: (id: string) => FlowNode
  limit?: number
}) {
  const shown = flows.edges.slice(0, limit)
  return (
    <Panel plain>
      <PanelHeader
        title="Paths in use"
        actions={
          flows.state === "ok" ? (
            <span className="text-hint text-muted-foreground">
              {plural(flows.classified, "tracked flow")}
              {flows.truncated ? " · first 65,536 read" : ""}
            </span>
          ) : undefined
        }
      />
      <PanelBody>
        {flows.state === "failed" ? (
          <Notice tone="warning" title="Connection tracking could not be read">
            <p>{flows.error}</p>
            <p className="mt-1">The wires above still show each device&rsquo;s traffic.</p>
          </Notice>
        ) : flows.state === "unavailable" ? (
          <p className="text-body text-muted-foreground">
            This host keeps no connection-tracking table, so which nodes trade traffic is not known
            here.
          </p>
        ) : shown.length === 0 ? (
          <p className="text-body text-muted-foreground">
            No tracked flow crosses between the nodes above right now.
          </p>
        ) : (
          <ul className="flex flex-col divide-y divide-hairline" aria-label="Paths in use">
            {shown.map((edge) => {
              const from = nodes(edge.from)
              const to = nodes(edge.to)
              return (
                <li
                  key={`${edge.from}>${edge.to}>${edge.translation}`}
                  className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1 py-2.5 text-body"
                >
                  <span className="min-w-0 font-medium">
                    <NodeName node={from} /> <span className="text-muted-foreground">→</span>{" "}
                    <NodeName node={to} />
                  </span>
                  <span className="numeric text-hint text-muted-foreground">
                    {plural(edge.flows, "flow")}
                    {flows.accounting && edge.bytes ? ` · ${bytes(edge.bytes)}` : ""}
                  </span>
                  <span className="flex gap-1.5">
                    {edge.protocols.map((p) => (
                      <Tag key={p}>{p}</Tag>
                    ))}
                    <Tag>{TRANSLATION_WORD[edge.translation]}</Tag>
                  </span>
                  <span className="w-full truncate font-mono text-hint text-muted-foreground">
                    {edge.path.join(" → ")}
                  </span>
                </li>
              )
            })}
          </ul>
        )}
        {flows.state === "ok" && !flows.accounting && shown.length > 0 && (
          <p className="mt-3 text-hint text-muted-foreground">
            Counts are flows open at the moment of reading; the kernel is not counting their bytes
            (net.netfilter.nf_conntrack_acct is off).
          </p>
        )}
      </PanelBody>
    </Panel>
  )
}

function NodeName({ node }: { node: FlowNode }) {
  return node.href ? (
    <Link href={node.href} className="underline-offset-2 focus-ring hover:underline">
      {node.label}
    </Link>
  ) : (
    <span>{node.label}</span>
  )
}
