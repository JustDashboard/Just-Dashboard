import type { Role } from "@/lib/types"
import { Eye, ShieldCheck, Wrench, type Icon } from "@/components/icons"
import { Tag } from "@/components/tag"

/**
 * What a role is called wherever a person reads one: the picker, the badge on
 * a key or an account, the profile's header and the ladder. The key
 * (`readonly`) stays in the API and the address; on the page one role was
 * spelt four ways ('readonly', 'READONLY', 'Read-only', 'READ-ONLY').
 */
export const ROLE_NAME: Record<Role, string> = {
  readonly: "Read-only",
  limited: "Limited",
  admin: "Administrator",
}

/**
 * A role's hue on the ladder and on the users page's share of roles: none of
 * them red or amber, so no rung reads as a warning.
 */
export const ROLE_HUE: Record<Role, string> = {
  readonly: "var(--tag-cyan)",
  limited: "var(--tag-green)",
  admin: "var(--tag-violet)",
}

/**
 * A role's mark, for the pickers that hand one out and the rows that show one:
 * a shield for the role that holds everything, a wrench for the one that
 * operates, an eye for the one that only looks.
 */
export const ROLE_MARK: Record<Role, Icon> = {
  admin: ShieldCheck,
  limited: Wrench,
  readonly: Eye,
}

/** A role as a badge: its mark, its hue and its name, the same on every page. */
export function RoleTag({ role, className }: { role: Role; className?: string }) {
  return (
    <Tag icon={ROLE_MARK[role]} hue={ROLE_HUE[role]} className={className}>
      {ROLE_NAME[role]}
    </Tag>
  )
}
