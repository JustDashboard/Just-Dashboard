"use client"

import { useEffect, useMemo, useState } from "react"
import { forgetMemoryState, useMemoryState, useSessionState } from "@/lib/view-state"
import { Clock, Copy, Pause, Pencil, Play, Plus, Trash } from "@/components/icons"
import { put } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import {
  CRON_PRESETS,
  describeNativeCron as describeCron,
  isValidNativeCron as isValidCron,
  nextNativeCronRun as nextCronRun,
  presetFor,
  supportsCronTimeZone,
  type CronPreset,
} from "@/lib/cron"
import { appendJob, removeJob, replaceJob, toggleJob, type JobEdit } from "@/lib/crontab"
import { cronLineName, cronProgram, longClock } from "@/lib/schedule"
import { notify } from "@/lib/toast"
import type { CronJob, Crontab } from "@/lib/types"
import { cn } from "@/lib/utils"
import { useArrivals } from "@/hooks/use-arrivals"
import { useAuth } from "@/hooks/use-auth"
import type { PollState } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { Field, FieldRow, FormNote, FormSection, OptionList, OptionRow } from "@/components/form"
import type { ServiceLogSource } from "@/components/logs/service-logs"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { ProductLogo, programProduct } from "@/components/product-logo"
import { SidePanel, SidePanelFooter } from "@/components/side-panel"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Tag } from "@/components/tag"
import { VerbActions, type Verb } from "@/components/verbs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  stickyTableHeader,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { CronJobSheet } from "@/components/procs/cron-job-sheet"
import { Countdown } from "@/components/procs/schedule-band"
import { WeekStrip } from "@/components/procs/schedule-sheet"
import { RunAt } from "@/components/procs/timers"
import { cronRowKeys } from "@/components/procs/row-identity"

type ConfirmFn = (request: ConfirmRequest) => void

/**
 * One account's crontab as a list of jobs you can act on, rather than a
 * file you edit. Each row says what the schedule means and counts down to
 * when it fires next; the controls disable, edit and remove one job by
 * rewriting only its own lines (`lib/crontab.ts`) and sending the file back.
 * The text editor is still there, one button away, for the crontab that does
 * something the builder cannot say.
 *
 * The crontab is polled by the page rather than here, because the page's
 * band — what runs in the next day — is made of it too. Each job is drawn as
 * the program it runs (§14), read past its guards: `cd / && run-parts …` is
 * run-parts', `docker system prune` Docker's, and a script of the
 * operator's own keeps the clock. The head counts the jobs and the disabled
 * as chips that narrow the table, where the Cron jobs tile used to count them.
 *
 * A row opens the job's sheet (`cron-job-sheet.tsx`); Add job and Edit open
 * the job editor, a sheet as well, which draws the week the schedule being
 * written makes as it is written.
 */
