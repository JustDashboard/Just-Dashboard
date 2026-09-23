"use client"

import { Check, Copy, External } from "@/components/icons"
import { useCopy } from "@/hooks/use-copy"
import { Button } from "@/components/ui/button"
import { Label } from "@/components/ui/label"

/**
 * The TOTP seed, grouped in fours so it can be read aloud or typed without
 * losing your place, with the otpauth:// link beside it for the phone that is
 * already holding the authenticator.
 *
 * Enrolling happens in two places — at sign-in, on an install that requires
 * it, and on the account's Security page — and both show the seed this way.
 */
export function TotpSecret({ secret, otpauthUrl }: { secret: string; otpauthUrl: string }) {
  const { copy, copied } = useCopy()
  const grouped = secret.replace(/\s+/g, "").match(/.{1,4}/g) ?? [secret]

  return (
    <div className="space-y-2">
      <Label>Secret</Label>
      <div className="flex flex-wrap gap-1 rounded-lg border border-hairline bg-surface-sunken p-2.5">
        {grouped.map((chunk, i) => (
          <code key={i} className="font-mono text-body tracking-widest">
            {chunk}
          </code>
        ))}
      </div>
      <div className="flex flex-wrap gap-2">
        <Button type="button" variant="outline" size="sm" onClick={() => void copy(secret)}>
          {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
          {copied ? "Copied" : "Copy secret"}
        </Button>
        <Button type="button" variant="ghost" size="sm" asChild>
          <a href={otpauthUrl}>
            <External className="size-3.5" />
            Open in authenticator
          </a>
        </Button>
      </div>
    </div>
  )
}
