/**
 * The arguments of a slow command, without the command's own words.
 *
 * The server keeps the whole line as it was sent, so its arguments begin
 * with the command — `["client", "list"]` under the command `CLIENT` — and a
 * row that printed both read "CLIENT client list". The words the command's
 * name already says are left out, however many it has and in whichever case
 * they were typed.
 */
export function argsAfterCommand(command: string, args: readonly string[]): string[] {
  const words = command.split(/\s+/).filter(Boolean)
  let at = 0
  while (at < words.length && args[at]?.toLowerCase() === words[at].toLowerCase()) at++
  return args.slice(at)
}