export function CronJobsPanel({
  user,
  users,
  crontab,
  onUserChange,
  confirm,
  adding,
  onAddingChange,
  open,
  onOpen,
  cron,
  zone,
}: {
  /** The host's zone, which a run's clock time is written in. */
  zone?: string
  user: string
  users: string[]
  crontab: PollState<Crontab>
  onUserChange: (user: string) => void
  confirm: ConfirmFn
  adding: boolean
  onAddingChange: (open: boolean) => void
  /** The job whose sheet is open, as its lane key (`cron:<line>`). */
  open: string | null
  onOpen: (key: string | null) => void
  cron?: ServiceLogSource
}) {
  const { can } = useAuth()
  // The draft remembers whose crontab it is, so switching accounts cannot
  // save one account's text over another's.
  const [draftFor, setDraftFor] = useState<{
    user: string
    text: string
    revision?: string
  } | null>(null)
  const draft = draftFor?.user === user ? draftFor.text : null
  const setDraft = (text: string | null) =>
    setDraftFor((previous) =>
      text === null
        ? null
        : {
            user,
            text,
            revision: previous?.user === user ? previous.revision : crontab.data?.revision,
          },
    )
  const [editing, setEditing] = useMemoryState<CronJob | null>(
    `processes.cron.editing.${user}`,
    null,
  )
  const [shown, setShown] = useSessionState<"" | "enabled" | "disabled">("processes.cron.shown", "")
  const [saving, setSaving] = useState(false)

  const save = async (
    content: string,
    done: string,
    confirmText?: string,
    expectedRevision = crontab.data?.revision,
  ) => {
    setSaving(true)
    try {
      if (!expectedRevision) throw new Error("Reload the crontab before saving this draft.")
      await put(
        `/cron/user/${encodeURIComponent(user)}`,
        { content, expectedRevision },
        { confirm: confirmText },
      )
      notify.success(done)
      crontab.refresh()
    } catch (err) {
      notify.error("Could not update the crontab", err)
      throw err
    } finally {
      setSaving(false)
    }
  }

  const raw = crontab.data?.raw ?? ""
  const jobs = useMemo(() => crontab.data?.jobs ?? [], [crontab.data])
  const disabled = jobs.filter((job) => job.disabled).length
  const listed = shown ? jobs.filter((job) => job.disabled === (shown === "disabled")) : jobs
  const identities = cronRowKeys(jobs)
  const rowKeys = new Map(jobs.map((job, index) => [job, identities[index]]))
  const arrived = useArrivals(identities)
  const opened = open ? jobs.find((job) => `cron:${job.line}` === open) : undefined
  const admin = can("system.admin")

  const actions = (job: CronJob) => ({
    onToggle: () =>
      void save(
        toggleJob(raw, job),
        `${job.command} ${job.disabled ? "enabled" : "disabled"}`,
      ).catch(() => undefined),
    onEdit: () => {
      onOpen(null)
      setEditing(job)
    },
    onRemove: async (phrase: string) => {
      await save(removeJob(raw, job), "Cron job removed", phrase)
      if (open === `cron:${job.line}`) onOpen(null)
    },
  })

  return (
    <>
      <Panel>
        <PanelHeader
          title="Cron jobs"
          actions={
            <>
              <Select value={user} onValueChange={onUserChange}>
                <SelectTrigger size="sm" className="w-28 sm:w-40" aria-label="Crontab account">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="root">root</SelectItem>
                  {users
                    .filter((u) => u !== "root")
                    .map((u) => (
                      <SelectItem key={u} value={u}>
                        {u}
                      </SelectItem>
                    ))}
                </SelectContent>
              </Select>
              {admin && draft === null && crontab.data && (
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => setDraft(raw)}
                  aria-label="Edit as text"
                >
                  <Pencil className="size-3.5" />
                  {/* A phone's head has room for the account and Add job;
                      the pencil says the rest. */}
                  <span className="max-sm:hidden">Edit as text</span>
                </Button>
              )}
              {admin && (
                <Button
                  size="sm"
                  disabled={!crontab.data?.revision}
                  onClick={() => onAddingChange(true)}
                >
                  <Plus className="size-3.5" />
                  Add job
                </Button>
              )}
            </>
          }
        >
          {jobs.length > 0 && draft === null && (
            <ChipStrip aria-label="Job state" className="mr-auto">
              <FilterChip selected={shown === ""} onClick={() => setShown("")}>
                All <ChipCount>{jobs.length}</ChipCount>
              </FilterChip>
              {disabled > 0 && (
                <>
                  <FilterChip
                    selected={shown === "enabled"}
                    onClick={() => setShown(shown === "enabled" ? "" : "enabled")}
                  >
                    <span aria-hidden className="size-1.5 rounded-full bg-success" />
                    Enabled <ChipCount>{jobs.length - disabled}</ChipCount>
                  </FilterChip>
                  <FilterChip
                    selected={shown === "disabled"}
                    onClick={() => setShown(shown === "disabled" ? "" : "disabled")}
                  >
                    <span aria-hidden className="size-1.5 rounded-full bg-muted-foreground/50" />
                    Disabled <ChipCount>{disabled}</ChipCount>
                  </FilterChip>
                </>
              )}
            </ChipStrip>
          )}
        </PanelHeader>
        <PanelBody flush={draft === null}>
          {crontab.loading && !crontab.data && <LoadingRows rows={3} className="pt-3" />}
          {crontab.error && !crontab.data && <ErrorState error={crontab.error} className="mt-3" />}
          {crontab.data && draft === null && (
            <>
              {jobs.length === 0 ? (
                <div className="flex flex-col items-center gap-3 px-6 py-10 text-center">
                  <span className="flex size-10 items-center justify-center rounded-xl border border-hairline bg-surface-sunken">
                    <Clock className="size-4 text-muted-foreground" />
                  </span>
                  <div className="space-y-1">
                    <p className="text-body font-medium">{user} has no cron jobs</p>
                    <p className="max-w-sm text-hint text-muted-foreground">
                      A job here runs as {user} on a schedule you set — a nightly backup, a cleanup,
                      a health check.
                      {admin && " Add one, or paste a whole crontab as text."}
                    </p>
                  </div>
                  {admin && crontab.data.revision && (
                    <Button size="sm" variant="outline" onClick={() => onAddingChange(true)}>
                      <Plus className="size-3.5" />
                      Add a job
                    </Button>
                  )}
                </div>
              ) : listed.length === 0 ? (
                <EmptyNote>No {shown} jobs.</EmptyNote>
              ) : (
                <Table
                  className="table-fixed"
                  containerClassName="group-data-[plain]/panel:-mx-4 max-h-[calc(100svh-22rem)] w-auto"
                >
                  <TableHeader className={stickyTableHeader}>
                    <TableRow>
                      <TableHead>Job</TableHead>
                      <TableHead className="hidden w-[30%] lg:table-cell">Schedule</TableHead>
                      <TableHead className="hidden w-36 md:table-cell">Next run</TableHead>
                      <TableHead className="w-28 md:w-32" />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {listed.map((job) => (
                      <CronJobRow
                        key={`${user}:${rowKeys.get(job)}`}
                        job={job}
                        zone={zone}
                        arrived={arrived.has(rowKeys.get(job)!)}
                        admin={admin}
                        confirm={confirm}
                        onOpen={() => onOpen(`cron:${job.line}`)}
                        {...actions(job)}
                      />
                    ))}
                  </TableBody>
                </Table>
              )}
            </>
          )}
          {draft !== null && (
            <div className="space-y-3">
              <Textarea
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                className="min-h-64 font-mono text-xs"
                spellCheck={false}
                aria-label={`Crontab of ${user}`}
              />
              <FormNote>
                The whole crontab of {user} is replaced with this text. Schedules are checked before
                anything is written; a line cron would reject is named, with its number.
              </FormNote>
              <div className="flex gap-2">
                <Button
                  size="sm"
                  disabled={saving}
                  onClick={() =>
                    confirm({
                      title: "Replace crontab",
                      confirmLabel: "Save",
                      description: (
                        <p>
                          Replaces the entire crontab for <b>{user}</b>. Jobs start running on the
                          new schedule immediately.
                        </p>
                      ),
                      action: async (phrase) => {
                        await save(draft, `Crontab of ${user} saved`, phrase, draftFor?.revision)
                        setDraft(null)
                      },
                    })
                  }
                >
                  Save
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setDraft(null)}>
                  Cancel
                </Button>
              </div>
            </div>
          )}
        </PanelBody>
        {crontab.data &&
          (crontab.data.env.length > 0 || (jobs.length > 0 && !crontab.data.timeZone)) &&
          draft === null && (
            <PanelFooter className="text-hint text-muted-foreground">
              {crontab.data.env.length > 0 ? (
                <>
                  <span>Environment</span>
                  {crontab.data.env.map((line) => (
                    <Tag mono key={line}>
                      {line}
                    </Tag>
                  ))}
                </>
              ) : null}
              {!crontab.data.timeZone && (
                <span>
                  The server&apos;s time zone is unknown, so times are this browser&apos;s.
                </span>
              )}
            </PanelFooter>
          )}
      </Panel>

      <CronJobSheet
        job={opened}
        zone={zone}
        owner={`${user}'s crontab`}
        verbs={opened ? cronJobVerbs({ job: opened, admin, confirm, ...actions(opened) }) : []}
        cron={cron}
        onOpenChange={(next) => !next && onOpen(null)}
      />

      <CronJobEditor
        open={(adding || editing !== null) && !!crontab.data?.revision}
        job={editing}
        user={user}
        baseline={{ raw, revision: crontab.data?.revision }}
        timeZone={editing?.timeZone ?? crontab.data?.timeZone}
        onClose={() => {
          onAddingChange(false)
          setEditing(null)
          forgetMemoryState(`processes.cron.job.${user}.`)
        }}
        onSave={async (edit, baseline) => {
          const next = editing
            ? replaceJob(baseline.raw, editing, edit)
            : appendJob(baseline.raw, edit)
          await save(
            next,
            editing ? "Cron job updated" : "Cron job added",
            undefined,
            baseline.revision,
          )
        }}
      />
    </>
  )
}

