"""That the stack's images are held to digests, and how pin-images.py moves them."""
import importlib.util
import pathlib
import unittest

ROOT = pathlib.Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("pin_images", ROOT / "scripts/pin-images.py")
pin_images = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pin_images)

OLD = "sha256:" + "a" * 64
NEW = "sha256:" + "b" * 64

DOCKERFILE = f"""# syntax=docker/dockerfile:1
FROM golang:1.26-bookworm AS build
RUN go build ./...
FROM --platform=$BUILDPLATFORM debian:bookworm-slim@{OLD} AS tools
FROM build AS tested
FROM scratch
COPY --from=tools /bin/true /true
"""

COMPOSE = f"""x-logging: &logging
  driver: json-file

services:
  backend:
    build:
      context: ./backend
    image: just-dashboard-backend:latest
  proxy:
    image: caddy:2-alpine@{OLD}
    restart: unless-stopped
  sidecar:
    image: busybox:1

volumes:
  data:
"""


def registry(name, tag):
    return NEW


class PinnedImagesTest(unittest.TestCase):
    def test_the_stack_names_every_image_by_digest(self):
        problems = [
            problem
            for name in pin_images.FILES
            for problem in pin_images.unpinned(name, (ROOT / name).read_text())
        ]
        self.assertEqual(problems, [])

    def test_the_stack_pulls_one_image_and_builds_from_five(self):
        # If a file gains an image or loses one, the script's own account of
        # the stack and this count are what to change, deliberately.
        named = {
            name: [match["reference"].split("@")[0] for _, match in pin_images.lines_naming_images(name, (ROOT / name).read_text())[1]]
            for name in pin_images.FILES
        }
        self.assertEqual([image.split(":")[0] for image in named["docker-compose.yml"]], ["caddy"])
        self.assertEqual([image.split(":")[0] for image in named["backend/Dockerfile"]], ["golang", "debian", "debian"])
        self.assertEqual([image.split(":")[0] for image in named["frontend/Dockerfile"]], ["oven/bun", "node", "node"])

    def test_a_dockerfile_is_checked_at_its_registry_images_only(self):
        self.assertEqual(
            pin_images.unpinned("backend/Dockerfile", DOCKERFILE),
            ["backend/Dockerfile:2: golang:1.26-bookworm has no digest; run scripts/pin-images.py"],
        )

    def test_a_compose_file_is_checked_at_what_it_pulls_and_not_at_what_it_builds(self):
        self.assertEqual(
            pin_images.unpinned("docker-compose.yml", COMPOSE),
            ["docker-compose.yml:13: busybox:1 has no digest; run scripts/pin-images.py"],
        )

    def test_a_digest_with_no_tag_says_nothing_about_what_to_follow(self):
        problems = pin_images.unpinned("backend/Dockerfile", f"FROM debian@{OLD}\n")
        self.assertEqual(len(problems), 1)
        self.assertIn("has no tag", problems[0])

    def test_pinning_writes_what_each_tag_names_and_leaves_the_rest_of_the_line(self):
        text, moved = pin_images.pin("backend/Dockerfile", DOCKERFILE, registry)
        self.assertIn(f"FROM golang:1.26-bookworm@{NEW} AS build\n", text)
        self.assertIn(f"FROM --platform=$BUILDPLATFORM debian:bookworm-slim@{NEW} AS tools\n", text)
        self.assertIn("FROM build AS tested\nFROM scratch\n", text)
        self.assertEqual(moved, [("golang:1.26-bookworm", None, NEW), ("debian:bookworm-slim", OLD, NEW)])
        self.assertEqual(pin_images.unpinned("backend/Dockerfile", text), [])

        text, moved = pin_images.pin("docker-compose.yml", COMPOSE, registry)
        self.assertIn(f"    image: caddy:2-alpine@{NEW}\n", text)
        self.assertIn(f"    image: busybox:1@{NEW}\n", text)
        self.assertIn("    image: just-dashboard-backend:latest\n", text)

    def test_pinning_again_at_the_same_digest_changes_nothing(self):
        text, _ = pin_images.pin("docker-compose.yml", COMPOSE, registry)
        again, moved = pin_images.pin("docker-compose.yml", text, registry)
        self.assertEqual(again, text)
        self.assertEqual(moved, [])

    def test_a_registry_that_gives_no_digest_stops_the_run(self):
        with self.assertRaises(SystemExit):
            pin_images.pin("docker-compose.yml", COMPOSE, lambda name, tag: "")


if __name__ == "__main__":
    unittest.main()
