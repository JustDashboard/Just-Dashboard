"use client"

import type { Certificate, StreamModule, StreamSpec } from "@/lib/types"
import { get } from "@/lib/api"
import { calendarDate } from "@/lib/format"
import { usePoll } from "@/hooks/use-poll"
import { Field, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

type StreamTLSFields = Pick<
  StreamSpec,
  | "protocol"
  | "tls"
  | "certPath"
  | "keyPath"
  | "upstreamTls"
  | "upstreamName"
  | "upstreamVerify"
  | "upstreamCa"
>

/** Debian's, Ubuntu's and Alpine's bundle of the public CAs. */
const SYSTEM_CAS = "/etc/ssl/certs/ca-certificates.crt"

/**
 * Where certbot and the import keep a certificate's key: beside it. A
 * certificate found only in a site's file has no known key, so the field
 * keeps what was typed.
 */
function keyBeside(cert: Certificate): string | undefined {
  if (cert.source !== "certbot" && cert.source !== "imported") return undefined
  return cert.path.replace(/[^/]+$/, "privkey.pem")
}

/**
 * A stream's TLS at both ends: nginx ending the client's TLS with a
 * certificate from the Certificates list, and nginx speaking TLS to the
 * backend with SNI and, when asked, a check of the backend's certificate.
 * nginx needs stream_ssl_module for either, so without it the controls are
 * not offered — unless a stream already uses TLS and has to be able to stop.
 */
export function StreamTLS({
  spec,
  module,
  open,
  onChange,
}: {
  spec: StreamTLSFields
  module: StreamModule
  /** Whether the form is open: the certificate list is read only then. */
  open: boolean
  onChange: (change: Partial<StreamSpec>) => void
}) {
  const certs = usePoll<Certificate[]>(
    (signal) => get("/certificates/", undefined, signal),
    0,
    [],
    { enabled: open && Boolean(spec.tls) },
  )
  const using = spec.tls || spec.upstreamTls
  // An unknown module is left to nginx's test, which the save runs.
  if (module.ssl === false && module.state !== "unknown" && !using) {
    return (
      <FormNote>
        This nginx was built without stream_ssl_module, so a stream cannot speak TLS at either end.
      </FormNote>
    )
  }
  const tcp = spec.protocol === "tcp"
  const usable = (certs.data ?? []).filter((c) => !c.error)
  const listed = usable.some((c) => c.path === spec.certPath)
  const pickCert = (path: string) => {
    const cert = usable.find((c) => c.path === path)
    if (!cert) return
    onChange({ certPath: cert.path, keyPath: keyBeside(cert) ?? spec.keyPath })
  }

  return (
    <OptionList>
      <OptionRow
        title="Serve it over TLS"
        hint={
          tcp
            ? "nginx ends the client's TLS with a certificate and forwards what is inside, so a plain TCP service is reached encrypted."
            : "TCP only — nginx has no DTLS for UDP."
        }
        checked={Boolean(spec.tls)}
        disabled={!tcp && !spec.tls}
        onCheckedChange={(v) => onChange({ tls: v })}
      >
        <FieldRow>
          <Field
            label="Certificate"
            hint={
              spec.certPath && certs.data && !listed
                ? "Not in the Certificates list. If the file does not exist, nginx refuses the stream."
                : "From the Certificates list."
            }
          >
            <Select value={spec.certPath ?? ""} onValueChange={pickCert}>
              <SelectTrigger className="w-full" aria-label="Certificate">
                <SelectValue placeholder={certs.loading ? "Reading…" : "A certificate"} />
              </SelectTrigger>
              <SelectContent>
                {spec.certPath && !listed && (
                  <SelectItem value={spec.certPath} hint="as the file has it">
                    {spec.certPath}
                  </SelectItem>
                )}
                {usable.map((c) => (
                  <SelectItem
                    key={c.path}
                    value={c.path}
                    hint={`${c.domains.join(", ")} · expires ${calendarDate(c.notAfter)}`}
                  >
                    {c.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <Field label="Private key" htmlFor="stream-key">
            <Input
              id="stream-key"
              value={spec.keyPath ?? ""}
              onChange={(e) => onChange({ keyPath: e.target.value })}
              placeholder="/etc/ssl/private/app.key"
              className="font-mono text-xs"
            />
          </Field>
        </FieldRow>
      </OptionRow>
      <OptionRow
        title="Encrypt to the backend"
        hint={
          tcp
            ? "nginx opens its own TLS connection to the backend, for a service that only speaks TLS."
            : "TCP only — nginx has no DTLS for UDP."
        }
        checked={Boolean(spec.upstreamTls)}
        disabled={!tcp && !spec.upstreamTls}
        onCheckedChange={(v) => onChange({ upstreamTls: v })}
      >
        <div className="space-y-3">
          <Field
            label="Backend's name"
            htmlFor="stream-upstream-name"
            hint="Sent as SNI, and the name its certificate must carry. Empty sends no SNI."
          >
            <Input
              id="stream-upstream-name"
              value={spec.upstreamName ?? ""}
              onChange={(e) => onChange({ upstreamName: e.target.value.trim() || undefined })}
              placeholder="db.internal"
              className="font-mono text-xs"
            />
          </Field>
          <OptionRow
            title="Verify its certificate"
            hint={
              spec.upstreamVerify
                ? "nginx refuses a backend whose certificate does not chain to these CAs or does not carry the name."
                : "Off, the connection is encrypted but anyone in the path could answer for the backend."
            }
            checked={Boolean(spec.upstreamVerify)}
            onCheckedChange={(v) =>
              onChange({
                upstreamVerify: v,
                upstreamCa: v ? spec.upstreamCa || SYSTEM_CAS : undefined,
              })
            }
          >
            <Field
              label="Trusted CAs"
              htmlFor="stream-upstream-ca"
              hint={
                spec.upstreamName
                  ? "A PEM file of the CAs to trust: the system's for a public certificate, your own CA's for a private one."
                  : "Needs the backend's name above to check the certificate against."
              }
            >
              <Input
                id="stream-upstream-ca"
                value={spec.upstreamCa ?? ""}
                onChange={(e) => onChange({ upstreamCa: e.target.value })}
                placeholder={SYSTEM_CAS}
                className="font-mono text-xs"
              />
            </Field>
          </OptionRow>
        </div>
      </OptionRow>
    </OptionList>
  )
}
