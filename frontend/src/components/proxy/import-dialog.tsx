"use client"

import { useState } from "react"
import { CheckCircle, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { post } from "@/lib/api"
import type { ImportResult } from "@/lib/types"
import { Field } from "@/components/form"
import { Notice } from "@/components/state"
import { Modal } from "@/components/modal"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"

/**
 * Not every certificate comes from Let's Encrypt.
 *
 * One a company bought, one from an internal CA, one a hosting provider handed
 * over — all of them arrive as two blocks of PEM and had nowhere to go on a
 * page that only knew how to run certbot.
 *
 * The key is checked against the certificate before either is written, because
 * a mismatched pair is accepted by every text editor and refused by nginx at
 * reload — which on a live server means finding out during an outage.
 */
export function ImportDialog({
  open,
  onOpenChange,
  onDone,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onDone: () => void
}) {
  return (
    <ImportDialogBody key={String(open)} open={open} onOpenChange={onOpenChange} onDone={onDone} />
  )
}

function ImportDialogBody({
  open,
  onOpenChange,
  onDone,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onDone: () => void
}) {
  const [name, setName] = useState("")
  const [certificate, setCertificate] = useState("")
  const [key, setKey] = useState("")
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<ImportResult | null>(null)

  const submit = async () => {
    setBusy(true)
    try {
      const res = await post<ImportResult>("/certificates/import", {
        name: name.trim(),
        certificate,
        key,
      })
      setResult(res)
      notify.success(`${res.name} imported`, {
        description: "Point a site at the paths below to start serving it.",
      })
      onDone()
    } catch (err) {
      notify.error("Not imported", err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      size="lg"
      title="Import a certificate"
      description="For a certificate you bought or were given. Nothing here renews it — that is what the expiry column is for."
      footer={
        result ? (
          <Button onClick={() => onOpenChange(false)}>Done</Button>
        ) : (
          <Button
            onClick={submit}
            disabled={busy || !name.trim() || !certificate.trim() || !key.trim()}
            pending={busy}
          >
            Check and import
          </Button>
        )
      }
    >
      {result ? (
        <div className="space-y-3">
          <Notice tone="success" icon={CheckCircle} title={`${result.name} is on disk`}>
            <div className="space-y-1">
              <p>
                Certificate: <code className="font-mono">{result.certPath}</code>
              </p>
              <p>
                Key: <code className="font-mono">{result.keyPath}</code>
              </p>
              <p>
                Covers {result.certificate.domains.join(", ")} · expires in{" "}
                {result.certificate.daysLeft} days.
              </p>
            </div>
          </Notice>
          {result.warnings.map((warning) => (
            <Notice key={warning} tone="warning" icon={Warning} title="Worth knowing">
              {warning}
            </Notice>
          ))}
        </div>
      ) : (
        <div className="grid gap-4">
          <Field
            label="Name"
            htmlFor="import-name"
            hint="Names the directory it is stored in. Kept outside certbot's tree so a renewal run can never prune a certificate it did not issue."
          >
            <Input
              id="import-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="example-com"
              className="font-mono text-xs"
            />
          </Field>
          <Field
            label="Certificate"
            htmlFor="import-cert"
            hint="Paste the full chain if your authority gave you one — leaf first, then the intermediates. Desktop browsers paper over a missing intermediate from cache; phones, curl and payment gateways do not."
          >
            <Textarea
              id="import-cert"
              value={certificate}
              onChange={(e) => setCertificate(e.target.value)}
              rows={6}
              className="font-mono text-micro"
              placeholder={"-----BEGIN CERTIFICATE-----\n…"}
            />
          </Field>
          <Field label="Private key" htmlFor="import-key">
            <Textarea
              id="import-key"
              value={key}
              onChange={(e) => setKey(e.target.value)}
              rows={5}
              className="font-mono text-micro"
              placeholder={"-----BEGIN PRIVATE KEY-----\n…"}
            />
          </Field>
        </div>
      )}
    </Modal>
  )
}
