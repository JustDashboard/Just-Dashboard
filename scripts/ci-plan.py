#!/usr/bin/env python3
"""Decide what one CI run has to check: what the change can reach, and nothing else.

    scripts/ci-plan.py [--pull-request] [base]      (from anywhere in the repository)

Prints the `name=value` lines `.github/workflows/verify.yml` starts its jobs
from. The change is everything between [base] and HEAD; with no base — a manual
run, a branch with nothing to compare against — or with a change to the
workflow or the scripts it runs, the answer is everything.

A pull request is what somebody waits on before merging, so it is checked as
the change's author checks it locally; the run of the merge it makes, which
nobody waits on, follows the change everywhere it can reach. Where the two
differ is said below.

  - Go: a package is checked when it changed or imports, however indirectly,
    one that did. The graph is read from the import lines, so this needs no
    toolchain; a test's own imports count for that test's package only. The
    packages of the race gate run under the race detector and nowhere else,
    the others plainly. A package whose tests read a file outside it
    (`../../../frontend/src/lib/version.ts`) runs plainly when only that file
    changed: those tests hold the two halves to one contract, and nothing that
    imports the package can tell.
  - Frontend: lint covers the changed files, since every rule here reads one
    file at a time, and the whole tree when the rules or the dependencies
    changed. The browser specs are picked as `scripts/test-changed.sh` picks
    them — a changed module is followed through its importers to the pages
    that use it, and a spec runs when it names one of their addresses. A
    module reaching more than one section, or the shell, runs the two specs
    that check the shell and the design system across the sections in a pull
    request, as that script does; the merge's run follows it to all of them.
    A public asset is followed from the modules that name it. A pull request
    deals its specs into smaller jobs, which finish sooner.
  - Live Docker fixtures: when one of the six deployment packages changed
    itself (they are the packages of the race gate), those of the packages
    being checked — in a pull request, those of the packages that import the
    one that changed. A package that only imports the terminal is checked,
    and builds no containers to find out. The framework builds, forty minutes
    of Docker, run in a pull request only when `internal/deploy` itself
    changed, and in the merge's run whenever it is reached.

`python3 scripts/test_ci_plan.py` checks the picking.
"""

import json
import math
import os
import re
import subprocess
import sys
from pathlib import Path

# The race gate, as the jobs it runs in: a name, how many processes split each
# package's tests, and the packages. ./internal/api runs its tests one at a
# time, so it is split to fill the runner; ./internal/deploy runs its own in
# parallel already.
RACE = [
    ("api", 4, ["backend/internal/api"]),
    (
        "deploy",
        1,
        [
            "backend/internal/deploy",
            "backend/internal/proxysvc",
            "backend/internal/backups",
            "backend/internal/store",
            "backend/internal/dockerx",
        ],
    ),
]

# Asserted without the race detector, which multiplies a SQLite read ten- to
# twenty-five-fold and so measures itself rather than the read.
BUDGETS = "StaysWithinItsBudgetAtReferenceScale"

# The live fixtures every run of them must pass, by package.
LIVE = {
    "backend/internal/deploy": [
        "TestLiveC4ArtifactAdapters",
        "TestLiveC5ActivationAdapters",
        "TestLivePreviewStorageCredentialsNetworkAndCleanup",
        "TestLiveLegacyPreviewQuarantinePreservesProduction",
        "TestLiveRuntimeDiagnoserReadsExitedContainer",
        "TestLiveRuntimeLossKeepsExactlyOneReleaseLive",
        "TestLiveBackendKillAtEveryStepLeavesOneRecoveredRun",
    ],
    "backend/internal/api": [
        "TestLiveDeploymentDatabaseConnection",
        "TestLiveSQLiteApplicationRecoveryVerification",
        "TestLiveBackupDumpsAndRestoresAPostgresDatabase",
    ],
    "backend/internal/dockerx": [
        "TestLiveDeploymentComposeStorageUsesMergedMountsAndFrozenVariables",
        "TestLiveComposeDatabaseNetworkMerge",
    ],
    "backend/internal/proxysvc": [
        "TestLiveCutoverTrafficContinuity",
        "TestLiveCutoverSurvivesProxyLoss",
    ],
}

