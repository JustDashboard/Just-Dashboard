import { useFocusTrail } from "@/hooks/use-focus-return"

/**
 * @deprecated use `useFocusTrail` from `@/hooks/use-focus-return`. It was this
 * folder's own hook under the name of the one every dialog runs; the ops
 * pages that still import it move over in their own pass.
 */
export const useFocusReturn = useFocusTrail
