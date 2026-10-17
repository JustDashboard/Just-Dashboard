"use client"

import { useId, useRef, useState } from "react"
import { Check, Copy, Layers, Plus, Trash } from "@/components/icons"
import { del, errorMessage, put } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { DbPooler, DbPoolerChange, DbPoolerStats } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { useCopy } from "@/hooks/use-copy"
import { usePoll } from "@/hooks/use-poll"
import { cn } from "@/lib/utils"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { Segments } from "@/components/deploy/settings/segments"
import {
  SettingForm,
  SettingSection,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { SettingPicture } from "@/components/deploy/settings/setting-picture"
import { WireHost, WireMark, WireNode } from "@/components/deploy/wire"
import { Field, FieldRow, FormFact, FormNote } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Detail, DetailList } from "@/components/page"
import { LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { AnimatedBeam } from "@/components/ui/animated-beam"
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
import { CouldNotRead } from "@/components/database/home/blocks"
import { read, record } from "@/components/database/home/read"
import { EngineGlyph, EngineMark } from "@/components/database/kit"
import { firewallOutcome } from "@/components/database/ops/settings-model"
import {
  AUTH_MODES,
  POOL_MODES,
  draftOfPooling,
  micros,
  poolTotals,
  poolerAddresses,
  poolerIsIdle,
  poolerReading,
  poolingChanges,
  poolingProblems,
  poolingSettings,
  restartsPooler,
  statTotals,
  type PoolingDraft,
} from "@/components/database/ops/settings-pooling-model"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * The pooler in front of a PostgreSQL server: PgBouncer, run by the dashboard
 * beside the database. One section turns it on and says how it behaves, one
 * lists where it is reached, and one reads its pools and statistics.
 *
 * Anyone may read it. Turning it on, changing it and turning it off are an
 * administrator's, because each one runs or replaces a container; turning it
 * off also needs the destructive capability, since it drops the connections
 * going through it.
 */
export function PoolingSections({ confirm }: { confirm: (request: ConfirmRequest) => void }) {
  const { id } = useDatabase()
  const poll = usePoll(
    (signal) =>
      read<DbPooler>(
        `/databases/${id}/pooler`,
        (answer) => record(answer.settings) && record(answer.limits),
        undefined,
        signal,
      ),
    5000,
    [id],
  )
  const pooler = poll.data
  if (!pooler) {
    return (
      <SettingSection id="pooler" title="Connection pooler">
        {poll.error ? (
          <CouldNotRead what="the pooler" error={poll.error} onRetry={poll.refresh} />
        ) : (
          <LoadingRows rows={3} />
        )}
      </SettingSection>
    )
  }
  return (
    <>
      <PoolerSection pooler={pooler} refresh={poll.refresh} confirm={confirm} />
      {pooler.enabled && <AddressesSection pooler={pooler} />}
      {pooler.enabled && <ActivitySection pooler={pooler} />}
    </>
  )
}

function PoolerSection({
  pooler,
  refresh,
  confirm,
}: {
  pooler: DbPooler
  refresh: () => void
  confirm: (request: ConfirmRequest) => void
}) {
  const { id, conn, engine, status } = useDatabase()
  const { can } = useAuth()
  const admin = can("system.admin")
  // A protected connection refuses every change to its pooler (one mode of it
  // writes to the database), for every role: the controls are not drawn.
  const locked = Boolean(conn.readOnly)
  const editable = admin && !locked
  const field = useId()
  const [edits, setEdits] = useState<Partial<PoolingDraft>>({})
  const [saving, setSaving] = useState(false)
  const [refusal, setRefusal] = useState<string>()

  const saved = pooler.enabled ? pooler.settings : pooler.defaults
  const draft: PoolingDraft = { ...draftOfPooling(saved), ...edits }
  const problems = poolingProblems(draft, pooler.limits)
  const invalid = Boolean(problems.poolSize || problems.maxClientConn)
  const changes = pooler.enabled ? poolingChanges(saved, draft) : 0
  const set = (patch: Partial<PoolingDraft>) => {
    setEdits((held) => ({ ...held, ...patch }))
    setRefusal(undefined)
  }
  const reading = poolerReading(pooler)

  const apply = async () => {
    const settings = poolingSettings(draft, pooler.limits)
    if (!settings) return false
    setSaving(true)
    setRefusal(undefined)
    try {
      const done = await put<DbPoolerChange>(`/databases/${id}/pooler`, settings)
      setEdits({})
      const said = done.firewall ? firewallOutcome(done.firewall, done.pooler.port) : ""
      if (done.firewallError) {
        notify.warning(`Pooling is on, and the firewall was not changed`, {
          description: done.firewallError,
        })
      } else {
        notify.success(
          pooler.enabled ? `Saved the pooler of ${conn.name}` : `Pooling is on for ${conn.name}`,
          { description: said || undefined },
        )
      }
      for (const warning of done.warnings) notify.warning(warning)
      refresh()
      status.refresh()
      return true
    } catch (err) {
      setRefusal(errorMessage(err))
      return false
    } finally {
      setSaving(false)
    }
  }

  const turnOff = () =>
    confirm({
      title: `Turn off pooling for ${conn.name}`,
      confirmLabel: "Turn off",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: conn.name,
        facts: (
          <FormFact label="Container" mono>
            {pooler.container}
          </FormFact>
        ),
      },
      description: (
        <>
          <p>
            Removes {pooler.container} and the private network it shares with the database. Every
            connection going through it is dropped, and a deployment that reads{" "}
            <span className="font-mono">{pooler.reference}</span> stops reaching the database until
            it is pointed at the plain reference.
          </p>
          <p>
            The database, its data and this connection are left as they are.
            {pooler.settings.authMode === "auth_query"
              ? " The account and function auth_query added to the database are removed."
              : ""}
          </p>
        </>
      ),
      action: async () => {
        await del(`/databases/${id}/pooler`)
        setEdits({})
        notify.success(`Pooling is off for ${conn.name}`)
        refresh()
        status.refresh()
      },
    })

  const state = pooler.enabled ? (
    <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
      <Status tone={reading.tone} label={reading.word} />
      <span className="font-mono text-hint text-muted-foreground">{pooler.image}</span>
    </span>
  ) : (
    <span className="flex">
      <Status tone="unknown" label="Off" />
    </span>
  )

  const fields = (
    <PoolerFields
      field={field}
      draft={draft}
      problems={problems}
      editable={editable}
      onChange={set}
    />
  )

  if (!pooler.enabled && !pooler.available) {
    return (
      <SettingSection id="pooler" title="Connection pooler" state={state}>
        <Notice tone="default" title="A pooler cannot be run for this connection">
          {pooler.reason}
        </Notice>
      </SettingSection>
    )
  }

  if (!pooler.enabled) {
    return (
      <SettingSection
        id="pooler"
        title="Connection pooler"
        state={state}
        actions={
          editable && (
            <Button
              size="sm"
              variant="outline"
              pending={saving}
              disabled={invalid}
              onClick={() => void apply()}
            >
              <Plus />
              Turn on
            </Button>
          )
        }
      >
        <PoolerPicture pooler={pooler} changing={saving} />
        <p className="text-body leading-relaxed text-muted-foreground">
          PgBouncer sits in front of the database and shares a few server connections between many
          clients, so an application that opens a connection per request does not use up the
          server&rsquo;s own. It runs in its own container on a private network with the database,
          and is published on this server only.
        </p>
        {fields}
        {refusal && (
          <FormNote tone="danger" role="alert">
            It was not turned on. {refusal}
          </FormNote>
        )}
        {locked && <ProtectedNotice />}
        {!locked && !admin && <FormNote>Turning it on is an administrator&rsquo;s to do.</FormNote>}
      </SettingSection>
    )
  }

  return (
    <SettingForm
      name="Connection pooler"
      onSave={apply}
      dirty={changes > 0}
      changes={changes}
      saving={saving}
      invalid={invalid}
      canEdit={editable}
      applies="immediately"
      onDiscard={() => {
        setEdits({})
        setRefusal(undefined)
      }}
      error={refusal && `It was not saved. ${refusal}`}
      note={
        restartsPooler(saved, draft) &&
        "Changing how clients authenticate restarts the pooler, which drops the connections going through it. Every other change is reloaded without dropping any."
      }
    >
      <SettingSection
        id="pooler"
        title="Connection pooler"
        state={state}
        status={settingStatus({ dirty: changes > 0, refused: changes > 0 && Boolean(refusal) })}
        actions={
          editable &&
          can("destructive") && (
            <Button size="sm" variant="outline" onClick={turnOff}>
              <Trash />
              Turn off
            </Button>
          )
        }
      >
        <PoolerPicture pooler={pooler} changing={saving} />
        {pooler.state === "missing" && (
          <Notice tone="warning" title="The pooler's container is gone">
            Something removed {pooler.container}.
            {editable && (
              <Button
                size="xs"
                variant="outline"
                className="mt-2 block"
                pending={saving}
                disabled={invalid}
                onClick={() => void apply()}
              >
                Build it again
              </Button>
            )}
          </Notice>
        )}
        {!pooler.inSync && pooler.state !== "missing" && (
          <Notice tone="warning" title="The pooler holds an old password">
            The connection&rsquo;s password has changed since the pooler was given it. It is written
            again within a minute.
            {editable && (
              <Button
                size="xs"
                variant="outline"
                className="mt-2 block"
                pending={saving}
                disabled={invalid}
                onClick={() => void apply()}
              >
                Write it now
              </Button>
            )}
          </Notice>
        )}
        {fields}
        {editable && !can("destructive") && (
          <FormNote>
            Turning it off drops the connections through it, which your role may not do.
          </FormNote>
        )}
        {locked && <ProtectedNotice />}
        {!locked && !admin && (
          <FormNote>Changing the pooler or turning it off is an administrator&rsquo;s.</FormNote>
        )}
      </SettingSection>
    </SettingForm>
  )
}

