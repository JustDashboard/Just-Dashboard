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
    A public asset is followed from the modules that name it. The UI docs
    (`docs/internal/frontend/`) run the unit tests, one of which holds them
    to the code.
  - Browser jobs: the specs are dealt into jobs by what each runs, read from
    its source: a test written once in a loop counts once for each item of
    the loop's list, and a test that an unset environment variable switches
    off (the screenshot tests) counts for nothing. A spec whose source does
    not say, or whose tests cost more than most, says so itself in a comment
    on a line of its own:

        // ci-plan: 190 tests
        // ci-plan: each weighs 2.5
        // ci-plan: 190 tests, each weighs 2.5

    A pull request deals its specs into smaller jobs, which finish sooner.
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

# What the frontend's unit tests read outside frontend/ and no module
# imports: the UI docs, which `src/docs-catalogue.test.js` holds to the
# components and the tokens. A change to them alone starts the frontend job,
# which runs the unit tests and has no file to lint.
FRONTEND_READS = ("docs/internal/frontend/",)

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

# A spec is weighed by reading it, since the job that plans has no Bun and no
# Playwright to list its tests with. What is not code in one: a comment, a
# string, a template, a pattern.
QUIET = re.compile(r"""//[^\n]*|/\*.*?\*/|"(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\\\n])*'|`|/""", re.S)
PATTERN = re.compile(r"/(?:\\.|\[(?:\\.|[^\]\\\n])*\]|[^/\\\n\[])+/[a-z]*")
PATTERN_AFTER = ("", "(", ",", "=", ":", "[", "!", "&", "|", "?", "{", "}", ";", "+", "-", "*", "%", "<", ">", "~", "^")
PATTERN_AFTER_WORD = re.compile(r"\b(return|typeof|case|of|in)\s*$")
TEMPLATE = re.compile(r"\\.|`|\$\{", re.S)
TEMPLATE_CODE = re.compile(r"""//[^\n]*|/\*.*?\*/|"(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\\\n])*'|[`{}]""", re.S)
NAME = re.compile(r"[A-Za-z_$][\w$]*")
# What a spec says of itself on a line of its own: how many tests it runs,
# where a loop's list is not in its source, and how many ordinary tests one of
# its own costs.
SAID = re.compile(r"^[ \t]*//[ \t]*ci-plan:[ \t]*(?:(\d+) tests?)?[, \t]*(?:each weighs (\d+(?:\.\d+)?))?[ \t]*$", re.M)

# A browser job is sized in tests. One is 1.2 seconds of a hosted runner's two
# workers (seven jobs of the full run of 15 Nov 2026, by the timestamps of
# their logs: 3,862 tests in 4,473 seconds, a twentieth of them switched
# off), and a job's setup, the build included, is two and a half minutes
# before the first.
TESTS_PER_SHARD = 200
# Smaller in a pull request, which somebody waits on: two hundred tests are
# four minutes after the setup, and a hundred are two.
PULL_REQUEST_TESTS_PER_SHARD = 100
# A full run is every spec, so this is what sizes its jobs, and they have
# twenty minutes each. Runners are not one speed and jobs not one mix: the job
# cancelled at that limit on 17 Nov 2026 (run 44029967642, `browser (7)`) had
# run 597 tests in 17m51s, 1.8 seconds each. At that rate 400 tests are twelve
# minutes, which leaves five and a half to spare, and the 4,700 the suite
# weighs after the 0.9.0 UI pass are twelve such jobs; in eight they were 580
# tests and seventeen minutes. A full run is then twenty-two jobs where
# GitHub runs twenty at once, so two of them wait for the first to finish,
# which costs less than a job that is cancelled. Raise this when a full run's
# jobs pass 400 tests again.
MOST_SHARDS = 12


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


def blanked(text):
    """A spec's source with its comments, strings and patterns blanked, everything else where it was."""
    out, at, last = [], 0, ""
    while True:
        found = QUIET.search(text, at)
        if not found:
            break
        start, end, token = found.start(), found.end(), found.group()
        out.append(text[at:start])
        last = text[at:start].rstrip()[-1:] or last
        if token == "`":
            end = template_end(text, end)
        elif token == "/":
            # A pattern where a value may begin, and a division anywhere else.
            begins = last in PATTERN_AFTER or PATTERN_AFTER_WORD.search(text, max(0, start - 12), start)
            pattern = PATTERN.match(text, start) if begins else None
            if not pattern:
                out.append(token)
                at, last = end, token
                continue
            end = pattern.end()
        hidden = text[start:end]
        hidden = re.sub(r"[^\n]", " ", hidden) if "\n" in hidden else " " * len(hidden)
        if token.startswith(("//", "/*")):
            out.append(hidden)
        else:
            out.append('"' + hidden[1:-1] + '"')
            last = '"'
        at = end
    out.append(text[at:])
    return "".join(out)


