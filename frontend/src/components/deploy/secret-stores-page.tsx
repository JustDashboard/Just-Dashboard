"use client"

import { useState } from "react"
import Link from "next/link"
import { Lightning, Pencil, Plus, Trash } from "@/components/icons"
import { ApiError, del, get, post, put } from "@/lib/api"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  SecretStore,
  SecretStoreKind,
  SecretStoreSettings,
  SecretStoreTest,
} from "@/lib/types"
import { Page, PageContext } from "@/components/page"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { EmptyNote, ErrorState, LoadingRows } from "@/components/state"
import { SidePanel } from "@/components/side-panel"
import { Tag } from "@/components/tag"
import { useConfirm } from "@/components/confirm-dialog"
import { ChoiceCard, ChoiceCardHint, ChoiceCardTitle, ChoiceGrid } from "@/components/choice-card"
import { ChoiceList, ChoiceRow } from "@/components/flow"
import { Field, FieldRow, FormNote, FormSection, OptionList, OptionRow } from "@/components/form"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { VerbActions, type Verb } from "@/components/verbs"
import { SecretStoreMark } from "@/components/deploy/secret-store-picker"
import {
  AZURE_CLOUDS,
  STORE_KINDS,
  credentialFields,
  storeAddressProblem,
  storeHealth,
  storeKind,
  storeReference,
  storeSettingsProblem,
  validStoreName,
} from "@/components/deploy/secret-stores"

type Draft = {
  id?: number
  kind: SecretStoreKind
  name: string
  address: string
  settings: SecretStoreSettings
  allowPreviews: boolean
  /** Undefined keeps the sealed credentials on an edit. */
  credentials?: Record<string, string>
}

function emptyDraft(kind: SecretStoreKind): Draft {
  return {
    kind,
    name: "",
    address: "",
    settings:
      kind === "vault" || kind === "openbao"
        ? { auth: "token", mount: "secret" }
        : kind === "azure"
          ? { cloud: "public" }
          : {},
    allowPreviews: false,
    credentials: {},
  }
}

function draftOf(store: SecretStore): Draft {
  return {
    id: store.id,
    kind: store.kind,
    name: store.name,
    address: store.address,
    settings: { ...store.settings },
    allowPreviews: store.allowPreviews,
  }
}

/**
 * What a store reads, in a phrase: the mount, the project and environment,
 * the region, or the tenant — and how it signs in.
 */
function storeWhere(store: Pick<SecretStore, "kind" | "settings">) {
  const { settings } = store
  if (store.kind === "vault" || store.kind === "openbao") {
    const parts = [`mount ${settings.mount ?? "secret"}`]
    if (settings.namespace) parts.push(`namespace ${settings.namespace}`)
    parts.push(settings.auth === "approle" ? "AppRole" : "token")
    return parts.join(" · ")
  }
  if (store.kind === "infisical") return `${settings.environment ?? ""} · machine identity`
  if (store.kind === "aws") return `${settings.region ?? ""} · access key`
  if (store.kind === "azure") {
    const parts = [`tenant ${settings.tenantId ?? ""}`]
    const cloud = AZURE_CLOUDS.find((option) => option.value === settings.cloud)
    if (cloud && cloud.value !== "public") parts.push(cloud.label)
    parts.push("client secret")
    return parts.join(" · ")
  }
  return "service token"
}

/**
 * Secret stores: the Vault, OpenBao, Infisical, Doppler, AWS Secrets Manager
 * and Azure Key Vault connections that deployment variables read as
 * `${{store.NAME/path#KEY}}`. A card per store says where it is, how it signs
 * in, who reads it and how the last test or run's read went — in amber, in
 * the store's own words, when it refused.
 */
