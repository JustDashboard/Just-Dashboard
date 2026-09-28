"use client"

import { useRef, useState } from "react"
import { CheckCircle, Copy, Download, FileText, Warning } from "@/components/icons"
import { ApiError, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { calendarDate } from "@/lib/format"
import { keyTypeName } from "@/lib/private-certificates"
import { notify } from "@/lib/toast"
import type { ImportResult, KeyType, SigningRequest } from "@/lib/types"
import { Disclosure, Field, FieldRow, FormFact, FormFacts } from "@/components/form"
import { Modal } from "@/components/modal"
import { Well } from "@/components/panel"
import { Notice } from "@/components/state"
import { ImportOutcome } from "@/components/proxy/import-dialog"
import {
  CertificateNameFields,
  KeyTypeField,
  useCertificateNames,
} from "@/components/proxy/private-cert-dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"

/**
 * Buying a certificate starts with a signing request, and the request starts
 * with a key. Made here, the key is written once on this server, readable by
 * root only, and never travels: the request is the only thing sent to the
 * authority, and the certificate it signs is matched to the key waiting for
 * it, with nothing pasted but the certificate.
 */
export function CsrDialog({
  open,
  onOpenChange,
  onDone,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onDone: () => void
}) {
  return (
    <CsrDialogBody key={String(open)} open={open} onOpenChange={onOpenChange} onDone={onDone} />
  )
}

function CsrDialogBody({
  open,
  onOpenChange,
  onDone,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onDone: () => void
}) {
  const names = useCertificateNames()
  const [keyType, setKeyType] = useState<KeyType>("ecdsa-p256")
  const [organization, setOrganization] = useState("")
  const [locality, setLocality] = useState("")
  const [province, setProvince] = useState("")
  const [country, setCountry] = useState("")
  const [busy, setBusy] = useState(false)
  const [taken, setTaken] = useState("")
  const [request, setRequest] = useState<SigningRequest | null>(null)
  const countryProblem =
    country.trim() !== "" && !/^[a-z]{2}$/i.test(country.trim())
      ? "Its two-letter code, such as DE or US."
      : undefined

  const submit = async () => {
    setBusy(true)
    try {
      const made = await post<SigningRequest>("/certificates/csr", {
        name: names.name,
        names: names.names,
        keyType,
        subject: { organization, locality, province, country },
      })
      setRequest(made)
      notify.success(`Signing request for ${made.name} made`, {
        description: "Send it to your certificate authority.",
      })
      onDone()
    } catch (err) {
      if (err instanceof ApiError && err.code === "request_exists") {
        setTaken(
          "A request is already waiting under that name. Add its certificate or discard it first.",
        )
      } else {
        notify.error("No request made", err)
      }
    } finally {
      setBusy(false)
    }
  }

  const subjectFacts = [organization, locality, province, country.toUpperCase()]
    .map((v) => v.trim())
    .filter(Boolean)
    .join(", ")

  return (
    <Modal
      open={open}
      onOpenChange={(o) => !busy && onOpenChange(o)}
      size="lg"
      title="Signing request for a certificate authority"
      description="Makes a private key on this server and the request an authority signs."
      footer={
        request ? (
          <Button onClick={() => onOpenChange(false)}>Done</Button>
        ) : (
          <Button
            onClick={submit}
            disabled={
              busy ||
              names.names.length === 0 ||
              !names.name ||
              Boolean(names.problem) ||
              Boolean(countryProblem)
            }
            pending={busy}
          >
            Make the key and request
          </Button>
        )
      }
    >
      {request ? (
        <div className="space-y-4">
          <Notice tone="success" icon={CheckCircle} title="The key is on this server">
            Made at <code className="font-mono break-all">{request.keyPath}</code>, readable by root
            only, and it never leaves. Send the request below to your authority, then add the
            certificate it signs from Signing requests on this page.
          </Notice>
          <SigningRequestView request={request} />
        </div>
      ) : (
        <div className="grid gap-4">
          <CertificateNameFields
            id="csr"
            state={names}
            onEdit={() => setTaken("")}
            namesHint="Every name the certificate covers, separated by spaces; the first is its common name."
            nameHint="The certificate is kept under this name when it arrives. Use the name of one you are renewing to replace it then."
          />
          {taken && (
            <Notice tone="warning" icon={Warning} title="That name is waiting already">
              {taken}
            </Notice>
          )}
          <KeyTypeField
            value={keyType}
            onChange={setKeyType}
            hint="ECDSA P-256 suits every client of the last decade. Some authorities and older devices ask for RSA."
          />
          <Disclosure quiet summary="Organisation details" facts={subjectFacts || "optional"}>
            <div className="grid gap-4">
              <p className="text-hint text-muted-foreground">
                Only an organisation-validated certificate prints these, and the authority checks
                them itself. Leave them empty for a domain-validated one.
              </p>
              <Field label="Organisation" htmlFor="csr-organization">
                <Input
                  id="csr-organization"
                  value={organization}
                  onChange={(e) => setOrganization(e.target.value)}
                  placeholder="Example Ltd"
                />
              </Field>
              <FieldRow>
                <Field label="City" htmlFor="csr-locality">
                  <Input
                    id="csr-locality"
                    value={locality}
                    onChange={(e) => setLocality(e.target.value)}
                  />
                </Field>
                <Field label="State or region" htmlFor="csr-province">
                  <Input
                    id="csr-province"
                    value={province}
                    onChange={(e) => setProvince(e.target.value)}
                  />
                </Field>
              </FieldRow>
              <Field label="Country" htmlFor="csr-country" error={countryProblem}>
                <Input
                  id="csr-country"
                  value={country}
                  onChange={(e) => setCountry(e.target.value)}
                  placeholder="DE"
                  maxLength={2}
                  className="w-20 font-mono text-xs uppercase"
                  aria-invalid={countryProblem ? true : undefined}
                />
              </Field>
            </div>
          </Disclosure>
        </div>
      )}
    </Modal>
  )
}

/** Saves text as a file the browser downloads, without a round trip. */
function saveFile(name: string, text: string, type: string) {
  const url = URL.createObjectURL(new Blob([text], { type }))
  const a = document.createElement("a")
  a.href = url
  a.download = name
  a.click()
  URL.revokeObjectURL(url)
}

/** A request as it is sent: its names and key, the PEM, and the two ways to take it. */
export function SigningRequestView({ request }: { request: SigningRequest }) {
  return (
    <div className="min-w-0 space-y-3">
      <FormFacts>
        <FormFact label="Covers" mono>
          {request.domains.join(", ")}
        </FormFact>
        <FormFact label="Key">{keyTypeName(request.keyType)}</FormFact>
      </FormFacts>
      <Well
        aria-label={`Signing request for ${request.name}`}
        className="max-h-64 break-all whitespace-pre-wrap"
      >
        {request.csr}
      </Well>
      <div className="flex flex-wrap gap-2">
        <Button
          size="sm"
          variant="outline"
          onClick={() => void copyText(request.csr, "Request copied")}
        >
          <Copy className="size-3.5" />
          Copy request
        </Button>
        <Button
          size="sm"
          variant="outline"
          onClick={() => saveFile(`${request.name}.csr`, request.csr, "application/pkcs10")}
        >
          <Download className="size-3.5" />
          Download {request.name}.csr
        </Button>
      </div>
    </div>
  )
}

/**
 * The authority's answer to a waiting request: its certificate, matched to
 * the key made for it. A request under the name of a certificate kept now is
 * a renewal, and says so before anything is written.
 */
export function CompleteRequestDialog({
  open,
  request,
  onOpenChange,
  onDone,
}: {
  open: boolean
  /** The request being completed, kept while the dialog closes. */
  request: SigningRequest | null
  onOpenChange: (open: boolean) => void
  onDone: () => void
}) {
  return (
    <CompleteRequestBody
      key={`${open}:${request?.name ?? ""}`}
      open={open}
      request={request}
      onOpenChange={onOpenChange}
      onDone={onDone}
    />
  )
}

function CompleteRequestBody({
  open,
  request,
  onOpenChange,
  onDone,
}: {
  open: boolean
  request: SigningRequest | null
  onOpenChange: (open: boolean) => void
  onDone: () => void
}) {
  const [certificate, setCertificate] = useState("")
  const [busy, setBusy] = useState(false)
  // A certificate that appeared under the name since the list was read.
  const [existing, setExisting] = useState("")
  const [result, setResult] = useState<ImportResult | null>(null)
  const file = useRef<HTMLInputElement>(null)
  const replacing = Boolean(request?.replaces) || existing !== ""

  const load = async (picked: File | undefined) => {
    if (!picked) return
    setCertificate(await picked.text())
    setExisting("")
  }

  const submit = async () => {
    if (!request) return
    setBusy(true)
    try {
      const res = await post<ImportResult>(
        `/certificates/csr/${encodeURIComponent(request.name)}/complete`,
        { certificate, replace: replacing },
      )
      setResult(res)
      notify.success(res.replaced ? `${res.name} replaced` : `${res.name} imported`, {
        description: res.replaced
          ? "nginx keeps serving the previous one until it reloads."
          : "Point a site at the paths shown to serve it.",
      })
      onDone()
    } catch (err) {
      if (err instanceof ApiError && err.code === "certificate_exists") {
        setExisting(err.message)
      } else {
        notify.error("Not added", err)
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      onOpenChange={(o) => !busy && onOpenChange(o)}
      size="lg"
      title={request ? `Add the certificate for ${request.name}` : "Add the certificate"}
      description="Imports the certificate an authority signed for this request, with the key made for it."
      footer={
        result ? (
          <Button onClick={() => onOpenChange(false)}>Done</Button>
        ) : (
          <Button
            variant={replacing ? "destructive" : undefined}
            onClick={submit}
            disabled={busy || !certificate.trim()}
            pending={busy}
          >
            {replacing ? "Replace it" : "Add certificate"}
          </Button>
        )
      }
    >
      {result ? (
        <ImportOutcome result={result} />
      ) : (
        request && (
          <div className="grid gap-4">
            <FormFacts>
              <FormFact label="Covers" mono>
                {request.domains.join(", ")}
              </FormFact>
              <FormFact label="Key">{keyTypeName(request.keyType)}</FormFact>
            </FormFacts>
            {request.replaces && (
              <Notice tone="warning" icon={Warning} title="This replaces the certificate kept now">
                {request.name} covers {request.replaces.domains.join(", ")} and expires{" "}
                {calendarDate(request.replaces.notAfter)}. It is kept beside the new one as{" "}
                <code className="font-mono">.bak</code>, and sites serve the new one once nginx
                reloads.
              </Notice>
            )}
            <Field
              label="Certificate"
              htmlFor="complete-certificate"
              hint="What the authority sent: the certificate, and its intermediates in any order."
              trailing={
                <>
                  <input
                    ref={file}
                    type="file"
                    accept=".crt,.pem,.cer,.txt"
                    className="hidden"
                    aria-hidden
                    tabIndex={-1}
                    onChange={(e) => void load(e.target.files?.[0])}
                  />
                  <Button size="xs" variant="ghost" onClick={() => file.current?.click()}>
                    <FileText className="size-3" />
                    Load a file
                  </Button>
                </>
              }
            >
              <Textarea
                id="complete-certificate"
                value={certificate}
                onChange={(e) => {
                  setCertificate(e.target.value)
                  setExisting("")
                }}
                rows={8}
                className="font-mono text-micro"
                placeholder={"-----BEGIN CERTIFICATE-----\n…"}
              />
            </Field>
            {existing && (
              <Notice tone="warning" icon={Warning} title="That name holds a certificate now">
                {existing} The pair there now is kept beside the new one as{" "}
                <code className="font-mono">.bak</code>.
              </Notice>
            )}
          </div>
        )
      )}
    </Modal>
  )
}
