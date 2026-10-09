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
import { del, put } from "@/lib/api"
import {
  observedSSHKeys,
  sshFingerprintProblem,
  sshKeyTypes,
  sshTrustTarget,
  type DiagnosticResult,
  type SSHTrust,
} from "@/lib/network-diagnostics"
import { notify } from "@/lib/toast"

/**
 * Save the fingerprints later scans are compared with. Trusting what a scan
 * just read is trust on first use; pasting a fingerprint read from the
 * server's console or provider is the stronger form, so both are offered and
 * each is recorded as what it is. Nothing here contacts the server.
 */
export function SSHTrustActions({
  target,
  port,
  result,
}: {
  target: string
  port: number
  result: DiagnosticResult | null
}) {
  const observed = result ? observedSSHKeys(result) : []
  const saved = Boolean(
    result?.facts?.some((fact) => fact.label === "Saved trust" && !fact.value.startsWith("none")),
  )
  const [type, setType] = useState("ssh-ed25519")
  const [fingerprint, setFingerprint] = useState("")
  const [busy, setBusy] = useState<string>()
  const [error, setError] = useState<Error>()
  const [savedNote, setSavedNote] = useState<string>()
  const { confirm, dialog } = useConfirm()
  const problem = fingerprint.trim() ? sshFingerprintProblem(fingerprint) : undefined
  const save = async (source: "observed" | "entered") => {
    if (busy) return
    setBusy(source)
    setError(undefined)
    try {
      const entry = await put<SSHTrust>("/network/diagnostics/ssh-trust", {
        target,
        port,
        source,
        keys: source === "observed" ? observed : [{ type, fingerprint: fingerprint.trim() }],
      })
      setSavedNote(
        `Saved ${entry.keys.length} fingerprint${entry.keys.length === 1 ? "" : "s"} for ${entry.target}. Run the scan again to compare.`,
      )
      if (source === "entered") setFingerprint("")
      notify.success("Trusted fingerprints saved")
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)))
    } finally {
      setBusy(undefined)
    }
  }
  return (
    <section aria-label="Trusted fingerprints" className="space-y-3 border-t border-hairline pt-4">
      <h3 className="eyebrow">Trusted fingerprints</h3>
      <p className="text-hint text-muted-foreground">
        Reading a key does not authenticate the server. Save fingerprints you verified out of band,
        or trust the keys this scan read, and later scans compare against them.
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
          onClick={() => void save("entered")}
        >
          Save fingerprint
        </Button>
      </div>
      <div className="flex flex-wrap gap-2">
        <Button
          size="sm"
          variant="outline"
          disabled={observed.length === 0 || Boolean(busy)}
          pending={busy === "observed"}
          onClick={() => void save("observed")}
        >
          Trust the keys this scan read
        </Button>
        {saved && (
          <Button
            size="sm"
            variant="ghost"
            disabled={Boolean(busy)}
            onClick={() =>
              confirm({
                title: "Forget saved fingerprints",
                description: `Later scans of ${sshTrustTarget(target, port)} will have nothing to compare with.`,
                confirmLabel: "Forget fingerprints",
                action: async () => {
                  await del("/network/diagnostics/ssh-trust", {
                    query: { target: sshTrustTarget(target, port) },
                  })
                },
                onDone: () => setSavedNote("Saved fingerprints forgotten."),
              })
            }
          >
            Forget saved fingerprints
          </Button>
        )}
      </div>
      {savedNote && <p className="text-hint text-muted-foreground">{savedNote}</p>}
      {error && <ErrorState error={error} />}
      {dialog}
    </section>
  )
}
