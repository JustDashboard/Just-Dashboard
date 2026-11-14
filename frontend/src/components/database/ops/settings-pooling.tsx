"use client"

import { useId, useRef, useState } from "react"
import { Check, Copy, Layers, Plus, Trash } from "@/components/icons"
import { del, errorMessage, put } from "@/lib/api"
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
import { WireHost, WireLabel, WireMark, WireNode } from "@/components/deploy/wire"
import { Field, FieldRow, FormFact, FormNote, InfoTip } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Detail, DetailList } from "@/components/page"
import { LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { CouldNotRead } from "@/components/database/home/blocks"
import { read, record } from "@/components/database/home/read"
import { EngineGlyph, EngineMark } from "@/components/database/kit"
import { firewallOutcome } from "@/components/database/ops/settings-model"
import {
  AUTH_MODES,
  POOL_MODES,
  draftOfPooling,
  micros,
  poolerAddresses,
  poolerCaptions,
  poolerReading,
  poolingChanges,
  poolingProblems,
  poolingSettings,
  restartsPooler,
  type PoolingDraft,
} from "@/components/database/ops/settings-pooling-model"
import { usePendingReport } from "@/components/database/ops/settings-pending"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * The pooler in front of a PostgreSQL server: PgBouncer, run by the dashboard
 * beside the database. One section turns it on and says how it behaves, with
 * its figures on the picture of the way a connection goes; one lists where it
 * is reached; and one reads its pools, each on a line.
 *
 * The figures are read once for the page (`SHOW POOLS` and `SHOW STATS`, while
 * it is open) and drawn where they belong: who is connected and who waits on
 * the line in, the server connections on the line out, how long a client
 * waits at the pooler itself. They were a list of six facts and two tables
 * under a heading of their own, and the second table repeated the list.
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
  const running = Boolean(pooler?.enabled && pooler.state === "running")
  const figures = usePoll(
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
  const stats = running ? figures.data : undefined
  return (
    <>
      <PoolerSection
        pooler={pooler}
        stats={stats}
        statsError={running && !stats ? figures.error : undefined}
        onStatsRetry={figures.refresh}
        refresh={poll.refresh}
        confirm={confirm}
      />
      {pooler.enabled && <AddressesSection pooler={pooler} />}
      {pooler.enabled && stats && <PoolsSection stats={stats} />}
    </>
  )
}

function PoolerSection({
  pooler,
  stats,
  statsError,
  onStatsRetry,
  refresh,
  confirm,
}: {
  pooler: DbPooler
  /** What the running pooler reports; nothing while it is off, stopped or being read. */
  stats: DbPoolerStats | undefined
  statsError: Error | undefined
  onStatsRetry: () => void
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
  usePendingReport("pooler", {
    edits: changes > 0 ? [{ name: "Pooler", state: "changed" }] : [],
  })

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
      {pooler.state !== "running" && pooler.state !== "missing" && (
        <span>starts with its database</span>
      )}
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
          PgBouncer shares a few server connections between many clients, in a container of its own
          beside the database.
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
        <PoolerPicture pooler={pooler} stats={stats} changing={saving} />
        {statsError && (
          <CouldNotRead what="the pooler's statistics" error={statsError} onRetry={onStatsRetry} />
        )}
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
    <FormNote data-slot="pooler-protected">
      <span className="font-medium text-foreground">This connection is protected.</span> Its pooler
      is turned on, changed or turned off once the protection is taken off under General; its
      figures are still read.
    </FormNote>
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
      <Field label="Pool mode" hint={mode?.line} info={mode?.hint}>
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
          info={
            <>
              Server connections kept for each database and account. The server&rsquo;s own limit is
              shared by every pool, so a pool size larger than{" "}
              <span className="font-mono">max_connections</span> divided by the number of pools only
              queues the difference.
            </>
          }
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
          info="Clients that may be connected to the pooler at once."
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
      <Field label="Authentication" info={auth?.hint}>
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
    </>
  )
}

/**
 * Applications, the pooler and the server, as the way a connection goes, with
 * the pooler's figures where they are true: the clients connected and the
 * ones waiting on the line in, the server connections in use and idle on the
 * line out, the wait for one at the pooler, the work on the applications and
 * what it costs on the server. On a phone the lines run down the marks and
 * have no room for words, so the two lines' figures join the captions beside
 * them. The beam stills while the pooler is not running or a change is being
 * made.
 */
function PoolerPicture({
  pooler,
  stats,
  changing,
}: {
  pooler: DbPooler
  stats?: DbPoolerStats
  changing: boolean
}) {
  const { conn, engine, summary } = useDatabase()
  const container = useRef<HTMLDivElement>(null)
  const apps = useRef<HTMLDivElement>(null)
  const pool = useRef<HTMLDivElement>(null)
  const server = useRef<HTMLDivElement>(null)
  const running = pooler.enabled && pooler.state === "running"
  const bind = pooler.port
    ? `${pooler.exposure === "public" ? "0.0.0.0" : pooler.exposure === "private" ? "one address" : "127.0.0.1"}:${pooler.port}`
    : `:${pooler.linkedPort}`
  const says = stats ? poolerCaptions(stats) : undefined
  const unused = running && stats !== undefined && !says
  return (
    <SettingPicture
      label="How a connection goes through the pooler"
      containerRef={container}
      // Room under the row for a running pooler's caption, which carries its wait as well as its
      // binding: kept from the first paint, so the figures landing move nothing.
      className={running ? "py-4 lg:pb-12" : "py-4"}
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
      startLabel={
        says ? (
          <>
            <WireLabel lit={running} className="bottom-1/2 mb-2">
              {says.clients}
            </WireLabel>
            {says.waiting && (
              <WireLabel lit className="top-1/2 mt-2 text-warning">
                {says.waiting}
              </WireLabel>
            )}
          </>
        ) : (
          unused && (
            <WireLabel lit={false} className="bottom-1/2 mb-2">
              no client yet
            </WireLabel>
          )
        )
      }
      endLabel={
        says && (
          <>
            <WireLabel lit={running} className="bottom-1/2 mb-2">
              {says.active}
            </WireLabel>
            <WireLabel lit={false} className="top-1/2 mt-2">
              {says.idle}
            </WireLabel>
          </>
        )
      }
      start={[
        <WireNode
          key="apps"
          nodeRef={apps}
          align="end"
          mark={<WireHost size="md" />}
          eyebrow="Applications"
          title="Programs and linked deployments"
          hint={
            says ? (
              <span className="numeric" data-slot="pooler-work">
                <span className="lg:hidden">
                  {says.clients}
                  {says.waiting && <span className="text-warning">, {says.waiting}</span>}
                  {" · "}
                </span>
                {says.work}
              </span>
            ) : unused ? (
              "nothing has gone through it yet"
            ) : (
              "many connections"
            )
          }
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
          eyebrow={stats?.version ? `PgBouncer ${stats.version}` : "PgBouncer"}
          title={
            <span className="font-mono text-xs wrap-anywhere">
              {pooler.container ?? "not running"}
            </span>
          }
          hint={
            <>
              <span className="font-mono">{pooler.enabled ? bind : "off"}</span>
              {says && (
                <span
                  className={cn("numeric block", says.waiting && "text-warning")}
                  data-slot="pooler-wait"
                >
                  {says.wait}
                  {says.longest && <span className="block">{says.longest}</span>}
                </span>
              )}
            </>
          }
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
          hint={
            says ? (
              <span className="numeric" data-slot="pooler-cost">
                <span className="lg:hidden">
                  {says.active}, {says.idle}
                  {" · "}
                </span>
                {says.cost}
              </span>
            ) : (
              "few connections"
            )
          }
        />
      }
    />
  )
}

/** Where the pooler is reached, and the reference a deployment holds for it. */
function AddressesSection({ pooler }: { pooler: DbPooler }) {
  const { conn } = useDatabase()
  const rows = poolerAddresses(pooler, conn.host || "127.0.0.1")
  const anywhere = pooler.exposure === "public"
  return (
    <SettingSection
      id="addresses"
      title="Where it is reached"
      state={
        <span className="flex items-center gap-1.5">
          {anywhere ? (
            <Status verdict="warning" label="Published to anywhere" />
          ) : (
            "Published on this server only"
          )}
          <InfoTip label="Why it is published there">
            {anywhere
              ? "The pooler is published on every interface because the database was opened to anywhere under Network. Restricting it there brings the pooler back to this server."
              : "Opening the database to anywhere under Network moves the pooler with it; nothing else does."}
          </InfoTip>
        </span>
      }
    >
      <div className="divide-y divide-hairline" role="list" aria-label="Pooler addresses">
        {rows.map((row) => (
          <AddressRow
            key={row.id}
            label={row.label}
            caption={
              row.id === "host"
                ? "the direct string with this address; Connect shows it with the password"
                : undefined
            }
            value={row.address}
          />
        ))}
        <AddressRow label="Deployment reference" value={pooler.reference} />
      </div>
    </SettingSection>
  )
}

function AddressRow({ label, caption, value }: { label: string; caption?: string; value: string }) {
  const { copy, copied } = useCopy()
  return (
    <div role="listitem" className="flex min-w-0 items-center justify-between gap-3 py-2.5">
      <div className="min-w-0">
        <p className="truncate text-hint text-muted-foreground" title={caption}>
          {label}
          {caption && ` · ${caption}`}
        </p>
        <p className="font-mono text-body wrap-anywhere">{value}</p>
      </div>
      <IconAction label={`Copy ${label.toLowerCase()}`} onClick={() => void copy(value, "Copied")}>
        {copied ? <Check /> : <Copy />}
      </IconAction>
    </div>
  )
}

/** The columns of a pool's line from `sm`, where there is room to read down them. */
const POOL_COLUMNS = "sm:grid sm:grid-cols-[minmax(0,1fr)_9.5rem_9.5rem_5.5rem] sm:gap-x-4"

/**
 * The pools, one a line: the database and the account, its clients, its
 * server connections, the longest a client has waited. Read down their
 * columns from `sm`; on a phone each line says its three readings in words
 * under its name, where a table lost two of its columns off the edge.
 *
 * A server whose pooler fronts more than one database lists what each has
 * done under them. With one, those are the figures on the picture above.
 */
function PoolsSection({ stats }: { stats: DbPoolerStats }) {
  if (stats.pools.length === 0) return null
  return (
    <SettingSection
      id="pools"
      title="Pools"
      state={
        <span className="numeric">
          {stats.pools.length === 1 ? "1 pool" : `${stats.pools.length} pools`}, each a database and
          an account
        </span>
      }
    >
      <div>
        <div
          aria-hidden
          className={cn("eyebrow hidden border-b border-hairline pb-2 text-right", POOL_COLUMNS)}
        >
          <span className="text-left">Pool</span>
          <span>Clients</span>
          <span>Servers</span>
          <span>Longest wait</span>
        </div>
        <ul aria-label="Pools" className="divide-y divide-hairline">
          {stats.pools.map((pool) => (
            <li
              key={`${pool.database}:${pool.user}`}
              data-pool={`${pool.database}:${pool.user}`}
              className={cn("flex min-w-0 flex-wrap items-baseline gap-x-2 py-2.5", POOL_COLUMNS)}
            >
              <span className="w-full min-w-0 truncate font-mono text-body sm:w-auto">
                {pool.database}
                <span className="text-muted-foreground"> · {pool.user}</span>
              </span>
              <span className="numeric text-hint text-muted-foreground sm:text-right sm:text-body sm:text-foreground">
                {pool.clActive} active
                {pool.clWaiting > 0 && (
                  <span className="text-warning">, {pool.clWaiting} waiting</span>
                )}
                <span className="sm:hidden"> clients ·</span>
              </span>
              <span className="numeric text-hint text-muted-foreground sm:text-right sm:text-body sm:text-foreground">
                {pool.svActive} active, {pool.svIdle + pool.svUsed} idle
                <span className="sm:hidden"> servers ·</span>
              </span>
              <span className="numeric text-hint text-muted-foreground sm:text-right sm:text-body sm:text-foreground">
                <span className="sm:hidden">waited </span>
                {pool.maxWaitSec} s
              </span>
            </li>
          ))}
        </ul>
      </div>
      {stats.stats.length > 1 && (
        <ul aria-label="Databases through the pooler" className="divide-y divide-hairline">
          {stats.stats.map((stat) => (
            <li
              key={stat.database}
              className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-0.5 py-2.5"
            >
              <span className="min-w-0 truncate font-mono text-body">{stat.database}</span>
              <span className="numeric text-hint text-muted-foreground">
                {stat.avgQueryCount} queries a second · {micros(stat.avgQueryTime)} a query · waits{" "}
                {micros(stat.avgWaitTime)} · {stat.totalQueryCount.toLocaleString("en-US")} queries
                so far
              </span>
            </li>
          ))}
        </ul>
      )}
    </SettingSection>
  )
}
