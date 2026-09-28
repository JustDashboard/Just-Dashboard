"use client"

import { useState } from "react"
import {
  ChevronDown,
  Download,
  FileText,
  Globe,
  LockClosed,
  ShieldCheck,
  Warning,
} from "@/components/icons"
import { ApiError, downloadUrl, post } from "@/lib/api"
import { parseDomains } from "@/lib/certificates"
import { KEY_TYPES, keyTypeName, nameProblem, suggestName } from "@/lib/private-certificates"
import { notify } from "@/lib/toast"
import type { ImportResult, KeyType } from "@/lib/types"
import { Disclosure, Field, FormNote } from "@/components/form"
import { Modal } from "@/components/modal"
import { Notice } from "@/components/state"
import { VerbMenu, type Verb } from "@/components/verbs"
import { ImportOutcome } from "@/components/proxy/import-dialog"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"

/**
 * Certificates for names no public authority signs — a NAS, a printer's admin
 * page, 10.0.0.5 — made on this server: signed by its local CA, or by their
 * own key. Neither is trusted anywhere by default, and each form says where
 * it will be.
 */

/** What the Issue menu offers besides Let's Encrypt. */
export type PrivateKind = "local-ca" | "self-signed"

/**
 * The page's one command, split: Let's Encrypt on its face, every other way to
 * get a certificate behind the chevron. Without certbot there is no face, and
 * the menu is the command.
 */
export function IssueCommand({
  onLetsEncrypt,
  onPrivate,
  onRequest,
}: {
  /** Opens certbot's issuance; absent where certbot is not installed. */
  onLetsEncrypt?: () => void
  onPrivate: (kind: PrivateKind) => void
  /** Opens the signing request for another authority. */
  onRequest: () => void
}) {
  const verbs: Verb[] = [
    ...(onLetsEncrypt
      ? [{ key: "lets-encrypt", label: "Let's Encrypt", icon: Globe, run: onLetsEncrypt }]
      : []),
    {
      key: "local-ca",
      label: "From the local CA",
      icon: ShieldCheck,
      run: () => onPrivate("local-ca"),
    },
    {
      key: "self-signed",
      label: "Self-signed",
      icon: LockClosed,
      run: () => onPrivate("self-signed"),
    },
    { key: "csr", label: "Signing request for a CA", icon: FileText, run: onRequest },
  ]
  if (!onLetsEncrypt) {
    return (
      <VerbMenu
        verbs={verbs}
        trigger={
          <Button size="sm">
            New certificate
            <ChevronDown className="size-3.5" />
          </Button>
        }
      />
    )
  }
  return (
    // The two halves are one command, parted by a hairline of the page.
    <div className="flex items-center gap-px">
      <Button size="sm" className="rounded-r-none" onClick={onLetsEncrypt}>
        Issue certificate
      </Button>
      <VerbMenu
        verbs={verbs}
        trigger={
          <Button
            size="sm"
            className="rounded-l-none px-2"
            aria-label="Other ways to get a certificate"
          >
            <ChevronDown className="size-3.5" />
          </Button>
        }
      />
    </div>
  )
}

/** The names a certificate covers and the name it is kept under, suggested from the first. */
export function useCertificateNames() {
  const [namesText, setNamesText] = useState("")
  const [chosen, setName] = useState("")
  const names = parseDomains(namesText)
  const suggested = suggestName(names)
  const name = chosen.trim() || suggested
  return {
    namesText,
    setNamesText,
    names,
    chosen,
    setName,
    suggested,
    name,
    problem: nameProblem(chosen.trim()),
  }
}

export function CertificateNameFields({
  id,
  state,
  namesHint,
  nameHint,
  onEdit,
}: {
  id: string
  state: ReturnType<typeof useCertificateNames>
  namesHint: React.ReactNode
  nameHint: React.ReactNode
  /** Anything typed here makes a server's refusal of the previous name stale. */
  onEdit?: () => void
}) {
  return (
    <>
      <Field label="Names" htmlFor={`${id}-names`} hint={namesHint}>
        <Input
          id={`${id}-names`}
          value={state.namesText}
          onChange={(e) => {
            state.setNamesText(e.target.value)
            onEdit?.()
          }}
          placeholder="nas.lan 192.168.1.10"
          className="font-mono text-xs"
          autoComplete="off"
          spellCheck={false}
        />
      </Field>
      <Field label="Kept as" htmlFor={`${id}-name`} hint={nameHint} error={state.problem}>
        <Input
          id={`${id}-name`}
          value={state.chosen}
          onChange={(e) => {
            state.setName(e.target.value)
            onEdit?.()
          }}
          placeholder={state.suggested || "nas.lan"}
          className="font-mono text-xs"
          autoComplete="off"
          spellCheck={false}
          aria-invalid={state.problem ? true : undefined}
        />
      </Field>
    </>
  )
}

