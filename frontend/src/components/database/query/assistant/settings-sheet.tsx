"use client"

import { useState } from "react"
import { Eye, EyeOff, Sparkles } from "@/components/icons"
import { ApiError, del, errorMessage, put } from "@/lib/api"
import { calendarDate } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { ChoiceGrid, ProductCard } from "@/components/choice-card"
import { Segments } from "@/components/deploy/settings/segments"
import { Field, FormFact, FormFacts, FormNote, FormSection } from "@/components/form"
import { ProductLogo } from "@/components/product-logo"
import { SidePanel, SidePanelFooter } from "@/components/side-panel"
import { Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupToggle,
} from "@/components/ui/input-group"
import type {
  AssistantProvider,
  AssistantSettings,
  AssistantSetup,
} from "@/components/database/query/assistant/use-assistant"

const PROVIDERS: { id: AssistantProvider; product: string; label: string; detail: string }[] = [
  { id: "anthropic", product: "claude", label: "Anthropic", detail: "Claude, with your API key" },
  {
    id: "openai",
    product: "openai",
    label: "OpenAI-compatible",
    detail: "OpenAI, a gateway, or a local model",
  },
]

/** The form's first draft: what is saved, or Claude's newest Opus. */
function draftOf(settings: AssistantSettings | undefined): AssistantSetup {
  if (settings?.configured && settings.provider) {
    return {
      provider: settings.provider,
      model: settings.model ?? "",
      baseUrl: settings.baseUrl ?? "",
      key: "",
    }
  }
  return {
    provider: "anthropic",
    model: settings?.models[0]?.id ?? "claude-opus-5-5",
    baseUrl: "",
    key: "",
  }
}

/**
 * Who answers the editor's assistant, for the whole install: Claude with the
 * operator's Anthropic key, or any server that speaks OpenAI's chat
 * completions, which is how a model running on this machine is reached.
 *
 * Saving asks the provider first whether the key, the address and the model
 * answer — without asking the model anything — so a mistyped key is said
 * under its field here rather than under somebody's first question. The key
 * is sealed with the install's master key and never read back: a blank field
 * keeps it, for the same provider at the same address.
 */
export function AssistantSettingsSheet({
  open,
  settings,
  onClose,
  onSaved,
  confirm,
}: {
  open: boolean
  settings: AssistantSettings | undefined
  onClose: () => void
  onSaved: () => void
  confirm: (request: ConfirmRequest) => void
}) {
  return (
    <SidePanel
      open={open}
      onOpenChange={(next) => !next && onClose()}
      title={settings?.configured ? "Assistant settings" : "Set up the assistant"}
      description="Choose the model provider the SQL editor's assistant asks"
      width="md"
      footer={
        <>
          <FormNote className="mr-auto">Saved once the provider answers a check.</FormNote>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
        </>
      }
    >
      {open && (
        <SettingsForm settings={settings} onClose={onClose} onSaved={onSaved} confirm={confirm} />
      )}
    </SidePanel>
  )
}

