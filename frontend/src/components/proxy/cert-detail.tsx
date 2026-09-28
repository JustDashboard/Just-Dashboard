"use client"

import { useState } from "react"
import { Check, Copy, Warning } from "@/components/icons"
import { errorMessage, get, post } from "@/lib/api"
import { calendarDate, timestamp } from "@/lib/format"
import type {
  Certificate,
  CertificateChain,
  CertificateDetail,
  CertificateFacts,
  CertificateHistory,
  CertificateKey,
  ChainVerdict,
  DecodedPEM,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { useCopy } from "@/hooks/use-copy"
import { Field } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Modal } from "@/components/modal"
import { Detail, DetailList, Section } from "@/components/page"
import { EmptyNote, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status, type Verdict } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"

const CHAIN_VERDICT: Record<ChainVerdict, { label: string; verdict: Verdict }> = {
  complete: { label: "Complete", verdict: "ok" },
  "wrong-order": { label: "Wrong order", verdict: "critical" },
  "missing-intermediate": { label: "Missing intermediate", verdict: "critical" },
  "private-ca": { label: "Private CA", verdict: "warning" },
  "self-signed": { label: "Self-signed", verdict: "warning" },
  invalid: { label: "Invalid", verdict: "critical" },
}

/** A literal value with a copy tick beside it: fingerprints are compared by pasting. */
function CopyValue({ value, label }: { value: string; label: string }) {
  const { copy, copied } = useCopy()
  return (
    <span className="flex min-w-0 items-start gap-1">
      <span className="min-w-0 flex-1 font-mono text-hint break-all">{value}</span>
      <IconAction
        label={copied ? "Copied" : `Copy ${label}`}
        onClick={() => void copy(value)}
        className="-my-1 shrink-0"
      >
        {copied ? <Check /> : <Copy />}
      </IconAction>
    </span>
  )
}

function CopyList({ values, label }: { values: string[]; label: string }) {
  if (values.length === 0) return <>—</>
  return (
    <div className="flex flex-col gap-1">
      {values.map((value) => (
        <CopyValue key={value} value={value} label={label} />
      ))}
    </div>
  )
}

function Identity({ facts }: { facts: CertificateFacts }) {
  const names = [...facts.dnsNames, ...facts.ipAddresses, ...facts.emails, ...facts.uris]
  return (
    <DetailList>
      <Detail label="Subject">
        <CopyValue value={facts.subject || "—"} label="subject" />
      </Detail>
      <Detail label="Issuer">
        <CopyValue value={facts.issuer} label="issuer" />
      </Detail>
      <Detail label="Alternative names">
        <CopyList values={names} label="name" />
      </Detail>
      <Detail label="Serial">
        <CopyValue value={facts.serial} label="serial" />
      </Detail>
      <Detail label="SHA-256">
        <CopyValue value={facts.sha256} label="SHA-256 fingerprint" />
      </Detail>
      <Detail label="SHA-1">
        <CopyValue value={facts.sha1} label="SHA-1 fingerprint" />
      </Detail>
      <Detail label="Valid">
        {calendarDate(facts.notBefore)} to {calendarDate(facts.notAfter)}
      </Detail>
      <Detail label="Public key">
        {facts.keyType}
        {facts.keyBits > 0 && ` ${facts.keyBits}-bit`}
      </Detail>
      <Detail label="Signature">{facts.signature}</Detail>
      <Detail label="OCSP">
        <CopyList values={facts.ocsp} label="OCSP address" />
      </Detail>
      <Detail label="Issuer certificate">
        <CopyList values={facts.issuerUrls} label="issuer address" />
      </Detail>
      <Detail label="CRL">
        <CopyList values={facts.crl} label="CRL address" />
      </Detail>
      <Detail label="SCTs">
        <span className="numeric">{facts.scts}</span>
        {facts.scts === 0 && !facts.ca && (
          <span className="block text-hint text-muted-foreground">
            None embedded. Browsers that require Certificate Transparency refuse a public
            certificate without them unless the server staples them.
          </span>
        )}
      </Detail>
    </DetailList>
  )
}

