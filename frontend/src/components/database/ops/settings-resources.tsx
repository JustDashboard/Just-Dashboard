"use client"

import { useId, useState } from "react"
import { errorMessage, put } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { DbResources, DbResourcesChange } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { LimitField, useContainerPeaks } from "@/components/deploy/settings/limit-field"
import {
  SettingForm,
  SettingSection,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { FieldRow, FormFact, FormNote } from "@/components/form"
import { Detail, DetailList } from "@/components/page"
import { CouldNotRead, FactsSkeleton } from "@/components/database/home/blocks"
import { read, record } from "@/components/database/home/read"
import { EngineMark } from "@/components/database/kit"
import {
  MIB,
  bufferFact,
  coresWord,
  draftOfLimits,
  limitChanges,
  limitProblems,
  limitsWords,
  liftedLimits,
  sizeOf,
  type Limits,
  type LimitsDraft,
} from "@/components/database/kit/limits"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * What the server's container may use of this machine: a memory limit and a
 * processor quota, each beside what the container came closest to in the
 * last hour, and the memory limit beside the engine's own buffer — which a
 * limit under it does not shrink: the engine fills it and the kernel stops
 * the server.
 *
 * Docker applies a new limit to the container at once and cannot take one
 * away, so a draft that lifts one says it replaces the container, and Save
 * asks before it does, the way publishing a port on Network asks. Where the
 * container is not the dashboard's to change — compose's, another machine's,
 * a process that is not in one — the reading says whose it is instead.
 */
export function ResourcesSection({ confirm }: { confirm: (request: ConfirmRequest) => void }) {
  const { id } = useDatabase()
  const { can } = useAuth()
  const resources = usePoll(
    (signal) =>
      read<DbResources>(
        `/databases/${id}/resources`,
        (answer) => record(answer.limits) && record(answer.host),
        undefined,
        signal,
      ),
    0,
    [id],
  )
  const data = resources.data
  if (!data) {
    return (
      <SettingSection id="resources" title="Resources">
        {resources.error ? (
          <CouldNotRead
            what="what it may use"
            error={resources.error}
            onRetry={resources.refresh}
          />
        ) : (
          <div role="status" aria-label="Reading what it may use">
            <FactsSkeleton rows={3} />
          </div>
        )}
      </SettingSection>
    )
  }
  if (can("system.admin") && data.editable) {
    return <ResourcesForm data={data} confirm={confirm} onSaved={resources.refresh} />
  }
  return <ResourcesReading data={data} />
}

/** The lines under the memory field: the machine, the engine's buffer, the last stop. */
function MemoryFacts({ data, memoryMb }: { data: DbResources; memoryMb: number }) {
  const { engine } = useDatabase()
  const setting = engine.capabilities.memoryBuffer
  const fact = data.buffer && bufferFact(data.buffer, memoryMb)
  // The setting's own spelling, at the size of the line it sits in.
  const name = (
    <code className="font-mono text-hint text-foreground">{data.buffer?.setting ?? setting}</code>
  )
  return (
    <>
      {data.host.memoryBytes > 0 && (
        <span className="block">this server has {sizeOf(data.host.memoryBytes)}</span>
      )}
      {fact ? (
        <span
          key={fact.effect ? "effect" : "fact"}
          className="block animate-rise"
          data-slot="memory-buffer"
        >
          {name} is {fact.value}
          {fact.effect && <span className="text-warning">, {fact.effect}</span>}
        </span>
      ) : (
        setting && (
          <span className="block" data-slot="memory-buffer" title={data.bufferError}>
            {name} {data.running ? "could not be read" : "is read while it runs"}
          </span>
        )
      )}
      {data.oomKilled && (
        <span className="mt-1 block text-warning">
          The kernel stopped it the last time it stopped, for going past its memory limit.
        </span>
      )}
    </>
  )
}

function ResourcesForm({
  data,
  confirm,
  onSaved,
}: {
  data: DbResources
  confirm: (request: ConfirmRequest) => void
  onSaved: () => void
}) {
  const { id, conn, engine, summary, status } = useDatabase()
  const { can } = useAuth()
  const field = useId()
  // What a save answered stands until the read after it lands, so the fields
  // do not show the limits from before the save while it is asked for.
  const [applied, setApplied] = useState<{ over: DbResources; limits: Limits }>()
  const limits = applied?.over === data ? applied.limits : data.limits
  const saved = draftOfLimits(limits)
  const [edits, setEdits] = useState<Partial<LimitsDraft>>({})
  const [saving, setSaving] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const peaks = useContainerPeaks(summary?.container?.id)

  const draft: LimitsDraft = { ...saved, ...edits }
  const changes = limitChanges(saved, draft)
  const count = Object.keys(changes).length
  const lifted = liftedLimits(saved, draft)
  const replaces = lifted.length > 0
  // What it uses now holds only a memory limit being changed: rounded to
  // whole MiB, the one it has may read a little under it.
  const problems = limitProblems(
    draft,
    data.host,
    "memoryMb" in changes && data.running ? data.usedBytes : undefined,
  )
  const held = replaces && !data.running ? "stopped" : replaces && !can("destructive") ? "role" : ""
  const invalid = Object.keys(problems).length > 0 || held !== ""
  const set = (patch: Partial<LimitsDraft>) => {
    setEdits((kept) => ({ ...kept, ...patch }))
    setRefusal(undefined)
  }
  const container = data.container ?? "its container"
  const after = { memoryBytes: draft.memoryMb * MIB, cpus: draft.cpus }
  const without =
    lifted.length === 2
      ? "no limits"
      : lifted[0] === "memory"
        ? "no memory limit"
        : "no processor limit"

  const apply = async () => {
    setSaving(true)
    setRefusal(undefined)
    try {
      const done = await put<DbResourcesChange>(`/databases/${id}/resources`, changes)
      setApplied({ over: data, limits: done.limits })
      setEdits({})
      if (done.warnings.length > 0) {
        notify.warning(`Docker took the limits of ${conn.name} with a caveat`, {
          description: done.warnings[0],
        })
      }
      onSaved()
      status.refresh()
      return true
    } catch (err) {
      setRefusal(errorMessage(err))
      return false
    } finally {
      setSaving(false)
    }
  }

  const save = async () => {
    if (count === 0) return true
    if (invalid) return false
    if (!replaces) return apply()
    // Asked first, because the replacement drops every session to the server.
    // The bar keeps the edit until the answer: a confirmed replacement saves
    // from the dialog, and a cancelled one leaves the edit where it was.
    confirm({
      title: `Replace ${container}`,
      confirmLabel: "Replace and save",
      workingLabel: `Replacing ${container}…`,
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: conn.name,
        facts: (
          <>
            <FormFact label="Container" mono>
              {container}
            </FormFact>
            <FormFact label="Then">{limitsWords(after)}</FormFact>
          </>
        ),
      },
      description: (
        <>
          <p>
            Docker cannot take a limit away from a container that exists, so {container} is replaced
            with the same configuration and {without}.
          </p>
          <p>The data is kept. Every open session is dropped while the container is replaced.</p>
        </>
      ),
      action: async () => {
        await apply()
        return "reported"
      },
    })
    return false
  }

  return (
    <SettingForm
      name="Resource limits"
      onSave={save}
      dirty={count > 0}
      changes={count}
      saving={saving}
      invalid={invalid}
      canEdit
      applies="immediately"
      onDiscard={() => {
        setEdits({})
        setRefusal(undefined)
      }}
      error={refusal && `It was not saved. ${refusal}`}
    >
      <SettingSection
        id="resources"
        title="Resources"
        state={limitsWords(limits)}
        status={settingStatus({ dirty: count > 0, refused: Boolean(refusal) })}
      >
        <FieldRow>
          <LimitField
            id={`${field}-memory`}
            label="Memory limit"
            unit="MiB"
            step={64}
            value={draft.memoryMb}
            max={data.host.memoryBytes ? Math.floor(data.host.memoryBytes / MIB) : undefined}
            peak={peaks.state === "ready" ? peaks.memory / MIB : undefined}
            peakLabel={(value) => `${Math.round(value)} MiB`}
            info="Zero means no limit. The kernel stops the container when it goes past its limit, so leave room for the engine's own buffers."
            facts={<MemoryFacts data={data} memoryMb={draft.memoryMb} />}
            error={problems.memory}
            disabled={false}
            onChange={(memoryMb) => set({ memoryMb })}
          />
          <LimitField
            id={`${field}-cpus`}
            label="CPU limit"
            unit="cores"
            step={0.25}
            value={draft.cpus}
            max={data.host.cpus || undefined}
            peak={peaks.state === "ready" ? peaks.cpus : undefined}
            peakLabel={(value) => `${value.toFixed(2)} cores`}
            info="Whole or fractional processors, for example 0.5 or 2. Zero means no limit."
            facts={
              data.host.cpus > 0 && (
                <span className="block">this server has {coresWord(data.host.cpus)}</span>
              )
            }
            error={problems.cpus}
            disabled={false}
            onChange={(cpus) => set({ cpus })}
          />
        </FieldRow>
        {held === "stopped" ? (
          <FormNote className="max-w-prose" data-slot="resources-held">
            <span className="font-medium text-foreground">Not while it is stopped.</span> Lifting a
            limit replaces {container} with one that is started; start it first.
          </FormNote>
        ) : held === "role" ? (
          <FormNote className="max-w-prose" data-slot="resources-held">
            Lifting a limit replaces the container, which your role may not do.
          </FormNote>
        ) : replaces ? (
          <FormNote
            tone="warning"
            className="max-w-prose animate-rise"
            data-slot="resources-replace"
          >
            Docker cannot take a limit away from a container that exists: saving replaces{" "}
            {container} with {without}. The data is kept; every open session is dropped while it
            restarts.
          </FormNote>
        ) : (
          <FormNote className="max-w-prose">
            Docker applies a new limit to {container} at once; nothing is restarted.
          </FormNote>
        )}
      </SettingSection>
    </SettingForm>
  )
}

