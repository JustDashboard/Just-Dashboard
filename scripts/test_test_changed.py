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
    '/^home_section() {/,/^}/p; /^pages_under() {/,/^}/p" "$1")"\n'
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


if __name__ == "__main__":
    unittest.main()
