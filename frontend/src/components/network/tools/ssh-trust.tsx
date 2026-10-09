"use client"

import { useState } from "react"
import { useConfirm } from "@/components/confirm-dialog"
import { Field } from "@/components/form"
import { ErrorState } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { ApiError, del, put } from "@/lib/api"
import {
  observedSSHKeys,
  sshFingerprintProblem,
  sshKeyTypes,
  type SSHTrust,
} from "@/lib/network-diagnostics"
import { notify } from "@/lib/toast"
import type { HeldResult } from "./use-tool-run"

type TrustBody =
  | { source: "observed"; resultId: string }
  | {
      source: "entered"
      target: string
      port: number
      keys: { type: string; fingerprint: string }[]
    }

/**
 * Save the fingerprints later scans are compared with. Trusting what a scan
 * read is trust on first use, and the server takes those keys and that host
 * from the scan it still holds, never from the inputs; pasting a fingerprint
 * read from the server's console is the stronger form. Replacing saved trust
 * is confirmed, because it erases what a changed key would be caught against.
 */
export function SSHTrustActions({
  target,
  port,
  scan,
}: {
  target: string
  port: number
  scan: HeldResult | null
}) {
  const observed = scan ? observedSSHKeys(scan) : []
  const saved = Boolean(
    scan?.facts?.some((fact) => fact.label === "Saved trust" && /^\d/.test(fact.value)),
  )
  const changed = Boolean(scan?.findings?.some((finding) => finding.id.startsWith("key-changed")))
  const scannedHost = scan?.request.target ?? ""
  const scannedPort = scan?.request.port ?? 22
  const [type, setType] = useState("ssh-ed25519")
  const [fingerprint, setFingerprint] = useState("")
  const [busy, setBusy] = useState<string>()
  const [error, setError] = useState<Error>()
  const [savedNote, setSavedNote] = useState<string>()
  const { confirm, dialog } = useConfirm()
  const problem = fingerprint.trim() ? sshFingerprintProblem(fingerprint) : undefined

  const announce = (entry: SSHTrust, body: TrustBody) => {
    setSavedNote(
      `Saved ${entry.keys.length} fingerprint${entry.keys.length === 1 ? "" : "s"} for ${entry.target}. Run the scan again to compare.`,
    )
    if (body.source === "entered") setFingerprint("")
    notify.success("Trusted fingerprints saved")
  }
  const replace = (body: TrustBody, where: string) =>
    confirm({
      title: "Replace saved fingerprints",
      description: changed
        ? `The scan reported a key that differs from the saved fingerprint for ${where}. Replace the saved fingerprint only after verifying the new key out of band.`
        : `Fingerprints are already saved for ${where}. Replacing them erases what later scans compare against.`,
      confirmLabel: "Replace fingerprints",
      action: async () => {
        announce(await put<SSHTrust>("/network/diagnostics/ssh-trust/replace", body), body)
      },
    })
  const save = async (body: TrustBody, where: string) => {
    if (busy) return
    setBusy(body.source)
    setError(undefined)
    try {
      announce(await put<SSHTrust>("/network/diagnostics/ssh-trust", body), body)
    } catch (err) {
      if (err instanceof ApiError && err.code === "ssh_trust_exists") replace(body, where)
      else setError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      setBusy(undefined)
    }
  }
  return (
    <section aria-label="Trusted fingerprints" className="space-y-3 border-t border-hairline pt-4">
      <h3 className="eyebrow">Trusted fingerprints</h3>
      <p className="text-hint text-muted-foreground">
        Reading a key does not authenticate the server. Save fingerprints you verified out of band,
        or trust the keys a scan read, and later scans compare against them.
      </p>
      <div className="flex flex-wrap items-end gap-3">
        <Field label="Key type">
          <Select value={type} onValueChange={setType}>
            <SelectTrigger className="w-52" aria-label="Key type">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {sshKeyTypes.map((value) => (
                <SelectItem key={value} value={value}>
                  {value}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field
          label="Fingerprint"
          htmlFor="ssh-trust-fingerprint"
          error={problem}
          className="min-w-64 flex-1"
        >
          <Input
            id="ssh-trust-fingerprint"
            value={fingerprint}
            onChange={(event) => setFingerprint(event.target.value)}
            placeholder="SHA256:…"
            aria-invalid={Boolean(problem)}
            className="font-mono"
          />
        </Field>
        <Button
          variant="outline"
          disabled={!target.trim() || !fingerprint.trim() || Boolean(problem) || Boolean(busy)}
          pending={busy === "entered"}
          onClick={() =>
            void save(
              {
                source: "entered",
                target: target.trim(),
                port,
                keys: [{ type, fingerprint: fingerprint.trim() }],
              },
              `${target.trim()} port ${port}`,
            )
          }
        >
          Save fingerprint
        </Button>
      </div>
      {scan && (
        <div className="flex flex-wrap gap-2">
          <Button
            size="sm"
            variant="outline"
            disabled={observed.length === 0 || !scan.resultId || Boolean(busy)}
            pending={busy === "observed"}
            onClick={() =>
              void save(
                { source: "observed", resultId: scan.resultId! },
                `${scannedHost} port ${scannedPort}`,
              )
            }
          >
            Trust the keys {scannedHost} offered
          </Button>
          {saved && (
            <Button
              size="sm"
              variant="ghost"
              disabled={Boolean(busy)}
              onClick={() =>
                confirm({
                  title: "Forget saved fingerprints",
                  description: `Later scans of ${scannedHost} port ${scannedPort} will have nothing to compare with.`,
                  confirmLabel: "Forget fingerprints",
                  action: async () => {
                    await del("/network/diagnostics/ssh-trust", {
                      query: { host: scannedHost, port: String(scannedPort) },
                    })
                  },
                  onDone: () => setSavedNote("Saved fingerprints forgotten."),
                })
              }
            >
              Forget saved fingerprints for {scannedHost}
            </Button>
          )}
        </div>
      )}
      {savedNote && <p className="text-hint text-muted-foreground">{savedNote}</p>}
      {error && <ErrorState error={error} />}
      {dialog}
    </section>
  )
}
