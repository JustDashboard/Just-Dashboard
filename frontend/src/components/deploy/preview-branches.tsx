"use client"

import { RefreshClockwise } from "@/components/icons"
import { post } from "@/lib/api"
import { relativeTime, timestamp } from "@/lib/format"
import { branchReading, branchSource } from "@/lib/pull-requests"
import { notify } from "@/lib/toast"
import type { PreviewDatabaseBranch } from "@/lib/types"
import { useConfirm } from "@/components/confirm-dialog"
import { IconAction } from "@/components/icon-action"
import { ProductLogo } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { Tag } from "@/components/tag"

/**
 * A preview's database branches under its row: each its database, its
 * reading, where its data came from and how long ago, what was masked, and
 * — for an administrator, once it is ready or has failed — the verb that
 * restores it again from the newest backup. The branch keeps its address and
 * password through that, so the preview reconnects to it as it is.
 */
export function PreviewBranchLines({
  projectId,
  number,
  branches,
  canRefresh,
  onRefreshed,
}: {
  projectId: number
  number: number
  branches: PreviewDatabaseBranch[]
  canRefresh: boolean
  onRefreshed: () => void
}) {
  const { confirm, dialog } = useConfirm()
  const refresh = (branch: PreviewDatabaseBranch) =>
    confirm({
      title: "Refresh the branch",
      confirmLabel: "Restore from the newest backup",
      subject: {
        mark: <ProductLogo size="sm" id={branch.engine} />,
        name: branch.name,
        facts: <span className="font-mono">{branch.host}</span>,
      },
      description: (
        <>
          <p>
            The preview&apos;s copy of {branch.name} is removed and restored again from its newest
            backup{branch.maskedColumns.length > 0 ? ", masked the same way" : ""}. Everything the
            preview wrote to it is gone.
          </p>
          <p>Its address and password stay the same. Production is not touched.</p>
        </>
      ),
      action: async () => {
        await post(
          `/deploy/${projectId}/pull-requests/${number}/preview/branches/${branch.connectionId}/refresh`,
          {},
        )
        notify.success(`Restoring ${branch.name} for #${number}`)
        onRefreshed()
      },
    })

  return (
    <>
      <ul
        aria-label={`Database branches of the preview of #${number}`}
        className="flex min-w-0 flex-col gap-1"
      >
        {branches.map((branch) => {
          const reading = branchReading(branch)
          const source = branchSource(branch, (iso) => timestamp(iso))
          const settled = branch.state === "ready" || branch.state === "failed"
          return (
            <li key={branch.id} className="flex min-w-0 items-center gap-2 text-hint">
              <ProductLogo size="sm" id={branch.engine} />
              <span className="shrink-0 font-medium text-foreground">{branch.name}</span>
              <Tag>branch</Tag>
              <Status tone={reading.tone} label={reading.label} />
              <span className="min-w-0 truncate text-muted-foreground">
                {branch.state === "failed" && branch.reason
                  ? branch.reason
                  : [
                      source,
                      branch.readyAt && `restored ${relativeTime(branch.readyAt)}`,
                      branch.maskedColumns.length > 0 &&
                        `${branch.maskedColumns.join(", ")} masked`,
                    ]
                      .filter(Boolean)
                      .join(" · ")}
              </span>
              {canRefresh && settled && (
                <IconAction
                  label={`Refresh the branch of ${branch.name} from the latest backup`}
                  onClick={() => refresh(branch)}
                >
                  <RefreshClockwise />
                </IconAction>
              )}
            </li>
          )
        })}
      </ul>
      {dialog}
    </>
  )
}
