"use client"

import { useState } from "react"
import Link from "next/link"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"
import { cn } from "@/lib/utils"
import type { SecretStore, SecretStoreKind } from "@/lib/types"
import { LockClosed } from "@/components/icons"
import { Field, FieldRow } from "@/components/form"
import { ProductGlyph } from "@/components/product-logo"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  parseStoreReference,
  parseStoreTarget,
  storeKind,
  storeLocation,
  storeReference,
  type StoreReference,
} from "@/components/deploy/secret-stores"

/**
 * A store kind's mark: the product's own logo, as the deploy pages draw
 * products. OpenBao's only published file is a wordmark that does not read
 * at this size, so it keeps the lock rather than a mark drawn from memory.
 */
export function SecretStoreMark({
  kind,
  className,
}: {
  kind: SecretStoreKind
  className?: string
}) {
  if (kind === "openbao") return <LockClosed aria-hidden className={cn("text-brand", className)} />
  return <ProductGlyph id={kind} className={className} />
}

export function useSecretStores(enabled = true) {
  return usePoll(
    (signal) => get<SecretStore[]>("/deploy/secret-stores", undefined, signal),
    30000,
    [],
    { enabled },
  )
}

/**
 * A key in a connected secret store, chosen rather than typed as
 * `${{store.NAME/path#KEY}}`: the store, the path in it, the key and, where
 * the store keeps versions, a version to pin. Each change writes the literal
 * into the Value field above, which stays the one the server parses. The
 * server requires a variable reading a store to be secret, so choosing one
 * says so in the Type too.
 */
export function StoreReferencePicker({
  value,
  onChange,
}: {
  value: string
  onChange: (reference: string) => void
}) {
  const stores = useSecretStores()
  // The parts are held here while they are typed, since a reference without
  // its key yet is not one the field could be read back from.
  const [current, setCurrent] = useState<StoreReference>(
    () => parseStoreReference(value) ?? { store: "", path: "", key: "" },
  )
  const list = stores.data ?? []
  if (list.length === 0) return null
  const store = list.find((candidate) => candidate.name === current.store)
  const info = store ? storeKind(store.kind) : undefined
  const write = (next: StoreReference) => {
    setCurrent(next)
    // Written out even before the key is typed, so the field shows what is
    // being built; the server names what is still missing.
    onChange(storeReference(next))
  }
  return (
    // The Reference toggle reveals it, so it rises in under the field.
    <div className="flex animate-rise flex-col gap-3">
      <Field label="Secret store" htmlFor="variable-store">
        <Select
          value={store ? store.name : ""}
          onValueChange={(name) => write({ ...current, store: name, version: undefined })}
        >
          <SelectTrigger id="variable-store" className="w-full">
            <SelectValue placeholder="Choose a secret store" />
          </SelectTrigger>
          <SelectContent>
            {list.map((candidate) => (
              <SelectItem
                key={candidate.id}
                value={candidate.name}
                hint={<span aria-hidden>{storeKind(candidate.kind).title}</span>}
              >
                <SecretStoreMark kind={candidate.kind} className="size-4" />
                <span className="font-mono">{candidate.name}</span>
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      {store && info && (
        <FieldRow>
          {store.kind !== "doppler" && (
            <Field label="Path" htmlFor="variable-store-path" hint={info.pathHint}>
              <Input
                id="variable-store-path"
                value={current.path}
                onChange={(event) => write({ ...current, path: event.target.value.trim() })}
                placeholder={store.kind === "infisical" ? "backend" : "apps/web"}
                spellCheck={false}
                className="font-mono text-body"
              />
            </Field>
          )}
          <Field label="Key" htmlFor="variable-store-key">
            <Input
              id="variable-store-key"
              value={current.key}
              onChange={(event) => write({ ...current, key: event.target.value.trim() })}
              placeholder="DATABASE_URL"
              spellCheck={false}
              className="font-mono text-body"
            />
          </Field>
          {info.versions && (
            <Field
              label="Version"
              htmlFor="variable-store-version"
              hint="Empty reads the newest at every run, rollbacks included."
            >
              <Input
                id="variable-store-version"
                inputMode="numeric"
                value={current.version ? String(current.version) : ""}
                onChange={(event) => {
                  const digits = event.target.value.replace(/\D/g, "").slice(0, 10)
                  write({ ...current, version: digits ? Number(digits) : undefined })
                }}
                placeholder="newest"
                className="font-mono text-body"
              />
            </Field>
          )}
        </FieldRow>
      )}
    </div>
  )
}

/**
 * A variable that reads a store, as its row says it: the store's mark and
 * name, linked to where it is connected, and where in it the key is. Never
 * the value, which the dashboard only ever holds for the run that read it.
 */
export function StoreReferenceLabel({ target, compact }: { target: string; compact: boolean }) {
  const reference = parseStoreTarget(target)
  const stores = useSecretStores()
  if (!reference) return <span className="font-mono">store.{target}</span>
  const store = stores.data?.find((candidate) => candidate.name === reference.store)
  return (
    <>
      {store && (
        <SecretStoreMark kind={store.kind} className="inline-block size-3.5 align-[-2px]" />
      )}{" "}
      {compact ? (
        <span className="font-mono text-foreground/90">{reference.store}</span>
      ) : (
        <Link
          href="/deploy/secret-stores"
          className="rounded-sm font-mono text-foreground/90 focus-ring hover:underline"
        >
          {reference.store}
        </Link>
      )}{" "}
      <span className="font-mono">{storeLocation(reference)}</span>
    </>
  )
}
