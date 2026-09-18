#!/usr/bin/env python3
"""Fail CI when a required live fixture was skipped, absent or did not pass."""

import json
import sys


def main():
    if len(sys.argv) < 3:
        raise SystemExit("usage: check-live-test-results.py RESULTS.jsonl TEST [TEST ...]")
    required = set(sys.argv[2:])
    passed = set()
    failures = []
    with open(sys.argv[1], encoding="utf-8") as events:
        for line in events:
            event = json.loads(line)
            action, test = event.get("Action"), event.get("Test", "")
            if action in ("fail", "skip"):
                failures.append(f"{action}: {test or event.get('Package', 'package')}")
            if action == "pass" and test in required:
                passed.add(test)
    failures.extend(f"missing pass: {test}" for test in sorted(required - passed))
    if failures:
        raise SystemExit("\n".join(failures))
    print(f"Verified {len(required)} required live fixtures without skips.")


if __name__ == "__main__":
    main()