function ProtectedNotice() {
  return (
    <Notice tone="default" title="This connection is protected">
      The dashboard refuses every change to a protected connection, and a pooler is one: turning it
      on, changing it and turning it off wait until the protection is taken off under General. The
      figures below are still read.
    </Notice>
  )
}

/** The four settings. Read-only text for a role that may not change them. */
function PoolerFields({
  field,
  draft,
  problems,
  editable,
  onChange,
}: {
  field: string
  draft: PoolingDraft
  problems: ReturnType<typeof poolingProblems>
  editable: boolean
  onChange: (patch: Partial<PoolingDraft>) => void
}) {
  const mode = POOL_MODES.find((one) => one.value === draft.poolMode)
  const auth = AUTH_MODES.find((one) => one.value === draft.authMode)
  if (!editable) {
    return (
      <DetailList className="gap-y-2.5">
        <Detail label="Pool mode">{mode?.label}</Detail>
        <Detail label="Pool size" className="numeric">
          {draft.poolSize} per database and account
        </Detail>
        <Detail label="Clients" className="numeric">
          up to {draft.maxClientConn}
        </Detail>
        <Detail label="Authentication">{auth?.label}</Detail>
      </DetailList>
    )
  }
  return (
    <>
      <Field label="Pool mode" hint={mode?.hint}>
        <Segments
          label="Pool mode"
          value={draft.poolMode}
          onChange={(poolMode) => onChange({ poolMode })}
          options={POOL_MODES.map(({ value, label }) => ({ value, label }))}
        />
      </Field>
      <FieldRow>
        <Field
          label="Pool size"
          htmlFor={`${field}-size`}
          hint="Server connections kept for each database and account."
          error={problems.poolSize}
        >
          <Input
            id={`${field}-size`}
            inputMode="numeric"
            value={draft.poolSize}
            onChange={(event) => onChange({ poolSize: event.target.value })}
            autoComplete="off"
            className="numeric"
          />
        </Field>
        <Field
          label="Max clients"
          htmlFor={`${field}-clients`}
          hint="Clients that may be connected to the pooler at once."
          error={problems.maxClientConn}
        >
          <Input
            id={`${field}-clients`}
            inputMode="numeric"
            value={draft.maxClientConn}
            onChange={(event) => onChange({ maxClientConn: event.target.value })}
            autoComplete="off"
            className="numeric"
          />
        </Field>
      </FieldRow>
      <Field label="Authentication" hint={auth?.hint}>
        <Segments
          label="Authentication"
          value={draft.authMode}
          onChange={(authMode) => onChange({ authMode })}
          options={AUTH_MODES.map(({ value, label }) => ({
            value,
            label,
            mono: value === "auth_query",
          }))}
        />
      </Field>
      <p className="text-hint text-muted-foreground">
        The server&rsquo;s own limit is shared by every pool, so a pool size larger than{" "}
        <span className="font-mono">max_connections</span> divided by the number of pools only
        queues the difference.
      </p>
    </>
  )
}

