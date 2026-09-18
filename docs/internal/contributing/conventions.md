# Contributor conventions

- **Comments explain why, not what.** The prose in this codebase is unusually dense with rationale — match
  it, and when you change a behaviour a comment justifies, update the reasoning rather than deleting it.
- **Commit messages are imperative sentences describing intent**, not conventional-commit prefixes:
  "Report on the server, not on the container it runs in".
- Prettier for TS/TSX: no semicolons, double quotes, `printWidth: 100`, trailing commas. Go: standard
  formatting, no extra linter config.
- AGPL-3.0 with an additional grant to the owner — read CONTRIBUTING.md before touching licence headers or
  adding dependencies.
