"""What doc-names.py finds missing in a page, and when test-changed.sh runs it."""
import os
import pathlib
import subprocess
import tempfile
import unittest

HERE = pathlib.Path(__file__).resolve().parent
SCRIPT = HERE / "doc-names.py"

# A repository with a little source: a hook, a constant, a page under a route group, a route the
# backend serves and a link the frontend builds with a template.
SOURCE = {
    "frontend/src/hooks/use-deferred-search.ts": "export function useDeferredSearch() {}\n",
    "frontend/src/components/icon-action.tsx": "export const ROW_DIM = 1\n",
    "frontend/src/app/(dashboard)/databases/[id]/access/page.tsx": "export default function P() {}\n",
    "frontend/src/components/site-link.tsx": "const href = `/proxy/sites/${encodeURIComponent(name)}`\n",
    "backend/internal/api/routes.go": 'r.Get("/api/v1/stacks/{name}/logs", logs)\n',
}

PAGE = """# A page

`useDeferredSearch` and `ROW_DIM` are real; `useGoneSearch` is not.
`components/icon-action.tsx` is a file; `components/top-bar.tsx` is not.
`/databases/[id]/access`, `/proxy/sites/<name>` and `GET /api/v1/stacks/<name>/logs` are routes;
`/proxy/nowhere/<name>` is not. `/etc/ssh/sshd_config` is the host's.
`TopBar` (removed in 0.8) and `old_helper` (retired) are recorded as gone.

```tsx
const x = useFencedAway()
```

| `newThing` | x | `components/new-thing.tsx` | Arrives with a package. | new (WP-99) |
"""


def git(root, *args):
    return subprocess.run(
        ["git", "-C", str(root), "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
         "-c", "commit.gpgsign=false", *args],
        env=dict(os.environ, GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_SYSTEM=os.devnull),
        capture_output=True, text=True, check=True)


def write(root, files):
    for name, content in files.items():
        path = root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content)


class DocNamesTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = pathlib.Path(directory.name)
        write(self.root, {**SOURCE, "docs/internal/frontend/page.md": PAGE})
        git(self.root, "init", "--quiet")
        git(self.root, "add", "-A")
        git(self.root, "commit", "--quiet", "-m", "before")

    def run_script(self, *args):
        return subprocess.run(["python3", str(SCRIPT), *args], cwd=self.root, capture_output=True,
                              text=True, env=dict(os.environ, GIT_CONFIG_GLOBAL=os.devnull))

    def misses(self, result):
        return [tuple(line.split("\t")[1:3]) for line in result.stdout.splitlines()]

    def test_a_page_with_misses_fails_and_says_which(self):
        result = self.run_script("docs/internal/frontend/page.md")
        self.assertEqual(result.returncode, 1, result.stderr)
        self.assertEqual(self.misses(result), [
            ("ident", "useGoneSearch"),
            ("file", "components/top-bar.tsx"),
            ("route", "/proxy/nowhere/<name>"),
        ])

    def test_lines_the_change_did_not_write_are_shown_and_do_not_fail(self):
        result = self.run_script("--since", "HEAD", "docs/internal/frontend/page.md")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(len(self.misses(result)), 3)
        self.assertTrue(all(line.endswith("\tolder") for line in result.stdout.splitlines()))

    def test_a_miss_in_a_line_the_change_wrote_fails(self):
        with open(self.root / "docs/internal/frontend/page.md", "a") as page:
            page.write("And `useDeferredSearch` beside `useNeverWritten`.\n")
        result = self.run_script("--since", "HEAD", "docs/internal/frontend/page.md")
        self.assertEqual(result.returncode, 1)
        new = [line.split("\t")[2] for line in result.stdout.splitlines() if line.endswith("\tnew")]
        self.assertEqual(new, ["useNeverWritten"])

    def test_a_page_git_does_not_track_is_new_throughout(self):
        write(self.root, {"docs/internal/frontend/fresh.md": "`useGoneSearch`\n"})
        result = self.run_script("--since", "HEAD", "docs/internal/frontend/fresh.md")
        self.assertEqual(result.returncode, 1)
        self.assertTrue(result.stdout.strip().endswith("\tnew"))

    def test_a_name_new_source_defines_is_found_before_it_is_committed(self):
        write(self.root, {"frontend/src/lib/gone.ts": "export const useGoneSearch = 1\n",
                          "frontend/src/components/top-bar.tsx": "export {}\n",
                          "frontend/src/lib/nowhere.ts": "const a = `/proxy/nowhere/${n}`\n"})
        result = self.run_script("docs/internal/frontend/page.md")
        self.assertEqual(result.returncode, 0, result.stdout)


# The plan test-changed.sh prints, over a repository whose first commit holds the scripts.
class PlanTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = pathlib.Path(directory.name)
        write(self.root, {
            "README.md": "# Readme\n",
            "docs/internal/frontend/page.md": "# Page\n",
            "frontend/src/app/globals.css": ":root {}\n",
            "scripts/ci-plan.py": (HERE / "ci-plan.py").read_text(),
        })
        git(self.root, "init", "--quiet")
        git(self.root, "add", "-A")
        git(self.root, "commit", "--quiet", "-m", "before")

    def plan(self, *changed):
        for name in changed:
            with open(self.root / name, "a") as file:
                file.write("\n")
        result = subprocess.run(
            ["bash", str(HERE / "test-changed.sh"), "HEAD"], cwd=self.root, capture_output=True, text=True,
            env=dict(os.environ, GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_SYSTEM=os.devnull,
                     JD_TEST_CHANGED_PLAN="1"))
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return dict(line.split(": ", 1) for line in result.stdout.splitlines() if ": " in line)

    def test_a_changed_frontend_page_runs_the_names_and_the_doc_checks(self):
        plan = self.plan("docs/internal/frontend/page.md")
        self.assertEqual(plan.get("Doc names"), "docs/internal/frontend/page.md")
        self.assertEqual(plan.get("Doc checks"), "src/docs-catalogue.test.js")

    def test_the_stylesheet_runs_the_doc_checks_alone(self):
        plan = self.plan("frontend/src/app/globals.css")
        self.assertNotIn("Doc names", plan)
        self.assertEqual(plan.get("Doc checks"), "src/docs-catalogue.test.js")

    def test_another_document_runs_neither(self):
        plan = self.plan("README.md")
        self.assertNotIn("Doc names", plan)
        self.assertNotIn("Doc checks", plan)


if __name__ == "__main__":
    unittest.main()
