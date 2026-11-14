"use client"

import type { Capability, Role } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductGlyphs } from "@/components/product-logo"
import { Tag } from "@/components/tag"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import {
  CAPABILITIES,
  ROLE_HUE,
  ROLE_LADDER,
  ROLE_MARK,
  ROLE_NAME,
  ROLE_SUMMARY,
} from "@/components/account/capabilities"

/**
 * What this account can do, as the three roles it could have been given, each
 * adding to the one before it: a rung's bar is lit in its hue up to the
 * account's own role and dark past it, and under each rung are the powers it
 * adds, each a glyph on a tile of the rung's hue, its name, and the things on
 * this server it reaches (Docker, systemd and PM2 for Start and stop).
 *
 * It was six rows of the same grey glyph, each with *granted* at its end, which
 * answered "what can I do" and never "what could I not". On the ladder the
 * powers past the account's rung are the answer to why a page is missing from
 * its sidebar, and what the next role up would add. Whether a power is lit is
 * read from the session's own capabilities, not from the role, so the ladder
 * never claims more than the API will allow.
 *
 * Names and glyphs only. Each rung and each power also carried a sentence,
 * nine of them under one heading, which made a reading of three columns into
 * a page of prose; the sentence is the name's tooltip now, reached by the
 * pointer or the keyboard.
 */
export function RoleLadder({ role, can }: { role: Role; can: (key: Capability) => boolean }) {
  const reached = ROLE_LADDER.indexOf(role)
  return (
    <ol aria-label="Roles" className="grid min-w-0 gap-x-6 gap-y-6 sm:grid-cols-3">
      {ROLE_LADDER.map((rung, index) => {
        const Mark = ROLE_MARK[rung]
        const lit = index <= reached
        const hue = ROLE_HUE[rung]
        const adds = CAPABILITIES.filter((cap) => cap.from === rung)
        return (
          <li key={rung} className="min-w-0 space-y-3">
            <span
              aria-hidden
              className={cn("block h-1 rounded-full transition-colors", !lit && "bg-meter-track")}
              style={lit ? { background: hue } : undefined}
            />
            <div className="flex min-h-6 min-w-0 items-center gap-2">
              <Mark
                aria-hidden
                className="size-4 shrink-0"
                style={{ color: lit ? hue : "var(--muted-foreground)" }}
              />
              <Explained tip={ROLE_SUMMARY[rung]}>
                <span
                  className={cn(
                    "min-w-0 truncate text-body font-semibold",
                    !lit && "text-muted-foreground",
                  )}
                >
                  {ROLE_NAME[rung]}
                </span>
              </Explained>
              {rung === role && <Tag className="text-brand">your role</Tag>}
            </div>
            <ul className="space-y-2">
              {adds.map((cap) => {
                const held = can(cap.key)
                const Glyph = cap.icon
                return (
                  <li
                    key={cap.key}
                    className={cn("flex h-8 min-w-0 items-center gap-2.5", !held && "opacity-55")}
                  >
                    <span
                      aria-hidden
                      className="flex size-7 shrink-0 items-center justify-center rounded-md border border-hairline"
                      style={
                        held
                          ? {
                              color: hue,
                              background: `color-mix(in oklab, ${hue} 14%, transparent)`,
                            }
                          : undefined
                      }
                    >
                      <Glyph className={cn("size-3.5", !held && "text-muted-foreground")} />
                    </span>
                    <Explained tip={cap.description}>
                      <span className="min-w-0 truncate text-body font-medium">{cap.title}</span>
                    </Explained>
                    {cap.products && <ProductGlyphs ids={cap.products} />}
                    <span className="sr-only">
                      {held ? "Granted." : `Not in the ${role} role.`}
                    </span>
                  </li>
                )
              })}
            </ul>
          </li>
        )
      })}
    </ol>
  )
}

/** A name whose sentence is its tooltip, for the pointer and for the keyboard. */
function Explained({ tip, children }: { tip: string; children: React.ReactNode }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- takes the keyboard so that its tooltip can be read without a pointer
          tabIndex={0}
          className="flex min-w-0 cursor-help rounded-sm focus-ring"
        >
          {children}
        </span>
      </TooltipTrigger>
      <TooltipContent className="max-w-xs leading-relaxed">{tip}</TooltipContent>
    </Tooltip>
  )
}
