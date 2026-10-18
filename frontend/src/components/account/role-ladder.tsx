"use client"

import type { Capability, Role } from "@/lib/types"
import { cn } from "@/lib/utils"
import { ProductGlyphs } from "@/components/product-logo"
import { Tag } from "@/components/tag"
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
 * adds, each drawn with its glyph on a tile of the rung's hue and the things on
 * this server it reaches (Docker, systemd and PM2 for Start and stop).
 *
 * It was six rows of the same grey glyph, each with *granted* at its end, which
 * answered "what can I do" and never "what could I not". On the ladder the
 * powers past the account's rung are the answer to why a page is missing from
 * its sidebar, and what the next role up would add. Whether a power is lit is
 * read from the session's own capabilities, not from the role, so the ladder
 * never claims more than the API will allow.
 */
export function RoleLadder({ role, can }: { role: Role; can: (key: Capability) => boolean }) {
  const reached = ROLE_LADDER.indexOf(role)
  return (
    <ol aria-label="Roles" className="grid min-w-0 gap-x-8 gap-y-8 md:grid-cols-3">
      {ROLE_LADDER.map((rung, index) => {
        const Mark = ROLE_MARK[rung]
        const lit = index <= reached
        const hue = ROLE_HUE[rung]
        const adds = CAPABILITIES.filter((cap) => cap.from === rung)
        return (
          <li key={rung} className="min-w-0 space-y-4">
            <div className="space-y-2.5">
              <span
                aria-hidden
                className={cn("block h-1 rounded-full transition-colors", !lit && "bg-meter-track")}
                style={lit ? { background: hue } : undefined}
              />
              <div className="flex min-w-0 items-center gap-2">
                <Mark
                  aria-hidden
                  className="size-4 shrink-0"
                  style={{ color: lit ? hue : "var(--muted-foreground)" }}
                />
                <span
                  className={cn(
                    "min-w-0 truncate text-body font-semibold",
                    !lit && "text-muted-foreground",
                  )}
                >
                  {ROLE_NAME[rung]}
                </span>
                {rung === role && <Tag className="text-brand">your role</Tag>}
              </div>
              <p className="text-hint text-muted-foreground">{ROLE_SUMMARY[rung]}</p>
            </div>
            <ul className="space-y-3">
              {adds.map((cap) => {
                const held = can(cap.key)
                const Glyph = cap.icon
                return (
                  <li
                    key={cap.key}
                    className={cn("flex min-w-0 items-start gap-3", !held && "opacity-55")}
                  >
                    <span
                      aria-hidden
                      className="flex size-8 shrink-0 items-center justify-center rounded-lg border border-hairline"
                      style={
                        held
                          ? {
                              color: hue,
                              background: `color-mix(in oklab, ${hue} 14%, transparent)`,
                            }
                          : undefined
                      }
                    >
                      <Glyph className={cn("size-4", !held && "text-muted-foreground")} />
                    </span>
                    <span className="min-w-0 flex-1 space-y-0.5">
                      <span className="flex min-w-0 items-center gap-2">
                        <span className="truncate text-body font-medium">{cap.title}</span>
                        {cap.products && <ProductGlyphs ids={cap.products} />}
                      </span>
                      <span className="block text-hint leading-snug text-muted-foreground">
                        {cap.description}
                      </span>
                      <span className="sr-only">
                        {held ? "Granted." : `Not in the ${role} role.`}
                      </span>
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
