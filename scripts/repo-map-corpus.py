#!/usr/bin/env python3
"""Recreate the pinned, public WP2.2 evaluation corpus without executing it."""

import argparse
import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import tarfile
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    manifest = json.loads(args.manifest.read_text())
    args.output.mkdir(parents=True, exist_ok=False)
    count = 0
    for source in manifest["sources"]:
        url = source["archive_url"]
        if not url.startswith("https://codeload.github.com/"):
            raise ValueError("expected a pinned public GitHub archive")
        with urllib.request.urlopen(url, timeout=60) as response:
            archive_bytes = response.read((64 << 20) + 1)
        if len(archive_bytes) > 64 << 20:
            raise ValueError("archive exceeds 64 MiB")
        if hashlib.sha256(archive_bytes).hexdigest() != source["archive_sha256"]:
            raise ValueError("archive checksum changed: " + source["name"])
        expected = {}
        for item in source["files"]:
            local = PurePosixPath(item["path"])
            if local.is_absolute() or ".." in local.parts or local.parts[0] != source["name"]:
                raise ValueError("unsafe corpus path")
            expected[str(PurePosixPath(*local.parts[1:]))] = item
        found = set()
        with tarfile.open(fileobj=io.BytesIO(archive_bytes), mode="r|gz") as archive:
            for member in archive:
                relative = "/".join(member.name.split("/")[1:])
                if relative not in expected:
                    continue
                item = expected[relative]
                if not member.isfile() or member.size != item["bytes"] or member.size > 8 << 20:
                    raise ValueError("unexpected archive member")
                stream = archive.extractfile(member)
                if stream is None:
                    raise ValueError("missing archive member")
                content = stream.read()
                if hashlib.sha256(content).hexdigest() != item["sha256"]:
                    raise ValueError("source checksum changed: " + item["path"])
                target = args.output / item["path"]
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(content)
                found.add(relative)
                count += 1
        if found != set(expected):
            raise ValueError("archive lacks required sources: " + source["name"])
    (args.output / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    print(f"Verified {count} files, {manifest['lines']} physical lines; no source was executed.")


if __name__ == "__main__":
    main()
