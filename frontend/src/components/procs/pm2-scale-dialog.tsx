"use client"

import { useState } from "react"
import type { PM2Process } from "@/lib/types"
import { post } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useMetrics } from "@/hooks/use-metrics"
import { ChartActivity, Minus, Plus } from "@/components/icons"
import { Modal } from "@/components/modal"
import { ProductLogo, pm2Product } from "@/components/product-logo"
import { Button } from "@/components/ui/button"

/**
 * How many workers a cluster runs: a stepper over the workers themselves,
 * the ones that stay, the ones that will start and the ones that will stop,
 * so the change is seen before it is made — and the host's core count one
 * press away, because one worker per core is the answer most of the time.
 *
 * Any worker of the cluster names it: PM2 scales an application by its name.
 * Mounted only while open, so the count starts from the live figure each time.
 */
export function PM2ScaleDialog({
  process,
  onClose,
  onChanged,
}: {
  process: PM2Process
  onClose: () => void
  onChanged: () => void
}) {
  const { snapshot } = useMetrics()
  const coreCount = snapshot?.cpu?.cores || snapshot?.cpu?.perCore?.length || 0
  const current = process.instances || 1
  const [count, setCount] = useState(current)
  const [busy, setBusy] = useState(false)
  const set = (value: number) => setCount(Math.max(1, Math.min(128, Math.round(value))))
  const change = count - current

  const submit = async () => {
    setBusy(true)
    try {
      await post(
        `/pm2/${encodeURIComponent(process.name)}/scale`,
        { instances: count },
        { query: { user: process.daemonId, id: process.id } },
      )
      notify.success(`${process.name} scaled to ${plural(count, "worker")}`)
      onChanged()
      onClose()
    } catch (err) {
      notify.error(`Could not scale ${process.name}`, err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open
      onOpenChange={(open) => !open && onClose()}
      title={
        <span className="flex min-w-0 items-center gap-2">
          <ProductLogo id={pm2Product(process.interpreter)} size="sm" fallback={ChartActivity} />
          <span className="truncate">Scale {process.name}</span>
        </span>
      }
      description="Set how many cluster workers run"
      size="sm"
      footer={
        <>
          <span className="mr-auto text-hint text-muted-foreground">
            {change === 0
              ? "No change"
              : change > 0
                ? `Starts ${plural(change, "worker")}; the running ones keep serving.`
                : `Stops ${plural(-change, "worker")}; the rest keep serving.`}
          </span>
          <Button variant="outline" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button size="sm" disabled={change === 0 || busy} onClick={() => void submit()}>
            {busy ? "Scaling…" : `Scale to ${count}`}
          </Button>
        </>
      }
    >
      <div className="space-y-5">
        <div className="flex items-center justify-center gap-4">
          <Button
            size="icon"
            variant="outline"
            aria-label="One worker fewer"
            disabled={count <= 1 || busy}
            onClick={() => set(count - 1)}
          >
            <Minus className="size-4" />
          </Button>
          <label className="flex flex-col items-center">
            <span className="sr-only">Workers</span>
            <input
              type="number"
              min={1}
              max={128}
              value={count}
              onChange={(event) => set(Number(event.target.value) || 1)}
              className="numeric w-24 [appearance:textfield] rounded-md bg-transparent text-center text-2xl font-semibold tracking-tight focus-ring [&::-webkit-inner-spin-button]:appearance-none"
            />
            <span className="text-hint text-muted-foreground">
              {count === 1 ? "worker" : "workers"} · {current} now
            </span>
          </label>
          <Button
            size="icon"
            variant="outline"
            aria-label="One worker more"
            disabled={count >= 128 || busy}
            onClick={() => set(count + 1)}
          >
            <Plus className="size-4" />
          </Button>
        </div>

        <WorkerStrip current={current} target={count} />

        {coreCount > 0 && count !== coreCount && (
          <p className="text-center text-hint text-muted-foreground">
            This host has {plural(coreCount, "core")}.{" "}
            <button
              type="button"
              className="font-medium text-foreground underline-offset-2 hover:underline"
              onClick={() => set(coreCount)}
            >
              One worker per core
            </button>
          </p>
        )}
      </div>
    </Modal>
  )
}

/**
 * The workers as cells: running and staying, about to start (outlined in the
 * command's blue, rising as they are added), about to stop (struck). Past
 * thirty-two the cells would be specks, so the strip says the number instead.
 */
function WorkerStrip({ current, target }: { current: number; target: number }) {
  const cells = Math.max(current, target)
  if (cells > 32) return null
  return (
    <div
      role="img"
      aria-label={`${current} workers now, ${target} after`}
      className="flex flex-wrap justify-center gap-1.5"
    >
      {Array.from({ length: cells }, (_, index) => {
        const kept = index < Math.min(current, target)
        const added = index >= current
        return (
          <span
            key={index}
            className={cn(
              "h-6 w-3 rounded-sm transition-colors",
              kept && "bg-chart-1",
              added && "animate-rise border border-dashed border-brand bg-brand/10",
              !kept && !added && "border border-hairline bg-muted-foreground/20 opacity-50",
            )}
          />
        )
      })}
    </div>
  )
}