# The framework builds are forty minutes of Docker on one runner, and one of
# them (leptos) is twelve alone, so they are dealt into jobs of about that
# length. The last job names nothing: it runs whatever the others do not, so a
# framework added to the fixture is built without being added here.
FRAMEWORKS = "TestLiveDetectedFrameworkBuildAndServing"
FRAMEWORK_JOBS = [
    ["leptos"],
    ["trunk", "rust", "rust-workspace", "dotnet", "dotnet-solution", "blazor-wasm", "fsharp", "dotnet-spa", "dotnet-multitarget"],
    ["rails", "sinatra", "phoenix", "laravel", "php", "laravel-vite", "symfony", "jekyll",
     "java", "gradle", "java-reactor", "gradle-multiproject", "gradle-composite", "play", "clojure",
     "go", "go-workspace", "go-embed", "gleam"],
    None,
]

# A change to any of these is a change to what CI itself does.
CI = re.compile(r"^(\.github/workflows/|scripts/(ci-plan\.py|go-test-race\.sh|check-live-test-results\.py)$)")

# What the lint rules and the tools that run them are made of.
LINT_ALL = re.compile(r"^frontend/(eslint\.config\.mjs|eslint-rules/|package\.json|bun\.lock|tsconfig\.json)")

# What every page is built from or served through: the dependencies, the
# build's own configuration and the scripts it runs, and the request proxy.
BUILD = re.compile(
    r"^frontend/("
    r"package\.json|bun\.lock|next\.config\.ts|playwright\.config\.ts|postcss\.config\.mjs|tsconfig\.json"
    r"|scripts/|src/proxy\.ts$"
    r")"
)

# What no walk along the imports of one page will find: the files Next reads
# by name, the files it serves as they are, and anything under src/ that is
# not a module.
NEXT_FILES = re.compile(r"^frontend/src/app/(.*/)?(error|global-error|loading|not-found|template|default)\.tsx$")
PUBLIC = "frontend/public/"
UNSEEN = re.compile(r"^frontend/(public/|src/(?!.*\.(ts|tsx|js|mjs|css|md)$))")

PAGE = re.compile(r"^frontend/src/app/(.*/)?(page|layout)\.tsx$")
ROOT_LAYOUT = re.compile(r"^frontend/src/app/(\([^)]*\)/)?layout\.tsx$")

SPECS = "frontend/tests/browser/"
DESIGN_SYSTEM = SPECS + "design-system.spec.ts"
# The two specs that check the shell and the design system across the
# sections: what a pull request runs for a module more than one section draws,
# as `scripts/test-changed.sh` does.
SHELL = {DESIGN_SYSTEM, SPECS + "navigation.spec.ts"}
# A module of one section that the shell reads as well keeps that section's
# specs beside those two: its own pages are what it breaks first.
HOME_SECTIONS = {"frontend/src/components/database/": "databases"}

TESTS_PER_SHARD = 200
# Smaller in a pull request, which somebody waits on: a job's setup is about a
# minute, and two hundred tests are six to ten more.
PULL_REQUEST_TESTS_PER_SHARD = 100
MOST_SHARDS = 8


