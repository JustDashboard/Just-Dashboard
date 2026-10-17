"use client"

import { useCallback, useRef, useState } from "react"
import Link from "next/link"
import {
  ArrowCircleUp,
  Check,
  CheckCircle,
  CrossCircle,
  RotateCounterClockwise,
  Warning,
} from "@/components/icons"
import { ApiError, errorMessage, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import type { Job } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { SettingSection } from "@/components/deploy/settings/setting-card"
import { SettingPicture } from "@/components/deploy/settings/setting-picture"
import { WireMark, WireNode, WirePlaceholder } from "@/components/deploy/wire"
import { Segments } from "@/components/deploy/settings/segments"
import { FormFact, FormNote, OptionList, OptionRow } from "@/components/form"
import { Detail, DetailList } from "@/components/page"
import { Notice } from "@/components/state"
import { AnimatedBeam } from "@/components/ui/animated-beam"
import { Button } from "@/components/ui/button"
import { CouldNotRead, FactsSkeleton } from "@/components/database/home/blocks"
import { read } from "@/components/database/home/read"
import { EngineGlyph, EngineMark } from "@/components/database/kit"
import { transferPrefix } from "@/components/database/ops/backups-model"
import {
  TransferProgress,
  useTransferJob,
  type WatchedJob,
} from "@/components/database/ops/backups-job"
import { TaskDialog } from "@/components/database/ops/settings-dialog"
import {
  blockers,
  endedWords,
  isUpgradeReading,
  keptProblem,
  keptWords,
  lastWords,
  reportOf,
  reportWords,
  upgradeJobKind,
  upgradeSteps,
  type DbUpgrade,
  type DbUpgradeCheck,
  type DbUpgradeRecord,
  type DbUpgradeReport,
} from "@/components/database/ops/settings-version-model"
import { useDatabase } from "@/components/database/shell/database-context"

/**
 * The server's release, and moving it to a newer one.
 *
 * A server Databases started runs one release of a closed list of images, and
 * a newer release of the same image is in that list too. The page reads which
 * release this is and which it can be moved to, checks one — the release is
 * started beside the server and asked what it carries, and nothing on the
 * server changes — and, once nothing stops it, upgrades: a new server beside
 * the old one with everything copied in, put in the old one's place under its
 * name, port and password. The old one is then kept, stopped, with its data,
 * until the reader finishes the upgrade, which removes it, or reverts it,
 * which puts it back.
 *
 * Each of the three is a job on the server, like a dump or a restore of this
 * connection and one at a time with them: the page begins one and follows it
 * by its id, and one found running is shown on arrival.
 */
export function VersionSections({ confirm }: { confirm: (request: ConfirmRequest) => void }) {
  const { id, conn, engine, summary, status, readOnly } = useDatabase()
  const { can } = useAuth()
  const upgrade = usePoll(
    (signal) => read<DbUpgrade>(`/databases/${id}/upgrade`, isUpgradeReading, undefined, signal),
    0,
    [id],
  )
  const view = upgrade.data
  const [began, setBegan] = useState<string>()
  const [dismissed, setDismissed] = useState<string[]>([])
  const [reports, setReports] = useState<Record<string, DbUpgradeReport>>({})
  const [chosen, setChosen] = useState<string>()
  const [reverting, setReverting] = useState(false)

  const running = view?.job
  const watchedId = began || (running && !dismissed.includes(running.id) ? running.id : "") || ""
  const live = Boolean(began) || running?.id === watchedId
  const refreshUpgrade = upgrade.refresh
  const refreshStatus = status.refresh
  const name = conn.name
  const onEnd = useCallback(
    ({ job, lines }: WatchedJob) => {
      refreshUpgrade()
      refreshStatus()
      const kind = upgradeJobKind(job)
      const said = endedWords(kind, job, name)
      if (kind === "check" && job.status === "succeeded") {
        const report = reportOf(lines)
        if (report) {
          setReports((held) => ({ ...held, [report.to]: report }))
          // The report below says what the console did, line by line.
          setDismissed((held) => [...held, job.id])
          setBegan(undefined)
          return
        }
      }
      if (said.tone === "success") notify.success(said.title)
      else if (said.tone === "error") notify.error(said.title, said.description)
      else notify.info(said.title)
    },
    [refreshUpgrade, refreshStatus, name],
  )
  const transfer = useTransferJob(watchedId, transferPrefix(id), live, onEnd)
  const busy = transfer.current?.job.status === "running"
  // A check moves nothing; an upgrade and a revert carry the server across.
  const runningKind = busy && transfer.current ? upgradeJobKind(transfer.current.job) : undefined
  const moving = runningKind === "upgrade" || runningKind === "revert"

  const begin = async (path: string, body: object) => {
    try {
      const job = await post<Job>(`/databases/${id}/upgrade${path}`, body)
      setBegan(job.id)
      refreshUpgrade()
    } catch (err) {
      // Another dump, restore or upgrade of this connection is running: it
      // is the one to watch.
      if (err instanceof ApiError && err.code === "transfer_running" && err.resource) {
        setBegan(err.resource)
        return
      }
      throw err
    }
  }
  const dismiss = () => {
    if (watchedId) setDismissed((held) => [...held, watchedId])
    setBegan(undefined)
  }

  const label = view?.label ?? engine.label
  const kept = view?.window?.state === "upgraded" ? view.window : undefined
  const targets = view?.targets ?? []
  const to = chosen && targets.some((t) => t.version === chosen) ? chosen : targets[0]?.version
  const report = to ? reports[to] : undefined
  const mayChange = can("destructive") && !readOnly

  const progress = transfer.current && (
    <TransferProgress
      watched={transfer.current}
      canStop={can("system.admin")}
      onStop={() => void transfer.stop()}
      onDismiss={dismiss}
    />
  )

  const upgradeNow = () => {
    if (!view || !to || !report) return
    const steps = upgradeSteps(
      {
        label,
        from: report.server,
        to,
        name,
        database: conn.database || name,
      },
      report,
    )
    confirm({
      title: `Upgrade to ${label} ${to}`,
      confirmLabel: "Upgrade",
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name,
        facts: (
          <>
            <FormFact label="From">{report.server}</FormFact>
            <FormFact label="To">{report.target}</FormFact>
            {view.container && (
              <FormFact label="Container" mono>
                {view.container}
              </FormFact>
            )}
          </>
        ),
      },
      description: (
        <>
          {steps.map((step) => (
            <p key={step}>{step}</p>
          ))}
        </>
      ),
      action: async () => {
        await begin("", { version: to })
        return "reported"
      },
    })
  }

  const finish = (record: DbUpgradeRecord) =>
    confirm({
      title: "Finish the upgrade",
      confirmLabel: `Remove ${label} ${record.fromVersion}`,
      subject: {
        mark: <EngineMark engine={engine} size="sm" />,
        name: <span className="font-mono">{record.previous}</span>,
        facts: (
          <>
            <FormFact label="Release">
              {label} {record.fromVersion}
            </FormFact>
            {record.previousVolume && (
              <FormFact label="Data volume" mono>
                {record.previousVolume}
              </FormFact>
            )}
          </>
        ),
      },
      description: (
        <>
          <p>
            Removes <span className="font-mono">{record.previous}</span> and its data volume
            {record.previousVolume ? (
              <>
                {" "}
                <span className="font-mono">{record.previousVolume}</span>
              </>
            ) : null}
            . {label} {record.toVersion} keeps running as {record.container}, and nothing that
            connects to it changes.
          </p>
          <p>
            After this the upgrade cannot be reverted.
            {record.safetyDump
              ? ` The safety dump ${record.safetyDump} taken before it is kept with the other dumps.`
              : ""}
          </p>
        </>
      ),
      action: async () => {
        const done = await post<{ detail: string; warnings?: string[] }>(
          `/databases/${id}/upgrade/complete`,
          {},
        )
        for (const warning of done.warnings ?? []) notify.warning("Kept", { description: warning })
        notify.success(`${name} stays on ${label} ${record.toVersion}`, {
          description: done.detail,
        })
        refreshUpgrade()
        return "reported"
      },
    })

  return (
    <>
      <VersionSection
        upgrade={view}
        error={upgrade.error}
        onRetry={refreshUpgrade}
        to={kept ? undefined : to}
        moving={moving}
      />
      {view && kept ? (
        <KeptSection
          record={kept}
          label={label}
          progress={progress}
          busy={busy}
          mayChange={mayChange}
          protectedNote={readOnly}
          onFinish={() => finish(kept)}
          onRevert={() => setReverting(true)}
        />
      ) : view && (targets.length > 0 || progress) ? (
        <UpgradeSection
          upgrade={view}
          label={label}
          to={to}
          onTo={setChosen}
          report={report}
          progress={progress}
          busy={busy}
          mayChange={mayChange}
          protectedNote={readOnly}
          onCheck={() => {
            if (!to) return
            void begin("/check", { version: to }).catch((err) =>
              notify.error(`Could not check ${label} ${to}`, err),
            )
          }}
          onUpgrade={upgradeNow}
          serverVersion={summary?.versionNumber}
        />
      ) : null}
      {reverting && kept && (
        <RevertDialog
          record={kept}
          label={label}
          onStart={(dumpFirst) => begin("/revert", { dumpFirst })}
          onClose={() => setReverting(false)}
        />
      )}
    </>
  )
}