/**
 * Every verb on one job, for its row and its sheet alike: Disable or Enable
 * and Edit are pressed daily and sit inline, Copy and Remove are words in the
 * menu.
 */
export function cronJobVerbs({
  job,
  admin,
  confirm,
  onToggle,
  onEdit,
  onRemove,
}: {
  job: CronJob
  admin: boolean
  confirm: ConfirmFn
  onToggle: () => void
  onEdit: () => void
  onRemove: (phrase: string) => Promise<void>
}): Verb[] {
  const list: Verb[] = []
  if (admin) {
    list.push(
      job.disabled
        ? { key: "toggle", label: "Enable", icon: Play, inline: true, run: onToggle }
        : { key: "toggle", label: "Disable", icon: Pause, inline: true, run: onToggle },
    )
    list.push({ key: "edit", label: "Edit", icon: Pencil, inline: true, run: onEdit })
  }
  list.push({
    key: "copy",
    label: "Copy command",
    icon: Copy,
    run: () => void copyText(job.command, "Command copied"),
  })
  if (admin) {
    list.push({
      key: "remove",
      label: "Remove",
      icon: Trash,
      danger: true,
      run: () =>
        confirm({
          title: "Remove cron job",
          confirmLabel: "Remove",
          description: (
            <p>
              <span className="font-mono">{job.schedule}</span> <b>{job.command}</b> is removed from
              the crontab.
            </p>
          ),
          action: onRemove,
        }),
    })
  }
  return list
}