export function SecretStoresPage() {
  const { can } = useAuth()
  const admin = can("system.admin")
  const stores = usePoll(
    (signal) => get<SecretStore[]>("/deploy/secret-stores", undefined, signal),
    10000,
    [],
  )
  const [draft, setDraft] = useState<Draft>()
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState<string>()
  const { confirm, dialog } = useConfirm()
  const list = stores.data

  const open = (next: Draft) => {
    setFormError(undefined)
    setDraft(next)
  }
  const close = () => {
    setDraft(undefined)
    setFormError(undefined)
  }

  const save = async () => {
    if (!draft) return
    setSaving(true)
    setFormError(undefined)
    const body = {
      name: draft.name.trim(),
      kind: draft.kind,
      address: draft.address.trim(),
      settings: draft.settings,
      allowPreviews: draft.allowPreviews,
      // Omitted on an edit that did not touch them, so the sealed ones stay.
      credentials: draft.credentials,
    }
    try {
      const saved = draft.id
        ? await put<SecretStore>(`/deploy/secret-stores/${draft.id}`, body)
        : await post<SecretStore>("/deploy/secret-stores", body)
      notify.success(draft.id ? `${body.name} saved` : `${body.name} added`, {
        description: draft.id ? undefined : "Test it to see the store answer.",
      })
      stores.refresh()
      // A new store stays open on its edit sheet, where Test is.
      if (draft.id) close()
      else open(draftOf(saved))
    } catch (err) {
      // A refusal of what was typed is said under the form, where it can be
      // corrected, rather than in a toast that leaves with the answer.
      if (err instanceof ApiError && err.status < 500) setFormError(err.message)
      else notify.error("Could not save the store", err)
    } finally {
      setSaving(false)
    }
  }

  const test = async (store: SecretStore) => {
    try {
      const result = await post<SecretStoreTest>(`/deploy/secret-stores/${store.id}/test`)
      if (result.ok) notify.success(`${store.name} answered`, { description: result.message })
      else notify.error(`${store.name} refused`, result.message)
    } catch (err) {
      notify.error(`Could not test ${store.name}`, err)
    } finally {
      stores.refresh()
    }
  }

  const remove = (store: SecretStore) =>
    confirm({
      title: `Remove ${store.name}`,
      confirmLabel: "Remove store",
      description: (
        <p>
          The connection and its sealed credentials are deleted. Nothing in the store itself
          changes.
          {store.usedBy.length > 0 &&
            ` ${plural(store.usedBy.length, "variable")} still read it, so the server will refuse until they point elsewhere.`}
        </p>
      ),
      action: async () => {
        await del(`/deploy/secret-stores/${store.id}`)
      },
      onDone: () => stores.refresh(),
    })

  const verbsFor = (store: SecretStore): Verb[] => {
    if (!admin) return []
    return [
      {
        key: "test",
        label: "Test",
        icon: Lightning,
        inline: true,
        progressive: "Testing…",
        run: () => void test(store),
      },
      { key: "edit", label: "Edit store", icon: Pencil, run: () => open(draftOf(store)) },
      { key: "remove", label: "Remove store", icon: Trash, danger: true, run: () => remove(store) },
    ]
  }

  const editing = draft?.id ? list?.find((store) => store.id === draft.id) : undefined

  return (
    <Page>
      <PageContext
        eyebrow={
          <Link href="/deploy" className="rounded-sm focus-ring hover:underline">
            Deployments
          </Link>
        }
        title="Secret stores"
      />
      <Panel plain>
        <PanelHeader
          title="Stores"
          actions={
            <span className="flex flex-wrap items-center gap-3">
              {list && list.length > 0 && (
                <span className="numeric text-hint text-muted-foreground">
                  {plural(list.length, "store")}
                </span>
              )}
              {admin && list && list.length > 0 && (
                <Button size="sm" onClick={() => open(emptyDraft("vault"))}>
                  <Plus className="size-3.5" />
                  Add store
                </Button>
              )}
            </span>
          }
        />
        <PanelBody>
          {stores.error ? (
            <ErrorState error={stores.error} onRetry={stores.refresh} />
          ) : stores.loading && !list ? (
            <LoadingRows rows={2} />
          ) : (list?.length ?? 0) === 0 ? (
            admin ? (
              // The empty page is the sheet's first question: which store.
              <ChoiceGrid columns={2} className="animate-rise">
                {STORE_KINDS.map((option, index) => (
                  <ChoiceCard
                    key={option.kind}
                    verb={`Add ${option.title}`}
                    mark={({ className }: { className?: string }) => (
                      <SecretStoreMark kind={option.kind} className={className} />
                    )}
                    title={option.title}
                    description={option.hint}
                    index={index}
                    onClick={() => open(emptyDraft(option.kind))}
                  />
                ))}
              </ChoiceGrid>
            ) : (
              <EmptyNote>No secret store is connected yet.</EmptyNote>
            )
          ) : (
            <ChoiceList aria-label="Secret stores" className="animate-rise">
              {list?.map((store) => (
                <StoreCard
                  key={store.id}
                  store={store}
                  verbs={verbsFor(store)}
                  onEdit={admin ? () => open(draftOf(store)) : undefined}
                />
              ))}
            </ChoiceList>
          )}
        </PanelBody>
      </Panel>

      <SidePanel
        open={Boolean(draft)}
        onOpenChange={(next) => !next && close()}
        title={draft?.id ? `Edit ${draft.name}` : "Add store"}
        description="A deployment's variables read a key from it as ${{store.NAME/path#KEY}}, fresh for each run. Values are never stored here."
        width="md"
        footer={
          <>
            <Button variant="outline" onClick={close} disabled={saving}>
              Cancel
            </Button>
            <Button pending={saving} disabled={!draft || !ready(draft)} onClick={save}>
              {draft?.id ? "Save store" : "Add store"}
            </Button>
          </>
        }
      >
        {draft && (
          <StoreForm
            draft={draft}
            onChange={setDraft}
            saved={editing}
            formError={formError}
            onTested={stores.refresh}
          />
        )}
      </SidePanel>
      {dialog}
    </Page>
  )
}