/**
 * Applications, the pooler and the server, as the way a connection goes. The
 * beam stills while the pooler is not running or a change is being made.
 */
function PoolerPicture({ pooler, changing }: { pooler: DbPooler; changing: boolean }) {
  const { conn, engine, summary } = useDatabase()
  const container = useRef<HTMLDivElement>(null)
  const apps = useRef<HTMLDivElement>(null)
  const pool = useRef<HTMLDivElement>(null)
  const server = useRef<HTMLDivElement>(null)
  const running = pooler.enabled && pooler.state === "running"
  const bind = pooler.port
    ? `${pooler.exposure === "public" ? "0.0.0.0" : pooler.exposure === "private" ? "one address" : "127.0.0.1"}:${pooler.port}`
    : `:${pooler.linkedPort}`
  return (
    <SettingPicture
      label="How a connection goes through the pooler"
      containerRef={container}
      className="py-4"
      lines={
        <>
          <AnimatedBeam
            containerRef={container}
            fromRef={apps}
            toRef={pool}
            still={!running || changing}
            dashed={!pooler.enabled}
          />
          <AnimatedBeam
            containerRef={container}
            fromRef={pool}
            toRef={server}
            still={!running || changing}
            dashed={!pooler.enabled}
            delay={0.6}
          />
        </>
      }
      start={[
        <WireNode
          key="apps"
          nodeRef={apps}
          align="end"
          mark={<WireHost size="md" />}
          eyebrow="Applications"
          title="Programs and linked deployments"
          hint="many connections"
        />,
      ]}
      middle={
        <WireNode
          nodeRef={pool}
          align="center"
          mark={
            <WireMark size="md" tone={pooler.enabled ? "logo" : "neutral"}>
              <Layers />
            </WireMark>
          }
          eyebrow="PgBouncer"
          title={
            <span className="font-mono text-xs wrap-anywhere">
              {pooler.container ?? "not running"}
            </span>
          }
          hint={<span className="font-mono">{pooler.enabled ? bind : "off"}</span>}
        />
      }
      end={
        <WireNode
          nodeRef={server}
          align="start"
          mark={
            <WireMark size="md" tone="logo">
              <EngineGlyph engine={engine} />
            </WireMark>
          }
          eyebrow={`${engine.label}${summary?.versionNumber ? ` ${summary.versionNumber}` : ""}`}
          title={<span className="font-mono text-xs wrap-anywhere">{conn.name}</span>}
          hint="few connections"
        />
      }
    />
  )
}

