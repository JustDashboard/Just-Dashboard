"use client"

import { useEffect, useRef, useState } from "react"
import { External, Eye, EyeOff, Sparkles } from "@/components/icons"
import { ApiError, del, errorMessage, post, put } from "@/lib/api"
import { calendarDate } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import type { ConfirmRequest } from "@/components/confirm-dialog"
import { ChoiceGrid, ProductCard } from "@/components/choice-card"
import { Segments } from "@/components/deploy/settings/segments"
import { Field, FormFact, FormFacts, FormNote, FormSection, OptionRow } from "@/components/form"
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { LoadingLine } from "@/components/assistant/loading-line"
import {
  announceAssistantChange,
  type AssistantPreset,
  type AssistantProfile,
  type AssistantSettings,
  type AssistantSetup,
} from "@/components/assistant/settings"

const FALLBACK_PRESET: AssistantPreset = {
  id: "anthropic",
  label: "Anthropic",
  detail: "Claude, with your API key",
  product: "claude",
  provider: "anthropic",
  keyRequired: true,
}

/** What each provider's key starts with, for the empty field to say. */
const KEY_SHAPE: Record<string, string> = {
  anthropic: "sk-ant-…",
  openai: "sk-…",
  openrouter: "sk-or-…",
  gemini: "AIza…",
  groq: "gsk_…",
  deepseek: "sk-…",
  xai: "xai-…",
}

function draftOf(
  preset: AssistantPreset,
  profile: AssistantProfile | undefined,
  settings: AssistantSettings | undefined,
): AssistantSetup {
  return {
    model:
      profile?.model ??
      (preset.provider === "anthropic" ? (settings?.models[0]?.id ?? "claude-opus-5-5") : ""),
    baseUrl: profile?.baseUrl ?? preset.baseUrl ?? "",
    key: "",
    toolCalls: profile?.toolCalls ?? true,
  }
}

/**
 * Who answers the assistant, for the whole install: Claude, a hosted provider
 * that speaks OpenAI's chat completions, or a model on a server of your own. Set up
 * here once, every place that asks it uses it.
 *
 * Saving asks the provider first whether the key, the address and the model answer
 * — without asking the model anything — so a mistyped key is said under its field
 * here rather than under somebody's first question. A key is sealed with the
 * install's master key and never read back: a blank field keeps it, for the same
 * provider at the same address.
 */
export function AssistantSettingsSheet({
  open,
  settings,
  preset,
  onClose,
  onSaved,
  confirm,
}: {
  open: boolean
  settings: AssistantSettings | undefined
  /** The provider to open on; otherwise the active one. */
  preset?: string
  onClose: () => void
  onSaved: () => void
  confirm: (request: ConfirmRequest) => void
}) {
  return (
    <SidePanel
      open={open}
      onOpenChange={(next) => !next && onClose()}
      title={settings?.configured ? "Assistant settings" : "Set up the assistant"}
      description="Choose the model provider the dashboard's assistant asks"
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
        <SettingsForm
          key={preset ?? "form"}
          settings={settings}
          initial={preset}
          onClose={onClose}
          onSaved={onSaved}
          confirm={confirm}
        />
      )}
    </SidePanel>
  )
}