function ready(draft: Draft) {
  if (!validStoreName(draft.name.trim())) return false
  const info = storeKind(draft.kind)
  if (!draft.address.trim() && !info.defaultAddress && !info.addressOptional) return false
  if (draft.address.trim() && storeAddressProblem(draft.address)) return false
  if (storeSettingsProblem(draft.kind, draft.settings)) return false
  if (draft.credentials) {
    return credentialFields(draft.kind, draft.settings.auth).every(
      (field) => field.optional || draft.credentials?.[field.name]?.trim(),
    )
  }
  return true
}

/**
 * One store: its product's mark, its name, and under it where it is and how
 * it signs in, how the newest test or read went, and who reads it.
 */
function StoreCard({
  store,
  verbs,
  onEdit,
}: {
  store: SecretStore
  verbs: Verb[]
  onEdit?: () => void
}) {
  const health = storeHealth(store)
  const endpoint = store.endpoint || store.address
  let host = endpoint
  try {
    host = new URL(endpoint).host
  } catch {
    // Shown as typed.
  }
  const readers = store.usedBy
  return (
    <ChoiceRow
      leading={<SecretStoreMark kind={store.kind} className="size-5" />}
      title={<span className="font-mono">{store.name}</span>}
      verb={`Edit ${store.name}`}
      onSelect={onEdit}
      disabled={!onEdit}
      description={
        <span className="flex min-w-0 flex-col gap-0.5">
          <span>
            <span className="font-mono">{host}</span> · {storeWhere(store)}
            {store.allowPreviews && " · previews too"}
          </span>
          <span className={health.tone === "warning" ? "text-warning" : undefined}>
            {health.line}
          </span>
          <span>
            {readers.length === 0
              ? "No variable reads it yet"
              : `Read by ${readers
                  .slice(0, 3)
                  .map((use) => `${use.projectName} (${use.environmentName}) as ${use.variable}`)
                  .join(", ")}${readers.length > 3 ? ` and ${readers.length - 3} more` : ""}`}
          </span>
        </span>
      }
      // At phone width the mark already says which product, and the row's
      // room goes to where it is and how it last answered.
      trailing={<Tag className="max-sm:hidden">{storeKind(store.kind).title}</Tag>}
      actions={<VerbActions dim verbs={verbs} menuLabel={`Actions for ${store.name}`} />}
    />
  )
}

/**
 * The sheet: which store (it decides the address and how one signs in), then
 * where it is and its settings, then the credential, then whether previews may
 * read it. An edit adds a test that reads a path and key.
 */