/** Where the pooler is reached, and the reference a deployment holds for it. */
function AddressesSection({ pooler }: { pooler: DbPooler }) {
  const { conn } = useDatabase()
  const rows = poolerAddresses(pooler, conn.host || "127.0.0.1")
  return (
    <SettingSection
      id="addresses"
      title="Where it is reached"
      state={
        pooler.exposure === "public" ? (
          <span className="flex">
            <Status verdict="warning" label="Published to anywhere" />
          </span>
        ) : undefined
      }
    >
      <div className="divide-y divide-hairline" role="list" aria-label="Pooler addresses">
        {rows.map((row) => (
          <AddressRow key={row.id} label={row.label} value={row.address} />
        ))}
        <AddressRow label="Deployment reference" value={pooler.reference} />
      </div>
      <FormNote>
        The pooled string is the direct one with this address: the same account, database and
        options. <span className="font-medium text-foreground">Connect</span> at the top of the page
        shows either, with the password.{" "}
        {pooler.exposure === "public"
          ? "The pooler is published on every interface, because the database was opened to anywhere under Network; restricting it there brings the pooler back to this server."
          : "The pooler is published on this server only. Opening the database to anywhere under Network moves the pooler with it; nothing else does."}
      </FormNote>
    </SettingSection>
  )
}

function AddressRow({ label, value }: { label: string; value: string }) {
  const { copy, copied } = useCopy()
  return (
    <div role="listitem" className="flex min-w-0 items-center justify-between gap-3 py-2.5">
      <div className="min-w-0">
        <p className="text-hint text-muted-foreground">{label}</p>
        <p className="font-mono text-body wrap-anywhere">{value}</p>
      </div>
      <IconAction label={`Copy ${label.toLowerCase()}`} onClick={() => void copy(value, "Copied")}>
        {copied ? <Check /> : <Copy />}
      </IconAction>
    </div>
  )
}

/** SHOW POOLS and SHOW STATS, read while the page is open. */
function ActivitySection({ pooler }: { pooler: DbPooler }) {
  const { id } = useDatabase()
  const running = pooler.state === "running"
  const poll = usePoll(
    (signal) =>
      read<DbPoolerStats>(
        `/databases/${id}/pooler/stats`,
        (answer) => Array.isArray(answer.pools) && Array.isArray(answer.stats),
        undefined,
        signal,
      ),
    5000,
    [id],
    { enabled: running },
  )
  const stats = poll.data
  return (
    <SettingSection
      id="activity"
      title="Pools and statistics"
      state={stats?.version ? `PgBouncer ${stats.version}` : undefined}
    >
      {!running ? (
        <Notice tone="default" title="The pooler is not running">
          It stops with its database and starts with it. Its figures are read from the running
          pooler.
        </Notice>
      ) : !stats ? (
        poll.error ? (
          <CouldNotRead what="the pooler's statistics" error={poll.error} onRetry={poll.refresh} />
        ) : (
          <LoadingRows rows={4} />
        )
      ) : (
        <Figures stats={stats} />
      )}
    </SettingSection>
  )
}

