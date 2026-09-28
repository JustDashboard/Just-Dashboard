"use client"

import { Plus, Trash } from "@/components/icons"
import type { StreamBalance, StreamServer, StreamSpec } from "@/lib/types"
import { Field, OptionList, OptionRow } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

type Pool = Pick<StreamSpec, "upstream" | "servers" | "balance" | "noRetry">

const BALANCES: { value: StreamBalance | "round-robin"; label: string; hint: string }[] = [
  {
    value: "round-robin",
    label: "In turn",
    hint: "Each new connection goes to the next server, in proportion to its weight.",
  },
  {
    value: "least-conn",
    label: "Least busy",
    hint: "Each new connection goes to the server with the fewest open, weighted.",
  },
  {
    value: "client-ip",
    label: "Same client",
    hint: "A client keeps reaching one server while the pool stays the same; changing the pool moves only a share of clients. nginx has no backup server here.",
  },
  {
    value: "random",
    label: "Random",
    hint: "A server picked at random, weighted. nginx has no backup server here.",
  },
]

/** A whole number typed into a server's option, or unset. */
function whole(text: string, min: number): number | undefined {
  const n = Number(text)
  return text.trim() !== "" && Number.isInteger(n) && n >= min ? n : undefined
}

/**
 * Where a stream forwards to: one server, or a pool nginx balances and fails
 * over. One server is all most streams need, so the pool's options appear
 * only once a second server is added — nginx weighs, counts and retries
 * nothing for a lone one, and the server folds its options away.
 */
export function StreamServers({
  pool,
  onChange,
  hint,
  placeholder,
  picker,
}: {
  pool: Pool
  onChange: (change: Pool) => void
  /** The field's hint while there is one server. */
  hint: React.ReactNode
  placeholder: string
  /** Beside the first server: a container to forward to. */
  picker?: React.ReactNode
}) {
  const servers: StreamServer[] = pool.servers ?? [{ address: pool.upstream }]
  const pooled = servers.length > 1
  const noBackup = pool.balance === "client-ip" || pool.balance === "random"
  const setServers = (next: StreamServer[]) =>
    onChange(
      next.length > 1
        ? { ...pool, upstream: next[0].address, servers: next }
        : { upstream: next[0].address, servers: undefined, balance: undefined, noRetry: undefined },
    )
  const update = (i: number, change: Partial<StreamServer>) =>
    setServers(servers.map((server, j) => (j === i ? { ...server, ...change } : server)))

  return (
    <div className="space-y-4">
      <Field
        label={pooled ? "Servers" : "Forward to"}
        htmlFor="stream-upstream"
        hint={
          pooled
            ? "Weight is each server’s share. After max fails failed connects inside the fail window, nginx leaves a server out for that long; a backup takes connections only while no other can."
            : hint
        }
      >
        <div className="space-y-2">
          <ol className="space-y-2">
            {servers.map((server, i) => (
              <li key={i} className="min-w-0 space-y-1.5">
                <div className="flex min-w-0 flex-col gap-2 sm:flex-row sm:items-center">
                  <div className="flex min-w-0 flex-1 items-center gap-1.5">
                    {pooled && (
                      <span className="numeric w-4 shrink-0 text-right text-hint text-muted-foreground">
                        {i + 1}
                      </span>
                    )}
                    <Input
                      id={i === 0 ? "stream-upstream" : undefined}
                      value={server.address}
                      onChange={(e) => update(i, { address: e.target.value })}
                      placeholder={placeholder}
                      aria-label={pooled ? `Server ${i + 1}` : undefined}
                      className="min-w-0 flex-1 font-mono text-xs"
                    />
                    {pooled && (
                      <IconAction
                        label={`Remove server ${i + 1}`}
                        className="text-muted-foreground hover:text-destructive"
                        onClick={() => setServers(servers.filter((_, j) => j !== i))}
                      >
                        <Trash />
                      </IconAction>
                    )}
                  </div>
                  {i === 0 && picker}
                </div>
                {pooled && (
                  <div className="flex min-w-0 flex-wrap items-center gap-1.5 pl-5.5">
                    <Input
                      value={server.weight ?? ""}
                      inputMode="numeric"
                      onChange={(e) => update(i, { weight: whole(e.target.value, 1) })}
                      placeholder="Weight 1"
                      aria-label={`Server ${i + 1} weight`}
                      className="w-24 font-mono text-xs"
                    />
                    <Input
                      value={server.maxFails ?? ""}
                      inputMode="numeric"
                      onChange={(e) => update(i, { maxFails: whole(e.target.value, 0) })}
                      placeholder="Max fails 1"
                      aria-label={`Server ${i + 1} max fails`}
                      className="w-28 font-mono text-xs"
                    />
                    <Input
                      value={server.failTimeout ?? ""}
                      inputMode="numeric"
                      onChange={(e) => update(i, { failTimeout: whole(e.target.value, 1) })}
                      placeholder="Window 10 s"
                      aria-label={`Server ${i + 1} fail window in seconds`}
                      className="w-28 font-mono text-xs"
                    />
                    <ToggleGroup
                      type="multiple"
                      value={[server.backup && "backup", server.down && "down"].filter(
                        (v): v is string => Boolean(v),
                      )}
                      onValueChange={(v) =>
                        update(i, {
                          backup: v.includes("backup") || undefined,
                          down: v.includes("down") || undefined,
                        })
                      }
                      variant="outline"
                      size="sm"
                      aria-label={`Server ${i + 1} state`}
                    >
                      <ToggleGroupItem
                        value="backup"
                        disabled={noBackup && !server.backup}
                        className="text-hint"
                      >
                        Backup
                      </ToggleGroupItem>
                      <ToggleGroupItem value="down" className="text-hint">
                        Down
                      </ToggleGroupItem>
                    </ToggleGroup>
                  </div>
                )}
              </li>
            ))}
          </ol>
          <Button
            size="xs"
            variant="outline"
            onClick={() => setServers([...servers, { address: "" }])}
          >
            <Plus />
            Server
          </Button>
        </div>
      </Field>

      {pooled && (
        <Field
          label="Balancing"
          hint={BALANCES.find((b) => b.value === (pool.balance ?? "round-robin"))?.hint}
        >
          <ToggleGroup
            type="single"
            value={pool.balance ?? "round-robin"}
            onValueChange={(v) =>
              v &&
              onChange({
                ...pool,
                balance: v === "round-robin" ? undefined : (v as StreamBalance),
              })
            }
            variant="outline"
            size="sm"
            className="w-full"
          >
            {BALANCES.map((b) => (
              <ToggleGroupItem key={b.value} value={b.value} className="flex-1 text-hint">
                {b.label}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
        </Field>
      )}

      {pooled && (
        <OptionList>
          <OptionRow
            title="Try the next server"
            hint="When a server does not accept, nginx hands the client to the next one instead of closing the connection. Turn it off for a backend where a connection must never be tried twice."
            checked={!pool.noRetry}
            onCheckedChange={(v) => onChange({ ...pool, noRetry: v ? undefined : true })}
          />
        </OptionList>
      )}
    </div>
  )
}