/**
 * What the server runs: the release, the image it was started from, its
 * container and the volume that holds its data — or, where it is not a
 * server this page can move, why.
 */
function VersionSection({
  upgrade,
  error,
  onRetry,
  to,
  moving,
}: {
  upgrade: DbUpgrade | undefined
  error: Error | undefined
  onRetry: () => void
  /** The release a check or an upgrade is about. */
  to: string | undefined
  /** An upgrade, a check or a revert is running. */
  moving: boolean
}) {
  const { engine, summary } = useDatabase()
  const version = summary?.versionNumber
  return (
    <SettingSection
      id="version"
      title="Version"
      state={version ? `${engine.label} ${version}` : undefined}
    >
      {!upgrade ? (
        error ? (
          <CouldNotRead what="which release it runs" error={error} onRetry={onRetry} />
        ) : (
          <FactsSkeleton rows={3} />
        )
      ) : (
        <div className="animate-rise space-y-4">
          <VersionPicture upgrade={upgrade} to={to} moving={moving} />
          {upgrade.image && (
            <DetailList className="gap-y-2.5">
              <Detail label="Image" className="font-mono wrap-anywhere">
                {upgrade.image}
              </Detail>
              {upgrade.container && (
                <Detail label="Container" className="font-mono wrap-anywhere">
                  <Link
                    href={`/docker/containers/${encodeURIComponent(upgrade.container)}`}
                    className="rounded-sm underline focus-ring"
                  >
                    {upgrade.container}
                  </Link>
                </Detail>
              )}
              {upgrade.volume && (
                <Detail label="Data volume" className="font-mono wrap-anywhere">
                  {upgrade.volume}
                </Detail>
              )}
            </DetailList>
          )}
          {!upgrade.window && upgrade.targets.length === 0 && upgrade.reason && (
            <FormNote data-slot="upgrade-reason">{upgrade.reason}</FormNote>
          )}
        </div>
      )}
    </SettingSection>
  )
}

