"use client"

import { Sparkles } from "@/components/icons"
import { useAuth } from "@/hooks/use-auth"
import { ProductGlyph } from "@/components/product-logo"
import { EmptyState, Notice } from "@/components/state"
import { Button } from "@/components/ui/button"
import type { AssistantSettings } from "@/components/assistant/settings"

/**
 * What every place that uses the assistant says while it is off, so turning it on
 * is the same press wherever it was wanted.
 */
export function AssistantOff({
  settings,
  onSetUp,
  what,
  className,
}: {
  settings: AssistantSettings | undefined
  onSetUp: () => void
  /** What it would do here, once it is on. */
  what: string
  className?: string
}) {
  const { can } = useAuth()
  const admin = can("system.admin")
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
                {["claude", "openai", "gemini", "openrouter", "mistral", "groq", "ollama"].map(
                  (id) => (
                    <ProductGlyph key={id} id={id} className="size-5" />
                  ),
                )}
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
