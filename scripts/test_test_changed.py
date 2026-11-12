"""Which browser specs test-changed.sh picks for a changed fixture or module."""
import pathlib
import subprocess
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).with_name("test-changed.sh").resolve()

# The script runs its checks at the top level, so only the functions under
# test are loaded.
PROBE = (
    'eval "$(sed -n "/^normalise() {/,/^}/p; /^specs_importing() {/,/^}/p" "$1")"\n'
    'specs_importing "$2"\n'
)

# A spec reaching a table through a module that gathers the tables, the shape
# proxy-ui.spec.ts has, beside a second spec whose table shares a basename.
TREE = {
    "a.spec.ts": 'import { mock } from "./a-fixtures"\n',
    "a-fixtures.ts": 'import * as table from "./fixtures/a/table"\n',
    "fixtures/a/table.ts": 'import { json } from "./shared"\nimport { now } from "../common"\n',
    "fixtures/a/shared.ts": "export const json = 1\n",
    "fixtures/common.ts": "export const now = 1\n",
    "b.spec.ts": 'import type { Table } from "./fixtures/b/table"\n',
    "fixtures/b/table.ts": "export type Table = 1\n",
    "lonely.ts": "export const unused = 1\n",
}


class SpecSelectionTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = pathlib.Path(directory.name)
        for name, content in TREE.items():
            path = self.root / "frontend/tests/browser" / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)

    def specs(self, fixture):
        result = subprocess.run(
            ["bash", "-c", "set -euo pipefail\n" + PROBE, "probe", str(SCRIPT),
             "frontend/tests/browser/" + fixture],
            cwd=self.root, capture_output=True, text=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        return sorted(set(result.stdout.split()))

    def test_a_table_reaches_the_spec_through_the_module_gathering_it(self):
        for fixture in ["fixtures/a/table.ts", "fixtures/a/shared.ts", "fixtures/common.ts"]:
            with self.subTest(fixture=fixture):
                self.assertEqual(self.specs(fixture), ["tests/browser/a.spec.ts"])

    def test_a_basename_shared_with_another_table_picks_only_its_own_spec(self):
        self.assertEqual(self.specs("fixtures/b/table.ts"), ["tests/browser/b.spec.ts"])

    def test_a_directly_imported_fixture_still_picks_its_spec(self):
        self.assertEqual(self.specs("a-fixtures.ts"), ["tests/browser/a.spec.ts"])

    def test_a_module_no_spec_imports_picks_nothing(self):
        self.assertEqual(self.specs("lonely.ts"), [])


# What the script does with a module that reaches more than one section: the
# pages it reaches, and those of them it keeps the specs of.
SECTION_PROBE = (
    'eval "$(sed -n "/^pages_of() {/,/^}/p; /^address_of() {/,/^}/p; '
    '/^home_section() {/,/^}/p; /^pages_under() {/,/^}/p; /^normalise() {/,/^}/p" "$1")"\n'
    'mapfile -t reached < <(pages_of "$2")\n'
    'home=$(home_section "$2")\n'
    'if [ -n "$home" ]; then pages_under "$home" "${reached[@]}"; fi\n'
)

# The engine registry as the rail reads it: a module of the Databases section
# that the dashboard's own layout reaches, and so every page with it.
SOURCES = {
    "components/database/engine.ts": "export const engine = 1\n",
    "components/database/fleet/index.tsx": 'import { engine } from "@/components/database/engine"\n',
    "components/app-sidebar.tsx": 'import { engine } from "@/components/database/engine"\n',
    "components/choice-card.tsx": "export const card = 1\n",
    "app/(dashboard)/layout.tsx": 'import { rail } from "@/components/app-sidebar"\n',
    "app/(dashboard)/logs/page.tsx": 'import { engine } from "@/components/database/engine"\n',
    "app/(dashboard)/databases/page.tsx": 'import { list } from "@/components/database/fleet"\n',
    "app/(dashboard)/databases/[id]/layout.tsx": (
        'import { engine } from "@/components/database/engine"\n'
    ),
    "app/(dashboard)/databases/new/page.tsx": 'import { card } from "@/components/choice-card"\n',
    "app/(dashboard)/deploy/new/page.tsx": 'import { card } from "@/components/choice-card"\n',
    # The data grid, whose parts import each other relative to themselves, and
    # a module one directory up that names one of them the same way.
    "components/database/grid/values.ts": "export const format = 1\n",
    "components/database/grid/grid-row.tsx": 'import { format } from "./values"\n',
    "components/database/grid/data-grid.tsx": 'import { Row } from "./grid-row"\n',
    "components/database/grid/index.ts": 'export { DataGrid } from "./data-grid"\n',
    "components/database/grid/unused.ts": "export const values = 1\n",
    "components/database/kit/values.ts": 'import { format } from "../grid/values"\n',
    "components/database/kit/index.ts": (
        'export { kind } from "@/components/database/kit/values"\n'
    ),
    "app/(dashboard)/databases/[id]/data/page.tsx": (
        'import { DataGrid } from "@/components/database/grid"\n'
    ),
    "app/(dashboard)/databases/[id]/query/page.tsx": (
        'import { kind } from "@/components/database/kit"\n'
    ),
}


class SectionSelectionTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = pathlib.Path(directory.name)
        for name, content in SOURCES.items():
            path = self.root / "frontend/src" / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)

    def kept(self, module):
        result = subprocess.run(
            ["bash", "-c", "set -euo pipefail\n" + SECTION_PROBE, "probe", str(SCRIPT),
             "frontend/src/" + module],
            cwd=self.root, capture_output=True, text=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        return sorted(result.stdout.splitlines())

    def test_a_section_module_the_shell_reads_keeps_its_own_pages(self):
        self.assertEqual(
            self.kept("components/database/engine.ts"),
            [
                "frontend/src/app/(dashboard)/databases/[id]/layout.tsx",
                "frontend/src/app/(dashboard)/databases/page.tsx",
            ],
        )

    def test_a_module_of_no_one_section_keeps_none(self):
        self.assertEqual(self.kept("components/choice-card.tsx"), [])

    def test_a_part_imported_relative_to_its_importer_reaches_the_pages_that_mount_it(self):
        data = "frontend/src/app/(dashboard)/databases/[id]/data/page.tsx"
        query = "frontend/src/app/(dashboard)/databases/[id]/query/page.tsx"
        self.assertEqual(self.kept("components/database/grid/values.ts"), [data, query])
        self.assertEqual(self.kept("components/database/grid/grid-row.tsx"), [data])
        # A file of the same name in another directory is another module.
        self.assertEqual(self.kept("components/database/kit/values.ts"), [query])
        self.assertEqual(self.kept("components/database/grid/unused.ts"), [])


# The type-check as the script runs it, from frontend/.
TYPECHECK_PROBE = 'eval "$(sed -n "/^typecheck() {/,/^}/p" "$1")"\ncd frontend\ntypecheck\n'

# Stands in for `bunx tsc --noEmit`: writes down what it was given to start
# from, leaves its own tsconfig.tsbuildinfo as tsc does whether or not the
# types hold, and ends as it is told to.
BUNX = (
    "#!/usr/bin/env bash\n"
    "cat tsconfig.tsbuildinfo > started-from 2>/dev/null || : > started-from\n"
    'printf %s "$TSC_LEAVES" > tsconfig.tsbuildinfo\n'
    'exit "${TSC_EXIT:-0}"\n'
)


class TypecheckCacheTest(unittest.TestCase):
    """A worktree's first type-check starts from what another worktree learnt."""

    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        base = pathlib.Path(directory.name)
        tools = base / "bin"
        tools.mkdir()
        (tools / "bunx").write_text(BUNX)
        (tools / "bunx").chmod(0o755)
        # Nothing of the machine's own git configuration: the test's clone only.
        self.env = {
            "PATH": f"{tools}:/usr/local/bin:/usr/bin:/bin",
            "HOME": str(base),
            "GIT_CONFIG_GLOBAL": "/dev/null",
            "GIT_CONFIG_SYSTEM": "/dev/null",
        }
        self.clone = base / "clone"
        self.other = base / "other"
        self.git(base, "init", "-q", str(self.clone))
        self.git(self.clone, "-c", "user.name=t", "-c", "user.email=t@example.invalid",
                 "commit", "-q", "--allow-empty", "-m", "start")
        self.git(self.clone, "worktree", "add", "-q", str(self.other))
        for tree in (self.clone, self.other):
            (tree / "frontend").mkdir()
        self.shared = self.clone / ".git/jd-cache/tsconfig.tsbuildinfo"

    def git(self, cwd, *arguments):
        subprocess.run(["git", *arguments], cwd=cwd, env=self.env, check=True, capture_output=True)

    def check(self, tree, leaves, exit_code=0):
        """Runs the type-check in a tree; answers how it ended and what tsc started from."""
        result = subprocess.run(
            ["bash", "-c", "set -euo pipefail\n" + TYPECHECK_PROBE, "probe", str(SCRIPT)],
            cwd=tree, capture_output=True, text=True,
            env={**self.env, "TSC_LEAVES": leaves, "TSC_EXIT": str(exit_code)},
        )
        return result.returncode, (tree / "frontend/started-from").read_text()

    def test_a_new_worktree_starts_from_what_another_one_left(self):
        self.assertEqual(self.check(self.clone, "first"), (0, ""))
        self.assertEqual(self.shared.read_text(), "first")
        self.assertEqual(self.check(self.other, "second"), (0, "first"))
        self.assertEqual(self.shared.read_text(), "second")

    def test_a_worktree_with_its_own_keeps_it(self):
        self.check(self.clone, "theirs")
        (self.other / "frontend/tsconfig.tsbuildinfo").write_text("mine")
        self.assertEqual(self.check(self.other, "mine again"), (0, "mine"))

    def test_a_check_that_fails_leaves_the_shared_copy_alone(self):
        self.check(self.clone, "good")
        code, _ = self.check(self.other, "of a tree that does not type-check", exit_code=2)
        self.assertEqual(code, 2)
        self.assertEqual(self.shared.read_text(), "good")
        # And nothing half written beside it.
        self.assertEqual([p.name for p in self.shared.parent.iterdir()], ["tsconfig.tsbuildinfo"])


if __name__ == "__main__":
    unittest.main()
