#!/usr/bin/env python3
"""Stream pg_dumpall into a fresh sandbox, dropping role passwords, never logging SQL."""
import re
import sys
copy = False
passwords = drops = 0
for line in sys.stdin.buffer:
    if copy:
        sys.stdout.buffer.write(line)
        if line.rstrip(b'\r\n') == b'\\.':
            copy = False
        continue
    if re.match(rb'COPY .* FROM stdin;', line):
        copy = True
    if line.startswith(b'ALTER ROLE ') and b' PASSWORD ' in line:
        line, n = re.subn(rb" PASSWORD '(?:[^']|'')*'", b' PASSWORD NULL', line)
        if n != 1:
            raise SystemExit('Unsupported role password syntax; refusing restore stream')
        passwords += n
    # --clean dumpall assumes roles/databases already exist; allow a fresh cluster.
    line, n = re.subn(rb'^(DROP (?:DATABASE|ROLE)) (?!IF EXISTS )', rb'\1 IF EXISTS ', line)
    drops += n
    sys.stdout.buffer.write(line)
print(f'role_password_clauses_removed={passwords}; conditional_drop_statements={drops}', file=sys.stderr)
