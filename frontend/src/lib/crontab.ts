import type { CronJob } from "@/lib/types"

/**
 * Line-level edits to a crontab's text.
 *
 * The server replaces a crontab wholesale — that is how `crontab -` works —
 * so a per-job control is an edit to the text the server sent, applied here,
 * then the whole thing written back. Each edit touches only the job's own
 * line and the comment the parser attached to it, so environment lines,
 * unrelated comments and blank lines survive untouched, which is what makes
 * the text editor and the row controls interchangeable.
 */

export type JobEdit = {
  schedule: string
  command: string
  comment?: string
  disabled?: boolean
}

function lines(raw: string): string[] {
  const out = raw.split("\n")
  // A trailing newline is not an empty last line.
  if (out.length > 0 && out[out.length - 1] === "") out.pop()
  return out
}

function join(list: string[]): string {
  return list.length === 0 ? "" : `${list.join("\n")}\n`
}

function jobLine(edit: JobEdit): string {
  const line = `${edit.schedule.trim()} ${edit.command.trim()}`
  return edit.disabled ? `# ${line}` : line
}

/** Whether the line before a job is the comment the parser attached to it. */
function ownsComment(list: string[], index: number, job: CronJob): boolean {
  if (!job.comment || index === 0) return false
  const previous = list[index - 1].trim()
  return previous.startsWith("#") && previous.replace(/^#\s*/, "") === job.comment
}

/** Enables a disabled job, or disables an enabled one. */
export function toggleJob(raw: string, job: CronJob): string {
  const list = lines(raw)
  const index = job.line - 1
  if (index < 0 || index >= list.length) return raw
  const current = list[index]
  list[index] = job.disabled ? current.replace(/^(\s*)#\s?/, "$1") : `# ${current.trimStart()}`
  return join(list)
}

/** Removes a job and the comment line that belonged to it. */
export function removeJob(raw: string, job: CronJob): string {
  const list = lines(raw)
  const index = job.line - 1
  if (index < 0 || index >= list.length) return raw
  const from = ownsComment(list, index, job) ? index - 1 : index
  list.splice(from, index - from + 1)
  return join(list)
}

/** Replaces a job's line, and its comment, with the edited values. */
export function replaceJob(raw: string, job: CronJob, edit: JobEdit): string {
  const list = lines(raw)
  const index = job.line - 1
  if (index < 0 || index >= list.length) return raw
  const hadComment = ownsComment(list, index, job)
  const replacement = edit.comment?.trim()
    ? [`# ${edit.comment.trim()}`, jobLine(edit)]
    : [jobLine(edit)]
  const from = hadComment ? index - 1 : index
  list.splice(from, index - from + 1, ...replacement)
  return join(list)
}

/** Appends a job at the end, with its comment above it. */
export function appendJob(raw: string, edit: JobEdit): string {
  const list = lines(raw)
  if (edit.comment?.trim()) list.push(`# ${edit.comment.trim()}`)
  list.push(jobLine(edit))
  return join(list)
}
