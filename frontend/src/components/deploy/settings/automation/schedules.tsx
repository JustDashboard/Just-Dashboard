"use client"

import { useEffect, useMemo, useState } from "react"
import {
  ArrowRight,
  ArrowUpDown,
  Check,
  Logs,
  Pause,
  Pencil,
  Play,
  Plus,
  Trash,
} from "@/components/icons"
import { ApiError, del, get, post, put } from "@/lib/api"
import { describeCron, isValidCron, nextCronRunsIn } from "@/lib/cron"
import { plural, relativeTime } from "@/lib/format"
import { useSessionState } from "@/lib/view-state"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import type {
  BackupJob,
  DeploymentEngineRun,
  DeploymentSchedule,
  DeploymentScheduleTest,
} from "@/lib/types"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import { ChoiceList, ChoiceRow, GroupRule } from "@/components/flow"
import {
  Field,
  FieldRow,
  FormFact,
  FormFacts,
  FormNote,
  FormSection,
  Statement,
} from "@/components/form"
import { OUTCOME_FILL, OutcomeStrip, type OutcomeTone } from "@/components/outcome-strip"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Status, StatusDot } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command"
import { Input } from "@/components/ui/input"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Textarea } from "@/components/ui/textarea"
import { useConfirm } from "@/components/confirm-dialog"
import { VerbActions, VerbBar, type Verb } from "@/components/verbs"
import {
  ScheduleBuilder,
  scheduleExpression,
  scheduleFields,
  scheduleValid,
  type ScheduleFields,
} from "@/components/schedule-builder"
import { contentsLabel, targetLabel } from "@/components/backups/shared"
import { destinationProduct } from "@/components/backups/marks"
import { SettingSection } from "@/components/deploy/settings/setting-card"
import {
  RunStatus,
  formatDuration,
  humanize,
  isActiveRun,
  runDurationSeconds,
  runTitle,
  useNow,
} from "@/components/deploy/vocabulary"
import { useProject } from "@/components/deploy/project-context"
import type { Automation } from "@/components/deploy/settings/automation/use-automation"
import {
  ACTIONS,
  ACTION_GLYPH,
  actionOf,
  browserZone,
  overdue,
  untilLabel,
  zoneOffset,
  zonedMoment,
  type ScheduleAction,
} from "@/components/deploy/settings/automation/marks"

/**
 * Schedules — deploy, restart, back up or run a command on a clock, in a
 * named timezone.
 *
 * A schedule is read as the sentence it is ("Every day at 03:00 · Europe/
 * Chisinau") rather than as the five cron fields the server stores, with the
 * chain of steps it runs drawn as their glyphs and words, and its last
 * fourteen firings as a strip — so a nightly deploy that has been failing
 * reads as that before its name is read. Its sheet lays the firings and the
 * next five runs on one axis around now.
 */

/** How many firings a card draws: two weeks of a nightly schedule. */
const STRIP = 14

export type ScheduleTarget =
  { mode: "add"; action?: ScheduleAction } | { mode: "edit"; schedule: DeploymentSchedule }

type ScheduleDraft = {
  name: string
  /** Whether the name is still the one the form suggested. */
  named: boolean
  fields: ScheduleFields
  timezone: string
  action: ScheduleAction
  backupJobId: string
  /** A runtime container's id, or "" with `otherContainer` for one typed by hand. */
  containerId: string
  otherContainer: boolean
  argv: string
  timeoutSeconds: string
}

const RHYTHM: Record<string, string> = {
  hourly: "Hourly",
  daily: "Nightly",
  weekly: "Weekly",
  monthly: "Monthly",
  custom: "Scheduled",
}

const NOUN: Record<ScheduleAction, string> = {
  deploy: "deploy",
  restart: "restart",
  backup: "backup",
  container_command: "command",
}

function suggestedName(fields: ScheduleFields, action: ScheduleAction) {
  return `${RHYTHM[fields.preset] ?? "Scheduled"} ${NOUN[action]}`
}

function blankDraft(action: ScheduleAction = "deploy"): ScheduleDraft {
  // An untouched form still posts the nightly "0 3 * * *" it always has.
  const fields = scheduleFields("0 3 * * *")
  return {
    name: suggestedName(fields, action),
    named: true,
    fields,
    timezone: browserZone(),
    action,
    backupJobId: "",
    containerId: "",
    otherContainer: false,
    argv: "",
    timeoutSeconds: "",
  }
}

function draftOf(schedule: DeploymentSchedule, containers: string[]): ScheduleDraft {
  const step = schedule.steps[0]
  const config = step?.config ?? {}
  const containerId = typeof config.containerId === "string" ? config.containerId : ""
  const action = (actionOf(step?.action ?? "")?.key ?? "deploy") as ScheduleAction
  return {
    name: schedule.name,
    named: false,
    fields: scheduleFields(schedule.expression),
    timezone: schedule.timezone,
    action,
    backupJobId: String(config.jobId ?? config.backupJobId ?? ""),
    containerId,
    otherContainer: Boolean(containerId) && !containers.includes(containerId),
    argv: Array.isArray(config.argv) ? config.argv.join("\n") : "",
    timeoutSeconds: config.timeoutSeconds ? String(config.timeoutSeconds) : "",
  }
}

/**
 * The Add and Edit sheet's state, held by the page: the picture's ring and the
 * section's starter cards open the one sheet, and a new schedule's half-typed
 * form is kept for the tab until it is created.
 */
