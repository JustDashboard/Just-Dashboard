"use client"

import { useState } from "react"
import { Cross } from "@/components/icons"
import { bytes, duration } from "@/lib/format"
import { LANES, hueFor } from "@/lib/hue"
import { usePoll } from "@/hooks/use-poll"
import { useConfirm } from "@/components/confirm-dialog"
import { FormFact } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { SearchInput } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, LoadingPanel } from "@/components/state"
import { Tag } from "@/components/tag"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { EngineMark } from "@/components/database/kit"
import { redisClients, redisKillClient } from "@/components/database/redis/api"
import { ReadError } from "@/components/database/redis/read-error"
import type { RedisClientInfo } from "@/components/database/redis/types"
import type { Redis } from "@/components/database/redis/use-redis"

/** The server's flag letters that say something a reader would act on. */
const FLAGS: Record<string, string> = {
  S: "replica",
  M: "primary",
  P: "pub/sub",
  x: "in a transaction",
  b: "blocked",
  O: "monitor",
  r: "read-only",
  c: "closing",
  u: "unblocked",
}

function flagWords(client: RedisClientInfo): string[] {
  return [...client.flags].map((flag) => FLAGS[flag]).filter(Boolean)
}

/**
 * Who is connected: where from, as whom, doing what, for how long.
 *
 * Each request the dashboard makes opens a short connection of its own, so
 * its reads pass through this list; the one this very request used is marked
 * and cannot be disconnected from here. Disconnecting a client is a removal
 * on the server's side and is offered to a role that may remove things — on
 * a protected connection too, since it changes no data.
 */
export function ClientsView({ redis }: { redis: Redis }) {
  const { id, conn, engine } = redis
  const { confirm, dialog } = useConfirm()
  const clients = usePoll((signal) => redisClients(id, signal), 5000, [id])
  const [filter, setFilter] = useState("")
  const mayKill = redis.canKill

  if (clients.error && !clients.data) {
    return <ReadError error={clients.error} onRetry={clients.refresh} />
  }
  if (!clients.data) return <LoadingPanel plain rows={5} />

  const wanted = filter.trim().toLowerCase()
  const shown = clients.data.filter(
    (client) =>
      !wanted ||
      `${client.addr} ${client.name} ${client.user ?? ""} ${client.command} ${client.library ?? ""}`
        .toLowerCase()
        .includes(wanted),
  )

  const kill = (client: RedisClientInfo) =>
    confirm({
      title: "Disconnect client",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono">{client.name || client.addr}</span>,
        facts: (
          <>
            <FormFact label="From" mono>
              {client.addr}
            </FormFact>
            <FormFact label="On">{conn.name}</FormFact>
            {client.user && <FormFact label="As">{client.user}</FormFact>}
          </>
        ),
      },
      description:
        "The server closes this connection. What the client was in the middle of is lost; a client that reconnects will come back.",
      confirmLabel: "Disconnect",
      action: async () => {
        await redisKillClient(id, client.id)
      },
      onDone: clients.refresh,
    })

  return (
    <Panel plain>
      {dialog}
      <PanelHeader
        title="Connected clients"
        actions={
          <SearchInput
            dense
            aria-label="Filter the clients"
            placeholder="Filter by address, name or command"
            value={filter}
            containerClassName="sm:w-64"
            onChange={(event) => setFilter(event.target.value)}
          />
        }
      />
      {clients.error && (
        <p role="status" className="pt-3 text-hint text-warning">
          The list could not be read again just now: {clients.error.message}
        </p>
      )}
      <PanelBody flush className="group-data-[plain]/panel:-mx-4">
        {shown.length === 0 ? (
          <EmptyNote>{wanted ? `No client matches ${filter}.` : "Nobody is connected."}</EmptyNote>
        ) : (
          <Table>
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>Client</TableHead>
                <TableHead>As</TableHead>
                <TableHead>Last command</TableHead>
                <TableHead className="text-right">Database</TableHead>
                <TableHead className="text-right">Connected</TableHead>
                <TableHead className="text-right">Idle</TableHead>
                <TableHead className="text-right">Memory</TableHead>
                <TableHead>
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {shown.map((client) => (
                <TableRow key={client.id}>
                  <TableCell>
                    <span className="flex items-center gap-2">
                      <span className="font-mono">{client.addr}</span>
                      {client.name && (
                        <span
                          className="font-medium"
                          style={{ color: hueFor(client.name.toLowerCase(), LANES) }}
                        >
                          {client.name}
                        </span>
                      )}
                      {client.self && <Tag>this dashboard</Tag>}
                      {flagWords(client).map((word) => (
                        <Tag key={word}>{word}</Tag>
                      ))}
                    </span>
                  </TableCell>
                  <TableCell className="font-mono text-muted-foreground">
                    {client.user ?? "—"}
                  </TableCell>
                  <TableCell className="font-mono">{client.command || "—"}</TableCell>
                  <TableCell className="numeric text-right">db{client.db}</TableCell>
                  <TableCell className="numeric text-right text-muted-foreground">
                    {duration(client.ageSeconds)}
                  </TableCell>
                  <TableCell className="numeric text-right text-muted-foreground">
                    {duration(client.idleSeconds)}
                  </TableCell>
                  <TableCell className="numeric text-right text-muted-foreground">
                    {bytes(client.totalMemory)}
                  </TableCell>
                  <TableCell className="py-1 text-right">
                    {mayKill && engine.can("kill") && !client.self && (
                      <IconAction
                        label={`Disconnect ${client.name || client.addr}`}
                        onClick={() => kill(client)}
                      >
                        <Cross />
                      </IconAction>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </PanelBody>
    </Panel>
  )
}
