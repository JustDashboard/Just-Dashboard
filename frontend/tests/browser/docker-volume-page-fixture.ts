import type { Page } from "@playwright/test"

/**
 * The backup jobs that cover the volumes of `docker-volumes-fixture`, as the
 * Backups page lists them: where each one writes and when it last finished.
 * A volume's page reads them to draw where its data is copied to. Laid over
 * `mockVolumes`, whose coverage report names the same four jobs.
 */

const ago = (hours: number) => new Date(Date.now() - hours * 3600_000).toISOString()

function job(
  id: number,
  name: string,
  options: {
    targetKind: "local" | "s3" | "b2" | "sftp"
    target: Record<string, string | number>
    schedule: string
    lastSuccessHours?: number
    enabled?: boolean
    lastStatus?: "running" | "success" | "failed"
  },
) {
  const last = options.lastSuccessHours
  return {
    id,
    revision: 1,
    name,
    sources: [],
    excludes: [],
    targetKind: options.targetKind,
    target: options.target,
    schedule: options.schedule,
    retention: 14,
    retentionDays: 0,
    enabled: options.enabled ?? true,
    createdAt: ago(24 * 90),
    hasCredentials: options.targetKind !== "local",
    lastSuccessAt: last === undefined ? undefined : ago(last),
    lastRun:
      last === undefined
        ? undefined
        : {
            id: id * 100,
            jobId: id,
            startedAt: ago(last),
            endedAt: ago(last),
            status: options.lastStatus ?? "success",
            artifact: `${name}.tar.gz`,
            sizeBytes: 1024 * 1024 * 40,
            log: "",
            trigger: "schedule",
          },
    stored: { runs: 14, bytes: 1024 * 1024 * 560 },
  }
}

export const JOBS = [
  job(3, "Shop data", {
    targetKind: "b2",
    target: { bucket: "acme-backups" },
    schedule: "0 3 * * *",
    lastSuccessHours: 3.2,
  }),
  job(4, "Passwords", {
    targetKind: "s3",
    target: { bucket: "acme-vault", region: "eu-central-1" },
    schedule: "0 * * * *",
    lastSuccessHours: 0.8,
  }),
  job(5, "Dashboards", {
    targetKind: "local",
    target: { path: "/srv/backups/dashboards" },
    schedule: "0 4 * * 0",
    lastSuccessHours: 26,
  }),
  job(6, "Object storage", {
    targetKind: "sftp",
    target: { host: "backup.example.net", username: "backup", path: "/srv/minio" },
    schedule: "30 2 * * *",
    lastSuccessHours: 24 * 9,
    enabled: false,
  }),
]

/** Answers the job list. Call it after `mockVolumes`: the later route is asked first. */
export async function mockJobs(page: Page, jobs: typeof JOBS = JOBS) {
  await page.route("**/api/v1/backups/", (route) =>
    route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(jobs) }),
  )
}