function CronJobRow({
  job,
  zone,
  arrived,
  admin,
  confirm,
  onOpen,
  onToggle,
  onEdit,
  onRemove,
}: {
  job: CronJob
  zone?: string
  arrived: boolean
  admin: boolean
  confirm: ConfirmFn
  onOpen: () => void
  onToggle: () => void
  onEdit: () => void
  onRemove: (phrase: string) => Promise<void>
}) {
  const next = useMemo(
    () => (job.disabled ? null : nextCronRun(job.schedule, new Date(), job.timeZone)),
    [job],
  )
  const verbs = useMemo(
    () => cronJobVerbs({ job, admin, confirm, onToggle, onEdit, onRemove }),
    [job, admin, confirm, onToggle, onEdit, onRemove],
  )

  return (
    <TableRow className={cn("group", arrived && "animate-rise")} onActivate={onOpen}>
      <TableCell className="whitespace-normal">
        <JobCell job={job} onOpen={onOpen} dim={job.disabled} />
      </TableCell>
      <TableCell className="hidden whitespace-normal lg:table-cell">
        <ScheduleCell schedule={job.schedule} dim={job.disabled} />
      </TableCell>
      <TableCell className="hidden md:table-cell">
        {job.disabled ? (
          <Status state="inactive" label="switched off" />
        ) : next ? (
          <RunAt at={next.getTime()} zone={zone} />
        ) : (
          <span className="text-muted-foreground">
            {job.schedule.toLowerCase() === "@reboot"
              ? "at boot"
              : job.timeZone && !supportsCronTimeZone(job.timeZone)
                ? "forecast unavailable"
                : "never"}
          </span>
        )}
      </TableCell>
      <TableCell>
        <VerbActions dim verbs={verbs} />
      </TableCell>
    </TableRow>
  )
}

/**
 * A cron line as a reader names it: the program's mark, its name — the note
 * above the line, the scripts `run-parts` runs, or the program — and under it
 * the command that does the work, past its guards and assignments. Shared
 * with the system cron files, so a job is one shape wherever it lives; where
 * it opens a sheet, the name is the button a keyboard reaches and carries
 * the whole command as its accessible name.
 */
