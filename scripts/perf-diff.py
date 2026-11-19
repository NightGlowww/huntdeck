#!/usr/bin/env python3
"""Compares two performance snapshots: what each route cost then and now.

    scripts/perf-diff.py perf/20261118T233839.md perf/20261118T233907.md

Reads the Pages table scripts/perf-snapshot.sh writes (three samples a route;
the last one is the warm number a visitor feels) and prints one table with the
difference, largest change first. A route only in one file is marked as such.
"""
import re
import sys

SAMPLE = re.compile(r"^(/[^\s]*)\s+(200/[0-9.]+)\s+(200/[0-9.]+)\s+(200/[0-9.]+)\s*(?:OVER.*)?$")


def pages(path):
    out = {}
    for line in open(path, encoding="utf-8"):
        m = SAMPLE.match(line.rstrip("\n"))
        if not m:
            continue
        route, *_rest = m.groups()
        last = m.group(4)
        try:
            out[route] = float(last.split("/")[1]) * 1000
        except (IndexError, ValueError):
            continue
    return out


def main(argv):
    if len(argv) != 3:
        print("usage: perf-diff.py BEFORE.md AFTER.md", file=sys.stderr)
        return 2
    before, after = pages(argv[1]), pages(argv[2])
    if not before or not after:
        print("one of the files has no page samples", file=sys.stderr)
        return 1
    routes = sorted(set(before) | set(after))
    rows = []
    for route in routes:
        b, a = before.get(route), after.get(route)
        if b is None:
            rows.append((route, None, a))
        elif a is None:
            rows.append((route, b, None))
        else:
            rows.append((route, b, a))
    rows.sort(key=lambda r: -abs((r[2] or 0) - (r[1] or 0)))
    print("| route | before | after | change |")
    print("|---|---|---|---|")
    for route, b, a in rows:
        bs = "—" if b is None else f"{b:.0f} ms"
        as_ = "—" if a is None else f"{a:.0f} ms"
        if b is None or a is None:
            change = "added" if b is None else "gone"
        else:
            d = a - b
            if abs(d) < 1:
                change = "same"
            else:
                change = f"{d:+.0f} ms"
        print(f"| `{route}` | {bs} | {as_} | {change} |")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