export function useScheduleSheet(projectId: number, environmentId: number) {
  const project = useProject()
  const containers = (project.detail.runtime?.services ?? []).map((one) => one.containerId)
  const [target, setTarget] = useState<ScheduleTarget>()
  const [added, setAdded] = useSessionState<ScheduleDraft>(
    `deploy.${projectId}.${environmentId}.schedules.draft`,
    blankDraft(),
  )
  const [edited, setEdited] = useState<ScheduleDraft>(blankDraft)
  const editing = target?.mode === "edit"
  return {
    target,
    draft: editing ? edited : added,
    setDraft: editing ? setEdited : setAdded,
    openAdd: (preset: { action?: ScheduleAction } = {}) => {
      const action = preset.action
      if (action)
        setAdded((draft) => ({
          ...draft,
          action,
          name: draft.named ? suggestedName(draft.fields, action) : draft.name,
        }))
      setTarget({ mode: "add", ...preset })
    },
    openEdit: (schedule: DeploymentSchedule) => {
      setEdited(draftOf(schedule, containers))
      setTarget({ mode: "edit", schedule })
    },
    close: () => setTarget(undefined),
    /** A created schedule's form starts over next time. */
    forget: () => setAdded(blankDraft()),
  }
}

export type ScheduleSheetController = ReturnType<typeof useScheduleSheet>

/** The backup jobs a step can run, read only while one is drawn or chosen. */
function useBackupJobs(enabled: boolean) {
  return usePoll((signal) => get<BackupJob[]>("/backups/", undefined, signal), 0, [], {
    enabled,
  })
}

function scheduleBody(schedule: DeploymentSchedule, enabled: boolean) {
  return {
    name: schedule.name,
    expression: schedule.expression,
    timezone: schedule.timezone,
    enabled,
    steps: schedule.steps,
  }
}

// ---------------------------------------------------------------------------
// The section
// ---------------------------------------------------------------------------

export function Schedules({
  projectId,
  environmentId,
  automation,
  sheet,
}: {
  projectId: number
  environmentId: number
  automation: Automation
  sheet: ScheduleSheetController
}) {
  const { can } = useAuth()
  const canAdmin = can("system.admin")
  const { confirm, dialog } = useConfirm()
  const now = useNow(60_000)
  const base = `/deploy/${projectId}/environments/${environmentId}`
  const { schedules } = automation
  const list = schedules.data ?? []
  const [openId, setOpenId] = useState<number>()
  const opened = list.find((schedule) => schedule.id === openId)
  const jobs = useBackupJobs(
    list.some((schedule) => schedule.steps.some((step) => step.action === "backup")),
  )

  const toggle = async (schedule: DeploymentSchedule) => {
    try {
      await put(`${base}/schedules/${schedule.id}`, scheduleBody(schedule, !schedule.enabled))
      automation.refresh()
      notify.success(schedule.enabled ? "Schedule paused" : "Schedule enabled")
    } catch (error) {
      notify.error("Could not update schedule", error)
    }
  }

  const remove = (schedule: DeploymentSchedule, fired?: number) => {
    const upcoming = nextRunsOf(schedule, now)[0]
    confirm({
      title: `Remove ${schedule.name}`,
      confirmLabel: "Remove schedule",
      subject: {
        mark: <ScheduleMark schedule={schedule} />,
        name: schedule.name,
        facts: (
          <>
            <FormFact label="Repeats">{describeCron(schedule.expression)}</FormFact>
            <FormFact label="In">{schedule.timezone}</FormFact>
          </>
        ),
      },
      description: [
        upcoming
          ? `It would next have run ${untilLabel(upcoming.toISOString())}`
          : "It stops for good",
        fired
          ? `; its ${plural(fired, "past run")} stay in the deployment history.`
          : "; nothing it already ran is removed.",
      ].join(""),
      action: async () => {
        await del(`${base}/schedules/${schedule.id}`)
      },
      onDone: () => {
        setOpenId(undefined)
        automation.refresh()
      },
    })
  }

  const verbsFor = (schedule: DeploymentSchedule, fired?: number, inSheet = false): Verb[] => [
    ...(inSheet
      ? []
      : [
          {
            key: "runs",
            label: "Runs",
            icon: Logs,
            inline: true,
            run: () => setOpenId(schedule.id),
          },
        ]),
    {
      key: "toggle",
      label: schedule.enabled ? "Pause" : "Enable",
      icon: schedule.enabled ? Pause : Play,
      inline: true,
      run: () => void toggle(schedule),
    },
    {
      key: "edit",
      label: "Edit",
      icon: Pencil,
      run: () => sheet.openEdit(schedule),
    },
    {
      key: "remove",
      label: "Remove",
      icon: Trash,
      danger: true,
      run: () => remove(schedule, fired),
    },
  ]

  const zones = [...new Set(list.map((schedule) => schedule.timezone))]
  const enabled = list.filter((schedule) => schedule.enabled).length

  return (
    <SettingSection
      id="schedules"
      title="Schedules"
      state={
        list.length > 0
          ? `times in ${zones.join(", ")}`
          : "Deploy, restart, back up or run a command on a clock, in a named timezone."
      }
      actions={
        <>
          {list.length > 0 && (
            <span className="mr-1.5 text-hint text-muted-foreground">
              <span className="numeric text-foreground">{plural(list.length, "schedule")}</span> ·{" "}
              <span className="numeric">{enabled}</span> on
            </span>
          )}
          {canAdmin && (
            <Button size="sm" variant="outline" onClick={() => sheet.openAdd()}>
              <Plus className="size-3.5" /> Add schedule
            </Button>
          )}
        </>
      }
    >
      {schedules.loading && !schedules.data ? (
        <LoadingRows rows={2} />
      ) : schedules.error && !schedules.data ? (
        <ErrorState error={schedules.error} onRetry={schedules.refresh} />
      ) : list.length === 0 ? (
        canAdmin ? (
          <div className="space-y-2">
            <p className="text-hint text-muted-foreground">Start one that will</p>
            <ChoiceGrid columns={2} className="grid-cols-2 lg:grid-cols-4">
              {ACTIONS.map((action) => (
                <ActionCard
                  key={action.key}
                  action={action.key}
                  onClick={() => sheet.openAdd({ action: action.key })}
                />
              ))}
            </ChoiceGrid>
          </div>
        ) : (
          <EmptyNote className="px-0 py-2 text-left">No schedules yet.</EmptyNote>
        )
      ) : (
        <ChoiceList aria-label="Schedules" className="animate-rise">
          {list.map((schedule) => (
            <ScheduleCard
              key={schedule.id}
              base={base}
              schedule={schedule}
              jobs={jobs.data}
              verbs={canAdmin ? (fired) => verbsFor(schedule, fired) : undefined}
              onOpen={() => setOpenId(schedule.id)}
            />
          ))}
        </ChoiceList>
      )}

      <ScheduleRunsSheet
        projectId={projectId}
        base={base}
        schedule={opened}
        jobs={jobs.data}
        verbs={canAdmin && opened ? (fired) => verbsFor(opened, fired, true) : undefined}
        onClose={() => setOpenId(undefined)}
      />
      {dialog}
    </SettingSection>
  )
}

