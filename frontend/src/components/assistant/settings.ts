"use client"

import { useEffect } from "react"
import { get } from "@/lib/api"
import { usePoll } from "@/hooks/use-poll"

/**
 * Who the assistant asks, for the whole install. Set up once, and every place
 * that uses it reads this: the SQL editor, a drawing's Describe, a failed run's
 * Ask why, a deployment's assistant, a new deployment's setup. A place added
 * later reads it too, and has the provider without asking for one.
 */

export type AssistantProvider = "anthropic" | "openai"
/** How much it may do before it has to ask, which the server holds it to. */
export type AssistantMode = "ask" | "changes" | "full"

/** A provider the settings offer by name. */
export interface AssistantPreset {
  id: string
  label: string
  detail: string
  /** Its logo (`components/product-logo.tsx`). */
  product: string
  provider: AssistantProvider
  baseUrl?: string
  /** A server on this machine or network, whose address is the person's to say. */
  local?: boolean
  keyRequired: boolean
  keyUrl?: string
}

/** A saved provider. Its key is never here. */
export interface AssistantProfile {
  preset: string
  provider: AssistantProvider
  model: string
  modelLabel: string
  baseUrl?: string
  keySet: boolean
  toolCalls: boolean
  active: boolean
  updatedAt?: string
  updatedBy?: string
}

/** `GET /assistant`: the provider as any page may read it. No key is ever here. */
export interface AssistantSettings {
  configured: boolean
  provider?: AssistantProvider
  model?: string
  modelLabel?: string
  baseUrl?: string
  keySet: boolean
  updatedAt?: string
  updatedBy?: string
  /** The Claude models the settings offer, newest tier first. */
  models: { id: string; label: string }[]
  /** A saved provider this install cannot read. */
  problem?: string
  /** The active provider, and whether it can be offered tools. */
  preset?: string
  tools?: boolean
  presets?: AssistantPreset[]
  profiles?: AssistantProfile[]
  defaultMode?: AssistantMode
}

/** What the settings form sends to `PUT /assistant/providers/{preset}`. */
export interface AssistantSetup {
  model: string
  baseUrl: string
  key: string
  toolCalls?: boolean
  activate?: boolean
}

const CHANGED = "jd:assistant-changed"

/** Every page's reading of the settings is read again once they are saved. */
export function announceAssistantChange() {
  window.dispatchEvent(new Event(CHANGED))
}

/**
 * The settings, read once and again when they are saved. They change when an
 * administrator changes them, so the read is slow. The rail, the dock, the
 * palette and the page each ask, so the read is keyed: one request and one
 * answer for all of them (the Overview used to read it three times at once).
 */
export function useAssistantSettings(enabled: boolean) {
  const poll = usePoll<AssistantSettings>(
    (signal) => get<AssistantSettings>("/assistant", undefined, signal),
    120_000,
    [],
    { enabled, key: "/assistant" },
  )
  const { refresh } = poll
  useEffect(() => {
    const again = () => refresh()
    window.addEventListener(CHANGED, again)
    return () => window.removeEventListener(CHANGED, again)
  }, [refresh])
  return poll
}

/**
 * The logo of whoever answers. Another server has none, and is not drawn as OpenAI
 * for speaking its protocol; an answer before the presets arrive falls to its kind.
 */
export function assistantProduct(view: AssistantSettings | undefined): string {
  const preset = view?.presets?.find((entry) => entry.id === view.preset)
  if (preset) return preset.product
  return view?.provider === "openai" ? "openai" : "claude"
}

/** Who a question goes to, in the line that says what is sent. */
export function assistantDestination(view: AssistantSettings | undefined): string {
  const preset = view?.presets?.find((entry) => entry.id === view.preset)
  if (preset && !preset.local) return preset.label
  if (view?.provider === "anthropic") return "Anthropic"
  if (view?.baseUrl) {
    try {
      return new URL(view.baseUrl).host
    } catch {
      return "the server"
    }
  }
  return "the server"
}

export const MODE_LABEL: Record<AssistantMode, string> = {
  ask: "Ask every time",
  changes: "Ask before changes",
  full: "Full access",
}

/** What each mode lets it do, in the words the choice is made on. */
export const MODE_DETAIL: Record<AssistantMode, string> = {
  ask: "Every read and every change waits for your Allow.",
  changes: "It reads on its own. Every change waits for your Allow before it runs.",
  full: "Runs everything without asking — reads, changes, restarts and deletions — as you, within your role. Every call is audited.",
}