function SettingsForm({
  settings,
  initial,
  onClose,
  onSaved,
  confirm,
}: {
  settings: AssistantSettings | undefined
  initial?: string
  onClose: () => void
  onSaved: () => void
  confirm: (request: ConfirmRequest) => void
}) {
  const presets = settings?.presets?.length ? settings.presets : [FALLBACK_PRESET]
  const [presetId, setPresetId] = useState(initial ?? settings?.preset ?? presets[0].id)
  const preset = presets.find((entry) => entry.id === presetId) ?? presets[0]
  const profile = settings?.profiles?.find((entry) => entry.preset === preset.id)
  const [draft, setDraft] = useState<AssistantSetup>(() => draftOf(preset, profile, settings))
  const [shown, setShown] = useState(false)
  const [saving, setSaving] = useState(false)
  const [refusal, setRefusal] = useState<{ field?: string; message: string }>()
  const [models, setModels] = useState<{ id: string; label: string }[]>()
  const [listing, setListing] = useState(false)
  const change = (patch: Partial<AssistantSetup>) => {
    setDraft((held) => ({ ...held, ...patch }))
    setRefusal(undefined)
  }
  const claude = preset.provider === "anthropic"
  // The one saved stays offered, whatever its server lists today.
  const listed = claude ? (settings?.models ?? []) : (models ?? [])
  const offered =
    !claude && listed.length > 0 && draft.model && !listed.some((m) => m.id === draft.model)
      ? [{ id: draft.model, label: draft.model }, ...listed]
      : listed
  const keeps =
    profile?.keySet === true && (!preset.local || profile.baseUrl === draft.baseUrl.trim())
  const errorOf = (field: string) => (refusal?.field === field ? refusal.message : undefined)

  // What it can answer with comes from the provider itself, once it can be asked:
  // a saved key, or one typed here, which is used for this and kept nowhere.
  const canList = !claude && (keeps || draft.key !== "" || !preset.keyRequired)
  const listFor = useRef("")
  useEffect(() => {
    if (!canList || (preset.local && !draft.baseUrl.trim())) return
    const asking = `${preset.id}\0${draft.baseUrl}\0${draft.key}`
    if (listFor.current === asking) return
    const timer = window.setTimeout(async () => {
      listFor.current = asking
      setListing(true)
      try {
        const answer = await post<{ models: { id: string; label: string }[] }>(
          "/assistant/models",
          {
            preset: preset.id,
            baseUrl: draft.baseUrl.trim(),
            key: draft.key.trim(),
          },
        )
        setModels(answer.models)
        setRefusal((held) => (held?.field === "models" ? undefined : held))
      } catch (err) {
        setModels(undefined)
        setRefusal({
          field: err instanceof ApiError && err.field ? err.field : "models",
          message: errorMessage(err),
        })
      } finally {
        setListing(false)
      }
    }, 400)
    return () => window.clearTimeout(timer)
  }, [canList, preset.id, preset.local, draft.baseUrl, draft.key])

  const choosePreset = (next: AssistantPreset) => {
    if (next.id === preset.id) return
    setPresetId(next.id)
    setModels(undefined)
    listFor.current = ""
    setDraft(
      draftOf(
        next,
        settings?.profiles?.find((entry) => entry.preset === next.id),
        settings,
      ),
    )
    setRefusal(undefined)
  }

  const save = async () => {
    if (saving) return
    setSaving(true)
    setRefusal(undefined)
    try {
      await put<AssistantSettings>(`/assistant/providers/${preset.id}`, {
        model: draft.model.trim(),
        baseUrl: draft.baseUrl.trim(),
        key: draft.key,
        toolCalls: preset.local ? draft.toolCalls : undefined,
        activate: true,
      })
      notify.success(`The assistant now asks ${preset.label}`)
      announceAssistantChange()
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

  const remove = () =>
    confirm({
      title: `Remove ${preset.label}`,
      confirmLabel: "Remove",
      subject: {
        mark: <ProductLogo id={preset.product} size="sm" fallback={Sparkles} />,
        name: profile?.modelLabel ?? preset.label,
        facts: profile?.updatedAt && (
          <FormFact label="Set up">
            {calendarDate(profile.updatedAt)}
            {profile.updatedBy ? ` by ${profile.updatedBy}` : ""}
          </FormFact>
        ),
      },
      description: profile?.active
        ? "Its key is forgotten and conversations in progress end. Another saved provider takes over, or the assistant turns off if there is none. It can be set up again."
        : "Its key is forgotten. It can be set up again.",
      action: async () => {
        await del(`/assistant/providers/${preset.id}`)
        announceAssistantChange()
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
        <FormFact label="Used by">everywhere the dashboard asks a model</FormFact>
      </FormFacts>

      {/* Opened on one provider, from the page that lists them, the choice is made. */}
      {!initial && (
        <div className="space-y-1.5">
          <p id="assistant-provider" className="text-body font-medium">
            Who answers
          </p>
          <ChoiceGrid
            columns={2}
            role="group"
            aria-labelledby="assistant-provider"
            className="gap-2"
          >
            {presets.map((entry) => (
              <ProductCard
                key={entry.id}
                product={entry.product || undefined}
                fallback={Sparkles}
                label={entry.label}
                detail={entry.detail}
                selected={preset.id === entry.id}
                onClick={() => choosePreset(entry)}
              />
            ))}
          </ChoiceGrid>
        </div>
      )}

      <FormSection title={preset.label}>
        {preset.local && (
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
        )}
        <KeyField
          id="assistant-key"
          label={preset.keyRequired ? "API key" : "Key"}
          placeholder={
            keeps
              ? "Leave blank to keep the saved key"
              : preset.keyRequired
                ? (KEY_SHAPE[preset.id] ?? "")
                : "Only if the server asks for one"
          }
          hint={
            <>
              {preset.keyUrl && (
                <a
                  href={preset.keyUrl}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex items-center gap-1 underline"
                >
                  Get a key from {preset.label} <External className="size-3" />
                </a>
              )}{" "}
              Sealed with this install&rsquo;s master key and never shown again.
            </>
          }
          value={draft.key}
          shown={shown}
          onShown={setShown}
          onChange={(key) => change({ key })}
          error={errorOf("key")}
        />
        <Field
          label="Model"
          htmlFor={claude ? undefined : "assistant-model"}
          hint={
            errorOf("model")
              ? undefined
              : claude
                ? "Opus answers best; Haiku is the cheapest and quickest."
                : offered.length > 0
                  ? `${offered.length.toLocaleString("en-US")} from ${preset.label}'s own list.`
                  : "As its own list names it. The list appears once it can be asked."
          }
          error={errorOf("model")}
        >
          {claude ? (
            <Segments
              label="Claude model"
              value={draft.model}
              onChange={(model) => change({ model })}
              options={offered.map((m) => ({
                value: m.id,
                label: m.label.replace(/^Claude /, ""),
              }))}
              fill
            />
          ) : offered.length > 0 ? (
            <Select value={draft.model} onValueChange={(model) => change({ model })}>
              <SelectTrigger id="assistant-model" aria-label="Model" className="w-full">
                <SelectValue placeholder="Choose a model" />
              </SelectTrigger>
              <SelectContent className="max-h-72">
                {offered.map((model) => (
                  <SelectItem key={model.id} value={model.id}>
                    {model.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          ) : (
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
          )}
          {listing && <LoadingLine />}
        </Field>
        {preset.local && (
          <OptionRow
            title="This model can use tools"
            hint="A deployment's assistant reads and changes things through tools; without them it can only answer from what the page shows."
            checked={draft.toolCalls ?? false}
            onCheckedChange={(toolCalls) => change({ toolCalls })}
          />
        )}
      </FormSection>

      <Notice title="What the provider is sent">
        The question, and what the place it is asked from says it needs. In the SQL editor, the
        structure of the database: table and column names, types, keys and references, and rows only
        with a question someone attaches a result to. About a deployment, what it reads to answer —
        runs, logs, requests, settings — with secret values masked; it never reads a secret&rsquo;s
        value. What it does runs as the person asking, under their role, and is in the audit log.
      </Notice>

      {refusal && !["key", "baseUrl", "model"].includes(refusal.field ?? "") && (
        <FormNote tone="danger" role="alert">
          {refusal.message}
        </FormNote>
      )}

      {profile && (
        <FormSection title="Remove">
          <div className="flex min-w-0 flex-wrap items-center justify-between gap-3">
            <p className="min-w-0 flex-1 text-hint leading-relaxed text-muted-foreground">
              Forgets {preset.label}&rsquo;s key and model for this install.
            </p>
            <Button type="button" size="sm" variant="outline" onClick={remove}>
              Remove {preset.label}
            </Button>
          </div>
        </FormSection>
      )}
      <SidePanelFooter>
        <Button type="submit" form="assistant-settings" pending={saving}>
          Save and use
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
  hint: React.ReactNode
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