/** One of the four things a step can do, as a card you pick it by. */
function ActionCard({
  action,
  selected,
  onClick,
}: {
  action: ScheduleAction
  selected?: boolean
  onClick: () => void
}) {
  const meta = actionOf(action)!
  const Glyph = meta.glyph
  return (
    <ChoiceCard selected={selected} onClick={onClick} className="min-h-0 gap-1 p-2.5">
      <span className="flex min-w-0 items-center gap-2">
        <Glyph
          aria-hidden
          className={cn("size-4 shrink-0", selected ? "text-brand" : "text-muted-foreground")}
        />
        <ChoiceCardTitle className="truncate">{meta.label}</ChoiceCardTitle>
      </span>
      <ChoiceCardHint>{meta.hint}</ChoiceCardHint>
    </ChoiceCard>
  )
}

/** A schedule's tile: the glyph of what its first step does. */
function ScheduleMark({ schedule }: { schedule: DeploymentSchedule }) {
  return (
    <ProductLogo
      size="sm"
      fallback={ACTION_GLYPH[schedule.steps[0]?.action ?? "deploy"] ?? ACTION_GLYPH.deploy}
      className={cn(!schedule.enabled && "opacity-50")}
    />
  )
}

/**
 * The steps a schedule runs, as their glyphs and words joined by arrows: a
 * backup names its job, a command its program.
 */
function StepChain({
  schedule,
  jobs,
  className,
}: {
  schedule: DeploymentSchedule
  jobs?: BackupJob[]
  className?: string
}) {
  return (
    <span className={cn("inline-flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-1", className)}>
      {schedule.steps.map((step, index) => {
        const meta = actionOf(step.action)
        const Glyph = ACTION_GLYPH[step.action]
        const jobId = Number(step.config.jobId ?? step.config.backupJobId)
        const job = jobs?.find((one) => one.id === jobId)
        const argv = Array.isArray(step.config.argv) ? (step.config.argv as string[]) : []
        return (
          <span key={index} className="inline-flex min-w-0 items-center gap-1.5">
            {index > 0 && <ArrowRight aria-label="then" className="size-3 shrink-0 opacity-60" />}
            {Glyph && <Glyph aria-hidden className="size-3.5 shrink-0" />}
            <span className="text-foreground/85">{meta?.label ?? humanize(step.action)}</span>
            {/* The dot is an item of its own, so the gap stands on both sides. */}
            {step.action === "backup" && job && (
              <>
                <span aria-hidden>·</span>
                <span className="truncate">{job.name}</span>
              </>
            )}
            {step.action === "container_command" && argv[0] && (
              <>
                <span aria-hidden>·</span>
                <span className="truncate font-mono">{argv[0]}</span>
              </>
            )}
          </span>
        )
      })}
    </span>
  )
}

/** The runs one firing produced, keyed by the moment it was due. */
type Firing = { key: string; dueAt?: string; runs: DeploymentEngineRun[] }

function firingsOf(runs: DeploymentEngineRun[]): Firing[] {
  const firings: Firing[] = []
  for (const run of runs) {
    const due =
      typeof run.metadata?.scheduleDueAt === "string" ? run.metadata.scheduleDueAt : undefined
    const key = due ?? `run-${run.id}`
    const last = firings.at(-1)
    if (last && last.key === key) last.runs.push(run)
    else firings.push({ key, dueAt: due ?? run.requestedAt, runs: [run] })
  }
  return firings
}

/**
 * How a firing went. Every firing ends with a summary run that records how
 * its chain went, whatever the steps were — a backup or a command leaves no
 * run of its own — so that is the answer where it exists; before it lands,
 * the steps' own runs say it.
 */
function firingTone(firing: Firing): OutcomeTone {
  const marker = firing.runs.find((run) => run.metadata?.scheduleMarker)
  if (marker) return marker.metadata.chainStatus === "failed" ? "danger" : "success"
  const states = firing.runs.map((run) => run.state)
  if (states.some(isActiveRun)) return "running"
  if (states.some((state) => state === "failed" || state === "failed_activation")) return "danger"
  if (states.includes("rolled_back")) return "warning"
  if (states.every((state) => state === "cancelled" || state === "superseded")) return "muted"
  return "success"
}

const TONE_WORD: Record<OutcomeTone, string> = {
  success: "ran",
  danger: "failed",
  warning: "rolled back",
  running: "running",
  muted: "cancelled",
}