def go_packages(root):
    """Each package directory under backend/, with what its files import and read."""
    backend = root / "backend"
    module = re.search(r"^module (\S+)", (backend / "go.mod").read_text(), re.M).group(1)
    imported = re.compile('"' + re.escape(module) + '/([^"]+)"')
    escaping = re.compile(r'"((?:\.\./)+[^"\s]*)"')
    packages = {}
    for path in sorted(backend.rglob("*.go")):
        relative = path.relative_to(root)
        if any(part == "testdata" or part[0] in "._" for part in relative.parts[:-1]):
            continue
        text = path.read_text(errors="replace")
        if re.search(r"^//go:build ignore$", text, re.M):
            continue
        directory = relative.parent.as_posix()
        package = packages.setdefault(directory, {"imports": set(), "test_imports": set(), "reads": set()})
        kind = "test_imports" if path.name.endswith("_test.go") else "imports"
        package[kind].update("backend/" + name for name in imported.findall(text))
        # A path that climbs out of backend/ to something that is there: the
        # other half of a contract. Most `../` in a test is an attack it
        # expects to be refused, or a sample of somebody's repository.
        for literal in escaping.findall(text):
            target = os.path.normpath(os.path.join(directory, literal))
            if not target.startswith(("backend", "..")) and target != "." and (root / target).exists():
                package["reads"].add(target)
    return packages


def go_reaches(packages):
    """Each package with every package its code or its tests reach, itself included."""
    reaches = {}

    def reach(package):
        if package not in reaches:
            reaches[package] = {package}
            for imported in packages.get(package, {"imports": ()})["imports"]:
                reaches[package] |= reach(imported)
        return reaches[package]

    return {
        package: set(reach(package)).union(*(reach(imported) for imported in facts["test_imports"]))
        for package, facts in packages.items()
    }


def go_affected(packages, changed):
    """The packages a change is in, those whose code it reaches, and those it reaches only through a file their tests read."""
    touched, read = set(), set()
    for name in changed:
        if name in ("backend/go.mod", "backend/go.sum"):
            return set(packages), set(packages), set()
        # A file beside no Go file (testdata, an embedded template) belongs to
        # the nearest package above it.
        directory = os.path.dirname(name)
        while directory.startswith("backend/") and directory not in packages:
            directory = os.path.dirname(directory)
        if directory in packages:
            touched.add(directory)
        for package, facts in packages.items():
            if any(name == target or name.startswith(target + "/") for target in facts["reads"]):
                read.add(package)

    affected = {package for package, seen in go_reaches(packages).items() if seen & touched}
    return touched, affected, read - affected


def has_budgets(root, package):
    return any(BUDGETS in path.read_text(errors="replace") for path in (root / package).glob("*_test.go"))


def module_of(name):
    """A file as an import names it: without its extension, and a directory for its index."""
    return re.sub(r"/index$", "", re.sub(r"\.(tsx?|js|mjs)$", "", name))


def frontend_imports(root):
    """What each module under frontend/src and frontend/tests imports, by file."""
    naming = re.compile(r'(?:\bfrom|\bimport)\s*\(?\s*"([^"]+)"')
    imports = {}
    for top in ("frontend/src", "frontend/tests"):
        for path in (root / top).rglob("*"):
            if path.suffix not in (".ts", ".tsx", ".js", ".mjs") or not path.is_file():
                continue
            name = path.relative_to(root).as_posix()
            imports[name] = set()
            for module in naming.findall(path.read_text(errors="replace")):
                if module.startswith("@/"):
                    imports[name].add(module_of("frontend/src/" + module[2:]))
                elif module.startswith("."):
                    imports[name].add(module_of(os.path.normpath(os.path.join(os.path.dirname(name), module))))
    return imports


def naming(page):
    """What a spec that opens this page or layout says: its address, a `[segment]` being whatever the spec put there."""
    address = re.sub(r"/\([^)]*\)", "", page[len("frontend/src/app"):].rsplit("/", 1)[0])
    pattern = "/".join(
        "[^\"'`?]*" if part.startswith("[...") else "[^/\"'`?]+" if part.startswith("[") else re.escape(part)
        for part in address.split("/")
    ) or "/"
    end = "[\"'`?#]|\\$\\{" + ("|/" if page.endswith("/layout.tsx") else "")
    return re.compile("[\"'`]" + pattern + "(?:" + end + ")")


def section(page):
    """The section a page or layout is in: the first segment of its address, and none for the root."""
    address = re.sub(r"/\([^)]*\)", "", page[len("frontend/src/app"):].rsplit("/", 1)[0])
    return address.split("/")[1] if address else ""


