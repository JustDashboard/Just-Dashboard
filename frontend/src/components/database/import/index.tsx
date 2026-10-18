"use client"

import { useCallback, useEffect, useMemo, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { ArrowLeft, Plus } from "@/components/icons"
import { del, errorMessage, post } from "@/lib/api"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { DbConnection, Job } from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useAuth } from "@/hooks/use-auth"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceList, ChoiceRow, FlowHeader, FlowSteps } from "@/components/flow"
import { FormNote } from "@/components/form"
import { Page } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyState, ErrorState, LoadingPanel } from "@/components/state"
import { StatusDot } from "@/components/status-dot"
import { Button } from "@/components/ui/button"
import { sectionHref } from "@/components/database/engine"
import { read } from "@/components/database/home/read"
import { EngineMark, ProtectedTag } from "@/components/database/kit"
import { CutoverDialog } from "@/components/database/import/cutover"
import {
  EMPTY_SOURCE,
  importStep,
  importSteps,
  importTitle,
  isLive,
  sourceDsn,
  statusWords,
  suggestedDatabase,
  type SourceDraft,
} from "@/components/database/import/model"
import { PlanStep, type Plan } from "@/components/database/import/plan"
import { RunStep } from "@/components/database/import/run"
import { SourceStep } from "@/components/database/import/source"
import type {
  DbImport,
  DbImportInspection,
  DbImportList,
  DbImportLive,
  DbImportMode,
  DbImportRemoved,
  DbImportRequest,
  DbImportStarted,
} from "@/components/database/import/types"
import { TransferProgress, useTransferJob } from "@/components/database/ops/backups-job"
import type { WatchedJob } from "@/components/database/ops/backups-job"
import { transferPrefix } from "@/components/database/ops/backups-model"
import { useDatabase } from "@/components/database/shell/database-context"
import { useDatabases } from "@/components/database/shell/databases-context"

const NO_PLAN: Plan = {
  mode: "stream",
  database: "",
  name: "",
  structureOnly: false,
  publicationExists: false,
}

/**
 * Bring a database over from another server into a new one here.
 *
 * A sequence with an outcome — a database on this server — so it is drawn in
 * the flow register (§16): the question at the page's rank, a spine taken from
 * what the server keeps, one focused surface at a time and its command. The
 * source and the plan are asked for in this tab and kept nowhere; what the
 * server holds is the import itself, so a reload, a second window or the next
 * day finds a replication where it was.
 *
 * `?import=` names the import on screen (`new` for a fresh one), and with
 * none named the page opens on the one that is still going.
 */