function FiringStrip({ firings, timezone }: { firings: Firing[]; timezone: string }) {
  const shown = firings.slice(0, STRIP).reverse()
  if (shown.length === 0) return null
  const failed = shown.filter((firing) => firingTone(firing) === "danger").length
  return (
    <OutcomeStrip
      label={`Last ${plural(shown.length, "firing")}: ${failed} failed`}
      items={shown.map((firing) => {
        const at = zonedMoment(firing.dueAt ?? "", timezone)
        return {
          key: firing.key,
          tone: firingTone(firing),
          title: `${at.day} ${at.time} · ${TONE_WORD[firingTone(firing)]}`,
        }
      })}
    />
  )
}

/**
 * One schedule as a card that opens its runs. When it fires next and whether
 * it is on sit beside the name when the card is wide and lead the line under
 * it on a phone — chosen once, so each is in the page once.
 */
function ScheduleCard({
  base,
  schedule,
  jobs,
  verbs,
  onOpen,
}: {
  base: string
  schedule: DeploymentSchedule
  jobs?: BackupJob[]
  verbs?: (fired?: number) => Verb[]
  onOpen: () => void
}) {
  const project = useProject()
  const wide = useMediaQuery("(min-width: 640px)")
  const runs = usePoll(
    (signal) =>
      get<{ runs: DeploymentEngineRun[] }>(
        `${base}/schedules/${schedule.id}/runs`,
        undefined,
        signal,
      ),
    60_000,
    [base, schedule.id],
  )
  const firings = firingsOf(runs.data?.runs ?? [])
  const running = project.runs.some(
    (run) => run.metadata?.scheduleId === schedule.id && isActiveRun(run.state),
  )
  const next = schedule.enabled ? schedule.nextRunAt : undefined
  const until = untilLabel(next)
  const readings = (
    <>
      {running ? (
        <TextShimmer className="pr-0.5 text-xs font-medium whitespace-nowrap">
          running now
        </TextShimmer>
      ) : next ? (
        <span
          className={cn(
            "numeric text-xs whitespace-nowrap",
            overdue(next) ? "text-warning" : "text-muted-foreground",
          )}
          title={`${zonedMoment(next, schedule.timezone).day} ${zonedMoment(next, schedule.timezone).time} ${schedule.timezone}`}
        >
          {until.startsWith("in ") ? `next ${until}` : until}
        </span>
      ) : null}
      <Status
        tone={schedule.enabled ? "running" : "stopped"}
        label={schedule.enabled ? "Enabled" : "Paused"}
      />
    </>
  )
  return (
    <ChoiceRow
      verb={`Open ${schedule.name}`}
      onSelect={onOpen}
      busy={running}
      className={cn(!schedule.enabled && "opacity-80")}
      leading={<ScheduleMark schedule={schedule} />}
      title={schedule.name}
      description={
        // The row's line is cut to one; on a phone the sentence wraps instead,
        // or the zone that says which three o'clock is the part that goes.
        <span className="max-sm:whitespace-normal">
          {describeCron(schedule.expression)} · {schedule.timezone}
        </span>
      }
      trailing={wide ? <span className="flex items-center gap-4">{readings}</span> : undefined}
      actions={
        verbs && (
          <VerbActions
            dim
            verbs={verbs(runs.data?.runs.length)}
            menuLabel={`Actions for ${schedule.name}`}
          />
        )
      }
    >
      <div className="flex min-w-0 flex-wrap items-center gap-x-6 gap-y-2 text-hint text-muted-foreground sm:pl-11">
        {!wide && <span className="flex flex-wrap items-center gap-x-4 gap-y-1">{readings}</span>}
        <StepChain schedule={schedule} jobs={jobs} />
        <FiringStrip firings={firings} timezone={schedule.timezone} />
        {wide && (
          <Tag mono className="ml-auto">
            {schedule.expression}
          </Tag>
        )}
      </div>
    </ChoiceRow>
  )
}

// ---------------------------------------------------------------------------
// Runs sheet
// ---------------------------------------------------------------------------

/**
 * The next five firings: the server's, which walks the clock that fires them,
 * else the page's own walk in the schedule's zone. A time already past is not
 * a next run — the dispatcher is catching up — so it is left off the line.
 */
function nextRunsOf(schedule: DeploymentSchedule, now: number): Date[] {
  if (!schedule.enabled) return []
  const server = (schedule.nextRuns ?? []).map((iso) => new Date(iso))
  const ahead = server.filter((at) => at.getTime() > now)
  if (ahead.length > 0) return ahead
  return nextCronRunsIn(schedule.expression, schedule.timezone, 5, new Date(now))
}

/**
 * The firings that happened and the ones to come, on one line around now.
 * Each half is its own time axis — the past from the oldest firing drawn to
 * now, the future from now to the fifth run — so a nightly schedule's two
 * weeks of history and its next five nights both get the width to be read,
 * and within each half a gap in the record is a gap on the line (§10).
 * Below `sm` the next runs are a list, which is how a phone reads times.
 */
