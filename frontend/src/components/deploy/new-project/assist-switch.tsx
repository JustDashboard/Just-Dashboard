"use client"

import Link from "next/link"
import { Sparkles } from "@/components/icons"
import { Switch } from "@/components/ui/switch"
import { AssistantMark } from "@/components/assistant/mark"
import { assistantProduct, useAssistantSettings } from "@/components/assistant/settings"

/**
 * The choice before there is anything to configure: "I do not want to bother with
 * the config." Pick a source and the assistant takes it from there, with Deploy the
 * one press left. With the assistant off, a word on how to have it.
 */
export function AssistSwitch({
  checked,
  onCheckedChange,
}: {
  checked: boolean
  onCheckedChange: (checked: boolean) => void
}) {
  const settings = useAssistantSettings(true)
  const view = settings.data
  if (!view) return null
  if (!view.configured || !view.tools) {
    return (
      <p className="flex shrink-0 items-center gap-2 text-hint text-muted-foreground">
        <Sparkles aria-hidden className="size-3.5" />
        <span>
          Rather not choose the settings yourself?{" "}
          <Link href="/assistant" className="underline">
            Set up the assistant
          </Link>{" "}
          and it will do it.
        </span>
      </p>
    )
  }
  return (
    <label
      data-slot="assist-switch"
      className="flex shrink-0 cursor-pointer items-center gap-3 rounded-xl border border-rule-brand bg-wash-brand p-3 transition-colors hover:border-brand"
    >
      <AssistantMark product={assistantProduct(view)} size="sm" />
      <span className="min-w-0 flex-1">
        <span className="block text-body font-medium">Configure it with the assistant</span>
        <span className="block text-hint leading-relaxed text-muted-foreground">
          Choose what to deploy and it picks the settings for you. You just press Deploy.
        </span>
      </span>
      <Switch
        aria-label="Configure it with the assistant"
        checked={checked}
        onCheckedChange={onCheckedChange}
      />
    </label>
  )
}
