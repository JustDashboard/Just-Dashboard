# Contributor conventions

- **Comments explain why, not what.** The prose in this codebase is unusually dense with rationale — match
  it, and when you change a behaviour a comment justifies, update the reasoning rather than deleting it.
- **Commit messages are imperative sentences describing intent**, not conventional-commit prefixes:
  "Report on the server, not on the container it runs in".
- Prettier for TS/TSX: no semicolons, double quotes, `printWidth: 100`, trailing commas. Go: standard
  formatting, no extra linter config.
- AGPL-3.0 with an additional grant to the owner — read CONTRIBUTING.md before touching licence headers or
  adding dependencies.

## Contribution templates

`.github/pull_request_template.md` prompts for the change, rationale, validation, UI evidence, documentation
review, and agreement to the contribution terms. `.github/ISSUE_TEMPLATE/` contains bug and feature forms;
its `config.yml` directs vulnerabilities to private reporting and hides blank issues from contributors
without write access. Template labels must already exist in the repository (`bug` and `enhancement`).

The PR and bug templates are adapted from T3 Code, with its MIT notice preserved in
[`.github/TEMPLATE_LICENSE`](../../../.github/TEMPLATE_LICENSE). Keep their prompts aligned with
[`CONTRIBUTING.md`](../../../CONTRIBUTING.md). GitHub activates the templates from the default branch;
a PR into a release branch still needs to reach the default branch before contributors see them.