function ScheduleTimeline({
  schedule,
  firings,
}: {
  schedule: DeploymentSchedule
  firings: Firing[]
}) {
  const wide = useMediaQuery("(min-width: 640px)")
  const now = useNow(60_000)
  const next = nextRunsOf(schedule, now)
  const past = firings.slice(0, STRIP).reverse()
  if (!wide) {
    return (
      <div className="space-y-2">
        {past.length > 0 && <FiringStrip firings={firings} timezone={schedule.timezone} />}
        {next.length === 0 ? (
          <p className="text-hint text-muted-foreground">Paused — nothing is due.</p>
        ) : (
          <ol aria-label="Next runs" className="space-y-1 text-hint">
            {next.map((at) => {
              const when = zonedMoment(at, schedule.timezone)
              return (
                <li key={at.toISOString()} className="flex items-center gap-2">
                  <span
                    aria-hidden
                    className="h-3 w-1.5 shrink-0 rounded-sm border border-border-strong"
                  />
                  <span className="numeric text-foreground/85">
                    {when.day} · {when.time}
                  </span>
                  <span className="text-muted-foreground">{untilLabel(at.toISOString())}</span>
                </li>
              )
            })}
          </ol>
        )}
      </div>
    )
  }
  const oldest = past.length > 0 ? Date.parse(past[0].dueAt ?? "") : now
  const latest = next.length > 0 ? next[next.length - 1].getTime() : now
  const at = (time: number) =>
    time <= now
      ? past.length > 0 && now > oldest
        ? 4 + ((time - oldest) / (now - oldest)) * 42
        : 46
      : 50 + ((time - now) / Math.max(1, latest - now)) * 46
  const pastAt = spread(
    past.map((firing) => at(Date.parse(firing.dueAt ?? ""))),
    4,
    46,
  )
  return (
    <div
      role="img"
      aria-label={`${plural(past.length, "firing")} so far; next ${next
        .slice(0, 3)
        .map((one) => {
          const when = zonedMoment(one, schedule.timezone)
          return `${when.day} ${when.time}`
        })
        .join(", ")}`}
      className="relative h-16 min-w-0"
    >
      <span aria-hidden className="absolute inset-x-0 top-5 h-px bg-hairline" />
      <span aria-hidden className="absolute top-2 h-7 w-px bg-brand" style={{ left: "48%" }} />
      <span
        aria-hidden
        className="absolute top-9 -translate-x-1/2 text-micro text-brand"
        style={{ left: "48%" }}
      >
        now
      </span>
      {past.map((firing, index) => (
        <span
          key={firing.key}
          title={`${zonedMoment(firing.dueAt ?? "", schedule.timezone).day} ${zonedMoment(firing.dueAt ?? "", schedule.timezone).time} · ${TONE_WORD[firingTone(firing)]}`}
          className={cn(
            "absolute top-3.5 h-3 w-1.5 -translate-x-1/2 rounded-sm",
            OUTCOME_FILL[firingTone(firing)],
          )}
          style={{ left: `${pastAt[index]}%` }}
        />
      ))}
      {past.length === 0 && (
        <span className="absolute top-8 left-0 text-micro text-muted-foreground">
          not fired yet
        </span>
      )}
      {next.map((when) => {
        const moment = zonedMoment(when, schedule.timezone)
        return (
          <span
            key={when.toISOString()}
            className="absolute top-3.5 flex -translate-x-1/2 flex-col items-center"
            style={{ left: `${at(when.getTime())}%` }}
          >
            <span className="h-3 w-1.5 rounded-sm border border-border-strong bg-background" />
            <span className="mt-1.5 text-micro leading-tight whitespace-nowrap text-muted-foreground">
              {moment.day.split(" ")[0]}
            </span>
            <span className="numeric text-micro leading-tight whitespace-nowrap text-foreground/85">
              {moment.time}
            </span>
          </span>
        )
      })}
    </div>
  )
}

/**
 * Positions on the line, oldest first, moved apart just enough that no two
 * marks touch: firings a minute apart three weeks ago would otherwise stand
 * as one blob. The gap to now is left as it is — that stretch is the record.
 */
function spread(positions: number[], from: number, to: number, gap = 1.4) {
  const out = [...positions]
  for (let i = 1; i < out.length; i++) out[i] = Math.max(out[i], out[i - 1] + gap)
  for (let i = out.length - 1; i >= 0; i--)
    out[i] = Math.min(out[i], i === out.length - 1 ? to : out[i + 1] - gap)
  return out.map((left) => Math.max(from, left))
}

/**
 * A schedule's own sheet: what it is, as data — the sentence, the zone, the
 * expression, the chain, the next run on its own clock — then the timeline,
 * then every run it produced under the firing that produced it, each opening
 * its run page.
 */
