import type { CronJob } from "@/lib/types"

/** A toggle or an inserted line must not replace an unchanged job's controls. */
export function cronRowKeys(jobs: CronJob[]) {
  const copies = new Map<string, number>()
  return jobs.map((job) => {
    const identity = JSON.stringify([job.schedule, job.command])
    const copy = copies.get(identity) ?? 0
    copies.set(identity, copy + 1)
    return `${identity}:${copy}`
  })
}
