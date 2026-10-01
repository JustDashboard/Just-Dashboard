"use client"

import { useMemo } from "react"
import { Box, Globe, Servers, type Icon } from "@/components/icons"
import { plural } from "@/lib/format"
import type { DbTopoEdge, DbTopoNode, DbTopology } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { ProductLogo } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { Status } from "@/components/status-dot"
import {
  Block,
  BlockLink,
  Quiet,
  Read,
  RowsSkeleton,
  staleOf,
} from "@/components/database/home/blocks"
import { read } from "@/components/database/home/read"
import { useDatabase } from "@/components/database/shell/database-context"
import { DATABASES_MAP_HREF } from "@/components/database/shell/routes"

const KIND: Record<DbTopoNode["kind"], { glyph: Icon; word: string }> = {
  database: { glyph: Box, word: "database" },
  deployment: { glyph: Box, word: "deployment" },
  container: { glyph: Box, word: "container" },
  host: { glyph: Servers, word: "this server" },
  remote: { glyph: Globe, word: "another machine" },
}

/** How a consumer is known to use the database, in the reader's words. */
const VIA: Record<string, string> = {
  binding: "bound to it",
  env: "holds its address",
  stack: "in its stack",
  network: "on its network",
  session: "connected now",
}

type Consumer = { node: DbTopoNode; edge: DbTopoEdge }

/**
 * What uses the database: the deployments bound to it, the containers that
 * hold its address, and whatever has a session open on it now — each drawn as
 * itself, with how it is known and how many sessions it holds. The whole
 * picture, for every database at once, is the map.
 */
export function UsedBy() {
  const { id, conn } = useDatabase()
  const topology = usePoll(
    (signal) =>
      read<DbTopology>(
        `/databases/${id}/consumers`,
        (answer) => Array.isArray(answer.nodes) && Array.isArray(answer.edges),
        undefined,
        signal,
      ),
    45_000,
    [id],
  )
  const consumers = useMemo<Consumer[]>(() => {
    const data = topology.data
    if (!data) return []
    const nodes = new Map(data.nodes.map((node) => [node.id, node]))
    return data.edges
      .flatMap((edge) => {
        const other = nodes.get(edge.from)?.connId === id ? edge.to : edge.from
        const node = nodes.get(other)
        return node && node.connId !== id ? [{ node, edge }] : []
      })
      .sort((a, b) => b.edge.sessions - a.edge.sessions || a.node.name.localeCompare(b.node.name))
  }, [topology.data, id])

  return (
    <Block
      title="Used by"
      stale={staleOf(topology)}
      actions={<BlockLink href={DATABASES_MAP_HREF}>Map</BlockLink>}
      bodyClassName="group-data-[plain]/panel:py-1"
    >
      <Read poll={topology} what="what uses it" skeleton={<RowsSkeleton />}>
        {() =>
          consumers.length === 0 ? (
            <Quiet className="py-3">
              Nothing is bound to {conn.name} and nothing has a session open on it. Link it to a
              deployment from that project&rsquo;s settings, or hand an application its address from
              Connect.
            </Quiet>
          ) : (
            <RowList>
              {consumers.map(({ node, edge }) => (
                <Row
                  key={node.id}
                  href={node.href}
                  leading={
                    <ProductLogo id={node.product} size="sm" fallback={KIND[node.kind].glyph} />
                  }
                  title={node.name}
                  subtitle={[
                    node.detail ?? KIND[node.kind].word,
                    ...edge.via.map((via) => VIA[via] ?? via),
                  ].join(" · ")}
                  trailing={
                    <>
                      {edge.status === "broken" && <Status tone="danger" label="broken" />}
                      {edge.status === "stale" && <Status tone="warning" label="stale" />}
                      {edge.status === "pending" && <Status tone="unknown" label="pending" />}
                      <span className="numeric text-hint text-muted-foreground">
                        {edge.sessions > 0 ? plural(edge.sessions, "session") : "no session"}
                      </span>
                    </>
                  }
                  className="py-2.5"
                />
              ))}
            </RowList>
          )
        }
      </Read>
    </Block>
  )
}
