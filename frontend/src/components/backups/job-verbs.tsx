"use client"

import { CloudUpload, Pause, Pencil, Play, Trash } from "@/components/icons"
import { notify } from "@/lib/toast"
import { del, post } from "@/lib/api"
import type { BackupJob } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import type { useConfirm } from "@/components/confirm-dialog"
import type { Verb } from "@/components/verbs"
import { scheduleLabel } from "@/components/backups/shared"

/**
 * Take a backup outside the schedule.
 *
 * Its own function because the attention findings offer it without the other
 * four verbs, and two copies of a POST that starts a backup is one copy too
 * many.
 */
export async function runJobNow(job: BackupJob, refresh: () => void) {
  try {
    await post(`/backups/${job.id}/run`)
    notify.success(`${job.name} started`, { description: "Progress appears in its run history." })
    refresh()
  } catch (err) {
    notify.error("Could not start", err)
  }
}

type Confirm = ReturnType<typeof useConfirm>["confirm"]

/**
 * What can be done to a job, declared once.
 *
 * The list drew these and handed the same array to the sheet it opened, which
 * worked for as long as there was one page. A job is its own page now, and a
 * page that reached back for the list's closure would either be a second copy
 * of five `post` calls or a prop drilled through a route — so the verbs moved
 * here and both callers ask for them.
 *
 * `onDeleted` is what separates the two: deleting a job from the list leaves
 * you on the list, and deleting it from its own page has to leave.
 */
export function useJobVerbs({
  confirm,
  refresh,
  onEdit,
  onDeleted,
}: {
  confirm: Confirm
  refresh: () => void
  onEdit: (job: BackupJob) => void
  onDeleted: (job: BackupJob) => void
}) {
  const { can } = useAuth()
  const admin = can("system.admin")

  const setEnabled = async (job: BackupJob, enabled: boolean) => {
    try {
      await post(`/backups/${job.id}/enabled`, { enabled })
      notify.success(enabled ? `${job.name} resumed` : `${job.name} paused`)
      refresh()
    } catch (err) {
      notify.error(enabled ? "Could not resume" : "Could not pause", err)
    }
  }

  const testTarget = async (job: BackupJob) => {
    try {
      const res = await post<{ ok: boolean; error?: string }>(`/backups/${job.id}/test`)
      if (res.ok) notify.success(`${job.name}: destination is reachable and writable`)
      else notify.error(`${job.name}: destination unreachable`, res.error)
    } catch (err) {
      notify.error("Could not test the destination", err)
    }
  }

  const remove = (job: BackupJob) =>
    confirm({
      title: "Delete backup job",
      confirmLabel: "Delete",
      description: (
        <p>
          Removes the schedule for <b>{job.name}</b>. Archives already taken are kept where they
          are.
        </p>
      ),
      action: async (c) => {
        await del(`/backups/${job.id}`, { confirm: c })
        onDeleted(job)
      },
    })

  return (job: BackupJob): Verb[] => {
    const running = job.lastRun?.status === "running"
    const out: Verb[] = []
    if (can("service.control")) {
      out.push({
        key: "run",
        label: "Run now",
        detail: "Take a backup outside the schedule.",
        icon: Play,
        inline: true,
        progressive: "Running…",
        disabled: running,
        run: () => void runJobNow(job, refresh),
      })
    }
    if (admin) {
      out.push(
        {
          key: "edit",
          label: "Edit",
          detail: "Sources, destination, schedule, retention and checks.",
          icon: Pencil,
          inline: true,
          run: () => onEdit(job),
        },
        job.enabled
          ? {
              key: "pause",
              label: "Pause schedule",
              detail: "Stops the schedule. Run now still works and archives stay put.",
              icon: Pause,
              run: () => void setEnabled(job, false),
            }
          : {
              key: "resume",
              label: "Resume schedule",
              detail: job.schedule
                ? `Starts firing again: ${scheduleLabel(job.schedule).toLowerCase()}.`
                : "The job has no schedule; set one under Edit.",
              icon: Play,
              disabled: !job.schedule,
              run: () => void setEnabled(job, true),
            },
        {
          key: "test",
          label: "Test destination",
          detail: "Checks the directory is writable or the bucket answers.",
          icon: CloudUpload,
          run: () => void testTarget(job),
        },
        {
          key: "delete",
          label: "Delete job",
          detail: "Removes the schedule and history. Archives already taken are kept.",
          icon: Trash,
          danger: true,
          run: () => void remove(job),
        },
      )
    }
    return out
  }
}
