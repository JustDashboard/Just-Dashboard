"use client"

import { useState } from "react"
import Link from "next/link"
import { Check, ChevronDown, SettingsGear, Sparkles } from "@/components/icons"
import { errorMessage, post } from "@/lib/api"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { ProductGlyph } from "@/components/product-logo"
import { LoadingLine } from "@/components/assistant/loading-line"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import {
  announceAssistantChange,
  assistantProduct,
  type AssistantProfile,
  type AssistantSettings,
} from "@/components/assistant/settings"

type Model = { id: string; label: string }

/**
 * Which model answers, from wherever the assistant is being asked. AI Elements'
 * `model-selector`, from its registry: a list you can type into, each entry drawn
 * as its provider. Every saved provider and each of the active one's models are one
 * press away, for the whole install, so it is an administrator's; anyone else reads
 * the name.
 */
export function ModelPicker({
  settings,
  onChanged,
  className,
}: {
  settings: AssistantSettings | undefined
  /** The model answering changed: what a conversation says next, it says from nothing. */
  onChanged?: (label: string) => void
  className?: string
}) {
  const { can } = useAuth()
  const [open, setOpen] = useState(false)
  // Listed for one provider: another taking over is another list.
  const [listed, setListed] = useState<{ preset: string; models: Model[] }>()
  const [listing, setListing] = useState(false)
  const [listError, setListError] = useState<string>()
  const product = assistantProduct(settings)
  const label = settings?.modelLabel ?? settings?.model ?? "No model"
  const admin = can("system.admin")
  const active = settings?.preset
  const others = (settings?.profiles ?? []).filter((profile) => !profile.active)

  const models = listed && listed.preset === active ? listed.models : undefined

  // Read when the list is opened, since it asks the provider.
  const opened = async (next: boolean) => {
    setOpen(next)
    if (!next || !active || models) return
    setListing(true)
    setListError(undefined)
    try {
      const answer = await post<{ models: Model[] }>("/assistant/models", {
        preset: active,
        baseUrl: settings?.baseUrl ?? "",
      })
      setListed({ preset: active, models: answer.models })
    } catch (err) {
      setListError(errorMessage(err))
    } finally {
      setListing(false)
    }
  }

  const choose = async (preset: string, model?: string) => {
    setOpen(false)
    try {
      const view = await post<AssistantSettings>(`/assistant/providers/${preset}/activate`, {
        model,
      })
      announceAssistantChange()
      onChanged?.(view.modelLabel ?? view.model ?? "another model")
    } catch (err) {
      notify.error("The model could not be changed", err instanceof Error ? err : undefined)
    }
  }

  const glyph = product ? (
    <ProductGlyph id={product} className="size-3.5" />
  ) : (
    <Sparkles aria-hidden className="size-3.5" />
  )
  if (!admin) {
    return (
      <span
        className={cn(
          "inline-flex h-7 max-w-52 items-center gap-1.5 px-2 text-hint text-muted-foreground",
          className,
        )}
      >
        {glyph}
        <span className="truncate">{label}</span>
      </span>
    )
  }
  return (
    <Popover open={open} onOpenChange={(next) => void opened(next)}>
      <PopoverTrigger asChild>
        <button
          type="button"
          data-slot="model-picker"
          aria-label={`Model: ${label}`}
          className={cn(
            "inline-flex h-7 max-w-52 min-w-0 items-center gap-1.5 rounded-md px-2 text-hint text-muted-foreground focus-ring transition-colors hover:bg-row-hover hover:text-foreground",
            className,
          )}
        >
          {glyph}
          <span className="truncate">{label}</span>
          <ChevronDown aria-hidden className="size-3 shrink-0" />
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-80 p-0">
        <Command>
          <CommandInput placeholder="Find a model" />
          <CommandList className="max-h-72">
            <CommandEmpty>
              {listing ? "Reading its models…" : "No model of that name."}
            </CommandEmpty>
            <CommandGroup
              heading={settings?.presets?.find((p) => p.id === active)?.label ?? "Models"}
            >
              {(models ?? []).map((model) => (
                <CommandItem
                  key={model.id}
                  value={`${model.id} ${model.label}`}
                  onSelect={() => void choose(active!, model.id)}
                >
                  {product && <ProductGlyph id={product} />}
                  <span className="min-w-0 flex-1 truncate">{model.label}</span>
                  {model.id === settings?.model && <Check aria-hidden className="size-3.5" />}
                </CommandItem>
              ))}
              {listing && <LoadingLine />}
              {listError && <p className="px-2 py-1.5 text-hint text-destructive">{listError}</p>}
            </CommandGroup>
            {others.length > 0 && (
              <>
                <CommandSeparator />
                <CommandGroup heading="Switch provider">
                  {others.map((profile: AssistantProfile) => {
                    const preset = settings?.presets?.find((p) => p.id === profile.preset)
                    return (
                      <CommandItem
                        key={profile.preset}
                        value={`${preset?.label ?? profile.preset} ${profile.model}`}
                        onSelect={() => void choose(profile.preset)}
                      >
                        {preset?.product && <ProductGlyph id={preset.product} />}
                        <span className="min-w-0 flex-1 truncate">{preset?.label}</span>
                        <span className="min-w-0 truncate text-hint text-muted-foreground">
                          {profile.modelLabel}
                        </span>
                      </CommandItem>
                    )
                  })}
                </CommandGroup>
              </>
            )}
          </CommandList>
        </Command>
        <Link
          href="/assistant"
          onClick={() => setOpen(false)}
          className="flex items-center gap-2 border-t border-hairline px-3 py-2 text-hint text-muted-foreground focus-ring transition-colors hover:bg-row-hover hover:text-foreground"
        >
          <SettingsGear aria-hidden className="size-3.5" />
          Providers and keys
        </Link>
      </PopoverContent>
    </Popover>
  )
}
