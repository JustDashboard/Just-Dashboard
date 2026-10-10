import { hasGlob } from "@/components/database/redis/bytes"

/** What was typed as the channels to listen to: names, and globs among them. */
export function listenTargets(text: string): { channel: string[]; pattern: string[] } {
  const words = text.split(/[\s,]+/).filter(Boolean)
  return {
    channel: words.filter((word) => !hasGlob(word)).slice(0, 20),
    pattern: words.filter(hasGlob).slice(0, 20),
  }
}
