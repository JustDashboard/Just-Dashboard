"""What test-changed.sh picks for a change: browser specs, script tests and Go packages."""
import os
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


# What the script runs for a part of what an operator runs or the stack is
# made from: the tests under scripts/ that name it.
SCRIPT_PROBE = 'eval "$(sed -n "/^script_tests_for() {/,/^}/p" "$1")"\nscript_tests_for "$2"\n'

SCRIPTS = {
    "scripts/manage.sh": "#!/bin/bash\n",
    "scripts/test_manage.py": 'TOOLS = ["manage.sh", "install.sh"]\nCOMPOSE = "docker-compose.yml"\n',
    "scripts/test_pin_images.py": 'FILES = ["backend/Dockerfile", "docker-compose.yml"]\n',
    "scripts/test_dotenv.py": 'SCRIPT = "dotenv.sh"\nEXAMPLE = ".env.example"\n',
    "scripts/test_proxy.py": 'ENTRYPOINT = "deploy/proxy-entrypoint.sh"\n',
}


class ScriptTestSelectionTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = pathlib.Path(directory.name)
        for name, content in SCRIPTS.items():
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)

    def picked(self, changed):
        result = subprocess.run(
            ["bash", "-c", "set -euo pipefail\n" + SCRIPT_PROBE, "probe", str(SCRIPT), changed],
            cwd=self.root, capture_output=True, text=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        return sorted(result.stdout.split())

    def test_a_script_runs_the_tests_that_name_it(self):
        self.assertEqual(self.picked("scripts/manage.sh"), ["scripts/test_manage.py"])
        self.assertEqual(self.picked("install.sh"), ["scripts/test_manage.py"])

    def test_what_the_stack_is_made_from_runs_the_tests_that_name_it(self):
        self.assertEqual(self.picked("docker-compose.yml"), ["scripts/test_manage.py", "scripts/test_pin_images.py"])
        self.assertEqual(self.picked("backend/Dockerfile"), ["scripts/test_pin_images.py"])
        self.assertEqual(self.picked(".env.example"), ["scripts/test_dotenv.py"])
        self.assertEqual(self.picked("deploy/proxy-entrypoint.sh"), ["scripts/test_proxy.py"])

    def test_a_changed_test_runs_as_itself_and_a_deleted_one_does_not(self):
        self.assertEqual(self.picked("scripts/test_dotenv.py"), ["scripts/test_dotenv.py"])
        self.assertEqual(self.picked("scripts/test_gone.py"), [])

    def test_a_file_no_test_names_and_a_file_of_the_product_pick_nothing(self):
        self.assertEqual(self.picked("deploy/Caddyfile"), [])
        self.assertEqual(self.picked("frontend/src/lib/manage.sh"), [])
        self.assertEqual(self.picked("README.md"), [])


# The packages run whole for a changed file that is not Go, which the script
# asks of ci-plan.py: here with the real one, over a tree of two packages.
OTHER_PROBE = (
    'eval "$(sed -n "/^packages_for_other_files() {/,/^}/p" "$1")"\n'
    'root=$PWD\nshift\npackages_for_other_files "$@"\n'
)

BACKEND = {
    "backend/go.mod": "module example.com/dashboard/backend\n\ngo 1.26\n",
    "backend/internal/selfupdate/check.go": "package selfupdate\n",
    "backend/internal/selfupdate/changelog.json": "{}\n",
    "backend/internal/selfupdate/testdata/old.json": "{}\n",
    "backend/internal/version/version.go": "package version\n",
    "backend/internal/version/version_test.go": (
        'package version\n\nvar files = []string{"../../../frontend/package.json", "../../../docker-compose.yml"}\n'
    ),
    "backend/Dockerfile": "FROM scratch\n",
    "frontend/package.json": "{}\n",
    "docker-compose.yml": "services: {}\n",
    "README.md": "\n",
}


class OtherFilePackageSelectionTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = pathlib.Path(directory.name)
        for name, content in BACKEND.items():
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)
        (self.root / "scripts").mkdir()
        (self.root / "scripts/ci-plan.py").write_text(SCRIPT.with_name("ci-plan.py").read_text())

    def packages(self, *changed):
        result = subprocess.run(
            ["bash", "-c", "set -euo pipefail\n" + OTHER_PROBE, "probe", str(SCRIPT), *changed],
            cwd=self.root, capture_output=True, text=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        return result.stdout.split()

    def test_a_file_beside_no_go_file_runs_the_nearest_package_above_it(self):
        self.assertEqual(self.packages("backend/internal/selfupdate/changelog.json"), ["./internal/selfupdate"])
        self.assertEqual(self.packages("backend/internal/selfupdate/testdata/old.json"), ["./internal/selfupdate"])

    def test_a_file_a_packages_tests_read_from_outside_runs_that_package(self):
        self.assertEqual(self.packages("frontend/package.json"), ["./internal/version"])
        self.assertEqual(self.packages("docker-compose.yml", "README.md"), ["./internal/version"])

    def test_a_file_no_package_holds_or_reads_picks_nothing(self):
        self.assertEqual(self.packages("backend/Dockerfile", "README.md"), [])


# The packages a change to go.mod reaches, with a `go` that lists three
# packages and what each depends on.
MODULE_PROBE = (
    'eval "$(sed -n "/^packages_using_changed_modules() {/,/^}/p" "$1")"\n'
    'fork=$2\npackages_using_changed_modules\n'
)

GO = """#!/bin/sh
if [ "$2" = -m ]; then echo example.com/dashboard/backend; exit 0; fi
cat <<'LIST'
example.com/dashboard/backend/internal/dbx fmt github.com/clickhouse/client github.com/andybalholm/brotli/matchfinder
example.com/dashboard/backend/internal/auth fmt golang.org/x/crypto/bcrypt
example.com/dashboard/backend/internal/files fmt os golang.org/x/cryptography
LIST
"""

GO_MOD = """module example.com/dashboard/backend

go 1.26.10

require (
\tgolang.org/x/crypto v0.58.0
\tgolang.org/x/unused v0.1.0
)

require (
\tgithub.com/andybalholm/brotli v1.2.2 // indirect
)
"""


class ModuleChangeSelectionTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = pathlib.Path(directory.name) / "repository"
        (self.root / "backend").mkdir(parents=True)
        self.bin = pathlib.Path(directory.name) / "bin"
        self.bin.mkdir()
        (self.bin / "go").write_text(GO)
        (self.bin / "go").chmod(0o755)
        self.env = dict(os.environ, PATH=f"{self.bin}:{os.environ['PATH']}",
                        GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_SYSTEM=os.devnull)
        self.git("init", "--quiet")
        (self.root / "backend/go.mod").write_text(GO_MOD)
        self.git("add", "-A")
        self.git("commit", "--quiet", "-m", "before")
        self.fork = self.git("rev-parse", "HEAD")

    def git(self, *args):
        return subprocess.run(
            ["git", "-C", str(self.root), "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
             "-c", "commit.gpgsign=false", *args],
            env=self.env, capture_output=True, text=True, check=True).stdout.strip()

    def packages(self, *replacements):
        text = GO_MOD
        for old, new in replacements:
            self.assertIn(old, text)
            text = text.replace(old, new)
        (self.root / "backend/go.mod").write_text(text)
        result = subprocess.run(
            ["bash", "-c", "set -euo pipefail\n" + MODULE_PROBE, "probe", str(SCRIPT), self.fork],
            cwd=self.root, env=self.env, capture_output=True, text=True,
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        return result.stdout.split()

    def test_a_bumped_module_runs_the_packages_that_import_it(self):
        self.assertEqual(self.packages(("x/crypto v0.58.0", "x/crypto v0.59.0")), ["./internal/auth"])

    def test_a_bumped_indirect_module_runs_the_packages_that_reach_it(self):
        self.assertEqual(self.packages(("brotli v1.2.2", "brotli v1.2.8")), ["./internal/dbx"])

    def test_a_module_nothing_imports_runs_nothing(self):
        self.assertEqual(self.packages(("x/unused v0.1.0", "x/unused v0.2.0")), [])

    def test_a_new_toolchain_runs_every_package(self):
        self.assertEqual(self.packages(("go 1.26.10", "go 1.26.11"), ("x/crypto v0.58.0", "x/crypto v0.59.0")), ["./..."])

    def test_an_unchanged_file_runs_nothing(self):
        self.assertEqual(self.packages(), [])


# The script from its first line to its plan, over a repository with one
# commit and a change to each kind of file it used to pick nothing for.
class PlanTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = pathlib.Path(directory.name)
        tree = {**BACKEND, **SCRIPTS, "install.sh": "#!/bin/bash\n", "frontend/src/lib/version.ts": "export const v = 1\n",
                "frontend/tests/browser/design-system.spec.ts": "", "frontend/tests/browser/navigation.spec.ts": "",
                "frontend/tests/browser/docker-ui.spec.ts": ""}
        for name, content in tree.items():
            path = self.root / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)
        (self.root / "scripts/ci-plan.py").write_text(SCRIPT.with_name("ci-plan.py").read_text())
        self.env = dict(os.environ, GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_SYSTEM=os.devnull, JD_TEST_CHANGED_PLAN="1")
        self.git("init", "--quiet")
        self.git("add", "-A")
        self.git("commit", "--quiet", "-m", "before")

    def git(self, *args):
        subprocess.run(
            ["git", "-C", str(self.root), "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
             "-c", "commit.gpgsign=false", *args],
            env=self.env, capture_output=True, text=True, check=True)

    def plan(self, *changed):
        for name in changed:
            with open(self.root / name, "a") as file:
                file.write("# changed\n")
        result = subprocess.run(["bash", str(SCRIPT), "HEAD"], cwd=self.root, env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return dict(line.split(":", 1) for line in result.stdout.splitlines() if ":" in line)

    def test_the_operators_scripts_and_the_stack_pick_their_tests(self):
        plan = self.plan("install.sh", "scripts/manage.sh", "docker-compose.yml", ".env.example")
        self.assertEqual(plan["Shell syntax"].split(), ["install.sh", "scripts/manage.sh"])
        self.assertEqual(plan["Script tests"].split(),
                         ["scripts/test_dotenv.py", "scripts/test_manage.py", "scripts/test_pin_images.py"])
        # And the package whose tests read the compose file.
        self.assertEqual(plan["Go packages"].split(), ["./internal/version"])

    def test_a_changelog_and_a_dependency_manifest_pick_packages_and_the_shell_specs(self):
        plan = self.plan("backend/internal/selfupdate/changelog.json", "frontend/package.json")
        self.assertEqual(plan["Go packages"].split(), ["./internal/selfupdate", "./internal/version"])
        self.assertEqual(plan["Lint"].strip(), ".")
        self.assertEqual(plan["Browser specs"].split(),
                         ["tests/browser/design-system.spec.ts", "tests/browser/navigation.spec.ts"])
        self.assertEqual(plan["Script tests"].strip(), "")

    def test_a_document_picks_nothing(self):
        plan = self.plan("README.md")
        self.assertEqual({key: value.strip() for key, value in plan.items() if not key.startswith("Against")},
                         {"Shell syntax": "", "Script tests": "", "Lint": "", "Go packages": "", "Browser specs": ""})


if __name__ == "__main__":
    unittest.main()