export function OnlineImport() {
  const { id, conn, engine, readOnly, status, param, select, href, goto } = useDatabase()
  const { refresh: refreshConnections } = useDatabases()
  const { can } = useAuth()
  const router = useRouter()
  const { confirm, dialog } = useConfirm()
  const driver = conn.driver
  // SectionPage mounts this only for an engine that offers it, so this is a word.
  const form = engine.capabilities.onlineImport
  const mayStart = can("system.admin") && !readOnly
  const mayStop = can("service.control")

  const [draft, setDraft] = useState<SourceDraft>(EMPTY_SOURCE)
  const [checkedDsn, setCheckedDsn] = useState("")
  const [inspection, setInspection] = useState<DbImportInspection>()
  const [plan, setPlan] = useState<Plan>(NO_PLAN)
  const [checking, setChecking] = useState(false)
  const [starting, setStarting] = useState(false)
  const [sourceRefusal, setSourceRefusal] = useState<string>()
  const [startRefusal, setStartRefusal] = useState<string>()
  const [asking, setAsking] = useState(false)
  const [working, setWorking] = useState(false)
  const [dismissed, setDismissed] = useState<string[]>([])

  const [anyLive, setAnyLive] = useState(false)
  const list = usePoll(
    async (signal) => {
      const answer = await read<DbImportList>(
        `/databases/${id}/imports`,
        (listed) => Array.isArray(listed.imports),
        undefined,
        signal,
      )
      setAnyLive(answer.imports.some((one) => isLive(one.status)))
      return answer
    },
    anyLive ? 3_000 : 20_000,
    [id],
  )
  const imports = useMemo(() => list.data?.imports ?? [], [list.data])
  const latestLive = imports.find((one) => isLive(one.status))

  const named = param("import")
  const chosen: number | "new" | undefined =
    named === "new"
      ? "new"
      : /^[1-9]\d*$/.test(named)
        ? Number(named)
        : !list.data
          ? undefined
          : latestLive
            ? latestLive.id
            : "new"
  const record = typeof chosen === "number" ? imports.find((one) => one.id === chosen) : undefined

  const liveRead = usePoll(
    (signal) =>
      read<DbImportLive>(
        `/databases/${id}/imports/${chosen}`,
        (answer) => typeof answer.import === "object",
        undefined,
        signal,
      ),
    record && isLive(record.status) ? 2_000 : 30_000,
    [id, chosen],
    { enabled: typeof chosen === "number" },
  )
  const live: DbImportLive | undefined =
    typeof chosen === "number" && liveRead.data?.import.id === chosen
      ? liveRead.data
      : record
        ? { import: record }
        : undefined
  const shown: DbImport | undefined = live?.import

  // The job on screen: the one the address names, else the one the import is
  // running now. A job found running is put in the address so that it stays on
  // screen when it ends and the import stops carrying it.
  const prefix = transferPrefix(id)
  const namedJob = param("job")
  const watchedId = namedJob || shown?.job?.id || ""
  useEffect(() => {
    if (!namedJob && shown?.job?.id) select({ job: shown.job.id })
  }, [namedJob, shown?.job?.id, select])
  const refreshList = list.refresh
  const refreshLive = liveRead.refresh
  const check = status.refresh
  const onEnd = useCallback(
    ({ job }: WatchedJob) => {
      refreshList()
      refreshLive()
      check()
      if (job.status === "succeeded") notify.success(`${job.title} finished`)
      else if (job.status === "failed")
        notify.error(`${job.title} failed`, job.error ?? "The job did not finish.")
      else notify.info(`${job.title} was stopped`)
    },
    [refreshList, refreshLive, check],
  )
  const transfer = useTransferJob(watchedId, prefix, Boolean(shown?.job), onEnd)

  const watch = (imported: number | undefined, job: Job | string) => {
    const jobId = typeof job === "string" ? job : job.id
    setAsking(false)
    select({ import: imported === undefined ? null : String(imported), job: jobId })
    refreshList()
    refreshLive()
  }

  const dsn = sourceDsn(driver, draft)
  const inspect = async () => {
    setChecking(true)
    setSourceRefusal(undefined)
    try {
      const answer = await post<DbImportInspection>(`/databases/${id}/imports/inspect`, {
        source: dsn,
        ...(plan.name ? { name: plan.name } : {}),
      })
      if (!answer.reachable) {
        setSourceRefusal(answer.error || "The source did not answer.")
        return
      }
      setInspection(answer)
      setCheckedDsn(dsn)
      const mode: DbImportMode = answer.forms.includes(plan.mode) ? plan.mode : answer.forms[0]
      setPlan((held) => ({
        ...held,
        mode: held.database ? mode : answer.forms[0],
        database: held.database || suggestedDatabase(answer.source.database ?? "", [conn.database]),
        name: held.name || answer.name,
      }))
    } catch (err) {
      setSourceRefusal(errorMessage(err))
    } finally {
      setChecking(false)
    }
  }

  const start = async () => {
    if (!inspection) return
    setStarting(true)
    setStartRefusal(undefined)
    const replication = plan.mode === "replication"
    const body: DbImportRequest = {
      source: checkedDsn,
      mode: plan.mode,
      database: plan.database.trim(),
      ...(replication
        ? { name: plan.name.trim(), publicationExists: plan.publicationExists || undefined }
        : { structureOnly: plan.structureOnly || undefined }),
    }
    try {
      const started = await post<DbImportStarted>(`/databases/${id}/imports`, body)
      // The string has done its work on this side: the server seals what a
      // replication needs and nothing here has a use for it.
      forgetSource()
      watch(started.import.id, started.job)
    } catch (err) {
      setStartRefusal(errorMessage(err))
    } finally {
      setStarting(false)
    }
  }

  const forgetSource = () => {
    setDraft(EMPTY_SOURCE)
    setCheckedDsn("")
    setInspection(undefined)
    setPlan(NO_PLAN)
    setSourceRefusal(undefined)
    setStartRefusal(undefined)
  }

  const stopReplication = () => {
    if (!shown) return
    void confirm({
      title: "Stop replicating",
      confirmLabel: "Stop replicating",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono">{shown.database}</span>,
      },
      description: (
        <p>
          Removes the subscription from this server and its slot, and the publication, from the
          source. {shown.database} stays as it is, with what was copied so far, and stops following.
          The source is not changed.
        </p>
      ),
      action: async () => {
        const removed = await del<DbImportRemoved>(`/databases/${id}/imports/${shown.id}`)
        if (removed?.warnings?.length) {
          notify.warning("Stopped, with something left on the source", {
            description: removed.warnings.join(" "),
          })
        } else {
          notify.success("Stopped replicating")
        }
        refreshList()
        refreshLive()
        return "reported"
      },
    })
  }

  const forget = () => {
    if (!shown) return
    void confirm({
      title: "Forget this import",
      confirmLabel: "Forget",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono">{importTitle(shown)}</span>,
      },
      description: (
        <p>Removes the entry from this list. {shown.database} and the source are not touched.</p>
      ),
      action: async () => {
        await del(`/databases/${id}/imports/${shown.id}`)
        notify.success("Forgot the import")
        select({ import: null, job: null })
        refreshList()
        return "reported"
      },
    })
  }

  const open = async (database: string) => {
    if (engine.can("dumpDatabases")) {
      goto("data", { db: database })
      return
    }
    setWorking(true)
    try {
      const saved = await post<DbConnection>(`/databases/${id}/server/databases/connect`, {
        database,
      })
      notify.success(`Connected ${saved.name}`)
      refreshConnections()
      router.push(sectionHref(saved.id))
    } catch (err) {
      notify.error(`Could not connect to ${database}`, err)
    } finally {
      setWorking(false)
    }
  }
  const mayOpen =
    engine.can("dumpDatabases") || (engine.can("serverDatabaseConnect") && can("system.admin"))

  const startNew = () => {
    forgetSource()
    select({ import: "new", job: null })
  }

  const eyebrow = (
    <Link
      href={href("backups")}
      className="inline-flex items-center gap-1 rounded-sm focus-ring hover:underline"
    >
      <ArrowLeft className="size-3" /> {conn.name}
    </Link>
  )

  // Where the sequence is: the draft's own state while there is no import, the
  // server's once there is.
  const mode: DbImportMode = shown?.mode ?? (form === "replication" ? plan.mode : "stream")
  const caughtUp = Boolean(live?.lag && live.lag.exists && live.lag.bytes === 0)
  const step = importStep(shown ? shown.mode : mode, {
    inspected: Boolean(inspection),
    import: shown,
    caughtUp:
      caughtUp &&
      Boolean(live?.subscription && live.subscription.ready === live.subscription.tables.length),
  })
  const steps = importSteps(
    shown ? shown.mode : inspection ? plan.mode : form === "replication" ? "replication" : "stream",
  )
  const question = shown
    ? shown.status === "done"
      ? "Is it all here?"
      : shown.status === "replicating" || shown.status === "cutting"
        ? "Ready to cut over?"
        : shown.status === "running"
          ? "How far has it got?"
          : "What happened?"
    : inspection
      ? "How should it come over?"
      : "Where is the database coming from?"

  return (
    <Page register="flow" className="animate-rise">
      <FlowHeader
        eyebrow={
          <span className="inline-flex items-center gap-2">
            {eyebrow}
            {readOnly && <ProtectedTag />}
          </span>
        }
        question={question}
        steps={<FlowSteps steps={steps} current={step} />}
        actions={
          mayStart &&
          chosen !== "new" &&
          chosen !== undefined && (
            <Button size="sm" variant="outline" onClick={startNew}>
              <Plus />
              New import
            </Button>
          )
        }
      />

      <div className="grid min-w-0 items-start gap-x-6 gap-y-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <div className="min-w-0 space-y-6">
          {chosen === undefined ? (
            list.error ? (
              <ErrorState error={list.error} onRetry={list.refresh} />
            ) : (
              <LoadingPanel rows={4} />
            )
          ) : chosen === "new" ? (
            !mayStart ? (
              <EmptyState
                mark={<EngineMark engine={engine} size="md" />}
                title={
                  readOnly
                    ? `${conn.name} is protected`
                    : "Bringing a database over needs an administrator"
                }
                description={
                  readOnly
                    ? "A protected connection refuses every change made through this dashboard, and an import makes a database here. Turn protection off in its settings first."
                    : "It creates a database on this server and opens a connection to another one."
                }
              />
            ) : inspection ? (
              <PlanStep
                engine={engine}
                current={conn.database}
                inspection={inspection}
                plan={plan}
                starting={starting}
                checking={checking}
                refusal={startRefusal}
                onPlan={setPlan}
                onStart={() => void start()}
                onRecheck={() => void inspect()}
                onChangeSource={() => {
                  setInspection(undefined)
                  setStartRefusal(undefined)
                }}
              />
            ) : (
              <SourceStep
                engine={engine}
                driver={driver}
                draft={draft}
                checking={checking}
                refusal={sourceRefusal}
                onDraft={setDraft}
                onCheck={() => void inspect()}
              />
            )
          ) : live ? (
            <>
              <RunStep
                live={live}
                mayAct={mayStart}
                mayStop={mayStop}
                busy={working}
                onCutover={() => setAsking(true)}
                onStopReplication={stopReplication}
                onStopJob={() => void transfer.stop()}
                onOpen={mayOpen ? () => void open(live.import.database) : undefined}
                onForget={forget}
                onNew={startNew}
              />
              {transfer.current && !dismissed.includes(watchedId) && (
                <TransferProgress
                  watched={transfer.current}
                  canStop={false}
                  onStop={() => {}}
                  onDismiss={() => setDismissed((held) => [...held, watchedId])}
                />
              )}
              {transfer.missing && namedJob && (
                <FormNote role="status">
                  No job of {conn.name} is kept under that id: the server remembers its last fifty
                  operations.
                </FormNote>
              )}
            </>
          ) : liveRead.error ? (
            <ErrorState error={liveRead.error} onRetry={liveRead.refresh} />
          ) : (
            <LoadingPanel rows={4} />
          )}
        </div>

        {imports.length > 0 && (
          <Panel plain aria-label="Imports">
            <PanelHeader
              title={
                <span className="flex items-baseline gap-2">
                  Imports
                  <span className="numeric text-hint font-normal text-muted-foreground">
                    {imports.length}
                  </span>
                </span>
              }
            />
            <PanelBody>
              <ChoiceList>
                {imports.map((one) => {
                  const { word, tone } = statusWords(one.status)
                  return (
                    <ChoiceRow
                      key={one.id}
                      leading={<StatusDot tone={tone} />}
                      title={<span className="font-mono">{one.database}</span>}
                      verb={`Open the import of ${one.database}`}
                      description={`${word} · ${one.source.address} · ${relativeTime(one.updatedAt)}`}
                      busy={one.status === "running" || one.status === "cutting"}
                      onSelect={() => select({ import: String(one.id), job: null })}
                    />
                  )
                })}
              </ChoiceList>
            </PanelBody>
          </Panel>
        )}
      </div>

      {asking && live && (
        <CutoverDialog
          live={live}
          onStarted={(job) => watch(live.import.id, job)}
          onRunning={(jobId) => watch(live.import.id, jobId)}
          onClose={() => setAsking(false)}
        />
      )}
      {dialog}
    </Page>
  )
}
