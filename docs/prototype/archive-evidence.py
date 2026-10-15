#!/usr/bin/env python3
"""Keep selected audit artifacts locally with hashes; never overwrite an archive."""
import argparse
import hashlib
import json
from pathlib import Path
import zipfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--output", type=Path, required=True)
parser.add_argument("files", type=Path, nargs="+")
args = parser.parse_args()
entries = []
names = {"manifest.json"}
for source in args.files:
    if not source.is_file():
        parser.error(f"Missing artifact: {source}")
    if source.name in names:
        parser.error(f"Duplicate artifact filename: {source.name}")
    names.add(source.name)
    data = source.read_bytes()
    entries.append((source, data))

manifest = {
    "note": "Preserved evidence, including failures; archival does not grant acceptance.",
    "files": [
        {"file": source.name, "source": str(source.resolve()), "bytes": len(data),
         "sha256": hashlib.sha256(data).hexdigest()}
        for source, data in entries
    ],
}
args.output.parent.mkdir(parents=True, exist_ok=True)
with zipfile.ZipFile(args.output, "x", compression=zipfile.ZIP_DEFLATED) as archive:
    for source, data in entries:
        archive.writestr(source.name, data)
    archive.writestr("manifest.json", json.dumps(manifest, ensure_ascii=False, indent=2))
print(f"Archived {len(entries)} artifacts: {args.output}")
