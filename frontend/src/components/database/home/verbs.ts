import type { Verb } from "@/components/verbs"
import type { DatabaseVerbSource } from "@/components/database/shell/types"

const NONE: Verb[] = []

/**
 * What the server behind the connection can be asked to do — start, stop,
 * restart, protect — as the verbs the database's menu draws on every page.
 * They are declared by the home because the home is where the server's state
 * is read; the menu itself is `shell/database-verbs.tsx`.
 *
 * None yet: the server routes these verbs call arrive with the connection
 * summary.
 */
export const useLifecycleVerbs: DatabaseVerbSource = () => NONE