function KeyFacts({ keyInfo }: { keyInfo: CertificateKey }) {
  return (
    <div className="space-y-3">
      {keyInfo.worldReadable ? (
        <Notice tone="danger" icon={Warning} title="Every account on this host can read the key">
          Mode {keyInfo.mode}. Anyone who reads it can impersonate these sites: chmod 600 it.
        </Notice>
      ) : keyInfo.groupReadable ? (
        <Notice tone="warning" icon={Warning} title="The key's group can read it">
          Mode {keyInfo.mode}, group {keyInfo.group}. Every member of that group can impersonate
          these sites; chmod 600 it unless something in the group needs it.
        </Notice>
      ) : null}
      <DetailList>
        <Detail label="Key file">
          <CopyValue value={keyInfo.path} label="key path" />
        </Detail>
        <Detail label="Found from">{keyInfo.from}</Detail>
        {keyInfo.mode && (
          <Detail label="Permissions">
            <span className="font-mono text-hint">
              {keyInfo.mode} {keyInfo.owner}:{keyInfo.group}
            </span>
          </Detail>
        )}
        <Detail label="Matches">
          {keyInfo.matches === null ? (
            <span className="text-muted-foreground">{keyInfo.error ?? "Could not tell"}</span>
          ) : keyInfo.matches ? (
            <Status verdict="ok" label="The key belongs to this certificate" />
          ) : (
            <Status
              verdict="critical"
              label="Another certificate's key: nginx refuses to load it"
            />
          )}
        </Detail>
      </DetailList>
    </div>
  )
}

/** The chain in file order, with what a client that fetches nothing makes of it. */
export function ChainView({ chain }: { chain: CertificateChain }) {
  const verdict = CHAIN_VERDICT[chain.verdict]
  return (
    <div className="space-y-3">
      <div className="space-y-1">
        <Status verdict={verdict.verdict} label={verdict.label} />
        <p className="text-xs text-muted-foreground">{chain.note}</p>
      </div>
      <ol className="space-y-3">
        {chain.certificates.map((facts, index) => (
          <li key={facts.sha256} className="min-w-0 space-y-1 border-l border-border pl-3">
            <div className="flex flex-wrap items-baseline gap-x-2">
              <span className="text-body font-medium break-all">
                {facts.subject || facts.dnsNames[0] || "—"}
              </span>
              <Tag>{index === 0 ? "leaf" : facts.selfSigned ? "root" : "intermediate"}</Tag>
              {index > 0 && !facts.signsPrevious && (
                <Tag tone="danger">does not sign the one above</Tag>
              )}
            </div>
            <p className="text-hint break-all text-muted-foreground">
              Issued by {facts.issuer} · until {calendarDate(facts.notAfter)}
            </p>
            <CopyValue value={facts.sha256} label="SHA-256 fingerprint" />
          </li>
        ))}
      </ol>
    </div>
  )
}

function Timeline({ name }: { name: string }) {
  const history = usePoll<CertificateHistory>(
    (signal) => get("/certificates/history", { name }, signal),
    0,
    [name],
  )
  if (history.loading && !history.data) return <LoadingRows rows={2} />
  if (history.error) return <ErrorState error={history.error} />
  const events = history.data?.events ?? []
  return (
    <div className="space-y-3">
      {history.data?.note && <p className="text-hint text-muted-foreground">{history.data.note}</p>}
      {events.length === 0 ? (
        <EmptyNote>Nothing recorded for this certificate yet.</EmptyNote>
      ) : (
        <ol className="space-y-3">
          {events.map((event, index) => (
            <li
              key={`${event.time}-${event.kind}-${index}`}
              className="min-w-0 space-y-0.5 border-l border-border pl-3"
            >
              <div className="flex flex-wrap items-baseline gap-x-2">
                <span className="text-body font-medium">{event.title}</span>
                <span className="text-hint text-muted-foreground">{timestamp(event.time)}</span>
              </div>
              {event.detail && (
                <p
                  className={
                    event.kind === "failure"
                      ? "text-hint break-words text-destructive"
                      : "text-hint break-words text-muted-foreground"
                  }
                >
                  {event.detail}
                </p>
              )}
              {event.sha256 && <CopyValue value={event.sha256} label="SHA-256 fingerprint" />}
            </li>
          ))}
        </ol>
      )}
    </div>
  )
}

