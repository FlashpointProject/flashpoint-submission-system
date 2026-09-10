#!/usr/bin/env python3
"""Remove credential INSERTs from a standard mariadb-dump .sql.zst.

This is deliberately not an arbitrary SQL parser. Unsupported auth data sections
fail closed. Input values are never logged; originals and existing outputs are
never overwritten. Requires the zstd CLI. PostgreSQL dumps are not inputs.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile

TARGETS = {b"session", b"oauth_client"}
HEADER = re.compile(rb"-- (Table structure|Dumping data) for table `([^`]+)`\r?\n?$")
INSERT = re.compile(rb"INSERT INTO `([^`]+)` VALUES(?:[ \r\n]|$)")
CREATE = re.compile(rb"CREATE TABLE `([^`]+)` \(")


class InsertEnd:
    """Track a dump INSERT terminator without retaining or logging its values."""
    def __init__(self):
        self.quote = None
        self.escaped = False

    def feed(self, line):
        for index, char in enumerate(line):
            if self.quote is not None:
                if self.escaped:
                    self.escaped = False
                elif char == 92:  # MySQL backslash escapes
                    self.escaped = True
                elif char == self.quote:
                    self.quote = None
                # Doubled quotes close and immediately reopen, preserving the
                # same quoted state for delimiter detection.
            elif char in (39, 34, 96):
                self.quote = char
            elif char == 59:
                if line[index + 1:].strip():
                    raise ValueError("unexpected SQL after auth INSERT terminator")
                return True
        return False


def filter_dump(source, destination):
    section = None
    schemas = set()
    sections = set()
    removed = {name.decode(): 0 for name in sorted(TARGETS)}
    kept_hash = hashlib.sha256()
    kept_bytes = 0
    skipping = None
    for number, line in enumerate(source, 1):
        if skipping is not None:
            if skipping.feed(line):
                skipping = None
            continue
        header = HEADER.fullmatch(line)
        if header:
            section = header[2] if header[1] == b"Dumping data" else None
            if section in TARGETS:
                sections.add(section)
        create = CREATE.match(line)
        if create:
            schemas.add(create[1])
        insert = INSERT.match(line)
        if insert and insert[1] in TARGETS:
            if section != insert[1]:
                raise ValueError(f"auth INSERT outside its data section at line {number}")
            removed[insert[1].decode()] += 1
            skipping = InsertEnd()
            if skipping.feed(line):
                skipping = None
            continue
        if section in TARGETS:
            stripped = line.strip()
            # Standard mariadb-dump table-data sections contain only these
            # wrappers and INSERT statements. Do not copy an unknown payload.
            if not (not stripped or stripped.startswith(b"--")
                    or stripped == b"LOCK TABLES `" + section + b"` WRITE;"
                    or stripped == b"UNLOCK TABLES;"
                    or stripped in (
                        b"/*!40000 ALTER TABLE `" + section + b"` DISABLE KEYS */;",
                        b"/*!40000 ALTER TABLE `" + section + b"` ENABLE KEYS */;")):
                raise ValueError(f"unsupported auth data section at line {number}")
        destination.write(line)
        kept_hash.update(line)
        kept_bytes += len(line)
    if skipping is not None:
        raise ValueError("unterminated auth INSERT")
    if not TARGETS <= schemas or not TARGETS <= sections:
        raise ValueError("expected both auth schemas and standard data-section markers")
    return {"removed_insert_statements": removed, "retained_bytes": kept_bytes,
            "retained_sql_sha256": kept_hash.hexdigest()}


def decompress(path):
    return subprocess.Popen(["zstd", "-q", "-dc", "--", str(path)],
                            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)


def verify(path, expected):
    # Independently decompress output, compare every retained byte via its hash,
    # and re-run the structural filter: there must be zero remaining auth INSERTs.
    class DigestSink:
        def write(self, value):
            pass
    process = decompress(path)
    try:
        result = filter_dump(process.stdout, DigestSink())
    finally:
        process.stdout.close()
        status = process.wait()
    if status or any(result["removed_insert_statements"].values()):
        raise ValueError("sanitized stream validation failed")
    if any(result[k] != expected[k] for k in ("retained_bytes", "retained_sql_sha256")):
        raise ValueError("retained SQL differs after compression")


def sanitize(source, output):
    if not source.name.endswith(".sql.zst") or not output.name.endswith(".sql.zst"):
        raise ValueError("input and output must end in .sql.zst")
    if source.resolve() == output.resolve() or output.exists():
        raise ValueError("refusing to overwrite input or existing output")
    output.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=".sanitizing-", dir=output.parent)
    temporary = Path(temporary)
    try:
        with os.fdopen(fd, "wb") as compressed:
            encoder = subprocess.Popen(["zstd", "-q", "-T2", "-3", "-c"],
                                       stdin=subprocess.PIPE, stdout=compressed,
                                       stderr=subprocess.DEVNULL)
            decoder = decompress(source)
            try:
                result = filter_dump(decoder.stdout, encoder.stdin)
            finally:
                decoder.stdout.close()
                encoder.stdin.close()
                decode_status = decoder.wait()
                encode_status = encoder.wait()
            if decode_status or encode_status:
                raise ValueError("compression/decompression failed")
        verify(temporary, result)
        # Atomic, exclusive publication: even a concurrent creator is preserved.
        os.link(temporary, output)
        return {"output": str(output), "verified": True, **result}
    finally:
        temporary.unlink(missing_ok=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    try:
        print(json.dumps(sanitize(args.source, args.output), indent=2))
    except (ValueError, OSError) as error:
        # Exceptions contain only our metadata messages or OS paths, never SQL.
        parser.exit(1, f"Sanitization failed: {error}\n")


if __name__ == "__main__":
    main()