/**
 * The server and the release it would become, drawn as the way it goes: the
 * server as its product, at the release it runs, and the newer one as a ring
 * not there yet, the line between them dashed until an upgrade carries it.
 * After an upgrade the kept server is the ring: still there, and no longer
 * what answers. A server with no newer release and no kept one has no
 * picture, since there is nowhere for it to go.
 */
function VersionPicture({
  upgrade,
  to,
  moving,
}: {
  upgrade: DbUpgrade
  to: string | undefined
  moving: boolean
}) {
  const { engine, summary } = useDatabase()
  const container = useRef<HTMLDivElement>(null)
  const fromMark = useRef<HTMLDivElement>(null)
  const toMark = useRef<HTMLDivElement>(null)
  const kept = upgrade.window?.state === "upgraded" ? upgrade.window : undefined
  const label = upgrade.label ?? engine.label
  const target = upgrade.targets.find((one) => one.version === to)
  if (!upgrade.container || (!kept && !target)) return null
  const now = `${label} ${summary?.versionNumber ?? upgrade.version ?? ""}`.trim()
  const live = (
    <WireMark size="md" tone="logo">
      <EngineGlyph engine={engine} />
    </WireMark>
  )
  const ring = <WirePlaceholder size="md" product={engine.logo} />
  return (
    <SettingPicture
      label={kept ? "The server and the one kept" : "The server and its newer release"}
      containerRef={container}
      className="py-4"
      lines={
        <AnimatedBeam
          containerRef={container}
          fromRef={fromMark}
          toRef={toMark}
          still={!moving}
          dashed={!moving}
        />
      }
      start={[
        kept ? (
          <WireNode
            key="kept"
            nodeRef={fromMark}
            align="end"
            mark={ring}
            eyebrow="Kept, stopped"
            title={`${label} ${kept.fromVersion}`}
            hint={<span className="font-mono wrap-anywhere">{kept.previous}</span>}
          />
        ) : (
          <WireNode
            key="now"
            nodeRef={fromMark}
            align="end"
            mark={live}
            eyebrow="Runs now"
            title={now}
            hint={<span className="font-mono wrap-anywhere">{upgrade.container}</span>}
          />
        ),
      ]}
      end={
        kept ? (
          <WireNode
            nodeRef={toMark}
            align="start"
            mark={live}
            eyebrow="Runs now"
            title={now}
            hint={<span className="font-mono wrap-anywhere">{upgrade.container}</span>}
          />
        ) : (
          <WireNode
            nodeRef={toMark}
            align="start"
            mark={ring}
            eyebrow={moving ? "Being copied into" : "Newer release"}
            title={`${label} ${target?.version}`}
            hint={<span className="font-mono wrap-anywhere">{target?.image}</span>}
          />
        )
      }
    />
  )
}