/**
 * The sections of a certificate's sheet read from its file: identity, key, chain and,
 * for an administrator, its timeline. Fetched when the sheet opens, not with the list.
 */
export function CertificateDetails({
  cert,
  canReadHistory,
}: {
  cert: Certificate
  /** The timeline reads the audit trail, which only administrators may. */
  canReadHistory: boolean
}) {
  // Caddy's certificates live inside its container, where this host cannot read them.
  const readable = cert.source !== "caddy" && !cert.error
  const detail = usePoll<CertificateDetail>(
    (signal) => get("/certificates/detail", { path: cert.path }, signal),
    0,
    [cert.path],
    { enabled: readable },
  )
  if (!readable) return null
  const leaf = detail.data?.chain.certificates[0]
  return (
    <div className="space-y-8">
      {detail.loading && !detail.data ? (
        <LoadingRows rows={4} />
      ) : detail.error ? (
        <ErrorState error={detail.error} />
      ) : detail.data && leaf ? (
        <>
          <Section title="Identity">
            <Identity facts={leaf} />
          </Section>
          <Section title="Key">
            {detail.data.key ? (
              <KeyFacts keyInfo={detail.data.key} />
            ) : (
              <EmptyNote>No site names a key for this file and none is kept beside it.</EmptyNote>
            )}
          </Section>
          <Section title="Chain">
            <ChainView chain={detail.data.chain} />
          </Section>
        </>
      ) : null}
      {canReadHistory && (
        <Section title="Timeline">
          <Timeline name={cert.name} />
        </Section>
      )}
    </div>
  )
}

/** Decodes a pasted certificate, chain or signing request on the server, fetching nothing. */
export function PemDecoder({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [text, setText] = useState("")
  const [busy, setBusy] = useState(false)
  const [result, setResult] = useState<DecodedPEM | null>(null)
  const [error, setError] = useState("")
  const decode = async () => {
    setBusy(true)
    setError("")
    try {
      setResult(await post<DecodedPEM>("/certificates/decode", { pem: text }))
    } catch (err) {
      setResult(null)
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Modal
      open={open}
      onOpenChange={onOpenChange}
      size="lg"
      title="Paste a certificate"
      description="Decodes a PEM certificate, chain or signing request. Nothing is fetched or kept."
      footer={
        <Button onClick={decode} disabled={busy || !text.trim()} pending={busy}>
          Decode
        </Button>
      }
    >
      <div className="space-y-6">
        <Field
          label="PEM"
          htmlFor="decode-pem"
          hint="A private key is never read: paste only the certificate or the request."
          error={error || undefined}
        >
          <Textarea
            id="decode-pem"
            value={text}
            onChange={(e) => {
              setText(e.target.value)
              setResult(null)
            }}
            rows={8}
            className="font-mono text-micro"
            placeholder={"-----BEGIN CERTIFICATE-----\n…"}
          />
        </Field>
        {result && result.refused.length > 0 && (
          <Notice tone="warning" icon={Warning} title="Left unread">
            {result.refused.join("; ")}
          </Notice>
        )}
        {result?.chain && (
          <>
            <Section title="Certificate">
              <Identity facts={result.chain.certificates[0]} />
            </Section>
            <Section title="Chain">
              <ChainView chain={result.chain} />
            </Section>
          </>
        )}
        {result?.requests.map((csr, index) => (
          <Section key={index} title="Signing request">
            <DetailList>
              <Detail label="Subject">
                <CopyValue value={csr.subject || "—"} label="subject" />
              </Detail>
              <Detail label="Alternative names">
                <CopyList
                  values={[...csr.dnsNames, ...csr.ipAddresses, ...csr.emails]}
                  label="name"
                />
              </Detail>
              <Detail label="Public key">
                {csr.keyType}
                {csr.keyBits > 0 && ` ${csr.keyBits}-bit`}
              </Detail>
              <Detail label="Signature">
                {csr.signature}{" "}
                {csr.signatureValid ? (
                  <Status verdict="ok" label="valid" />
                ) : (
                  <Status verdict="critical" label="does not verify" />
                )}
              </Detail>
            </DetailList>
          </Section>
        ))}
      </div>
    </Modal>
  )
}