function ScheduleRunsSheet({
  projectId,
  base,
  schedule,
  jobs,
  verbs,
  onClose,
}: {
  projectId: number
  base: string
  schedule?: DeploymentSchedule
  jobs?: BackupJob[]
  verbs?: (fired?: number) => Verb[]
  onClose: () => void
}) {
  const runs = usePoll(
    (signal) =>
      get<{ runs: DeploymentEngineRun[] }>(
        `${base}/schedules/${schedule?.id}/runs`,
        undefined,
        signal,
      ),
    10_000,
    [base, schedule?.id],
    { enabled: schedule !== undefined },
  )
  const firings = firingsOf(runs.data?.runs ?? [])
  const now = useNow(60_000)
  // The next run the timeline draws, so the facts and the line give one
  // answer; a recorded next run long past is a firing the dispatcher missed,
  // said as that rather than as "due now".
  const upcoming = schedule ? nextRunsOf(schedule, now)[0] : undefined
  const nextAt = upcoming?.toISOString()
  const missed =
    schedule?.enabled && overdue(schedule.nextRunAt, now) ? schedule.nextRunAt : undefined

  return (
    <SidePanel
      open={schedule !== undefined}
      onOpenChange={(next) => !next && onClose()}
      title={`${schedule?.name ?? "Schedule"} runs`}
      description="The runs this schedule has produced, and when it fires next."
      width="md"
      actions={
        schedule &&
        verbs && (
          <VerbBar
            verbs={verbs(runs.data?.runs.length)}
            menuLabel={`Actions for ${schedule.name} in its sheet`}
          />
        )
      }
    >
      {schedule && (
        <div className="space-y-6">
          <div className="flex min-w-0 items-start gap-3">
            <ScheduleMark schedule={schedule} />
            <div className="min-w-0 flex-1 space-y-1">
              <p className="text-title font-semibold tracking-tight">
                {describeCron(schedule.expression)}
              </p>
              <FormFacts>
                <FormFact label="In">
                  {schedule.timezone}
                  <span className="text-muted-foreground"> · {zoneOffset(schedule.timezone)}</span>
                </FormFact>
                <FormFact label="Expression" mono>
                  {schedule.expression}
                </FormFact>
                <FormFact label="Next">
                  {nextAt
                    ? `${zonedMoment(nextAt, schedule.timezone).day} ${zonedMoment(nextAt, schedule.timezone).time} · ${untilLabel(nextAt)}`
                    : "paused"}
                </FormFact>
                {missed && (
                  <FormFact label="Missed">
                    <span className="text-warning">
                      {zonedMoment(missed, schedule.timezone).day}{" "}
                      {zonedMoment(missed, schedule.timezone).time}
                    </span>
                  </FormFact>
                )}
              </FormFacts>
              <StepChain
                schedule={schedule}
                jobs={jobs}
                className="text-hint text-muted-foreground"
              />
            </div>
          </div>

          <ScheduleTimeline schedule={schedule} firings={firings} />

          {runs.error && !runs.data ? (
            <ErrorState error={runs.error} onRetry={runs.refresh} />
          ) : runs.loading && !runs.data ? (
            <LoadingRows rows={4} />
          ) : firings.length === 0 ? (
            <EmptyNote className="px-0 text-left">
              {nextAt
                ? `It has not fired yet — first run ${untilLabel(nextAt)}.`
                : "It has not fired yet, and it is paused."}
            </EmptyNote>
          ) : (
            <div className="space-y-4">
              {firings.map((firing) => {
                const when = zonedMoment(firing.dueAt ?? "", schedule.timezone)
                return (
                  <section key={firing.key} className="space-y-2">
                    <GroupRule label={`${when.day} ${when.time}`} count={firing.runs.length} />
                    <ChoiceList aria-label={`Runs of ${when.day} ${when.time}`}>
                      {firing.runs.map((run) => (
                        <ChoiceRow
                          key={run.id}
                          href={`/deploy/${projectId}/runs/${run.id}`}
                          verb={`Open ${runTitle(run)}`}
                          busy={isActiveRun(run.state)}
                          title={runTitle(run)}
                          description={[
                            relativeTime(run.requestedAt),
                            typeof run.metadata.chainAction === "string" &&
                              actionOf(run.metadata.chainAction)?.label,
                            Boolean(run.metadata.scheduleMarker) && "the firing's summary",
                          ]
                            .filter(Boolean)
                            .join(" · ")}
                          trailing={
                            <>
                              <RunStatus state={run.state} />
                              <span className="numeric w-16 text-right text-hint whitespace-nowrap text-muted-foreground">
                                {formatDuration(runDurationSeconds(run))}
                              </span>
                            </>
                          }
                        />
                      ))}
                    </ChoiceList>
                  </section>
                )
              })}
            </div>
          )}
        </div>
      )}
    </SidePanel>
  )
}

// ---------------------------------------------------------------------------
// Add and Edit
// ---------------------------------------------------------------------------

/** The zones Intl knows, UTC and this browser's own first. */
function zones(): string[] {
  const all = Intl.supportedValuesOf("timeZone")
  const own = browserZone()
  return [...new Set(["UTC", own, ...all])]
}

/**
 * A timezone, picked from the zones the platform knows by typing any part of
 * one — "chis", "new_y" — rather than typed out whole into a free field where
 * "Europe/Chisnau" was a schedule that never fired. The trigger shows the zone
 * and its offset now.
 */