export function JobCell({
  job,
  onOpen,
  dim,
}: {
  job: Pick<CronJob, "command" | "comment">
  onOpen?: () => void
  dim?: boolean
}) {
  const program = cronProgram(job.command)
  const name = cronLineName(job)
  return (
    <div className={cn("flex min-w-0 items-center gap-3", dim && "opacity-55")}>
      <ProductLogo id={programProduct(program.segment)} size="sm" fallback={Clock} />
      <div className="min-w-0">
        {onOpen ? (
          <button
            type="button"
            onClick={onOpen}
            aria-label={`${name}: ${job.command}`}
            className="block max-w-full truncate text-left text-body font-medium hover:underline"
          >
            {name}
          </button>
        ) : (
          <p className="truncate text-body font-medium">{name}</p>
        )}
        <p className="truncate font-mono text-hint text-muted-foreground" title={job.command}>
          {program.segment}
        </p>
      </div>
    </div>
  )
}

/** A five-field schedule in words, with the expression it was written as under it. */
export function ScheduleCell({ schedule, dim }: { schedule: string; dim?: boolean }) {
  return (
    <div className={cn("min-w-0", dim && "opacity-55")}>
      <p className="truncate text-xs">{describeCron(schedule)}</p>
      <p className="truncate font-mono text-hint text-muted-foreground">{schedule}</p>
    </div>
  )
}

const DAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"]

/** What an existing job's schedule looks like in the builder's fields. */
function fieldsOf(job: CronJob | null) {
  const preset: CronPreset = job ? presetFor(job.schedule) : "daily"
  const out = { preset, time: "03:00", weekday: "1", monthDay: "1" }
  const fields = job?.schedule.split(/\s+/) ?? []
  if (job && preset !== "custom" && preset !== "reboot" && fields.length === 5) {
    const [m, h, dom, , dow] = fields
    if (/^\d+$/.test(m) && /^\d+$/.test(h)) out.time = `${h.padStart(2, "0")}:${m.padStart(2, "0")}`
    if (/^\d$/.test(dow)) out.weekday = dow === "7" ? "0" : dow
    if (/^\d+$/.test(dom)) out.monthDay = dom
  }
  return out
}

/** The editor's presets as the words on its toggles, shorter than the list's. */
const PRESET_WORDS: Record<CronPreset, string> = {
  minute: "Every minute",
  "5min": "5 min",
  "15min": "15 min",
  hourly: "Hourly",
  daily: "Daily",
  weekly: "Weekly",
  monthly: "Monthly",
  reboot: "At boot",
  custom: "Custom",
}

/**
 * Adding or editing one job, as a sheet: when it runs, what it runs, and a
 * picture of the week that schedule makes, drawn as the fields change — so
 * "every day at 3" and "at 3 every day" cannot drift apart, and a custom
 * expression that fires every minute of a working day is seen as a block
 * before it is saved. The command is drawn as the program it starts while
 * it is typed, as its row will be.
 *
 * It was a dialog with a select for the frequency and a line of three dates
 * under the fields; the frequency is a row of toggles now, since nine choices
 * are read faster laid out than opened.
 */
function CronJobEditor({
  open,
  job,
  user,
  onClose,
  onSave,
  baseline,
  timeZone,
}: {
  open: boolean
  job: CronJob | null
  user: string
  onClose: () => void
  onSave: (edit: JobEdit, baseline: { raw: string; revision?: string }) => Promise<void>
  baseline: { raw: string; revision?: string }
  timeZone?: string
}) {
  return (
    <SidePanel
      open={open}
      onOpenChange={(next) => !next && onClose()}
      title={job ? "Edit cron job" : "Add cron job"}
      description={`A schedule and the command cron runs on it as ${user}`}
      width="md"
      bodyClassName="p-5"
    >
      {open && (
        <CronJobForm
          key={`${user}:${job ? job.line : "new"}`}
          job={job}
          user={user}
          onClose={onClose}
          onSave={onSave}
          baseline={baseline}
          timeZone={timeZone}
        />
      )}
    </SidePanel>
  )
}