/**
 * Moving the server to a newer release: which one, what the checks found,
 * and the command once nothing stops it. The reader runs the checks first;
 * the upgrade repeats them on the server before it changes anything.
 */
function UpgradeSection({
  upgrade,
  label,
  to,
  onTo,
  report,
  progress,
  busy,
  mayChange,
  protectedNote,
  onCheck,
  onUpgrade,
  serverVersion,
}: {
  upgrade: DbUpgrade
  label: string
  to: string | undefined
  onTo: (version: string) => void
  report: DbUpgradeReport | undefined
  progress: React.ReactNode
  busy: boolean
  mayChange: boolean
  protectedNote: boolean
  onCheck: () => void
  onUpgrade: () => void
  serverVersion: string | undefined
}) {
  const { conn } = useDatabase()
  const last = lastWords(upgrade.last, label)
  const stopping = report ? blockers(report) : []
  const steps = to
    ? upgradeSteps(
        {
          label,
          from: serverVersion ?? upgrade.version ?? "",
          to,
          name: conn.name,
          database: conn.database || conn.name,
        },
        report,
      )
    : []
  return (
    <SettingSection
      id="upgrade"
      title="Upgrade to a newer release"
      state={to ? reportWords(report) : undefined}
    >
      {progress}
      {last && (
        <Notice
          tone={last.tone}
          icon={last.tone === "danger" ? Warning : undefined}
          title={last.title}
        >
          {last.body}
        </Notice>
      )}
      {to && (
        <div className="space-y-4">
          {upgrade.targets.length > 1 && (
            <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-2">
              <span className="text-body text-muted-foreground">To</span>
              <Segments
                label="The release to upgrade to"
                value={to}
                options={upgrade.targets.map((target) => ({
                  value: target.version,
                  label: `${label} ${target.version}`,
                }))}
                onChange={onTo}
                disabled={busy}
              />
            </div>
          )}
          <ol className="max-w-prose list-decimal space-y-1.5 pl-5 text-body leading-relaxed text-muted-foreground marker:text-hint">
            {steps.map((step) => (
              <li key={step}>{step}</li>
            ))}
          </ol>
          {report && <CheckReport report={report} />}
          <div className="flex flex-wrap items-center gap-2">
            <Button
              size="sm"
              variant="outline"
              disabled={busy || !upgrade.offered}
              onClick={onCheck}
            >
              <Check />
              {report ? "Check again" : `Check ${label} ${to}`}
            </Button>
            {mayChange && report?.ready && (
              <Button size="sm" disabled={busy || !upgrade.offered} onClick={onUpgrade}>
                <ArrowCircleUp />
                Upgrade to {label} {to}
              </Button>
            )}
          </div>
          {report && stopping.length > 0 && (
            <FormNote>
              {stopping.length === 1
                ? "One check stops the upgrade. Fix it on the server and check again."
                : `${stopping.length} checks stop the upgrade. Fix them on the server and check again.`}
            </FormNote>
          )}
          {protectedNote && (
            <FormNote>
              This connection is protected, so its server is not replaced from here. Turn Protection
              off on General to upgrade it; the checks can run either way.
            </FormNote>
          )}
          {!protectedNote && !mayChange && (
            <FormNote>The upgrade replaces the server, which your role may not do.</FormNote>
          )}
        </div>
      )}
    </SettingSection>
  )
}