def template_end(text, at):
    """Where the template string that opened just before `at` closes, past whatever its `${}` hold."""
    while True:
        found = TEMPLATE.search(text, at)
        if not found:
            return len(text)
        at = found.end()
        if found.group() == "`":
            return at
        depth = 1 if found.group() == "${" else 0
        while depth:
            inner = TEMPLATE_CODE.search(text, at)
            if not inner:
                return len(text)
            at = inner.end()
            if inner.group() == "`":
                at = template_end(text, at)
            elif inner.group() in ("{", "}"):
                depth += 1 if inner.group() == "{" else -1


def outermost(code, pattern):
    """The matches of a pattern that no bracket of a piece of blanked source encloses."""
    depth, found = 0, []
    for match in re.finditer(r"[(\[{]|[)\]}]|" + pattern, code):
        token = match.group()
        if token in ("(", "[", "{"):
            depth += 1
        elif token in (")", "]", "}"):
            depth -= 1
        elif depth == 0:
            found.append(match)
    return found


def pieces(code):
    """What the commas no bracket encloses separate."""
    cuts = [match.start() for match in outermost(code, ",")]
    return [code[start:end].strip() for start, end in zip([0] + [cut + 1 for cut in cuts], cuts + [len(code)])]


def last_bracket(code):
    """Where the bracket that closes a piece of blanked source at its last character opened."""
    stack = []
    for match in re.finditer(r"[(\[{]|[)\]}]", code):
        if match.group() in ("(", "[", "{"):
            stack.append(match.start())
        elif stack:
            opened = stack.pop()
            if match.start() == len(code) - 1:
                return opened
    return None


def statement(code, at):
    """The rest of the statement at `at`, as Prettier breaks one: after an operator, or before a `.` or one."""
    depth = 0
    for match in re.finditer(r"[(\[{]|[)\]}]|[;\n]", code[at:]):
        token, so_far = match.group(), code[at:at + match.start()]
        if token in ("(", "[", "{"):
            depth += 1
        elif token in (")", "]", "}"):
            depth -= 1
            if depth < 0:
                return so_far
        elif depth == 0 and (
            token == ";"
            or so_far.strip()
            and not re.search(r"[=(\[{,:?|&+*/<>-]\s*$", so_far)
            and not re.match(r"\s*[.?:|&+*/<>-]", code[at + match.end():])
        ):
            return so_far
    return code[at:]


def length(code, text, seen=frozenset(), elsewhere=None):
    """How many items an expression of a blanked source holds, and None where the source does not say.

    `elsewhere` gives, for a name the source imports, the blanked module it
    comes from and what it is called there.
    """
    text = text.strip()
    cast = outermost(text, r"\s(?:as|satisfies)\s")
    if cast:
        text = text[:cast[0].start()].rstrip()
    if not text:
        return None
    opened = last_bracket(text)
    if opened == 0 and text[0] == "(":
        return length(code, text[1:-1], seen, elsewhere)
    if opened == 0:
        # A literal: one for each item, and a `...spread` for what it holds.
        return sum(
            (length(code, item[3:], seen, elsewhere) or 1) if item.startswith("...") else 1
            for item in pieces(text[1:-1]) if item
        )
    if NAME.fullmatch(text):
        if text in seen:
            return None
        declared = re.search(r"\b(?:const|let|var)\s+" + re.escape(text) + r"\b", code)
        if not declared:
            other = elsewhere(text) if elsewhere else None
            return length(*other) if other else None
        rest = statement(code, declared.end())
        assigned = outermost(rest, r"(?<![=!<>])=(?![=>])")
        return length(code, rest[assigned[0].end():], seen | {text}, elsewhere) if assigned else None
    if opened is None or text[opened] != "(":
        return None
    called, arguments = text[:opened].rstrip(), pieces(text[opened + 1:-1])
    argument = arguments[0]
    if re.fullmatch(r"Object\.(keys|values|entries)", called):
        return length(code, argument, seen, elsewhere)
    method = re.search(r"\.(\w+)$", called)
    held = length(code, called[:method.start()], seen, elsewhere) if method else None
    # What returns an item for every item of what it is called on.
    if held is None or method.group(1) in ("map", "entries"):
        return held
    # A run of them: `.slice(2, 8)`.
    if method.group(1) == "slice" and all(bound.isdigit() for bound in arguments if bound):
        bounds = [int(bound) for bound in arguments if bound]
        return max(0, min([held] + bounds[1:2]) - min([held] + bounds[:1])) if bounds else held
    # And a list for every item: `.flatMap((page) => [390, 1440].map(…))`.
    arrow = outermost(argument, r"=>")
    if method.group(1) == "flatMap" and arrow and not argument[arrow[0].end():].lstrip().startswith("{"):
        each = length(code, argument[arrow[0].end():], seen, elsewhere)
        return None if each is None else held * each
    return None


