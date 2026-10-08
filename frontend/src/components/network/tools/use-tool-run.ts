"use client"

import { useState } from "react"
import { notify } from "@/lib/toast"
import { post } from "@/lib/api"
import type { ProbeResult } from "@/lib/types"
import type { ToolDef } from "./tool-defs"
import { portProblem, toolReady } from "./tool-input"

/** What another page hands a tool on arrival: the address it was looking at. */
export type ToolPrefill = { target?: string; record?: string }

/**
 * One block's whole state: its inputs, its run, its current result and the
 * three runs before it.
 *
 * A hook instance per block is what makes the tools independent — no shared
 * target, no shared busy flag, and one block's slow traceroute never blocks
 * another's quick DNS lookup. A new deep link replaces only that tool's inputs:
 * Connections, Logins and Intrusion pages send an address here with the tool
 * already chosen, and the run itself is still a press — a probe is traffic
 * this server sends to an address, and arriving on a page should not send any.
 */
export function useToolRun(def: ToolDef, prefill?: ToolPrefill) {
  const [target, setTarget] = useState(prefill?.target ?? "")
  const [port, setPort] = useState(def.portDefault ?? "443")
  const [record, setRecord] = useState(
    prefill?.record && def.recordOptions?.includes(prefill.record)
      ? prefill.record
      : (def.recordOptions?.[0] ?? ""),
  )
  const [option, setOption] = useState(def.optionDefault ?? def.optionOptions?.[0]?.value ?? "")
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<ProbeResult | null>(null)
  const [past, setPast] = useState<ProbeResult[]>([])
  const [error, setError] = useState<Error>()

  // A link can choose a different target without leaving the workbench. Keep
  // the hook mounted so other drafts, results and in-flight requests survive.
  const seed = prefill ? JSON.stringify(prefill) : ""
  const [previousSeed, setPreviousSeed] = useState(seed)
  if (seed !== previousSeed) {
    setPreviousSeed(seed)
    if (prefill) {
      setTarget(prefill.target ?? "")
      setRecord(
        prefill.record && def.recordOptions?.includes(prefill.record)
          ? prefill.record
          : (def.recordOptions?.[0] ?? ""),
      )
    }
  }

  const portError = def.needsPort && port ? portProblem(port) : undefined
  const canRun = toolReady(def, target, port, option)

  const run = async () => {
    if (busy || !canRun) return
    setBusy(true)
    setError(undefined)
    try {
      const res = await post<ProbeResult>("/network/probe", {
        tool: def.key,
        target: target.trim(),
        ...(def.needsPort ? { port: Number(port.trim()) } : {}),
        ...(def.recordOptions ? { record } : {}),
        ...(def.optionOptions || def.optionPlaceholder ? { option: option.trim() } : {}),
      })
      setPast((prev) => (result ? [result, ...prev].slice(0, 3) : prev))
      setResult(res)
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)))
      notify.error(`Could not run ${def.label}`, err)
    } finally {
      setBusy(false)
    }
  }

  const restore = (res: ProbeResult) => {
    setPast((prev) => (result ? [result, ...prev.filter((p) => p !== res)].slice(0, 3) : prev))
    setResult(res)
  }

  const clear = () => {
    setError(undefined)
    setResult(null)
    setPast([])
  }

  return {
    target,
    setTarget,
    port,
    setPort,
    record,
    setRecord,
    option,
    setOption,
    busy,
    error,
    portError,
    result,
    past,
    canRun,
    run,
    restore,
    clear,
  }
}

export type ToolRun = ReturnType<typeof useToolRun>
