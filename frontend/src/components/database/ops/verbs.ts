import type { Verb } from "@/components/verbs"
import type { DatabaseVerbSource } from "@/components/database/shell/types"

const NONE: Verb[] = []

/**
 * What is done with the database as a thing kept — back it up now, forget
 * the connection — as the verbs the database's menu draws on every page.
 * They are declared beside the pages that do the same work in full; the menu
 * itself is `shell/database-verbs.tsx`.
 *
 * None yet: they are written with Backups and Settings.
 */
export const useOperateVerbs: DatabaseVerbSource = () => NONE