/** The key a certificate made here is given, as one segmented choice. */
export function KeyTypeField({
  label = "Key",
  value,
  onChange,
  hint,
}: {
  /** "Type" inside a fold that is already headed "Key". */
  label?: string
  value: KeyType
  onChange: (value: KeyType) => void
  hint: React.ReactNode
}) {
  return (
    <Field label={label} hint={hint}>
      <ToggleGroup
        type="single"
        value={value}
        onValueChange={(v) => v && onChange(v as KeyType)}
        variant="outline"
        size="sm"
        aria-label={label === "Key" ? "Key" : `Key ${label.toLowerCase()}`}
        className="w-full"
      >
        {KEY_TYPES.map((kind) => (
          <ToggleGroupItem
            key={kind.value}
            value={kind.value}
            aria-label={kind.name}
            className="flex-1 px-1 text-hint"
          >
            {kind.label}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>
    </Field>
  )
}

/** Where a certificate made here is trusted, said wherever one is shown. */
export function trustSentence(kind: PrivateKind): string {
  return kind === "local-ca"
    ? "Trusted only on devices where the local CA's root is installed."
    : "Trusted only on devices told to trust this certificate itself; every other browser warns before the site."
}

/**
 * A certificate made here: by the local CA — created first when there is
 * none yet — or by its own key. A name in use is refused by the server, and
 * replacing it is a second, deliberate press, as an import's is.
 */
export function PrivateCertificateDialog({
  open,
  onOpenChange,
  kind,
  caExists,
  onDone,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  kind: PrivateKind
  /** The local CA exists; without it, issuing creates it first. */
  caExists: boolean
  onDone: () => void
}) {
  return (
    <PrivateCertificateBody
      key={`${open}:${kind}`}
      open={open}
      onOpenChange={onOpenChange}
      kind={kind}
      caExists={caExists}
      onDone={onDone}
    />
  )
}

function PrivateCertificateBody({
  open,
  onOpenChange,
  kind,
  caExists,
  onDone,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  kind: PrivateKind
  caExists: boolean
  onDone: () => void
}) {
  const localCA = kind === "local-ca"
  const names = useCertificateNames()
  const [keyType, setKeyType] = useState<KeyType>("ecdsa-p256")
  const [busy, setBusy] = useState(false)
  const [existing, setExisting] = useState("")
  const [result, setResult] = useState<ImportResult | null>(null)
  // Whether the CA exists by the time this dialog answers: it may have made it.
  const [created, setCreated] = useState(false)
  const creating = localCA && !caExists && !created

  const submit = async () => {
    setBusy(true)
    try {
      if (creating) {
        try {
          await post("/certificates/local-ca")
        } catch (err) {
          // Made meanwhile, from the panel or another tab: issue from it.
          if (!(err instanceof ApiError && err.code === "local_ca_exists")) throw err
        }
        setCreated(true)
        onDone()
      }
      const res = await post<ImportResult>(
        localCA ? "/certificates/local-ca/issue" : "/certificates/self-signed",
        { name: names.name, names: names.names, keyType, replace: existing !== "" },
      )
      setResult(res)
      notify.success(res.replaced ? `${res.name} replaced` : `${res.name} made`, {
        description: "Point a site at the paths shown to serve it.",
      })
      onDone()
    } catch (err) {
      if (err instanceof ApiError && err.code === "certificate_exists") {
        setExisting(err.message)
      } else {
        notify.error(localCA ? "Not issued" : "Not made", err)
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
      title={localCA ? "Issue from the local CA" : "Make a self-signed certificate"}
      description={
        localCA
          ? "A certificate for internal names or addresses, signed by this server's own authority and renewed here."
          : "A certificate its own key signs, for a device that is told to trust it."
      }
      footer={
        result ? (
          <Button onClick={() => onOpenChange(false)}>Done</Button>
        ) : (
          <Button
            variant={existing ? "destructive" : undefined}
            onClick={submit}
            disabled={busy || names.names.length === 0 || !names.name || Boolean(names.problem)}
            pending={busy}
          >
            {existing
              ? "Replace it"
              : creating
                ? "Create the CA and issue"
                : localCA
                  ? "Issue"
                  : "Make the certificate"}
          </Button>
        )
      }
    >
      {result ? (
        <div className="space-y-3">
          <ImportOutcome result={result} />
          <FormNote>{trustSentence(kind)}</FormNote>
          {localCA && (
            <div className="flex flex-wrap items-center justify-between gap-2">
              <FormNote>Install the root once on each device that should trust it.</FormNote>
              <Button size="sm" variant="outline" asChild>
                <a href={downloadUrl("/certificates/local-ca/root.pem")} download>
                  <Download className="size-3.5" />
                  Download root
                </a>
              </Button>
            </div>
          )}
        </div>
      ) : (
        <div className="grid gap-4">
          {creating && (
            <Notice icon={ShieldCheck} title="This creates the local CA first">
              A root key made on this server, readable by root only and never exported, and a root
              certificate valid for ten years. Install that certificate on each device that should
              trust what the CA signs.
            </Notice>
          )}
          <CertificateNameFields
            id="private"
            state={names}
            onEdit={() => setExisting("")}
            namesHint="Host names or addresses, separated by spaces. A *. wildcard covers one level."
            nameHint="The folder beside the imports that sites name it by."
          />
          <Disclosure quiet summary="Key" facts={keyTypeName(keyType)}>
            <KeyTypeField
              label="Type"
              value={keyType}
              onChange={setKeyType}
              hint="ECDSA P-256 suits every client of the last decade. Choose RSA for a device that asks for it."
            />
          </Disclosure>
          <FormNote>
            {localCA
              ? "Valid for 397 days and renewed here 45 days before it expires. "
              : "Valid for 397 days, and not renewed: a new one is a certificate every device has to be told about again. "}
            {trustSentence(kind)}
          </FormNote>
          {existing && (
            <Notice tone="warning" icon={Warning} title="That name is taken">
              {existing} The pair there now is kept beside the new one as{" "}
              <code className="font-mono">.bak</code>.
            </Notice>
          )}
        </div>
      )}
    </Modal>
  )
}
