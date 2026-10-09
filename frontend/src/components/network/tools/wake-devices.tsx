"use client"

import { useState } from "react"
import { useConfirm } from "@/components/confirm-dialog"
import { Field } from "@/components/form"
import { ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { usePoll } from "@/hooks/use-poll"
import { del, get, post } from "@/lib/api"
import { wakeDeviceProblem, type WakeDevice } from "@/lib/network-diagnostics"
import { NetworkReadWarning } from "../read-warning"
import type { ToolRun } from "./use-tool-run"

/**
 * Saved Wake-on-LAN targets. Choosing one fills the tool's inputs; sending
 * the packet is still its own press, so arriving here never wakes anything.
 */
export function WakeDevices({ run }: { run: ToolRun }) {
  const devices = usePoll(
    (signal) => get<WakeDevice[]>("/network/diagnostics/wol-devices", undefined, signal),
    0,
    [],
  )
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<Error>()
  const { confirm, dialog } = useConfirm()
  const draft = { name, mac: run.target, interface: run.option }
  const problem = name.trim() ? wakeDeviceProblem(draft) : undefined
  const save = async () => {
    if (busy || problem || !name.trim()) return
    setBusy(true)
    setError(undefined)
    try {
      await post<WakeDevice>("/network/diagnostics/wol-devices", {
        ...draft,
        name: name.trim(),
        mac: run.target.trim(),
        interface: run.option.trim(),
        ...(run.verify.trim() ? { verify: run.verify.trim() } : {}),
        ...(run.verify.trim() && run.verifyPort.trim()
          ? { port: Number(run.verifyPort.trim()) }
          : {}),
      })
      setName("")
      devices.refresh()
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      setBusy(false)
    }
  }
  return (
    <section aria-label="Saved devices" className="space-y-3 border-t border-hairline pt-4">
      <h3 className="eyebrow">Saved devices</h3>
      <NetworkReadWarning
        error={devices.data ? devices.error : undefined}
        refresh={devices.refresh}
        lastSuccess={devices.lastSuccess}
        reading="saved devices"
      />
      {!devices.data && devices.error && (
        <ErrorState error={devices.error} onRetry={devices.refresh} />
      )}
      {devices.data && devices.data.length === 0 && (
        <p className="text-hint text-muted-foreground">No devices saved yet.</p>
      )}
      {devices.data && devices.data.length > 0 && (
        <ul className="divide-y divide-hairline">
          {devices.data.map((device) => (
            <li
              key={device.id}
              className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 py-1.5"
            >
              <span className="text-body font-medium">{device.name}</span>
              <span className="font-mono text-hint text-muted-foreground">
                {device.mac} · {device.interface}
                {device.verify
                  ? ` · verify ${device.verify}${device.port ? `:${device.port}` : " (ICMP)"}`
                  : ""}
              </span>
              <span className="ml-auto flex gap-1">
                <Button size="xs" variant="outline" onClick={() => run.applyDevice(device)}>
                  Use {device.name}
                </Button>
                <Button
                  size="xs"
                  variant="ghost"
                  onClick={() =>
                    confirm({
                      title: "Delete saved device",
                      description: `Delete ${device.name} (${device.mac}). This sends nothing to the device.`,
                      confirmLabel: "Delete device",
                      action: async () => {
                        await del(
                          `/network/diagnostics/wol-devices/${encodeURIComponent(device.id)}`,
                        )
                      },
                      onDone: devices.refresh,
                    })
                  }
                >
                  Delete
                </Button>
              </span>
            </li>
          ))}
        </ul>
      )}
      <div className="flex flex-wrap items-end gap-3">
        <Field
          label="Device name"
          htmlFor="wol-device-name"
          error={problem}
          className="min-w-48 flex-1"
        >
          <Input
            id="wol-device-name"
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="NAS"
            aria-invalid={Boolean(problem)}
          />
        </Field>
        <Button
          variant="outline"
          disabled={!name.trim() || Boolean(problem) || busy}
          pending={busy}
          onClick={() => void save()}
        >
          Save as device
        </Button>
      </div>
      {error && <ErrorState error={error} />}
      {dialog}
    </section>
  )
}