const VERDICT: Record<
  DbUpgradeCheck["verdict"],
  { icon: typeof CheckCircle; className: string; word: string }
> = {
  ok: { icon: CheckCircle, className: "text-success", word: "fine" },
  warning: { icon: Warning, className: "text-warning", word: "worth knowing" },
  blocker: { icon: CrossCircle, className: "text-destructive", word: "stops the upgrade" },
}

/** What the checks found, one line each, read before anything changes. */
function CheckReport({ report }: { report: DbUpgradeReport }) {
  return (
    <ul
      aria-label="What the checks found"
      data-slot="upgrade-report"
      className="animate-rise divide-y divide-hairline border-y border-hairline"
    >
      {report.checks.map((check) => {
        const mark = VERDICT[check.verdict]
        const Icon = mark.icon
        return (
          <li
            key={check.id}
            data-check={check.id}
            data-verdict={check.verdict}
            className="flex min-w-0 gap-3 py-3"
          >
            <Icon aria-hidden className={cn("mt-0.5 size-4 shrink-0", mark.className)} />
            <div className="min-w-0 flex-1 space-y-1">
              <p className="text-sm font-medium">
                {check.title}
                <span className="sr-only">, {mark.word}</span>
              </p>
              <p className="max-w-prose text-xs leading-relaxed text-muted-foreground">
                {check.detail}
              </p>
              {check.items && check.items.length > 0 && (
                <p className="font-mono text-hint leading-relaxed wrap-anywhere text-muted-foreground">
                  {check.items.join(" · ")}
                </p>
              )}
            </div>
          </li>
        )
      })}
    </ul>
  )
}

/**
 * The old server, kept after an upgrade: where it is, and the two ways out —
 * finishing, which removes it, and reverting, which puts it back.
 */
