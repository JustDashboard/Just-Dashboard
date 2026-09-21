"use client"

import { useMemo, useState } from "react"
import { forgetSessionState, useSessionState } from "@/lib/view-state"
import { Clock, Copy, Pause, Pencil, Play, Trash } from "@/components/icons"
import { get, put } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import {
  CRON_PRESETS,
  describeCron,
  isValidCron,
  nextCronRun,
  presetFor,
  type CronPreset,
} from "@/lib/cron"
import { appendJob, removeJob, replaceJob, toggleJob, type JobEdit } from "@/lib/crontab"
import { relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import type { CronJob, Crontab } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { Field, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import { Modal } from "@/components/modal"
import { Panel, PanelBody, PanelFooter, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
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

type ConfirmFn = (request: ConfirmRequest) => void

/**
 * One account's crontab as a list of jobs you can act on, rather than a
 * file you edit. Each row says what the schedule means and when it fires
 * next; the controls disable, edit and remove one job by rewriting only its
 * own lines (`lib/crontab.ts`) and sending the file back. The text editor
 * is still there, one button away, for the crontab that does something the
 * builder cannot say.
 */
export function CronJobsPanel({
  user,
  users,
  onUserChange,
  confirm,
  adding,
  onAddingChange,
}: {
  user: string
  users: string[]
  onUserChange: (user: string) => void
  confirm: ConfirmFn
  adding: boolean
  onAddingChange: (open: boolean) => void
}) {
  const { can } = useAuth()
  const crontab = usePoll(
    (signal) => get<Crontab>(`/cron/user/${encodeURIComponent(user)}`, undefined, signal),
    30_000,
    [user],
  )
  // The draft remembers whose crontab it is, so switching accounts cannot
  // save one account's text over another's.
  const [draftFor, setDraftFor] = useState<{ user: string; text: string } | null>(null)
  const draft = draftFor?.user === user ? draftFor.text : null
  const setDraft = (text: string | null) => setDraftFor(text === null ? null : { user, text })
  const [editing, setEditing] = useSessionState<CronJob | null>("processes.cron.editing", null)
  const [saving, setSaving] = useState(false)

  const save = async (content: string, done: string, confirmText?: string) => {
    setSaving(true)
    try {
      await put(`/cron/user/${encodeURIComponent(user)}`, { content }, { confirm: confirmText })
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
  const jobs = crontab.data?.jobs ?? []
  const admin = can("system.admin")

  return (
    <>
      <Panel>
        <PanelHeader
          title="Cron jobs"
          actions={
            <>
              <Select value={user} onValueChange={onUserChange}>
                <SelectTrigger size="sm" className="w-40" aria-label="Crontab account">
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
                <Button size="xs" variant="ghost" onClick={() => setDraft(raw)}>
                  <Pencil className="size-3" />
                  Edit as text
                </Button>
              )}
            </>
          }
        />
        <PanelBody flush={draft === null}>
          {crontab.loading && !crontab.data && <LoadingRows rows={3} className="pt-3" />}
          {crontab.error && !crontab.data && <ErrorState error={crontab.error} className="mt-3" />}
          {crontab.data && draft === null && (
            <>
              {jobs.length === 0 ? (
                <EmptyNote>
                  No cron jobs for {user}.
                  {admin && " Add one, or paste a crontab with Edit as text."}
                </EmptyNote>
              ) : (
                <Table containerClassName="group-data-[plain]/panel:-mx-4 max-h-[calc(100svh-22rem)] w-auto">
                  <TableHeader className={stickyTableHeader}>
                    <TableRow>
                      <TableHead className="w-56">Schedule</TableHead>
                      <TableHead className="hidden w-40 md:table-cell">Next run</TableHead>
                      <TableHead className="w-full">Command</TableHead>
                      <TableHead>State</TableHead>
                      <TableHead className="w-px" />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {jobs.map((job) => (
                      <CronJobRow
                        key={`${job.line}:${job.raw}`}
                        job={job}
                        admin={admin}
                        confirm={confirm}
                        onToggle={() =>
                          void save(
                            toggleJob(raw, job),
                            `${job.command} ${job.disabled ? "enabled" : "disabled"}`,
                          ).catch(() => undefined)
                        }
                        onEdit={() => setEditing(job)}
                        onRemove={(phrase) => save(removeJob(raw, job), "Cron job removed", phrase)}
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
                        await save(draft, `Crontab of ${user} saved`, phrase)
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
        {crontab.data && (crontab.data.env.length > 0 || jobs.length > 0) && draft === null && (
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
            ) : (
              <span>
                Next runs are computed in this browser&apos;s time zone; cron uses the
                server&apos;s.
              </span>
            )}
          </PanelFooter>
        )}
      </Panel>

      {(adding || editing !== null) && (
        <CronJobDialog
          job={editing}
          onClose={() => {
            onAddingChange(false)
            setEditing(null)
            forgetSessionState("processes.cron.job.")
          }}
          onSave={async (edit) => {
            const next = editing ? replaceJob(raw, editing, edit) : appendJob(raw, edit)
            await save(next, editing ? "Cron job updated" : "Cron job added")
          }}
        />
      )}
    </>
  )
}

function CronJobRow({
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
}) {
  const next = useMemo(() => (job.disabled ? null : nextCronRun(job.schedule)), [job])
  const verbs = useMemo<Verb[]>(() => {
    const list: Verb[] = []
    if (admin) {
      list.push(
        job.disabled
          ? {
              key: "enable",
              label: "Enable",
              detail: "Uncomments the line. It runs at its next scheduled time.",
              icon: Play,
              inline: true,
              run: onToggle,
            }
          : {
              key: "disable",
              label: "Disable",
              detail: "Comments the line out. It stays here and can be enabled again.",
              icon: Pause,
              inline: true,
              run: onToggle,
            },
      )
      list.push({
        key: "edit",
        label: "Edit",
        detail: "Change the schedule, the command or the note above it.",
        icon: Pencil,
        inline: true,
        run: onEdit,
      })
    }
    list.push({
      key: "copy",
      label: "Copy command",
      detail: "The command line exactly as cron runs it.",
      icon: Copy,
      run: () => void copyText(job.command, "Command copied"),
    })
    if (admin) {
      list.push({
        key: "remove",
        label: "Remove",
        detail: "Deletes the line from the crontab.",
        icon: Trash,
        danger: true,
        run: () =>
          confirm({
            title: "Remove cron job",
            confirmLabel: "Remove",
            description: (
              <p>
                <span className="font-mono">{job.schedule}</span> <b>{job.command}</b> is removed
                from the crontab.
              </p>
            ),
            action: onRemove,
          }),
      })
    }
    return list
  }, [job, admin, confirm, onToggle, onEdit, onRemove])

  return (
    <TableRow className="group">
      <TableCell className="whitespace-normal">
        <div className="min-w-44">
          <p className="font-mono whitespace-nowrap">{job.schedule}</p>
          <p className="text-hint text-muted-foreground">{describeCron(job.schedule)}</p>
        </div>
      </TableCell>
      <TableCell className="hidden md:table-cell">
        {next ? (
          <>
            <p>{relativeTime(next.toISOString())}</p>
            <p className="text-hint text-muted-foreground">{timestamp(next.toISOString())}</p>
          </>
        ) : (
          <span className="text-muted-foreground">
            {job.disabled ? "—" : job.schedule.toLowerCase() === "@reboot" ? "at boot" : "never"}
          </span>
        )}
      </TableCell>
      <TableCell className="whitespace-normal">
        <p className="font-mono text-xs break-all">{job.command}</p>
        {job.comment && <p className="text-hint text-muted-foreground">{job.comment}</p>}
      </TableCell>
      <TableCell>
        <Status
          state={job.disabled ? "inactive" : "active"}
          label={job.disabled ? "disabled" : "active"}
        />
      </TableCell>
      <TableCell>
        <VerbActions dim verbs={verbs} />
      </TableCell>
    </TableRow>
  )
}

const DAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"]

/**
 * A schedule built from a preset, or typed. The expression the builder
 * writes is shown in words with its next three runs before it is saved, so
 * "every day at 3" and "at 3 every day" cannot drift apart.
 */
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

function CronJobDialog({
  job,
  onClose,
  onSave,
}: {
  job: CronJob | null
  onClose: () => void
  onSave: (edit: JobEdit) => Promise<void>
}) {
  // Every field starts from the job being edited and is kept for the tab
  // until the dialog is closed, so a walk to the Files page for the exact
  // path of a script comes back to the half-written job.
  const [initial] = useState(() => fieldsOf(job))
  const draft = `processes.cron.job.${job ? job.line : "new"}`
  const [preset, setPreset] = useSessionState<CronPreset>(`${draft}.preset`, initial.preset)
  const [time, setTime] = useSessionState(`${draft}.time`, initial.time)
  const [weekday, setWeekday] = useSessionState(`${draft}.weekday`, initial.weekday)
  const [monthDay, setMonthDay] = useSessionState(`${draft}.monthDay`, initial.monthDay)
  const [custom, setCustom] = useSessionState(`${draft}.custom`, job?.schedule ?? "")
  const [command, setCommand] = useSessionState(`${draft}.command`, job?.command ?? "")
  const [comment, setComment] = useSessionState(`${draft}.comment`, job?.comment ?? "")
  const [enabled, setEnabled] = useSessionState(`${draft}.enabled`, job ? !job.disabled : true)
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

  const valid = isValidCron(expression) && command.trim().length > 0
  const previews = useMemo(() => {
    if (!isValidCron(expression)) return []
    const out: Date[] = []
    let from = new Date()
    for (let i = 0; i < 3; i++) {
      const next = nextCronRun(expression, from)
      if (!next) break
      out.push(next)
      from = next
    }
    return out
  }, [expression])

  const submit = async () => {
    setBusy(true)
    try {
      await onSave({
        schedule: expression,
        command: command.trim(),
        comment: comment.trim() || undefined,
        disabled: !enabled,
      })
      onClose()
    } catch {
      // The panel already said what went wrong.
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(open) => !open && onClose()}
      title={job ? "Edit cron job" : "Add cron job"}
      description="A schedule and the command cron runs on it"
      size="md"
      footer={
        <>
          <Button variant="ghost" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button size="sm" disabled={!valid || busy} onClick={() => void submit()}>
            {busy ? "Saving…" : job ? "Save" : "Add job"}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Field label="Runs" htmlFor="cron-preset">
          <Select value={preset} onValueChange={(v) => setPreset(v as CronPreset)}>
            <SelectTrigger id="cron-preset" size="sm" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {CRON_PRESETS.map((p) => (
                <SelectItem key={p.key} value={p.key}>
                  {p.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        {(preset === "daily" || preset === "weekly" || preset === "monthly") && (
          <FieldRow>
            <Field label="At" htmlFor="cron-time" hint="Server time, 24-hour clock.">
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
        <Field
          label="Command"
          htmlFor="cron-command"
          hint="Run by /bin/sh as this account, with cron's minimal PATH. Use absolute paths."
        >
          <Input
            id="cron-command"
            value={command}
            onChange={(e) => setCommand(e.target.value)}
            placeholder="/usr/local/bin/backup >> /var/log/backup.log 2>&1"
            className="font-mono"
            spellCheck={false}
          />
        </Field>
        <Field
          label="Note"
          htmlFor="cron-comment"
          hint="A comment above the line, for whoever reads the crontab next."
        >
          <Input
            id="cron-comment"
            value={comment}
            onChange={(e) => setComment(e.target.value)}
            placeholder="Nightly database backup"
          />
        </Field>
        <OptionList>
          <OptionRow
            title="Enabled"
            hint="Off writes the line commented out, so it is kept but never runs."
            checked={enabled}
            onCheckedChange={setEnabled}
          />
        </OptionList>

        <div className="space-y-1.5">
          <p className="eyebrow">Schedule</p>
          <div className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-1">
            <span className="font-mono text-xs">{expression || "—"}</span>
            <span className="text-hint text-muted-foreground">
              {expression ? describeCron(expression) : "Choose how often it runs"}
            </span>
          </div>
          {previews.length > 0 && (
            <p className="text-hint text-muted-foreground">
              <Clock className="mr-1 inline size-3 align-[-2px]" />
              Next: {previews.map((d) => timestamp(d.toISOString())).join(" · ")}
            </p>
          )}
        </div>
      </div>
    </Modal>
  )
}
