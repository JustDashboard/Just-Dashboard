"use client"

import { useCallback } from "react"
import { useRouter } from "next/navigation"
import { get, patch, post } from "@/lib/api"
import { dockerRemedyKind } from "@/lib/docker-remedies"
import { notify } from "@/lib/toast"
import { prune, pruneSummary, RECLAIM_SAFE } from "@/lib/docker-prune"
import type { ContainerDetail, ContainerSpec, DockerFinding } from "@/lib/types"
import { useAuth } from "@/hooks/use-auth"
import type { ConfirmFn } from "@/components/docker/shared"
import type { FindingAction } from "@/components/docker/attention"

const TABS: Record<string, string> = {
  logs: "logs",
  usage: "usage",
  changes: "mounts",
  routes: "configure",
  inspect: "inspect",
  healthcheck: "configure",
  pin: "configure",
}

/** Every Docker surface uses the same reviewed controls and permission fallback. */
export function useDockerFindingActions({
  confirm,
  onChanged,
  open,
}: {
  confirm: ConfirmFn
  onChanged: () => void
  open?: (id: string, tab?: string) => void
}): FindingAction {
  const { can } = useAuth()
  const router = useRouter()
  const navigate = useCallback(
    (id: string, tab = "overview") => {
      if (open) open(id, tab)
      else
        router.push(`/docker/containers/${encodeURIComponent(id)}?tab=${encodeURIComponent(tab)}`)
    },
    [open, router],
  )
  const run = useCallback(
    (finding: DockerFinding) => {
      const id = finding.targetId
      const action = finding.action
      if (id && dockerRemedyKind(finding)) {
        navigate(id, "configure")
        return
      }
      if (TABS[action ?? ""] && id) {
        navigate(id, TABS[action!])
        return
      }
      if (action === "volumes") {
        router.push("/docker/volumes")
        return
      }
      if (action === "stack.up") {
        router.push(
          finding.target
            ? `/docker/stacks/${encodeURIComponent(finding.target)}`
            : "/docker/stacks",
        )
        return
      }
      if (action === "prune" && can("destructive")) {
        confirm({
          title: "Reclaim unused Docker data",
          confirmLabel: "Reclaim",
          description:
            "Removes images no container uses and unused build cache. No container, network or volume is removed. Previous images may need another registry pull for rollback, and the next build may be slower.",
          action: async () => {
            const summary = pruneSummary(await prune({ ...RECLAIM_SAFE, imagesAndCacheOnly: true }))
            if (summary.failed.length) notify.warning(summary.message)
            else notify.success(summary.message)
            onChanged()
            return "reported"
          },
        })
        return
      }
      if (
        (action === "set-restart" && can("service.control")) ||
        (action === "cap-logs" && can("destructive") && can("system.admin")) ||
        (action === "unpause" && can("service.control"))
      ) {
        if (!id) return
        void get<ContainerDetail>(`/docker/containers/${id}`)
          .then(async (detail) => {
            if (action !== "unpause" && detail.composeStack) {
              router.push(
                `/docker/stacks/${encodeURIComponent(detail.composeStack)}?tab=compose&remedy=${encodeURIComponent(action ?? "")}`,
              )
              notify.info("Edit the owning Compose service", {
                description:
                  "Update the service configuration there and deploy it so the remedy survives the next deployment.",
              })
              return
            }
            if (action === "unpause") {
              await post(`/docker/containers/${id}/unpause`)
              notify.success(`${detail.name} resumed`)
              onChanged()
              return
            }
            if (action === "set-restart") {
              confirm({
                title: "Set a restart policy",
                confirmLabel: "Apply policy",
                description: `${detail.name} will use unless-stopped. Docker applies this in place, keeping its writable layer and current process. It restarts after process exits or daemon restarts unless you stopped it; an unhealthy health check alone does not trigger a restart.`,
                action: async () => {
                  const result = await patch<{ warnings: string[] }>(
                    `/docker/containers/${id}/restart-policy`,
                    { policy: "unless-stopped" },
                  )
                  if (result.warnings?.length)
                    notify.warning("Policy applied with a caveat", {
                      description: result.warnings.join("; "),
                    })
                  else notify.success("Restart policy applied without replacing the container")
                  onChanged()
                  return "reported"
                },
              })
              return
            }
            const spec = await get<ContainerSpec>(`/docker/containers/${id}/spec`)
            confirm({
              title: "Cap the log size",
              confirmLabel: "Replace container",
              description: `${detail.name} will be replaced with a container keeping up to three 10 MB log files. The service is interrupted. Volumes and mounts remain, but the old logs and everything written only in the container’s writable layer are permanently lost. Inspect Storage first and move any needed data into a volume.`,
              action: async () => {
                const result = await post<{ id: string }>(`/docker/containers/${id}/recreate`, {
                  spec: {
                    ...spec,
                    logging: {
                      driver: "json-file",
                      options: { "max-size": "10m", "max-file": "3" },
                    },
                  },
                })
                notify.success("Log rotation applied")
                onChanged()
                if (result.id) navigate(result.id)
                return "reported"
              },
            })
          })
          .catch((err) => notify.error("Could not prepare the remedy", err))
        return
      }
      if (action === "prune") {
        router.push("/docker/images")
        return
      }
      if (id) navigate(id, "inspect")
    },
    [can, confirm, navigate, onChanged, router],
  )
  return Object.assign(run, {
    label: (finding: DockerFinding) => {
      if (finding.action === "set-restart" && !can("service.control"))
        return "Inspect restart policy"
      if (finding.action === "cap-logs" && (!can("destructive") || !can("system.admin")))
        return "Inspect log configuration"
      if (finding.action === "prune" && !can("destructive")) return "Inspect Docker storage"
      if (finding.action === "unpause" && !can("service.control")) return "Inspect paused container"
      if (dockerRemedyKind(finding)) return "Review configuration remedy"
      if (finding.action === "stack.up") return "Open stack controls"
      return finding.actionLabel ?? "Review container configuration"
    },
  })
}