function StoreForm({
  draft,
  onChange,
  saved,
  formError,
  onTested,
}: {
  draft: Draft
  onChange: (draft: Draft) => void
  saved?: SecretStore
  formError?: string
  onTested: () => void
}) {
  const patch = (fields: Partial<Draft>) => onChange({ ...draft, ...fields })
  const setting = (fields: Partial<SecretStoreSettings>) =>
    patch({ settings: { ...draft.settings, ...fields } })
  const info = storeKind(draft.kind)
  const vaultLike = draft.kind === "vault" || draft.kind === "openbao"
  const fields = credentialFields(draft.kind, draft.settings.auth)
  const problem = storeAddressProblem(draft.address)
  const name = draft.name.trim()
  const editing = Boolean(draft.id)
  // The two hosted kinds derive where they are from a region or a vault
  // name; the address only overrides it, so it is asked for after them.
  const derived = Boolean(info.addressOptional)
  return (
    <div className="flex flex-col gap-6">
      {!editing && (
        <fieldset className="min-w-0">
          <legend className="mb-1.5 text-body font-medium">Store</legend>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            {STORE_KINDS.map((option) => {
              const selected = draft.kind === option.kind
              return (
                <ChoiceCard
                  key={option.kind}
                  selected={selected}
                  className="min-h-0 flex-row items-start gap-2.5 px-3 py-2.5"
                  onClick={() =>
                    onChange({
                      ...emptyDraft(option.kind),
                      name: draft.name,
                      allowPreviews: draft.allowPreviews,
                    })
                  }
                >
                  <SecretStoreMark kind={option.kind} className="mt-px size-4 shrink-0" />
                  <span className="flex min-w-0 flex-col gap-0.5">
                    <ChoiceCardTitle>{option.title}</ChoiceCardTitle>
                    <ChoiceCardHint className="line-clamp-2">{option.short}</ChoiceCardHint>
                  </span>
                </ChoiceCard>
              )
            })}
          </div>
          <FormNote className="mt-2">{info.hint}</FormNote>
        </fieldset>
      )}

      <FormSection title="Connection">
        <Field
          label="Name"
          htmlFor="store-name"
          hint={
            editing
              ? "A store keeps its name: references say it."
              : "Lowercase letters, digits and dashes, as a reference says it."
          }
        >
          <Input
            id="store-name"
            value={draft.name}
            disabled={editing}
            onChange={(event) => patch({ name: event.target.value.toLowerCase() })}
            placeholder={info.namePlaceholder}
            spellCheck={false}
            className="font-mono text-body"
          />
        </Field>
        {name && validStoreName(name) && (
          <FormNote className="animate-rise">
            Variables read it as{" "}
            <span className="font-mono text-foreground/90">
              {storeReference({ store: name, ...info.sample })}
            </span>
            {info.keyOptional &&
              (info.sample.key
                ? "; without #KEY, the whole secret."
                : "; a field of a JSON value as #KEY.")}
          </FormNote>
        )}
        {draft.kind === "aws" && (
          <Field
            label="Region"
            htmlFor="store-region"
            hint="Where the secrets are, such as eu-west-1."
          >
            <Input
              id="store-region"
              value={draft.settings.region ?? ""}
              onChange={(event) => setting({ region: event.target.value.trim().toLowerCase() })}
              placeholder="us-east-1"
              spellCheck={false}
              className="font-mono text-body"
            />
          </Field>
        )}
        {draft.kind === "azure" && (
          <>
            <FieldRow>
              <Field
                label="Vault name"
                htmlFor="store-vault-name"
                hint="Becomes NAME.vault.azure.net."
              >
                <Input
                  id="store-vault-name"
                  value={draft.settings.vaultName ?? ""}
                  onChange={(event) => setting({ vaultName: event.target.value.trim() })}
                  placeholder="my-vault"
                  spellCheck={false}
                  className="font-mono text-body"
                />
              </Field>
              <Field
                label="Tenant ID"
                htmlFor="store-tenant"
                hint="Entra ID → Overview → Tenant ID."
              >
                <Input
                  id="store-tenant"
                  value={draft.settings.tenantId ?? ""}
                  onChange={(event) => setting({ tenantId: event.target.value.trim() })}
                  spellCheck={false}
                  className="font-mono text-body"
                />
              </Field>
            </FieldRow>
            <Field label="Cloud" htmlFor="store-cloud">
              <ToggleGroup
                id="store-cloud"
                type="single"
                variant="outline"
                aria-label="Azure cloud"
                value={draft.settings.cloud ?? "public"}
                onValueChange={(next) => {
                  if (next) setting({ cloud: next as SecretStoreSettings["cloud"] })
                }}
                className="w-full sm:w-auto"
              >
                {AZURE_CLOUDS.map((option) => (
                  <ToggleGroupItem
                    key={option.value}
                    value={option.value}
                    className="h-11 flex-1 sm:h-9 sm:flex-none"
                  >
                    {option.label}
                  </ToggleGroupItem>
                ))}
              </ToggleGroup>
            </Field>
          </>
        )}
        <Field
          label={derived ? "Endpoint" : "Address"}
          htmlFor="store-address"
          hint={
            info.addressHint ??
            (info.defaultAddress ? `Empty for ${info.defaultAddress}.` : undefined)
          }
        >
          <Input
            id="store-address"
            value={draft.address}
            onChange={(event) => patch({ address: event.target.value })}
            placeholder={info.placeholder}
            spellCheck={false}
            className="font-mono text-body"
          />
        </Field>
        {problem === "credential-over-http" && (
          <FormNote tone="warning">
            A credential goes over https, or to this machine; this address would send it in the
            clear.
          </FormNote>
        )}
        {vaultLike && (
          <>
            <FieldRow>
              <Field
                label="KV mount"
                htmlFor="store-mount"
                hint="Where the KV version 2 engine is."
              >
                <Input
                  id="store-mount"
                  value={draft.settings.mount ?? ""}
                  onChange={(event) => setting({ mount: event.target.value })}
                  placeholder="secret"
                  spellCheck={false}
                  className="font-mono text-body"
                />
              </Field>
              <Field label="Namespace" htmlFor="store-namespace" hint="Only if you use them.">
                <Input
                  id="store-namespace"
                  value={draft.settings.namespace ?? ""}
                  onChange={(event) => setting({ namespace: event.target.value })}
                  spellCheck={false}
                  className="font-mono text-body"
                />
              </Field>
            </FieldRow>
          </>
        )}
        {draft.kind === "infisical" && (
          <FieldRow>
            <Field label="Project ID" htmlFor="store-project" hint="Project settings → Project ID.">
              <Input
                id="store-project"
                value={draft.settings.project ?? ""}
                onChange={(event) => setting({ project: event.target.value })}
                spellCheck={false}
                className="font-mono text-body"
              />
            </Field>
            <Field label="Environment" htmlFor="store-environment" hint="Its slug.">
              <Input
                id="store-environment"
                value={draft.settings.environment ?? ""}
                onChange={(event) => setting({ environment: event.target.value })}
                placeholder="prod"
                spellCheck={false}
                className="font-mono text-body"
              />
            </Field>
          </FieldRow>
        )}
        {draft.kind !== "doppler" && !derived && (
          <Field
            label="Certificate authority"
            htmlFor="store-ca"
            hint="A self-hosted store's own CA, in PEM. Trusted beside the system's."
          >
            <Textarea
              id="store-ca"
              value={draft.settings.caCert ?? ""}
              onChange={(event) => setting({ caCert: event.target.value })}
              placeholder="-----BEGIN CERTIFICATE-----"
              spellCheck={false}
              rows={3}
              className="font-mono text-hint"
            />
          </Field>
        )}
      </FormSection>

      <FormSection title="Sign-in">
        {vaultLike && (
          <ToggleGroup
            type="single"
            variant="outline"
            aria-label="Sign in with"
            value={draft.settings.auth ?? "token"}
            onValueChange={(next) => {
              if (!next) return
              // Signing in another way needs that way's credentials.
              onChange({
                ...draft,
                settings: { ...draft.settings, auth: next as "token" | "approle" },
                credentials: next === saved?.settings.auth && editing ? undefined : {},
              })
            }}
            className="w-full sm:w-auto"
          >
            <ToggleGroupItem value="token" className="h-11 flex-1 sm:h-9 sm:flex-none">
              Token
            </ToggleGroupItem>
            <ToggleGroupItem value="approle" className="h-11 flex-1 sm:h-9 sm:flex-none">
              AppRole
            </ToggleGroupItem>
          </ToggleGroup>
        )}
        {vaultLike && draft.settings.auth === "approle" && (
          <Field label="AppRole mount" htmlFor="store-auth-mount">
            <Input
              id="store-auth-mount"
              value={draft.settings.authMount ?? ""}
              onChange={(event) => setting({ authMount: event.target.value })}
              placeholder="approle"
              spellCheck={false}
              className="font-mono text-body"
            />
          </Field>
        )}
        {draft.credentials ? (
          <div className="flex flex-col gap-3">
            {fields.map((field) => (
              <Field
                key={field.name}
                label={field.label}
                htmlFor={`store-credential-${field.name}`}
                hint={field.optional ? "Optional." : undefined}
              >
                <Input
                  id={`store-credential-${field.name}`}
                  type={field.secret ? "password" : "text"}
                  autoComplete="off"
                  value={draft.credentials?.[field.name] ?? ""}
                  onChange={(event) =>
                    patch({
                      credentials: { ...draft.credentials, [field.name]: event.target.value },
                    })
                  }
                  placeholder={field.placeholder}
                  spellCheck={false}
                  className="font-mono text-body"
                />
              </Field>
            ))}
            <span className="text-hint text-muted-foreground">
              Sealed with the dashboard&apos;s master key and never shown again.
            </span>
          </div>
        ) : (
          <FormNote>
            Signs in with{" "}
            {fields
              .filter((field) => saved?.credentialNames.includes(field.name))
              .map((field) => field.label.toLowerCase())
              .join(" and ") || "nothing"}
            , sealed.{" "}
            <Button
              variant="link"
              size="sm"
              className="h-auto p-0"
              onClick={() => patch({ credentials: {} })}
            >
              Replace
            </Button>
          </FormNote>
        )}
      </FormSection>

      <FormSection title="Who may read it">
        <OptionList>
          <OptionRow
            title="Pull request previews"
            hint="Off, only production and other environments read it: a preview runs code nobody has merged. Turn on for a store holding previews' own secrets."
            checked={draft.allowPreviews}
            onCheckedChange={(allowPreviews) => patch({ allowPreviews })}
          />
        </OptionList>
      </FormSection>

      {saved && <StoreTest store={saved} kind={draft.kind} onTested={onTested} />}

      {formError && (
        <FormNote tone="danger" role="alert">
          {formError}
        </FormNote>
      )}
    </div>
  )
}