def naming_asset(root, asset):
    """The files under src/ that name a public asset, which a page reaches it through."""
    name = asset.rsplit("/", 1)[-1]
    return sorted(
        path.relative_to(root).as_posix() for path in (root / "frontend/src").rglob("*")
        if path.suffix in (".ts", ".tsx", ".js", ".mjs", ".css") and path.is_file()
        and name in path.read_text(errors="replace")
    )


def browser_specs(root, imports, changed, pull_request=False):
    """The browser specs a change reaches."""
    importers = {}
    for name, modules in imports.items():
        for module in modules:
            importers.setdefault(module, set()).add(name)
    every = {name for name in imports if name.startswith(SPECS) and name.endswith(".spec.ts")}
    if any(BUILD.match(name) for name in changed) or not pull_request and any(
        NEXT_FILES.match(name) or UNSEEN.match(name) for name in changed
    ):
        return every

    def reached(name):
        seen, queue = set(), [name]
        while queue:
            name = queue.pop()
            if name not in seen:
                seen.add(name)
                queue.extend(importers.get(module_of(name), ()))
        return seen

    sources = []
    for name in changed:
        if pull_request and name.startswith(PUBLIC):
            sources += naming_asset(root, name) or [name]
        else:
            sources.append(name)

    specs, pages, shell = set(), set(), False
    for name in sources:
        seen = reached(name)
        found = {page for page in seen if PAGE.match(page)}
        broad = (
            NEXT_FILES.match(name) or UNSEEN.match(name) and not found
            or re.match(r"^frontend/src/.*\.css$", name)
            or any(ROOT_LAYOUT.match(page) for page in found)
            or len({section(page) for page in found}) > 1
        )
        if pull_request and broad:
            # The shell's two specs, and not every spec importing it as well:
            # a quarter of them import `lib/types.ts` for its types alone.
            shell = True
            home = next((home for prefix, home in HOME_SECTIONS.items() if name.startswith(prefix)), None)
            pages |= {page for page in found if section(page) == home}
        else:
            specs |= seen & every
            pages |= found
    if not pull_request and any(ROOT_LAYOUT.match(page) for page in pages):
        return every
    if any(re.match(r"^frontend/src/.*\.(tsx|css)$", name) for name in sources):
        specs.add(DESIGN_SYSTEM)
    if shell:
        specs |= SHELL
    for page in pages:
        pattern = naming(page)
        specs |= {spec for spec in every if pattern.search((root / spec).read_text(errors="replace"))}
    return specs & every


def shards(root, specs, per_shard=TESTS_PER_SHARD):
    """The specs dealt into jobs of about `per_shard` tests each."""
    tests = {spec: len(re.findall(r"(?<![\w.])test\(", (root / spec).read_text(errors="replace"))) for spec in specs}
    jobs = [[] for _ in range(max(1, min(MOST_SHARDS, math.ceil(sum(tests.values()) / per_shard))))]
    # In name order, each spec to the job with the fewest tests so far. The
    # specs of one section sit together by name and cost alike — a database
    # test takes twice what a deployment test does — so this spreads a slow
    # section over the jobs, where Playwright's own --shard cuts the list into
    # consecutive runs and handed one job most of it.
    for spec in sorted(specs):
        lightest = min(jobs, key=lambda job: sum(tests[name] for name in job))
        lightest.append(spec)
    return jobs


def go_paths(packages):
    return " ".join("./" + package.removeprefix("backend/") for package in sorted(packages))


