"use client"

import { Sparkles } from "@/components/icons"
import { useAuth } from "@/hooks/use-auth"
import { ProductGlyph } from "@/components/product-logo"
import { EmptyState, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import type { AssistantSettings } from "@/components/assistant/settings"

const PROVIDERS = ["claude", "openai", "gemini", "openrouter", "mistral", "groq", "ollama"]

/**
 * What every place that uses the assistant says while it is off, so turning it on
 * is the same press wherever it was wanted.
 *
 * With `failed` it is the same state for a page whose read of the settings did
 * not come back: the page does not know whether the assistant is on, so it says
 * that in the off state's own shape, with the read to try again as its button,
 * rather than printing the transport's error as its headline.
 */
export function AssistantOff({
  settings,
  onSetUp,
  what,
  failed,
  className,
}: {
  settings: AssistantSettings | undefined
  onSetUp: () => void
  /** What it would do here, once it is on. */
  what: string
  /** The settings could not be read: the state says so, and its button asks again. */
  failed?: { onRetry: () => void; retrying?: boolean }
  className?: string
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
  if (failed) {
    return (
      <div className={className}>
        <EmptyState
          className="border-0 px-3 py-10"
          icon={Sparkles}
          title="The assistant did not answer"
          description="Its settings could not be read just now. Nothing was changed."
          action={
            <div className="flex flex-col items-center gap-3">
              <span aria-hidden className="flex items-center gap-2 opacity-90">
                {PROVIDERS.map((id) => (
                  <ProductGlyph key={id} id={id} className="size-5" />
                ))}
              </span>
              <Button size="sm" pending={failed.retrying} onClick={failed.onRetry}>
                {failed.retrying ? "Trying again…" : "Try again"}
              </Button>
            </div>
          }
        />
      </div>
    )
  }
  return (
    <div className={className}>
      {settings?.problem && (
        <Notice tone="warning" title="The saved provider cannot be read" className="mb-3">
          {settings.problem}
        </Notice>
      )}
      <EmptyState
        className="border-0 px-3 py-10"
        icon={Sparkles}
        title="The assistant is off"
        description={
          admin
            ? `Choose who answers it — Claude, OpenAI, Gemini, OpenRouter or a model of your own — and ${what}`
            : "An administrator turns it on by choosing who answers it."
        }
        action={
          admin && (
            <div className="flex flex-col items-center gap-3">
              <span aria-hidden className="flex items-center gap-2 opacity-90">
                {PROVIDERS.map((id) => (
                  <ProductGlyph key={id} id={id} className="size-5" />
                ))}
              </span>
              <Button size="sm" onClick={onSetUp}>
                Set up the assistant
              </Button>
            </div>
          )
        }
      />
    </div>
  )
}
