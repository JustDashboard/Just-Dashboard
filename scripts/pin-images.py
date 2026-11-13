#!/usr/bin/env python3
"""Hold the images the stack is made of to exact contents, and move them on purpose.

    scripts/pin-images.py            ask Docker Hub what each tag names today and write it down
    scripts/pin-images.py --check    fail unless every image below carries a digest (no network)

The stack is built from five images and runs a sixth as it is:

    backend/Dockerfile     golang, debian (twice)
    frontend/Dockerfile    oven/bun, node (twice)
    docker-compose.yml     caddy, the only process that listens on the network

Each was named by a tag, which is a name the registry may point somewhere else
at any time. So what an update built depended on what Docker Hub served that
day, two installs of one version were not the same bytes, and Caddy, which
Compose pulls only when the tag is missing, stayed for ever at whatever the
first install fetched: a server installed in the spring ran spring's Caddy
under every later version of the dashboard.

A reference here is `name:tag@sha256:...`. Docker runs the digest and ignores
the tag, so the tag is kept for the reader and for this script: it says which
line of releases to follow (`caddy:2-alpine`, `debian:bookworm-slim`), and a run
without --check writes the digest that tag names today. That makes moving a
base image a change in a commit, which a release can be tested with, reverted
from and bisected over, and an update pulls the new Caddy because the release
names it. The installer and the updater need no --pull for this: a digest that
is not on the machine is fetched, and one that is there is never asked about.

Run it when a release is cut (scripts/release.sh names the step), then build
both images before the release is merged: nothing else builds them first.
`python3 scripts/test_pin_images.py` checks the rewriting and that the three
files are pinned.
"""

import json
import re
import sys
import urllib.request
from pathlib import Path

FILES = ["backend/Dockerfile", "frontend/Dockerfile", "docker-compose.yml"]

DIGEST = r"sha256:[0-9a-f]{64}"
# name[:tag][@digest], as far as these files use them: no registry port.
REFERENCE = re.compile(rf"^(?P<name>[a-z0-9][a-z0-9._/-]*)(?::(?P<tag>[A-Za-z0-9_][A-Za-z0-9_.-]*))?(?:@(?P<digest>{DIGEST}))?$")
FROM = re.compile(r"^(?P<head>FROM\s+(?:--\S+\s+)*)(?P<reference>\S+)(?P<tail>(?:\s+AS\s+(?P<stage>\S+))?\s*)$", re.I)
IMAGE = re.compile(r"^(?P<head>\s+image:\s+)(?P<reference>\S+)(?P<tail>\s*)$")
BUILD = re.compile(r"^\s+build:")
SERVICE = re.compile(r"^  [A-Za-z0-9_.-]+:\s*$")

ACCEPT = ", ".join(
    [
        "application/vnd.oci.image.index.v1+json",
        "application/vnd.docker.distribution.manifest.list.v2+json",
        "application/vnd.oci.image.manifest.v1+json",
        "application/vnd.docker.distribution.manifest.v2+json",
    ]
)


def lines_naming_images(name, text):
    """The lines of a file that name an image pulled from a registry, as (line number, match)."""
    lines = text.split("\n")
    found = []
    if name.endswith("Dockerfile"):
        stages = set()
        for number, line in enumerate(lines):
            match = FROM.match(line)
            if not match:
                continue
            # `FROM build` is an earlier stage and `FROM scratch` is nothing.
            if match["reference"] not in stages and match["reference"] != "scratch":
                found.append((number, match))
            if match["stage"]:
                stages.add(match["stage"])
        return lines, found
    # A compose service that is built here names the image it builds, which no
    # registry has. A service is its lines down to the next one.
    start = 0
    for number, line in enumerate(lines + ["  end:"]):
        if not SERVICE.match(line):
            continue
        block = lines[start:number]
        if not any(BUILD.match(inner) for inner in block):
            for offset, inner in enumerate(block):
                match = IMAGE.match(inner)
                if match:
                    found.append((start + offset, match))
        start = number
    return lines, found


def unpinned(name, text):
    """What is wrong with the file's image lines, as messages; empty when all are pinned."""
    problems = []
    lines, found = lines_naming_images(name, text)
    for number, match in found:
        reference = REFERENCE.match(match["reference"])
        where = f"{name}:{number + 1}: {match['reference']}"
        if not reference:
            problems.append(f"{where} is not a reference this can read")
        elif not reference["digest"]:
            problems.append(f"{where} has no digest; run scripts/pin-images.py")
        elif not reference["tag"]:
            problems.append(f"{where} has no tag, so nothing says which releases it follows")
    return problems


def pin(name, text, resolve):
    """The file with every image line carrying the digest resolve(name, tag) gives, and what moved."""
    lines, found = lines_naming_images(name, text)
    moved = []
    for number, match in found:
        reference = REFERENCE.match(match["reference"])
        if not reference or not reference["tag"]:
            raise SystemExit(f"{name}:{number + 1}: {match['reference']} names no tag to follow")
        digest = resolve(reference["name"], reference["tag"])
        if not re.fullmatch(DIGEST, digest or ""):
            raise SystemExit(f"{name}:{number + 1}: the registry gave no digest for {reference['name']}:{reference['tag']}")
        if digest != reference["digest"]:
            moved.append((f"{reference['name']}:{reference['tag']}", reference["digest"], digest))
        lines[number] = f"{match['head']}{reference['name']}:{reference['tag']}@{digest}{match['tail']}"
    return "\n".join(lines), moved


def docker_hub(name, tag, cache={}):
    """The digest Docker Hub serves for name:tag: of the index for an image built for several machines."""
    if "." in name.split("/")[0]:
        raise SystemExit(f"{name} is not on Docker Hub, which is the only registry this asks")
    repository = name if "/" in name else "library/" + name
    if (repository, tag) not in cache:
        token = json.load(
            urllib.request.urlopen(
                f"https://auth.docker.io/token?service=registry.docker.io&scope=repository:{repository}:pull", timeout=30
            )
        )["token"]
        request = urllib.request.Request(
            f"https://registry-1.docker.io/v2/{repository}/manifests/{tag}",
            method="HEAD",
            headers={"Authorization": f"Bearer {token}", "Accept": ACCEPT},
        )
        with urllib.request.urlopen(request, timeout=30) as response:
            cache[(repository, tag)] = response.headers.get("Docker-Content-Digest", "")
    return cache[(repository, tag)]


def main(arguments):
    root = Path(__file__).resolve().parent.parent
    if arguments == ["--check"]:
        problems = [problem for name in FILES for problem in unpinned(name, (root / name).read_text())]
        for problem in problems:
            print(problem, file=sys.stderr)
        return 1 if problems else 0
    if arguments:
        print(__doc__.strip().split("\n\n")[1], file=sys.stderr)
        return 2
    # Every file is resolved before any is written, so a registry that stops
    # answering halfway leaves the tree as it was.
    written, changes = [], 0
    for name in FILES:
        text, moved = pin(name, (root / name).read_text(), docker_hub)
        written.append((name, text))
        changes += len(moved)
        for image, before, after in moved:
            print(f"{name}: {image}\n    was {before or 'not pinned'}\n    now {after}")
    for name, text in written:
        (root / name).write_text(text)
    if changes:
        print("Build both images before the release is merged, since nothing else builds them first:")
        print("    docker compose build")
    else:
        print("Every image already names what its tag serves today.")
    return 0

if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
