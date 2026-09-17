"use client"

import { useState } from "react"
import { notify } from "@/lib/toast"
import { post } from "@/lib/api"
import type { ProbeResult } from "@/lib/types"
import type { ToolDef } from "./tool-defs"

/** What another page hands a tool on arrival: the address it was looking at. */
export type ToolPrefill = { target?: string; record?: string }

/**
 * One block's whole state: its inputs, its run, its current result and the
 * three runs before it.
 *
 * A hook instance per block is what makes the tools independent — no shared
 * target, no shared busy flag, and one block's slow traceroute never blocks
 * another's quick DNS lookup. A prefill seeds the inputs once, on mount: the
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

  const canRun = !def.needsTarget || target.trim().length > 0

  const run = async () => {
    if (busy || !canRun) return
    setBusy(true)
    try {
      const res = await post<ProbeResult>("/network/probe", {
        tool: def.key,
        target: target.trim(),
        ...(def.needsPort ? { port: Number(port) || 0 } : {}),
        ...(def.recordOptions ? { record } : {}),
        ...(def.optionOptions ? { option } : {}),
      })
      setPast((prev) => (result ? [result, ...prev].slice(0, 3) : prev))
      setResult(res)
    } catch (err) {
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
    result,
    past,
    canRun,
    run,
    restore,
    clear,
  }
}

export type ToolRun = ReturnType<typeof useToolRun>