function CronJobForm({
  job,
  user,
  onClose,
  onSave,
  baseline: incomingBaseline,
  timeZone,
}: {
  job: CronJob | null
  user: string
  onClose: () => void
  onSave: (edit: JobEdit, baseline: { raw: string; revision?: string }) => Promise<void>
  baseline: { raw: string; revision?: string }
  timeZone?: string
}) {
  // Every field starts from the job being edited and is kept for the tab
  // until the editor is closed, so a walk to the Files page for the exact
  // path of a script comes back to the half-written job.
  const [initial] = useState(() => fieldsOf(job))
  const draft = `processes.cron.job.${user}.${job ? job.line : "new"}`
  const [baseline, setBaseline] = useMemoryState(`${draft}.baseline`, incomingBaseline)
  useEffect(() => {
    // Keep the revision paired with the draft across SPA navigation. A fresh
    // poll must never silently rebase an old edit onto a changed crontab.
    setBaseline((previous) => previous)
  }, [setBaseline])
  const [saveError, setSaveError] = useState<string>()
  const [preset, setPreset] = useMemoryState<CronPreset>(`${draft}.preset`, initial.preset)
  const [time, setTime] = useMemoryState(`${draft}.time`, initial.time)
  const [weekday, setWeekday] = useMemoryState(`${draft}.weekday`, initial.weekday)
  const [monthDay, setMonthDay] = useMemoryState(`${draft}.monthDay`, initial.monthDay)
  const [custom, setCustom] = useMemoryState(`${draft}.custom`, job?.schedule ?? "")
  const [command, setCommand] = useMemoryState(`${draft}.command`, job?.command ?? "")
  const [comment, setComment] = useMemoryState(`${draft}.comment`, job?.comment ?? "")
  const [enabled, setEnabled] = useMemoryState(`${draft}.enabled`, job ? !job.disabled : true)
  const [busy, setBusy] = useState(false)

  const [hh, mm] = time.split(":").map((v) => Number(v))
  const expression = useMemo(() => {
    const minute = Number.isFinite(mm) ? mm : 0
    const hour = Number.isFinite(hh) ? hh : 0
    switch (preset) {
      case "daily":
        return `${minute} ${hour} * * *`
      case "weekly":
        return `${minute} ${hour} * * ${weekday}`
      case "monthly":
        return `${minute} ${hour} ${monthDay} * *`
      case "custom":
        return custom.trim()
      default:
        return CRON_PRESETS.find((p) => p.key === preset)?.expression ?? ""
    }
  }, [preset, hh, mm, weekday, monthDay, custom])

  const validSchedule = isValidCron(expression)
  const valid = validSchedule && command.trim().length > 0
  const previews = useMemo(() => {
    if (!validSchedule) return []
    const out: Date[] = []
    let from = new Date()
    for (let i = 0; i < 3; i++) {
      const next = nextCronRun(expression, from, timeZone)
      if (!next) break
      out.push(next)
      from = next
    }
    return out
  }, [expression, validSchedule, timeZone])
  const program = cronProgram(command)

  const submit = async () => {
    setBusy(true)
    setSaveError(undefined)
    try {
      await onSave(
        {
          schedule: expression,
          command: command.trim(),
          comment: comment.trim() || undefined,
          disabled: !enabled,
        },
        baseline,
      )
      onClose()
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form
      id="cron-job-form"
      className="space-y-8"
      onSubmit={(event) => {
        event.preventDefault()
        if (valid && !busy) void submit()
      }}
    >
      {saveError && (
        <Notice tone="danger" title="Crontab was not saved">
          {saveError} Your draft is kept. Reload and review the current crontab before trying again.
        </Notice>
      )}
      <FormSection title="What it runs">
        <Field
          label="Command"
          htmlFor="cron-command"
          hint={`Run by /bin/sh as ${user}, with cron's minimal PATH — use absolute paths.`}
        >
          <div className="flex items-center gap-2">
            <ProductLogo
              id={programProduct(program.segment)}
              size="sm"
              fallback={Clock}
              className="size-9"
            />
            <Input
              id="cron-command"
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              placeholder="/usr/local/bin/backup >> /var/log/backup.log 2>&1"
              className="font-mono"
              spellCheck={false}
            />
          </div>
        </Field>
        <Field
          label="Note"
          htmlFor="cron-comment"
          hint="Its name on this page, written as a comment above the line."
        >
          <Input
            id="cron-comment"
            value={comment}
            onChange={(e) => setComment(e.target.value)}
            placeholder="Nightly database backup"
          />
        </Field>
      </FormSection>

      <FormSection title="When it runs">
        <ToggleGroup
          type="single"
          value={preset}
          onValueChange={(next) => next && setPreset(next as CronPreset)}
          variant="outline"
          size="sm"
          aria-label="Runs"
          className="grid w-full grid-cols-3 gap-1.5 *:w-full *:rounded-md! *:border-l! sm:grid-cols-5"
        >
          {CRON_PRESETS.map((p) => (
            <ToggleGroupItem
              key={p.key}
              value={p.key}
              title={p.label}
              className="h-8 px-2 text-xs text-muted-foreground data-[state=on]:border-brand data-[state=on]:bg-row-hover data-[state=on]:text-foreground"
            >
              {PRESET_WORDS[p.key]}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        {(preset === "daily" || preset === "weekly" || preset === "monthly") && (
          <FieldRow>
            <Field
              label="At"
              htmlFor="cron-time"
              hint={timeZone ? `On the server's clock, ${timeZone}.` : "On the server's clock."}
            >
              <Input
                id="cron-time"
                type="time"
                value={time}
                onChange={(e) => setTime(e.target.value)}
                className="font-mono"
              />
            </Field>
            {preset === "weekly" && (
              <Field label="On" htmlFor="cron-weekday">
                <Select value={weekday} onValueChange={setWeekday}>
                  <SelectTrigger id="cron-weekday" size="sm" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {DAYS.map((d, i) => (
                      <SelectItem key={d} value={String(i)}>
                        {d}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            )}
            {preset === "monthly" && (
              <Field label="On day" htmlFor="cron-day" hint="Months without that day skip it.">
                <Input
                  id="cron-day"
                  type="number"
                  min={1}
                  max={31}
                  value={monthDay}
                  onChange={(e) => setMonthDay(e.target.value)}
                  className="font-mono"
                />
              </Field>
            )}
          </FieldRow>
        )}
        {preset === "custom" && (
          <Field
            label="Expression"
            htmlFor="cron-custom"
            hint="Five fields — minute, hour, day of month, month, day of week — or @hourly, @daily, @weekly, @monthly, @reboot."
            error={
              custom.trim() && !isValidCron(custom) ? "Not a schedule cron understands." : undefined
            }
          >
            <Input
              id="cron-custom"
              value={custom}
              onChange={(e) => setCustom(e.target.value)}
              placeholder="*/10 8-18 * * 1-5"
              className="font-mono"
              spellCheck={false}
            />
          </Field>
        )}
        <div
          data-slot="schedule-preview"
          className="space-y-4 rounded-lg border border-hairline bg-surface-sunken p-4"
        >
          <div className="min-w-0 space-y-1">
            <p className="text-body font-semibold first-letter:uppercase">
              {expression ? describeCron(expression) : "Choose how often it runs"}
            </p>
            <p className="font-mono text-hint text-muted-foreground">{expression || "—"}</p>
          </div>
          {validSchedule && preset !== "reboot" && (
            <WeekStrip schedule={expression} timeZone={timeZone} />
          )}
          {previews.length > 0 && (
            <p className="numeric text-hint text-muted-foreground">
              Next: {previews.map((d) => longClock(d.getTime(), timeZone)).join(" · ")}
            </p>
          )}
        </div>
        <OptionList>
          <OptionRow
            title="Enabled"
            hint="Off writes the line commented out, so it is kept but never runs."
            checked={enabled}
            onCheckedChange={setEnabled}
          />
        </OptionList>
      </FormSection>

      <SidePanelFooter>
        {previews[0] && enabled && (
          <span className="mr-auto text-hint text-muted-foreground">
            First run <Countdown at={previews[0].getTime()} />
          </span>
        )}
        <Button type="button" variant="outline" size="sm" onClick={onClose}>
          Cancel
        </Button>
        <Button type="submit" form="cron-job-form" size="sm" disabled={!valid || busy}>
          {busy ? "Saving…" : job ? "Save" : "Add job"}
        </Button>
      </SidePanelFooter>
    </form>
  )
}