def plan(root, changed, pull_request=False):
    """The workflow's outputs for a change; `changed` is None when it is everything."""
    everything = changed is None or any(CI.match(name) for name in changed)
    changed = changed or []
    out = {"scripts": str(everything or any(name.startswith("scripts/") for name in changed)).lower()}

    packages = go_packages(root)
    touched, affected, read = (set(packages), set(packages), set()) if everything else go_affected(packages, changed)
    raced = {package for _, _, group in RACE for package in group}
    out["backend"] = str(bool(affected or read)).lower()
    out["go_packages"] = "./..." if affected == set(packages) else go_paths(affected)
    out["go_plain"] = go_paths(affected - raced | read)
    out["go_budgets"] = go_paths(package for package in affected & raced if has_budgets(root, package))
    # Each matrix is a list of names, which is what GitHub shows beside the
    # job, and what a name stands for is looked up in the object beside it.
    race = {
        name: {"procs": procs, "packages": go_paths(set(group) & affected)}
        for name, procs, group in RACE if set(group) & affected
    }
    out["race"], out["race_jobs"] = json.dumps(list(race)), json.dumps(race)

    live = {}
    if pull_request:
        # Those of the packages built on the deployment package that changed:
        # a change to the metrics `internal/deploy` reads is not one to its
        # Docker fixtures.
        reaches = go_reaches(packages)
        fixtures = [package for package in LIVE if reaches.get(package, set()) & touched & raced]
    else:
        fixtures = [package for package in LIVE if package in affected and touched & raced]
    if fixtures:
        tests = [test for package in fixtures for test in LIVE[package]]
        live["fixtures"] = {
            "packages": go_paths(fixtures), "run": "^(%s)$" % "|".join(tests), "skip": "",
            "tests": " ".join(tests), "docker": "backend/internal/api" in fixtures,
        }
    if "backend/internal/deploy" in (touched if pull_request else fixtures):
        named = [name for job in FRAMEWORK_JOBS if job for name in job]
        for number, job in enumerate(FRAMEWORK_JOBS, 1):
            live["frameworks-%d" % number] = {
                "packages": "./internal/deploy",
                "run": "^%s$" % FRAMEWORKS + ("/^(%s)$" % "|".join(job) if job else ""),
                "skip": "" if job else "^%s$/^(%s)$" % (FRAMEWORKS, "|".join(named)),
                "tests": FRAMEWORKS, "docker": False,
            }
    out["live"], out["live_jobs"] = json.dumps(list(live)), json.dumps(live)

    imports = frontend_imports(root)
    specs = browser_specs(root, imports, changed, pull_request)
    if everything:
        specs = {name for name in imports if name.startswith(SPECS) and name.endswith(".spec.ts")}
    imported = {module for modules in imports.values() for module in modules}
    out["frontend"] = str(everything or any(
        name.startswith("frontend/") or module_of(name) in imported for name in changed
    )).lower()
    if everything or any(LINT_ALL.match(name) for name in changed):
        out["lint"] = "."
    else:
        out["lint"] = " ".join(
            name.removeprefix("frontend/") for name in changed
            if re.match(r"^frontend/.*\.(ts|tsx|js|mjs)$", name) and (root / name).exists()
        )
    jobs = shards(root, specs, PULL_REQUEST_TESTS_PER_SHARD if pull_request else TESTS_PER_SHARD) if specs else []
    out["browser"] = json.dumps(list(range(1, len(jobs) + 1)))
    # Job n reads entry n, so the first entry is nobody's.
    out["browser_specs"] = json.dumps([""] + [" ".join(spec.removeprefix("frontend/") for spec in job) for job in jobs])
    return out


def main():
    root = Path(subprocess.check_output(["git", "rev-parse", "--show-toplevel"], text=True).strip())
    arguments = sys.argv[1:]
    pull_request = "--pull-request" in arguments
    arguments = [argument for argument in arguments if argument != "--pull-request"]
    changed = None
    if arguments:
        changed = subprocess.check_output(
            ["git", "diff", "--name-only", "--no-renames", arguments[0], "HEAD"], cwd=root, text=True
        ).splitlines()
        print("\n".join(changed), file=sys.stderr)
    for name, value in plan(root, changed, pull_request).items():
        print(f"{name}={value}")


if __name__ == "__main__":
    main()