/**
 * Test, on the saved connection: sign in, say who the store took it to be
 * and, given a path and key, read it — showing the version found, never the
 * value.
 */
function StoreTest({
  store,
  kind,
  onTested,
}: {
  store: SecretStore
  kind: SecretStoreKind
  onTested: () => void
}) {
  const [path, setPath] = useState("")
  const [key, setKey] = useState("")
  const [testing, setTesting] = useState(false)
  const [result, setResult] = useState<SecretStoreTest>()
  const info = storeKind(kind)
  const run = async () => {
    setTesting(true)
    try {
      setResult(
        await post<SecretStoreTest>(`/deploy/secret-stores/${store.id}/test`, {
          path: path.trim(),
          key: key.trim(),
        }),
      )
      onTested()
    } catch (err) {
      setResult({ ok: false, message: err instanceof Error ? err.message : String(err) })
    } finally {
      setTesting(false)
    }
  }
  return (
    <FormSection title="Test">
      <FieldRow>
        {kind !== "doppler" && (
          <Field label={info.pathLabel} htmlFor="store-test-path" hint={info.pathHint}>
            <Input
              id="store-test-path"
              value={path}
              onChange={(event) => setPath(event.target.value)}
              placeholder={info.pathPlaceholder}
              spellCheck={false}
              className="font-mono text-body"
            />
          </Field>
        )}
        <Field
          label="Key"
          htmlFor="store-test-key"
          hint={
            info.keyOptional
              ? "Optional: a field of a JSON value. The value is not shown."
              : "Optional: read it too. The value is not shown."
          }
        >
          <Input
            id="store-test-key"
            value={key}
            onChange={(event) => setKey(event.target.value)}
            placeholder={info.keyOptional ? "whole secret" : "DATABASE_URL"}
            spellCheck={false}
            className="font-mono text-body"
          />
        </Field>
      </FieldRow>
      <span className="flex flex-wrap items-center gap-3">
        <Button variant="outline" size="sm" pending={testing} onClick={run}>
          <Lightning className="size-3.5" />
          Test
        </Button>
        {result && (
          <span
            role="status"
            className={cn("animate-rise text-hint", result.ok ? "text-success" : "text-warning")}
          >
            {result.message}
          </span>
        )}
      </span>
    </FormSection>
  )
}
