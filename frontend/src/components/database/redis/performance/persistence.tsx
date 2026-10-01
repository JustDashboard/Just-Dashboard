"use client"

import { useEffect, useState } from "react"
import { errorMessage } from "@/lib/api"
import { bytes, duration, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { Detail, DetailList } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, LoadingPanel } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { redisSave } from "@/components/database/redis/api"
import { scheduleWords } from "@/components/database/redis/performance/schedule"
import { ReadError } from "@/components/database/redis/read-error"
import type { Redis } from "@/components/database/redis/use-redis"

/**
 * How the data survives: what is written to disk, and who else holds a copy.
 *
 * A server keeps its keys in memory; a snapshot and the append-only file are
 * the two ways it writes them down, and replicas are the copies kept
 * elsewhere. Each is stated as the server reports it, with the one thing to
 * do about it — save now, rewrite the file — beside the facts it changes.
 */
export function PersistenceView({ redis }: { redis: Redis }) {
  const { id, server, engine, canWrite } = redis
  const [asked, setAsked] = useState<"bgsave" | "bgrewriteaof" | null>(null)
  const refresh = server.refresh
  const data = server.data
  const working = Boolean(
    data?.persistence.rdb.inProgress || data?.persistence.aof.rewriteInProgress,
  )

  // The server says when a save has finished: while one is in flight it is
  // asked every second instead of every fifteen.
  useEffect(() => {
    if (!working && !asked) return
    const timer = setInterval(refresh, 1000)
    return () => clearInterval(timer)
  }, [working, asked, refresh])

  const save = async (mode: "bgsave" | "bgrewriteaof") => {
    setAsked(mode)
    try {
      const answer = await redisSave(id, mode)
      notify.success(mode === "bgsave" ? "Snapshot started" : "Rewrite started", {
        description: answer.status,
      })
      refresh()
    } catch (err) {
      notify.error(
        mode === "bgsave" ? "Could not start a snapshot" : "Could not start a rewrite",
        err,
      )
    } finally {
      // Held a moment longer: the server takes a beat to report it has begun.
      setTimeout(() => setAsked(null), 2500)
    }
  }

  if (server.error && !data) return <ReadError error={server.error} onRetry={refresh} />
  if (!data) return <LoadingPanel plain rows={6} />

  const { rdb, aof } = data.persistence
  const replication = data.replication
  const saving = rdb.inProgress || asked === "bgsave"
  const rewriting = aof.rewriteInProgress || asked === "bgrewriteaof"
  const file = [data.persistence.dir, data.persistence.file].filter(Boolean).join("/")

  return (
    <div className="space-y-8">
      {server.error && (
        <p role="status" className="text-hint text-warning">
          The server could not be read again just now: {errorMessage(server.error)}
        </p>
      )}
      <div className="grid items-start gap-8 lg:grid-cols-2 [&>*]:min-w-0">
        {engine.can("persistence") && (
          <Panel plain>
            <PanelHeader
              className="group-data-[plain]/panel:min-h-9"
              title="Snapshots"
              actions={
                canWrite && (
                  <Button
                    size="xs"
                    variant="outline"
                    pending={saving}
                    onClick={() => void save("bgsave")}
                  >
                    Save now
                  </Button>
                )
              }
            />
            <PanelBody>
              <DetailList>
                <Detail label="State">
                  {saving ? (
                    <TextShimmer className="text-xs font-medium">Saving a snapshot…</TextShimmer>
                  ) : rdb.lastStatus && rdb.lastStatus !== "ok" ? (
                    <Status tone="danger" label={`The last save failed: ${rdb.lastStatus}`} />
                  ) : (
                    <Status tone="running" label="The last save succeeded" />
                  )}
                </Detail>
                <Detail label="Last saved">
                  {rdb.lastSaveAt ? (
                    <span title={timestamp(rdb.lastSaveAt)}>{relativeTime(rdb.lastSaveAt)}</span>
                  ) : (
                    "never"
                  )}
                  {rdb.lastDurationSeconds !== undefined && rdb.lastSaveAt
                    ? ` · took ${rdb.lastDurationSeconds < 1 ? "under a second" : duration(rdb.lastDurationSeconds)}`
                    : ""}
                </Detail>
                <Detail label="Changed since" className="numeric">
                  {rdb.changesSinceSave.toLocaleString()}{" "}
                  {rdb.changesSinceSave === 1 ? "key" : "keys"}
                </Detail>
                <Detail label="Saves by itself">
                  {rdb.schedule === undefined ? (
                    <span className="text-muted-foreground">the server does not say</span>
                  ) : rdb.schedule === "" ? (
                    "never: snapshots are off"
                  ) : (
                    <span className="block space-y-0.5">
                      {scheduleWords(rdb.schedule).map((line) => (
                        <span key={line} className="block">
                          {line}
                        </span>
                      ))}
                    </span>
                  )}
                </Detail>
                {file && (
                  <Detail label="Written to" className="font-mono break-all">
                    {file}
                  </Detail>
                )}
              </DetailList>
            </PanelBody>
          </Panel>
        )}

        {engine.can("persistence") && aof.supported && (
          <Panel plain>
            <PanelHeader
              className="group-data-[plain]/panel:min-h-9"
              title="Append-only file"
              actions={
                canWrite &&
                engine.can("aofRewrite") &&
                data.features.aof &&
                aof.enabled && (
                  <Button
                    size="xs"
                    variant="outline"
                    pending={rewriting}
                    onClick={() => void save("bgrewriteaof")}
                  >
                    Rewrite now
                  </Button>
                )
              }
            />
            <PanelBody>
              <DetailList>
                <Detail label="State">
                  {rewriting ? (
                    <TextShimmer className="text-xs font-medium">Rewriting the file…</TextShimmer>
                  ) : !aof.enabled ? (
                    <Status tone="stopped" label="Off: only snapshots are kept" />
                  ) : aof.lastWriteStatus && aof.lastWriteStatus !== "ok" ? (
                    <Status tone="danger" label={`The last write failed: ${aof.lastWriteStatus}`} />
                  ) : (
                    <Status tone="running" label="On: every write is appended" />
                  )}
                </Detail>
                {aof.enabled && aof.fsync && <Detail label="Flushed to disk">{aof.fsync}</Detail>}
                {aof.enabled && aof.currentSize !== undefined && (
                  <Detail label="Size" className="numeric">
                    {bytes(aof.currentSize)}
                    {aof.baseSize !== undefined
                      ? ` · ${bytes(aof.baseSize)} after the last rewrite`
                      : ""}
                  </Detail>
                )}
                {aof.lastRewriteStatus && (
                  <Detail label="Last rewrite">{aof.lastRewriteStatus}</Detail>
                )}
              </DetailList>
            </PanelBody>
          </Panel>
        )}
      </div>

      {engine.can("replication") && (
        <Panel plain>
          <PanelHeader title="Replication" />
          <PanelBody className="space-y-4">
            <DetailList>
              <Detail label="This server is">
                {replication.role === "primary"
                  ? `a primary with ${replication.replicas.length.toLocaleString()} ${replication.replicas.length === 1 ? "replica" : "replicas"}`
                  : "a replica"}
              </Detail>
              <Detail label="Replication offset" className="numeric">
                {replication.offset.toLocaleString()}
              </Detail>
              {replication.primary && (
                <>
                  <Detail label="Follows" className="font-mono">
                    {replication.primary.addr}
                  </Detail>
                  <Detail label="Link">
                    <Status
                      tone={replication.primary.up ? "running" : "danger"}
                      label={
                        replication.primary.syncing
                          ? "Syncing from the primary"
                          : replication.primary.up
                            ? `Up · heard from ${duration(replication.primary.lastIoSecondsAgo)} ago`
                            : "Down"
                      }
                    />
                  </Detail>
                  <Detail label="Accepts writes">
                    {replication.primary.readOnly ? "no: it is read-only" : "yes"}
                  </Detail>
                </>
              )}
              {data.cluster && (
                <Detail label="Cluster">
                  {data.cluster.state} · {data.cluster.knownNodes.toLocaleString()} nodes ·{" "}
                  {data.cluster.slotsAssigned.toLocaleString()} slots assigned
                </Detail>
              )}
            </DetailList>
            {replication.role === "primary" &&
              (replication.replicas.length === 0 ? (
                <EmptyNote className="px-0 text-left">
                  No replica follows this server: the only copy of its data is its own.
                </EmptyNote>
              ) : (
                <div className="group-data-[plain]/panel:-mx-4">
                  <Table>
                    <TableHeader>
                      <TableRow className="hover:bg-transparent">
                        <TableHead>Replica</TableHead>
                        <TableHead>State</TableHead>
                        <TableHead className="text-right">Offset</TableHead>
                        <TableHead className="text-right">Behind by</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {replication.replicas.map((replica) => (
                        <TableRow key={replica.addr}>
                          <TableCell className="font-mono">{replica.addr}</TableCell>
                          <TableCell>
                            <Status
                              tone={replica.state === "online" ? "running" : "warning"}
                              label={replica.state}
                            />
                          </TableCell>
                          <TableCell className="numeric text-right">
                            {replica.offset.toLocaleString()}
                          </TableCell>
                          <TableCell className="numeric text-right text-muted-foreground">
                            {(replication.offset - replica.offset).toLocaleString()} bytes ·{" "}
                            {duration(replica.lagSeconds)}
                          </TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                </div>
              ))}
          </PanelBody>
        </Panel>
      )}
    </div>
  )
}
