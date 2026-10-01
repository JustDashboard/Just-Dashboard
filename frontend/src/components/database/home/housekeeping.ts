/**
 * The commands a client sends to ask about the server rather than to use
 * it: handshakes, statistics, configuration. On a quiet server they are
 * mostly this dashboard's own reads — INFO every five seconds — and ranked
 * with the rest they sat at the top of "busiest commands" of a store nobody
 * was using. They are counted apart, and the block says how much they were.
 */
const HOUSEKEEPING = new Set([
  "INFO",
  "COMMAND",
  "HELLO",
  "AUTH",
  "SELECT",
  "PING",
  "CLIENT",
  "CONFIG",
  "SLOWLOG",
  "LATENCY",
  "MEMORY",
  "DBSIZE",
  "ACL",
  "ROLE",
  "LASTSAVE",
  "MODULE",
])

/** Whether a command — "CONFIG GET", "info" — is housekeeping. Its first word decides. */
export function housekeeping(command: string): boolean {
  return HOUSEKEEPING.has(command.trim().split(/[\s|]/)[0].toUpperCase())
}
