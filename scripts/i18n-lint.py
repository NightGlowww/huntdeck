#!/usr/bin/env python3
"""Reports interface copy that is still English.

Every page's words go through {{T "..."}}/{{TF "..."}} (templates) or the
scripts' catalog (static/js); this finds the text nodes and tooltips the
templates still write in English, so a sweep can list what is left:

  make i18n
  python3 scripts/i18n-lint.py --count

Exit status is 0 always (it is a report, not a gate): the count is the
metric worth watching.
"""
import glob
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
TEMPLATES = os.path.join(ROOT, "internal", "web", "templates")

# Names, codes and brands a page may show as they are.
NAMES = {
    "hackerone", "bugcrowd", "intigriti", "yeswehack", "vulbox", "issuehunt", "gobugfree", "inspectiv", "huntdeck", "huntscore",
    "api", "url", "urls", "ip", "cidr", "json", "csv", "http", "https", "get",
    "post", "cve", "bbp", "vdp", "id", "ids", "caido", "burp", "zap", "discord",
    "telegram", "webhook", "burpsuite", "scope", "insomnia", "bruno", "postman",
    "chrome", "github", "signal", "cvss", "tls", "ssl", "vnc", "env", "sqlite",
    "huntmail",  # the product's own name, like HuntDeck's
    "claude", "desktop", "cursor", "code", "streamable", "json-rpc", "stdio",
}
# Two ASCII words in a row that aren't only names: prose the page still
# writes in English.
# An HTML entity is punctuation, not a word: &middot; between two links is
# not English copy. Entities are dropped before counting.
ENTITY = re.compile(r"&[a-zA-Z]+;|&#\d+;")
# A word is letters, hyphens or apostrophes of any length: a three-letter
# floor let "Set it up" and "Overview" through. A token carrying . / _ or a
# digit is an identifier (a host, a path, a file name), not prose.
WORDS = re.compile(r"[A-Za-z][A-Za-z'’-]*")
IDENT = re.compile(r"[\w.-]*[./_\d][\w.-]*")
# Anything handed to T/TF is translated already.
TRANSLATED = re.compile(r'\{\{-?\s*T(?:F)?\s+"(?:[^"\\]|\\.)*"(?:[^}]*)\}\}')
# An attribute's value may hold template actions, whose own quotes do not
# end it: title="{{TF "%s" .Name}}".
ATTRS = re.compile(r'(?:placeholder|aria-label|title)="((?:[^"{}]|\{\{.*?\}\})*)"', re.S)
TEXT = re.compile(r">([^<>{}]+)<")
ACTION = re.compile(r"\{\{.*?\}\}", re.S)
# A label: the whole text of an anchor, button, tab or table head. One word is
# copy too - "Overview" and "Save" rendered in English under Chinese headings.
LABEL = re.compile(r"<(a|button|th|summary|label)\b[^>]*>([A-Za-z][A-Za-z\'’ -]{1,24})</\1>")


def main() -> int:
    count_only = "--count" in sys.argv
    total = 0
    for path in sorted(glob.glob(os.path.join(TEMPLATES, "**", "*.html"), recursive=True)):
        if path.endswith(".zh.html") or os.path.basename(path) == "methodology.html":
            continue  # a whole page per language, and the English source of one
        body = TRANSLATED.sub(" ", open(path, encoding="utf-8").read())
        found = []
        for kind, pattern in (("text", TEXT), ("attr", ATTRS)):
            for m in pattern.finditer(body):
                # Template actions are data or already translated: judge
                # only the words written around them.
                text = ACTION.sub(" ", m.group(1)).strip()
                # URLs, paths, code and file names are not copy.
                if any(c in text for c in ("://", "&#10;", "sha256=", "=")) or text.startswith(("/", "*.")):
                    continue
                if "secret.key" in text or text.endswith((".com", ".net", ".key", ".json")):
                    continue
                words = [w for w in WORDS.findall(IDENT.sub(" ", ENTITY.sub(" ", text))) if w.lower() not in NAMES]
                if len(words) >= 2:
                    found.append((kind, text))
        for m in LABEL.finditer(body):
            text = m.group(2).strip()
            if [w for w in WORDS.findall(text) if w.lower() not in NAMES]:
                found.append(("label", text))

        for kind, text in found:
            total += 1
            if not count_only:
                print(f"{os.path.basename(path)}:{kind}: {text[:110]}")
    print(f"english left: {total}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
