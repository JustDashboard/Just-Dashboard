#!/usr/bin/env python3
"""The names a Markdown page writes in backticks that the code does not have.

    scripts/doc-names.py [--since <ref>] <doc.md> [<doc.md> ...]   (from anywhere in the repository)

Each backticked token that reads as a code identifier, a file or a route is looked for in the
repository's source: everything git sees (tracked and untracked, ignored files aside) outside
docs/ and other Markdown.

  - An identifier (`useDeferredSearch`, `ROW_DIM`, `JD_PUBLIC_URL`, `Panel.Header`) is found when
    each of its dotted parts is a word somewhere in the source. A plain word is not checked: only
    a token with a hump, an underscore or a dot reads as code.
  - A file (`components/page.tsx`, `deploy/run-page.tsx`) is found when a file's path ends with it.
  - A route (`/proxy/sites/<name>`, `/databases/[id]/access`) is found when some route in the
    source, or a page under frontend/src/app, holds it once each `<name>`, `[name]`, `{name}`,
    `:name` and `${...}` is read as one placeholder.

A name a page records as gone, "`ShareBar` (removed …)", and the names in a catalogue row whose
status is new or retired, are not looked for.

Every miss is printed with its line. Without --since, any miss fails. With --since, only a miss on
a line added or changed since <ref> fails, so a page's older misses are shown without blocking an
edit to it; scripts/test-changed.sh runs it that way on each changed page under
docs/internal/frontend. A miss is a candidate rather than a verdict (prose examples and other
programs' names miss too), which is why it says where, and the fix is the name or the backticks.
"""
import argparse
import os
import pathlib
import re
import subprocess
import sys

TICK = re.compile(r"`([^`\n]+)`")
IDENT = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*(?:\(\))?$")
FILE = re.compile(r"[A-Za-z0-9_\[\]@.-]+\.(?:go|tsx|ts|mjs|js|sh|py|sql|css|json|yml|yaml)$")
VERB = re.compile(r"^(GET|POST|PUT|PATCH|DELETE|WS)\s+")
# A route as source writes it: in quotes or backticks, from its first slash.
SOURCE_ROUTE = re.compile(r"""["'`](/[A-Za-z0-9_{}\[\]:.*<>-]+(?:/[A-Za-z0-9_{}\[\]:.*<>-]*)+)""")
TEMPLATE = re.compile(r"\$\{[^}]*\}")
PLACEHOLDER = re.compile(r"<[^>/]*>|\[[^\]]*\]|\{[^}]*\}|:[A-Za-z_][A-Za-z0-9_]*|\*")
REMOVED = re.compile(r"\s*\((removed|retired)\b")
EXCUSED_ROW = re.compile(r"^\|.*\|\s*(new|retired)\b[^|]*\|\s*$")
HOST_PATH = re.compile(r"^/(etc|usr|var|tmp|opt|home|root|proc|sys|dev|run|srv|bin|sbin|lib)/")
SOURCE_PATHS = [":!docs", ":!*.md"]


def git(root, *args, check=True):
    result = subprocess.run(["git", "-C", str(root), *args], capture_output=True, text=True)
    if check and result.returncode not in (0, 1):
        sys.exit(f"git {' '.join(args[:2])}: {result.stderr.strip()}")
    return result


def normalise(route):
    route = route.split("?")[0].split("#")[0].rstrip("/")
    route = TEMPLATE.sub("{}", route)
    return PLACEHOLDER.sub("{}", route)


def candidates(text):
    """(line, kind, token, words) for each backticked token worth looking for."""
    fenced = False
    for number, line in enumerate(text.splitlines(), 1):
        if line.lstrip().startswith("```"):
            fenced = not fenced
            continue
        if fenced or EXCUSED_ROW.match(line):
            continue
        for match in TICK.finditer(line):
            token = match.group(1).strip()
            if REMOVED.match(line, match.end()):
                continue
            route = VERB.sub("", token)
            if route.startswith("/") and route.count("/") >= 2 and " " not in route:
                # A path on the host (`/etc/ssh/sshd_config`) is not the product's to have.
                if not HOST_PATH.match(route):
                    yield number, "route", token, ()
                continue
            if FILE.search(token) and not re.search(r"[\s*<]", token) and token[0] not in "-.":
                yield number, "file", token, ()
                continue
            if IDENT.match(token):
                core = token.removesuffix("()")
                parts = core.split(".")
                looks_code = ("_" in core or "." in core or re.search(r"[a-z][A-Z]", core)
                              or core.startswith("JD_"))
                if looks_code and len(parts[-1]) >= 4:
                    yield number, "ident", token, tuple(p for p in parts if len(p) >= 3)


