"use client"

import { useCallback } from "react"
import { usePathname, useRouter, useSearchParams } from "next/navigation"
import { Sparkles } from "@/components/icons"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { navLocation } from "@/components/nav"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { useAssistantSettings } from "@/components/assistant/settings"
import { askAboutHref, isAssistantPage } from "@/components/assistant/agent/about"

/**
 * The page's own name as it names itself for assistive technology — "shop · Logs",
 * a project's name — read when Ask is pressed, since the shell cannot know it
 * otherwise; the rail's name for the address where a page names nothing.
 */
export function pageTitle(pathname: string) {
  const named = document.querySelector('[data-slot="page"] h1')?.textContent?.trim()
  return named || navLocation(pathname)?.title || ""
}

/**
 * Where a press of Ask goes from this page, and whether there is one: a role that
 * may ask, a page that is not the assistant's own, and an assistant that is on —
 * or an administrator, who is asked there to turn it on.
 */
export function useAskAboutPage(): (() => void) | undefined {
  const { can } = useAuth()
  const pathname = usePathname()
  const search = useSearchParams()
  const router = useRouter()
  const asks = can("service.control")
  const settings = useAssistantSettings(asks)
  const shown =
    asks &&
    !isAssistantPage(pathname) &&
    (settings.data?.configured === true || can("system.admin"))
  const ask = useCallback(() => {
    const path = `${pathname}${search.size ? `?${search}` : ""}`
    router.push(askAboutHref(path, pageTitle(pathname)))
  }, [pathname, search, router])
  return shown ? ask : undefined
}

/**
 * Ask about this page, from any page: a new chat about the whole server that is
 * told which page the person was on, and draws the way back to it. Quiet beside
 * Search in the rail and in the phone's strip, because it is there on every page.
 */
export function AskAboutPage({ className }: { className?: string }) {
  const ask = useAskAboutPage()
  if (!ask) return null
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          data-slot="ask-about-page"
          aria-label="Ask the assistant about this page"
          className={cn("shrink-0 gap-1.5 px-2 text-muted-foreground", className)}
          onClick={ask}
        >
          <Sparkles className="size-4 text-brand" />
          Ask
        </Button>
      </TooltipTrigger>
      <TooltipContent>Ask the assistant about this page</TooltipContent>
    </Tooltip>
  )
}