function Figures({ stats }: { stats: DbPoolerStats }) {
  if (poolerIsIdle(stats)) {
    return (
      <p className="animate-rise text-body leading-relaxed text-muted-foreground">
        <span className="font-medium text-foreground">Nothing has gone through it yet.</span> Point
        an application at the pooled string and its pools appear here as clients connect.
      </p>
    )
  }
  const pools = poolTotals(stats.pools)
  const rates = statTotals(stats.stats)
  return (
    <div className="animate-rise space-y-6">
      <DetailList className="gap-y-2.5">
        <Detail label="Clients" className="numeric">
          {pools.clientsActive} active
          {pools.clientsWaiting > 0 ? `, ${pools.clientsWaiting} waiting` : ""}
        </Detail>
        <Detail label="Server connections" className="numeric">
          {pools.serversActive} active, {pools.serversIdle} idle
        </Detail>
        <Detail label="Queries" className="numeric">
          {rates.queriesPerSecond} a second · {plural(rates.queries, "query", "queries")} since it
          started
        </Detail>
        <Detail label="Transactions" className="numeric">
          {rates.transactionsPerSecond} a second
        </Detail>
        <Detail label="Query time" className="numeric">
          {micros(rates.queryTime)} on average
        </Detail>
        <Detail
          label="Wait for a connection"
          className={cn("numeric", pools.clientsWaiting > 0 && "text-warning")}
        >
          {micros(rates.waitTime)} on average
          {pools.maxWaitSec > 0 ? `, the longest now ${pools.maxWaitSec} s` : ""}
        </Detail>
      </DetailList>

      <div className="space-y-2">
        <h3 className="text-body font-medium">Pools</h3>
        {stats.pools.length === 0 ? (
          <p className="text-hint text-muted-foreground">No client is connected right now.</p>
        ) : (
          <Table aria-label="Pools">
            <TableHeader>
              <TableRow className="hover:bg-transparent">
                <TableHead>Database</TableHead>
                <TableHead className="px-2">Account</TableHead>
                <TableHead className="px-2 text-right">Clients</TableHead>
                <TableHead className="px-2 text-right">Servers</TableHead>
                <TableHead className="px-2 text-right max-sm:hidden">Longest wait</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {stats.pools.map((pool) => (
                <TableRow key={`${pool.database}:${pool.user}`}>
                  <TableCell className="py-2 font-mono">{pool.database}</TableCell>
                  <TableCell className="px-2 py-2 font-mono">{pool.user}</TableCell>
                  <TableCell className="numeric px-2 py-2 text-right max-sm:whitespace-normal">
                    {pool.clActive} active
                    {pool.clWaiting > 0 && (
                      <span className="text-warning">, {pool.clWaiting} waiting</span>
                    )}
                  </TableCell>
                  <TableCell className="numeric px-2 py-2 text-right max-sm:whitespace-normal">
                    {pool.svActive} active, {pool.svIdle + pool.svUsed} idle
                  </TableCell>
                  <TableCell className="numeric px-2 py-2 text-right max-sm:hidden">
                    {pool.maxWaitSec} s
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </div>

      <div className="space-y-2">
        <h3 className="text-body font-medium">Statistics</h3>
        <Table aria-label="Statistics">
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead>Database</TableHead>
              <TableHead className="px-2 text-right">Queries a second</TableHead>
              <TableHead className="px-2 text-right max-sm:hidden">Query time</TableHead>
              <TableHead className="px-2 text-right">Wait</TableHead>
              <TableHead className="px-2 text-right max-sm:hidden">Total queries</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {stats.stats.map((stat) => (
              <TableRow key={stat.database}>
                <TableCell className="py-2 font-mono">{stat.database}</TableCell>
                <TableCell className="numeric px-2 py-2 text-right">{stat.avgQueryCount}</TableCell>
                <TableCell className="numeric px-2 py-2 text-right max-sm:hidden">
                  {micros(stat.avgQueryTime)}
                </TableCell>
                <TableCell className="numeric px-2 py-2 text-right">
                  {micros(stat.avgWaitTime)}
                </TableCell>
                <TableCell className="numeric px-2 py-2 text-right max-sm:hidden">
                  {stat.totalQueryCount}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </div>
  )
}