def spec_tests(text, read=None):
    """How many tests a spec declares, which is what `playwright test --list` counts, and how many CI runs.

    A test written once in a loop is as many as the loop's list is long: a
    literal, a constant of the spec, or one it imports from a module `read`
    returns the source of.
    """
    code = blanked(text)
    closes, stack = {}, []
    for match in re.finditer(r"[(\[{]|[)\]}]", code):
        if match.group() in ("(", "[", "{"):
            stack.append(match.start())
        elif stack:
            closes[stack.pop()] = match.start()

    def elsewhere(name):
        found = re.search(
            r'\bimport\s*\{[^}]*?\b(?:(\w+)\s+as\s+)?%s\s*[,}][^;]*?from\s*"(\.[^"]+)"' % re.escape(name), text
        )
        source = read(found.group(2)) if found and read else None
        return (blanked(source), found.group(1) or name) if source else None

    # A loop's body, with the header that says how often it runs.
    loops = []
    for loop in re.finditer(r"\bfor\s*\(", code):
        opened = loop.end() - 1
        closed = closes.get(opened)
        body = closed is not None and re.search(r"\S", code[closed + 1:closed + 200])
        if not body:
            continue
        start = closed + 1 + body.start()
        end = closes.get(start) if body.group() == "{" else closed + 1 + len(statement(code, closed + 1))
        if end:
            loops.append((closed, end, code[opened + 1:closed]))

    # A block that a missing environment variable switches off, as the
    # screenshot tests are: `test.skip(!process.env.JD_SHOTS, …)`.
    off = []
    for skip in re.finditer(r"(?<![\w.$])test\.skip\s*\(\s*!", code):
        around = [(opened, closed) for opened, closed in closes.items() if opened < skip.start() < closed]
        off.append(max((block for block in around if code[block[0]] == "{"), default=(0, len(code))))

    declared = run = 0
    for test in re.finditer(r"(?<![\w.$])test(\.(?:skip|fixme|fail))?\s*\(", code):
        at, opened = test.start(), test.end() - 1
        # `test.skip("a title", …)` is a test; `test.skip(isMobile, "why")` is said inside one.
        if test.group(1) and not re.match(r'\s*"', code[opened + 1:opened + 200]):
            continue
        times = 1
        for start, end, header in loops:
            of = start <= at < end and outermost(header, r"\bof\b")
            if of:
                # Once, where the source does not say how long the list is.
                times *= length(code, header[of[0].end():], elsewhere=elsewhere) or 1
        declared += times
        closed = closes.get(opened, opened)
        # Switched off around the test, or inside it.
        if test.group(1) in (None, ".fail") and not any(
            start < at < end or opened < start and end < closed for start, end in off
        ):
            run += times
    return declared, run


def spec_weight(text, read=None):
    """What a spec costs a job, in ordinary tests: those CI runs of it, or what its own comment says."""
    _, tests = spec_tests(text, read)
    each = 1.0
    for said in SAID.finditer(text):
        tests = int(said.group(1)) if said.group(1) else tests
        each = float(said.group(2)) if said.group(2) else each
    return max(1, round(tests * each))


def shards(root, specs, per_shard=TESTS_PER_SHARD):
    """The specs dealt into jobs of about `per_shard` tests each, none of them empty."""
    weights = {}
    for spec in specs:
        def read(module, directory=os.path.dirname(spec)):
            path = root / os.path.normpath(os.path.join(directory, module + ".ts"))
            return path.read_text(errors="replace") if path.is_file() else None

        text = (root / spec).read_text(errors="replace")
        weights[spec] = spec_weight(text, read)
        if "ci-plan:" in text and not any(said.group(1) or said.group(2) for said in SAID.finditer(text)):
            print(f"{spec}: its `ci-plan:` comment says neither `N tests` nor `each weighs N`", file=sys.stderr)
    jobs = [[] for _ in range(max(1, min(MOST_SHARDS, math.ceil(sum(weights.values()) / per_shard))))]
    loads, held = [0] * len(jobs), [{} for _ in jobs]
    # The heaviest spec first, each to the job that is lightest when what it
    # already holds of the spec's own section is counted twice. The heaviest
    # first is what evens the jobs out, the small specs filling what the large
    # ones left. Counting the section again is what spreads a slow one: the
    # specs of a section cost alike and not what another's do — a database
    # test took 1.5 seconds on 15 Nov 2026 where the others took 1.1 — and
    # dealt by weight alone one job of twelve held 205 database tests and
    # another 14. Playwright's own --shard cuts the list into consecutive
    # runs instead, which handed one job most of a section.
    for spec in sorted(specs, key=lambda spec: (-weights[spec], spec)):
        section = re.split(r"[-.]", spec.rsplit("/", 1)[-1])[0]
        job = min(range(len(jobs)), key=lambda job: loads[job] + held[job].get(section, 0))
        jobs[job].append(spec)
        loads[job] += weights[spec]
        held[job][section] = held[job].get(section, 0) + weights[spec]
    # A spec heavier than a job is a job to itself, and the jobs it was
    # counted as are left with nothing. They are not started: a job with no
    # spec to name would run every one.
    return [sorted(job) for job in jobs if job]


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
        name.startswith(("frontend/",) + FRONTEND_READS) or module_of(name) in imported for name in changed
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