/**
 * The same facts for a role that may only read them, or for a server whose
 * limits are not the dashboard's to change, with whose they are instead. A
 * server that is not in a container here has nothing to list: the sentence
 * saying where its limits are set is the whole reading.
 */
function ResourcesReading({ data }: { data: DbResources }) {
  const { can } = useAuth()
  const { memoryBytes, cpus } = data.limits
  const host = [
    data.host.memoryBytes > 0 && sizeOf(data.host.memoryBytes),
    data.host.cpus > 0 && coresWord(data.host.cpus),
  ].filter(Boolean)
  return (
    <SettingSection
      id="resources"
      title="Resources"
      state={data.container ? limitsWords(data.limits) : undefined}
    >
      {data.container && (
        <DetailList className="animate-rise gap-y-2.5">
          <Detail label="Container" className="font-mono wrap-anywhere">
            {data.container}
          </Detail>
          <Detail label="Memory limit" className="numeric">
            {memoryBytes ? sizeOf(memoryBytes) : "None"}
          </Detail>
          <Detail label="CPU limit" className="numeric">
            {cpus ? coresWord(cpus) : "None"}
          </Detail>
          {data.usedBytes ? (
            <Detail label="In use now" className="numeric">
              {sizeOf(data.usedBytes)}
            </Detail>
          ) : null}
          {data.buffer && (
            <Detail label={<code className="font-mono">{data.buffer.setting}</code>}>
              <span data-slot="memory-buffer">
                {bufferFact(data.buffer, memoryBytes / MIB).value}
              </span>
            </Detail>
          )}
          {host.length > 0 && (
            <Detail label="This server" className="numeric">
              {host.join(" · ")}
            </Detail>
          )}
        </DetailList>
      )}
      {data.refusal ? (
        <FormNote data-slot="resources-refusal">
          <span className="font-medium text-foreground">Not changed from here.</span> {data.refusal}
        </FormNote>
      ) : (
        !can("system.admin") && (
          <FormNote>What it may use is an administrator&rsquo;s to change.</FormNote>
        )
      )}
    </SettingSection>
  )
}
