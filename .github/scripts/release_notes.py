"""Adds a release's notes to its Sparkle appcasts.

NSGo writes appcasts without release notes, so Sparkle's update dialog shows
none. This reads the version's "chore(release)" commit message on stdin, takes its "- " lines
(the release's changes), and adds them to each appcast as the item's
<description>, which Sparkle shows in the dialog. Each appcast's .sha256 is
rewritten to match. The same lines are written as Markdown to --markdown, for
the GitHub release.

Only the installer is EdDSA-signed, not the appcast, so editing the appcast
leaves the update's signature valid.

    git log -1 --format=%b --fixed-strings --grep "chore(release): 0.8.1" v0.8.1 | python3 -I release_notes.py \
        --markdown notes.md dist/releases/appcast-*.xml
"""

import argparse
import hashlib
import html
import os
import sys


def changes(message):
    return [line[2:].strip() for line in message.splitlines() if line.startswith("- ") and line[2:].strip()]


def description(items):
    rows = "".join(f"<li>{html.escape(item)}</li>" for item in items)
    page = f"<ul>{rows}</ul>"
    # A CDATA section cannot hold "]]>"; escaping each item already rules it out.
    assert "]]>" not in page
    return f"      <description><![CDATA[{page}]]></description>\n"


def add_notes(path, notes):
    with open(path, encoding="utf-8") as f:
        feed = f.read()
    if "<description>" in feed:
        sys.exit(f"{path} already has release notes")
    marker = "</pubDate>\n"
    if feed.count(marker) != 1:
        sys.exit(f"{path}: expected one item with a pubDate")
    feed = feed.replace(marker, marker + notes, 1)
    with open(path, "w", encoding="utf-8") as f:
        f.write(feed)
    digest = hashlib.sha256(feed.encode("utf-8")).hexdigest()
    with open(path + ".sha256", "w", encoding="utf-8") as f:
        f.write(f"{digest}  {os.path.basename(path)}\n")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--markdown", required=True)
    parser.add_argument("appcasts", nargs="+")
    args = parser.parse_args()
    items = changes(sys.stdin.read())
    if not items:
        sys.exit('The release commit lists no changes; add "- " lines to its message.')
    for path in args.appcasts:
        add_notes(path, description(items))
    with open(args.markdown, "w", encoding="utf-8") as f:
        f.write("".join(f"- {item}\n" for item in items))


if __name__ == "__main__":
    main()