def present_words(root, files, words):
    """Which of these words the source has, matched as whole words.

    GNU grep, given the words as one list, answers for a thousand of them in about two seconds;
    `git grep` with as many `-e` took over a minute.
    """
    if not words:
        return set()
    patterns = "\n".join(sorted(words)) + "\n"
    sources = [name for name in files if not name.endswith(".md")]
    present = set()
    for start in range(0, len(sources), 2000):
        result = subprocess.run(["grep", "-I", "-s", "-howF", "-f", "-", "--", *sources[start:start + 2000]],
                                cwd=root, input=patterns, capture_output=True, text=True)
        present.update(result.stdout.split())
    return present


def source_files(root):
    listed = git(root, "ls-files", "--cached", "--others", "--exclude-standard").stdout
    return [name for name in listed.splitlines() if name and not name.startswith("docs/")]


def source_routes(root, files):
    routes = set()
    found = git(root, "grep", "--untracked", "-I", "-hE", r"""["'`]/[A-Za-z]""", "--", ".",
                *SOURCE_PATHS).stdout
    for line in found.splitlines():
        for route in SOURCE_ROUTE.findall(TEMPLATE.sub("{}", line)):
            routes.add(normalise(route))
    # The pages themselves: frontend/src/app/(group)/databases/[id]/page.tsx serves /databases/{}.
    for name in files:
        match = re.match(r"^frontend/src/app/(.*)/(page|route)\.tsx?$", name)
        if match:
            routes.add(normalise("/" + re.sub(r"\([^)]*\)/?", "", match.group(1))))
    return routes


def route_found(route, routes):
    wanted = normalise(VERB.sub("", route))
    if wanted.endswith("/{}") and route.rstrip("/").endswith("*"):
        prefix = wanted[: -len("{}")]
        return any(prefix in r for r in routes)
    return any(wanted in r for r in routes)


def changed_lines(root, ref, doc):
    """The line numbers of doc added or changed since ref; every line when git does not track it."""
    if git(root, "ls-files", "--error-unmatch", "--", doc, check=False).returncode != 0:
        return None
    diff = git(root, "diff", "--unified=0", "--no-color", ref, "--", doc).stdout
    lines = set()
    for start, count in re.findall(r"^@@ -\S+ \+(\d+)(?:,(\d+))? @@", diff, re.M):
        first, length = int(start), 1 if count == "" else int(count)
        lines.update(range(first, first + length))
    return lines


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    parser.add_argument("--since", help="fail only on misses in lines changed since this ref")
    parser.add_argument("docs", nargs="+")
    args = parser.parse_args(argv)
    root = pathlib.Path(git(pathlib.Path.cwd(), "rev-parse", "--show-toplevel").stdout.strip())

    found = []
    for doc in args.docs:
        path = pathlib.Path(doc).resolve()
        relative = os.path.relpath(path, root)
        for number, kind, token, words in candidates(path.read_text(encoding="utf-8")):
            found.append((relative, number, kind, token, words))

    files = source_files(root)
    words = present_words(root, files, {word for *_, parts in found for word in parts})
    routes = source_routes(root, files) if any(kind == "route" for _, _, kind, _, _ in found) else set()

    failed = False
    since = {}
    for doc, number, kind, token, parts in found:
        if kind == "ident":
            missing = [p for p in parts if p not in words]
            if not missing:
                continue
            detail = f"\t(missing: {', '.join(missing)})"
        elif kind == "file":
            if any(name == token or name.endswith("/" + token) for name in files):
                continue
            detail = ""
        else:
            if route_found(token, routes):
                continue
            detail = ""
        new = True
        if args.since:
            if doc not in since:
                since[doc] = changed_lines(root, args.since, doc)
            new = since[doc] is None or number in since[doc]
        failed = failed or new
        mark = "" if not args.since else ("\tnew" if new else "\tolder")
        print(f"{doc}:{number}\t{kind}\t{token}{detail}{mark}")
    if failed:
        print("doc-names: a page names what the code does not have (above). Fix the name, or mark "
              "a gone one \"(removed)\".", file=sys.stderr)
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