function KeptSection({
  record,
  label,
  progress,
  busy,
  mayChange,
  protectedNote,
  onFinish,
  onRevert,
}: {
  record: DbUpgradeRecord
  label: string
  progress: React.ReactNode
  busy: boolean
  mayChange: boolean
  protectedNote: boolean
  onFinish: () => void
  onRevert: () => void
}) {
  const { href, conn } = useDatabase()
  const problem = keptProblem(record)
  const gone = record.previousState === "missing"
  return (
    <SettingSection
      id="kept"
      title={`${label} ${record.fromVersion} is kept`}
      state={keptWords(record)}
    >
      {progress}
      <p className="max-w-prose text-body leading-relaxed text-muted-foreground">
        {gone ? (
          <>
            The old server is gone, and {conn.name} runs {label} {record.toVersion}.
          </>
        ) : (
          <>
            <span className="font-mono text-foreground">{record.previous}</span> is stopped and
            holds the data as it was at the upgrade. Revert to go back to it: what was written to{" "}
            {label} {record.toVersion} since then is discarded. Finish the upgrade to remove it and
            its data volume.
          </>
        )}
      </p>
      <DetailList className="gap-y-2.5">
        {record.previous && (
          <Detail label="Kept as" className="font-mono wrap-anywhere">
            {gone ? (
              record.previous
            ) : (
              <Link
                href={`/docker/containers/${encodeURIComponent(record.previous)}`}
                className="rounded-sm underline focus-ring"
              >
                {record.previous}
              </Link>
            )}
            {record.previousState && !gone ? ` · ${record.previousState}` : ""}
          </Detail>
        )}
        {record.previousVolume && (
          <Detail label="Its data" className="font-mono wrap-anywhere">
            {record.previousVolume}
          </Detail>
        )}
        {record.volume && (
          <Detail label="New data" className="font-mono wrap-anywhere">
            {record.volume}
          </Detail>
        )}
        {record.safetyDump && (
          <Detail label="Safety dump" className="font-mono wrap-anywhere">
            <Link href={href("backups")} className="rounded-sm underline focus-ring">
              {record.safetyDump}
            </Link>
          </Detail>
        )}
      </DetailList>
      {problem && (
        <Notice tone="warning" icon={Warning} title={problem}>
          {gone
            ? "Its data volume is left as it was."
            : "It holds the port the new server answers on, so it cannot have started."}
        </Notice>
      )}
      {mayChange ? (
        <div className="flex flex-wrap items-center gap-2">
          <Button
            size="sm"
            variant="outline"
            disabled={busy || record.previousState === "running"}
            onClick={onFinish}
          >
            <Check />
            Finish the upgrade
          </Button>
          {!gone && (
            <Button
              size="sm"
              variant="outline"
              disabled={busy || record.previousState === "running"}
              onClick={onRevert}
            >
              <RotateCounterClockwise />
              Revert to {label} {record.fromVersion}…
            </Button>
          )}
        </div>
      ) : (
        <FormNote>
          {protectedNote
            ? "This connection is protected, so its server is not replaced from here. Turn Protection off on General to finish or revert the upgrade."
            : "Finishing and reverting replace or remove a server, which your role may not do."}
        </FormNote>
      )}
    </SettingSection>
  )
}

/**
 * Going back to the kept server. What was written to the new one since the
 * upgrade is discarded with it, so a dump of it is taken first unless the
 * reader says otherwise.
 */
function RevertDialog({
  record,
  label,
  onStart,
  onClose,
}: {
  record: DbUpgradeRecord
  label: string
  onStart: (dumpFirst: boolean) => Promise<void>
  onClose: () => void
}) {
  const { conn, engine } = useDatabase()
  const [dumpFirst, setDumpFirst] = useState(true)
  const [busy, setBusy] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const database = conn.database || conn.name
  const run = async () => {
    setBusy(true)
    setRefusal(undefined)
    try {
      await onStart(dumpFirst)
      onClose()
    } catch (err) {
      setRefusal(errorMessage(err))
      setBusy(false)
    }
  }
  return (
    <TaskDialog
      title={`Revert to ${label} ${record.fromVersion}`}
      description={`Put ${record.previous} back in the place of ${label} ${record.toVersion}`}
      subject={{
        mark: <EngineMark engine={engine} size="sm" />,
        name: conn.name,
        facts: (
          <>
            <FormFact label="Back to" mono>
              {record.previous}
            </FormFact>
            {record.volume && (
              <FormFact label="Removes" mono>
                {record.volume}
              </FormFact>
            )}
          </>
        ),
      }}
      dirty={!dumpFirst}
      busy={busy}
      refusal={refusal}
      note="It runs on the server; this page shows it."
      command="Revert"
      commandIcon={RotateCounterClockwise}
      destructive
      onRun={() => void run()}
      onClose={onClose}
    >
      <Notice
        tone="warning"
        icon={Warning}
        title={`What was written to ${label} ${record.toVersion} is discarded`}
      >
        {label} {record.fromVersion} comes back with the data it had at the upgrade. Anything
        written to {conn.name} since then is only in {label} {record.toVersion}, which is removed
        with its data volume.
      </Notice>
      <OptionList>
        <OptionRow
          title={`Take a dump of ${database} on ${label} ${record.toVersion} first`}
          hint="Kept with the other dumps as a safety dump. If it cannot be taken, nothing is reverted."
          checked={dumpFirst}
          onCheckedChange={setDumpFirst}
        />
      </OptionList>
    </TaskDialog>
  )
}