function SettingsForm({
  settings,
  onClose,
  onSaved,
  confirm,
}: {
  settings: AssistantSettings | undefined
  onClose: () => void
  onSaved: () => void
  confirm: (request: ConfirmRequest) => void
}) {
  const [draft, setDraft] = useState<AssistantSetup>(() => draftOf(settings))
  const [shown, setShown] = useState(false)
  const [saving, setSaving] = useState(false)
  const [refusal, setRefusal] = useState<{ field?: string; message: string }>()
  const change = (patch: Partial<AssistantSetup>) => {
    setDraft((held) => ({ ...held, ...patch }))
    setRefusal(undefined)
  }
  const models = settings?.models ?? []
  // A saved key is kept only for the same provider at the same address.
  const keeps =
    settings?.configured === true &&
    settings.keySet &&
    settings.provider === draft.provider &&
    (draft.provider === "anthropic" || settings.baseUrl === draft.baseUrl.trim())
  const errorOf = (field: string) => (refusal?.field === field ? refusal.message : undefined)

  const save = async () => {
    if (saving) return
    setSaving(true)
    setRefusal(undefined)
    try {
      await put<AssistantSettings>("/databases/assistant", {
        ...draft,
        model: draft.provider === "anthropic" ? draft.model : draft.model.trim(),
        baseUrl: draft.provider === "openai" ? draft.baseUrl.trim() : "",
      })
      notify.success("The assistant is on")
      onSaved()
      onClose()
    } catch (err) {
      setRefusal({
        field: err instanceof ApiError ? err.field : undefined,
        message: errorMessage(err),
      })
    } finally {
      setSaving(false)
    }
  }

  const turnOff = () =>
    confirm({
      title: "Turn off the assistant",
      confirmLabel: "Turn off",
      subject: {
        mark: (
          <ProductLogo id={settings?.provider === "anthropic" ? "claude" : "openai"} size="sm" />
        ),
        name: settings?.modelLabel ?? "The assistant",
        facts: settings?.updatedAt && (
          <FormFact label="Set up">
            {calendarDate(settings.updatedAt)}
            {settings.updatedBy ? ` by ${settings.updatedBy}` : ""}
          </FormFact>
        ),
      },
      description:
        "The provider and its key are forgotten, and conversations in progress end. Nothing in any database changes, and it can be set up again.",
      action: async () => {
        await del("/databases/assistant")
        onSaved()
        onClose()
      },
    })

  return (
    <form
      id="assistant-settings"
      className="space-y-6"
      aria-busy={saving}
      onSubmit={(event) => {
        event.preventDefault()
        void save()
      }}
    >
      <FormFacts>
        <FormFact label="Used by">every SQL database&rsquo;s editor on this server</FormFact>
      </FormFacts>

      <div className="space-y-1.5">
        <p id="assistant-provider" className="text-body font-medium">
          Who answers
        </p>
        <ChoiceGrid columns={2} role="group" aria-labelledby="assistant-provider" className="gap-2">
          {PROVIDERS.map((provider) => (
            <ProductCard
              key={provider.id}
              product={provider.product}
              fallback={Sparkles}
              label={provider.label}
              detail={provider.detail}
              selected={draft.provider === provider.id}
              onClick={() =>
                draft.provider !== provider.id &&
                change({
                  provider: provider.id,
                  model:
                    provider.id === "anthropic"
                      ? (models[0]?.id ?? "claude-opus-5-5")
                      : settings?.provider === "openai"
                        ? (settings.model ?? "")
                        : "",
                  key: "",
                })
              }
            />
          ))}
        </ChoiceGrid>
      </div>

      {draft.provider === "anthropic" ? (
        <FormSection title="Claude">
          <Field
            label="Model"
            hint={
              errorOf("model")
                ? undefined
                : "Opus answers best; Haiku is the cheapest and quickest."
            }
            error={errorOf("model")}
          >
            <Segments
              label="Claude model"
              value={draft.model}
              onChange={(model) => change({ model })}
              options={models.map((m) => ({ value: m.id, label: m.label.replace(/^Claude /, "") }))}
              fill
            />
          </Field>
          <KeyField
            id="assistant-key"
            label="API key"
            placeholder={keeps ? "Leave blank to keep the saved key" : "sk-ant-…"}
            hint="From the Claude Console. Sealed with this install's master key and never shown again."
            value={draft.key}
            shown={shown}
            onShown={setShown}
            onChange={(key) => change({ key })}
            error={errorOf("key")}
          />
        </FormSection>
      ) : (
        <FormSection title="Server">
          <Field
            label="Address"
            htmlFor="assistant-base-url"
            hint="Where its /v1 answers. HTTPS, or HTTP on this machine or a private network."
            error={errorOf("baseUrl")}
          >
            <Input
              id="assistant-base-url"
              type="url"
              inputMode="url"
              autoComplete="off"
              spellCheck={false}
              placeholder="http://127.0.0.1:11434/v1"
              value={draft.baseUrl}
              aria-invalid={Boolean(errorOf("baseUrl"))}
              onChange={(event) => change({ baseUrl: event.target.value })}
              className="font-mono"
            />
          </Field>
          <Field
            label="Model"
            htmlFor="assistant-model"
            hint="As the server's own list names it."
            error={errorOf("model")}
          >
            <Input
              id="assistant-model"
              autoComplete="off"
              spellCheck={false}
              placeholder="qwen2.5-coder:14b"
              value={draft.model}
              aria-invalid={Boolean(errorOf("model"))}
              onChange={(event) => change({ model: event.target.value })}
              className="font-mono"
            />
          </Field>
          <KeyField
            id="assistant-key"
            label="Key"
            placeholder={
              keeps ? "Leave blank to keep the saved key" : "Only if the server asks for one"
            }
            hint="Sealed with this install's master key and never shown again."
            value={draft.key}
            shown={shown}
            onShown={setShown}
            onChange={(key) => change({ key })}
            error={errorOf("key")}
          />
        </FormSection>
      )}

      <Notice title="What the provider is sent">
        The question, and the structure of the database it is about: table and column names, types,
        keys and references. Rows are sent only with a question someone attaches a result to.
        Nothing the assistant writes is run, and every question is in the audit log with what it
        cost.
      </Notice>

      {refusal && !["key", "baseUrl", "model"].includes(refusal.field ?? "") && (
        <FormNote tone="danger" role="alert">
          {refusal.message}
        </FormNote>
      )}

      {settings?.configured && (
        <FormSection title="Turn off">
          <div className="flex min-w-0 flex-wrap items-center justify-between gap-3">
            <p className="min-w-0 flex-1 text-hint leading-relaxed text-muted-foreground">
              Forgets the provider and its key for every database.
            </p>
            <Button type="button" size="sm" variant="outline" onClick={turnOff}>
              Turn off the assistant
            </Button>
          </div>
        </FormSection>
      )}
      <SidePanelFooter>
        <Button type="submit" form="assistant-settings" pending={saving}>
          Save
        </Button>
      </SidePanelFooter>
    </form>
  )
}

/** A key, masked until the reader asks to see what they typed. */
function KeyField({
  id,
  label,
  placeholder,
  hint,
  value,
  shown,
  onShown,
  onChange,
  error,
}: {
  id: string
  label: string
  placeholder: string
  hint: string
  value: string
  shown: boolean
  onShown: (shown: boolean) => void
  onChange: (value: string) => void
  error?: string
}) {
  return (
    <Field label={label} htmlFor={id} hint={hint} error={error}>
      <InputGroup>
        <InputGroupInput
          id={id}
          type={shown ? "text" : "password"}
          autoComplete="off"
          spellCheck={false}
          placeholder={placeholder}
          value={value}
          aria-invalid={Boolean(error)}
          onChange={(event) => onChange(event.target.value)}
          className={cn(shown && "font-mono")}
        />
        <InputGroupAddon align="inline-end" className="gap-0 p-0">
          <InputGroupToggle
            icon={shown ? EyeOff : Eye}
            label="Show"
            aria-label="Show the key"
            pressed={shown}
            onPressedChange={onShown}
          />
        </InputGroupAddon>
      </InputGroup>
    </Field>
  )
}