function TimezonePicker({
  id,
  value,
  onChange,
}: {
  id: string
  value: string
  onChange: (zone: string) => void
}) {
  const [open, setOpen] = useState(false)
  // Each offset is an Intl formatter of its own; four hundred of them on
  // every keystroke in the sheet is lag a phone feels.
  const list = useMemo(() => zones().map((zone) => ({ zone, offset: zoneOffset(zone) })), [])
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          id={id}
          type="button"
          variant="outline"
          role="combobox"
          aria-expanded={open}
          className="h-11 w-full justify-between font-normal sm:h-9"
        >
          <span className="flex min-w-0 items-center gap-2">
            <span className="truncate">{value}</span>
            <span className="numeric shrink-0 text-hint text-muted-foreground">
              {zoneOffset(value)}
            </span>
          </span>
          <ArrowUpDown className="size-3.5 opacity-50" />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-(--radix-popover-trigger-width) p-0" align="start">
        <Command>
          <CommandInput placeholder="Search zones…" className="h-9" />
          <CommandList>
            <CommandEmpty>No zone by that name.</CommandEmpty>
            <CommandGroup>
              {list.map(({ zone, offset }) => (
                <CommandItem
                  key={zone}
                  value={zone}
                  onSelect={() => {
                    onChange(zone)
                    setOpen(false)
                  }}
                >
                  <Check className={cn("size-3.5", value === zone ? "opacity-100" : "opacity-0")} />
                  <span className="flex-1 truncate">{zone}</span>
                  <span className="numeric text-micro text-muted-foreground">{offset}</span>
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}

/** An argument vector as a shell would need it typed, for the preview of what runs. */
function shellQuote(argv: string[]) {
  return argv
    .map((arg) => (/^[\w@%+=:,./-]+$/.test(arg) ? arg : `'${arg.replaceAll("'", `'\\''`)}'`))
    .join(" ")
}

const OTHER_CONTAINER = "__other__"

/**
 * Adding a schedule, or editing one.
 *
 * When it fires is built from the words people use — every day, at three —
 * in a zone picked from the ones that exist, and read back as a sentence
 * with its next runs, checked against the server's own clock (which is the
 * one that fires it). What it runs is picked by glyph from four cards, and a
 * command shows the exact argument vector it will exec before it is saved.
 */
export function ScheduleSheet({
  projectId,
  environmentId,
  sheet,
  onSaved,
}: {
  projectId: number
  environmentId: number
  sheet: ScheduleSheetController
  onSaved: () => void
}) {
  const project = useProject()
  const base = `/deploy/${projectId}/environments/${environmentId}`
  const { target, draft, setDraft } = sheet
  const editing = target?.mode === "edit" ? target.schedule : undefined
  const first = editing?.steps[0]
  // A first step this form has no card for — one made through the API.
  const foreign = editing && !actionOf(first?.action ?? "") ? editing : undefined
  // The steps after the first, which the form does not draw and an edit keeps.
  const rest = editing?.steps.slice(1) ?? []
  const [saving, setSaving] = useState(false)
  const jobs = useBackupJobs(Boolean(target) && draft.action === "backup")
  const services = [...(project.detail.runtime?.services ?? [])].sort(
    (a, b) => Number(b.liveRelease) - Number(a.liveRelease),
  )
  const patch = (next: Partial<ScheduleDraft>) =>
    setDraft((prev) => {
      const merged = { ...prev, ...next }
      return merged.named
        ? { ...merged, name: suggestedName(merged.fields, merged.action) }
        : merged
    })
  const expression = scheduleExpression(draft.fields)
  const argv = draft.argv
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
  const job = jobs.data?.find((one) => String(one.id) === draft.backupJobId)

  // The server walks the clock that actually fires the schedule, so its
  // answer is the one the footer reads — asked for once the expression and
  // zone have settled, not on every keystroke, since each answer is audited.
  const [tested, setTested] = useState<{
    key: string
    answer?: DeploymentScheduleTest
    refused?: string
  }>()
  const testKey = `${expression}\u0000${draft.timezone}`
  const testable = Boolean(target) && expression !== "" && isValidCron(expression)
  useEffect(() => {
    if (!testable) return
    const controller = new AbortController()
    const timer = setTimeout(() => {
      post<DeploymentScheduleTest>(
        `${base}/schedules/test`,
        { name: draft.name, expression, timezone: draft.timezone, enabled: true, steps: [] },
        { signal: controller.signal },
      )
        .then((answer) => setTested({ key: testKey, answer }))
        .catch((error: unknown) => {
          // Only a refusal of the expression is an answer; a server that could
          // not be asked leaves the page's own reading standing.
          if (controller.signal.aborted) return
          if (error instanceof ApiError && (error.status === 400 || error.status === 422))
            setTested({ key: testKey, refused: error.message })
        })
    }, 700)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
    // The name rides along for the audit line only; it is not a reason to ask again.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [testKey, testable, base])
  const test = tested?.key === testKey ? tested : undefined
  const firstRun =
    test?.answer?.nextRunAt ?? nextCronRunsIn(expression, draft.timezone, 1)[0]?.toISOString()

  const configFor = (): Record<string, unknown> => {
    const timeout = draft.timeoutSeconds ? Number(draft.timeoutSeconds) : undefined
    switch (draft.action) {
      case "backup":
        return { jobId: Number(draft.backupJobId), timeoutSeconds: timeout }
      case "container_command":
        return { containerId: draft.containerId.trim(), argv, timeoutSeconds: timeout }
      default:
        return {}
    }
  }

  const save = async () => {
    setSaving(true)
    const body = {
      name: draft.name.trim(),
      expression,
      timezone: draft.timezone,
      enabled: editing?.enabled ?? true,
      steps: foreign
        ? foreign.steps
        : editing
          ? [
              {
                action: draft.action,
                // Keys the form does not draw survive an edit of the same action.
                config:
                  first?.action === draft.action
                    ? { ...first.config, ...configFor() }
                    : configFor(),
                required: first?.required ?? true,
              },
              ...rest,
            ]
          : [{ action: draft.action, config: configFor(), required: true }],
    }
    try {
      if (editing) {
        await put(`${base}/schedules/${editing.id}`, body)
        notify.success("Schedule saved")
      } else {
        await post(`${base}/schedules`, body)
        notify.success("Schedule created")
        sheet.forget()
      }
      onSaved()
      sheet.close()
    } catch (error) {
      notify.error(editing ? "Could not save schedule" : "Could not create schedule", error)
    } finally {
      setSaving(false)
    }
  }

  const invalid =
    !draft.name.trim() ||
    !expression ||
    !scheduleValid(expression) ||
    Boolean(test?.refused) ||
    (draft.action === "backup" && !draft.backupJobId) ||
    (draft.action === "container_command" && (!draft.containerId.trim() || argv.length === 0))

  return (
    <SidePanel
      open={Boolean(target)}
      onOpenChange={(open) => !open && !saving && sheet.close()}
      title={editing ? `Edit ${editing.name}` : "Add schedule"}
      description="Run an action on a clock, in a named timezone."
      width="md"
      footer={
        <>
          <FormNote className="mr-auto basis-full sm:basis-auto">
            {expression && scheduleValid(expression) ? (
              <>
                {describeCron(expression)}
                {firstRun && ` · next ${untilLabel(firstRun)}`}
                {test?.answer && (
                  <Check
                    aria-label="checked by the server"
                    className="ml-1 inline size-3 align-[-1px] text-success"
                  />
                )}
              </>
            ) : (
              "Fix the expression to see when it fires"
            )}
          </FormNote>
          <Button variant="outline" onClick={sheet.close} disabled={saving}>
            Cancel
          </Button>
          <Button onClick={() => void save()} disabled={invalid} pending={saving}>
            {editing ? "Save schedule" : "Add schedule"}
          </Button>
        </>
      }
    >
      {target && (
        <div className="space-y-6" aria-busy={saving}>
          <Field label="Name" htmlFor="schedule-name">
            <Input
              id="schedule-name"
              value={draft.name}
              onChange={(event) =>
                setDraft((prev) => ({ ...prev, name: event.target.value, named: false }))
              }
            />
          </Field>

          <FormSection title="When">
            <ScheduleBuilder
              idPrefix="schedule"
              timeZone={draft.timezone}
              fields={draft.fields}
              onChange={(fields) => patch({ fields })}
            />
            {test?.refused && (
              <FormNote tone="danger" role="alert">
                {test.refused}
              </FormNote>
            )}
            <Field
              label="Timezone"
              htmlFor="schedule-timezone"
              hint="The clock the times above are read on."
            >
              <TimezonePicker
                id="schedule-timezone"
                value={draft.timezone}
                onChange={(timezone) => patch({ timezone })}
              />
            </Field>
          </FormSection>

          <FormSection title="What">
            {foreign ? (
              // A step this form has no card for — made through the API — is
              // shown as it is and saved as it is, not quietly turned into a
              // deploy.
              <div className="space-y-2">
                <StepChain schedule={foreign} className="text-body text-muted-foreground" />
                <FormNote>
                  These steps were set up outside this form and are kept as they are.
                </FormNote>
              </div>
            ) : (
              <>
                <div role="group" aria-label="Action">
                  <ChoiceGrid columns={2} className="grid-cols-2">
                    {ACTIONS.map((action) => (
                      <ActionCard
                        key={action.key}
                        action={action.key}
                        selected={draft.action === action.key}
                        onClick={() => patch({ action: action.key })}
                      />
                    ))}
                  </ChoiceGrid>
                </div>

                {draft.action === "backup" && (
                  <Field
                    label="Backup job"
                    htmlFor="schedule-backup-job"
                    error={!draft.backupJobId ? "Choose a backup job." : undefined}
                  >
                    <Select
                      value={draft.backupJobId}
                      onValueChange={(backupJobId) => patch({ backupJobId })}
                      disabled={jobs.loading}
                    >
                      <SelectTrigger id="schedule-backup-job" className="w-full">
                        <SelectValue placeholder={jobs.loading ? "Loading…" : "Choose a job"} />
                      </SelectTrigger>
                      <SelectContent>
                        {jobs.data?.map((one) => (
                          <SelectItem key={one.id} value={String(one.id)} hint={contentsLabel(one)}>
                            {one.name}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </Field>
                )}
                {draft.action === "backup" && job && (
                  <FormFacts className="animate-rise">
                    <FormFact label="Takes">{contentsLabel(job) || "nothing yet"}</FormFact>
                    <FormFact label="Writes to">
                      <span className="inline-flex items-center gap-1.5">
                        {destinationProduct(job) && <ProductGlyph id={destinationProduct(job)!} />}
                        {targetLabel(job)}
                      </span>
                    </FormFact>
                  </FormFacts>
                )}

                {draft.action === "container_command" && (
                  <>
                    <Field label="Container" htmlFor="schedule-container-pick">
                      <Select
                        value={draft.otherContainer ? OTHER_CONTAINER : draft.containerId}
                        onValueChange={(value) =>
                          value === OTHER_CONTAINER
                            ? patch({ otherContainer: true, containerId: "" })
                            : patch({ otherContainer: false, containerId: value })
                        }
                      >
                        <SelectTrigger id="schedule-container-pick" className="w-full">
                          <SelectValue placeholder="Choose a container" />
                        </SelectTrigger>
                        <SelectContent>
                          {services.map((service) => (
                            <SelectItem
                              key={service.containerId}
                              value={service.containerId}
                              hint={service.liveRelease ? "live release" : service.state}
                            >
                              <StatusDot state={service.state} />
                              {service.name}
                            </SelectItem>
                          ))}
                          <SelectItem value={OTHER_CONTAINER}>Another container ID…</SelectItem>
                        </SelectContent>
                      </Select>
                    </Field>
                    {draft.otherContainer && (
                      <Field label="Container ID" htmlFor="schedule-container">
                        <Input
                          id="schedule-container"
                          className="font-mono sm:text-xs"
                          value={draft.containerId}
                          onChange={(event) => patch({ containerId: event.target.value })}
                          spellCheck={false}
                          autoComplete="off"
                        />
                      </Field>
                    )}
                    <Field
                      label="Command"
                      htmlFor="schedule-argv"
                      hint="One argument per line: the program, then each argument."
                    >
                      <Textarea
                        id="schedule-argv"
                        rows={3}
                        className="font-mono sm:text-xs"
                        value={draft.argv}
                        onChange={(event) => patch({ argv: event.target.value })}
                        spellCheck={false}
                      />
                    </Field>
                    <Statement
                      label="Runs"
                      sql={argv.length > 0 ? shellQuote(argv) : ""}
                      placeholder="The command appears here as it will run."
                    />
                  </>
                )}

                {(draft.action === "backup" || draft.action === "container_command") && (
                  <FieldRow>
                    <Field
                      label="Timeout"
                      htmlFor="schedule-timeout"
                      hint="Empty waits up to an hour; at most 12 hours."
                    >
                      <InputGroup>
                        <InputGroupInput
                          id="schedule-timeout"
                          type="number"
                          inputMode="numeric"
                          min={1}
                          max={43200}
                          placeholder="3600"
                          className="numeric"
                          value={draft.timeoutSeconds}
                          onChange={(event) => patch({ timeoutSeconds: event.target.value })}
                        />
                        <InputGroupAddon align="inline-end">
                          <InputGroupText>seconds</InputGroupText>
                        </InputGroupAddon>
                      </InputGroup>
                    </Field>
                  </FieldRow>
                )}
                {rest.length > 0 && (
                  <FormNote>
                    Then{" "}
                    {rest
                      .map((step) => actionOf(step.action)?.label ?? humanize(step.action))
                      .join(" → ")}
                    , as saved — this form changes the first step.
                  </FormNote>
                )}
              </>
            )}
          </FormSection>
        </div>
      )}
    </SidePanel>
  )
}
