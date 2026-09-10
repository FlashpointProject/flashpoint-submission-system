#!/usr/bin/env python3
"""Read-only post-parity check of the original PostgreSQL data fingerprints.

Run AFTER timed workloads: this scans every original table including the index.
"""
import argparse
import json
from pathlib import Path
import re
import subprocess
import time


def build_sql(expected):
    pieces = []
    for name in sorted(expected):
        if not re.fullmatch(r'[a-z][a-z0-9_]*', name):
            raise ValueError('Unsupported table identifier in import manifest')
        pieces.append(f"SELECT '{name}' AS name, COUNT(*)::text || ':' || "
                      "COALESCE(SUM(hashtextextended(row_to_json(t)::text,0)::numeric),0)::text AS fingerprint "
                      f'FROM public."{name}" t')
    if not pieces:
        raise ValueError('Manifest has no original PostgreSQL fingerprints')
    return ("BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;\n"
            "SET LOCAL statement_timeout='5min';\n"
            "SELECT json_object_agg(name,fingerprint) FROM (\n"
            + '\nUNION ALL\n'.join(pieces) + ") fingerprints;\nCOMMIT;\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('manifest', type=Path)
    parser.add_argument('--container', required=True)
    parser.add_argument('--out', required=True, type=Path)
    args = parser.parse_args()
    report = json.loads(args.manifest.read_text())
    if not report['Complete'] or report['ExistingBefore'] != report['ExistingAfter']:
        raise ValueError('A successful reconciled import manifest is required')
    database = report['TargetDatabase']
    if not re.fullmatch(r'submission_import_[a-z0-9_]+', database):
        raise ValueError('Refusing non-disposable database')
    if not re.fullmatch(r'fpfss-snapshot-run-[a-z0-9-]+-postgres-1', args.container):
        raise ValueError('Refusing non-snapshot PostgreSQL container')
    expected = report['ExistingAfter']
    started = time.monotonic()
    result = subprocess.run(['docker', 'exec', '-i', args.container, 'psql', '-X', '-qAt',
                             '-v', 'ON_ERROR_STOP=1', '-U', 'snapshot_admin', '-d', database],
                            input=build_sql(expected), text=True, capture_output=True, timeout=330)
    if result.returncode:
        raise RuntimeError(result.stderr.strip() or 'PostgreSQL fingerprint check failed')
    actual = json.loads(result.stdout)
    differences = [name for name in sorted(expected.keys() | actual.keys())
                   if expected.get(name) != actual.get(name)]
    output = {'complete': not differences, 'target_database': database,
              'tables': len(expected), 'elapsed_seconds': round(time.monotonic() - started, 3),
              'changed_tables': differences, 'expected': expected, 'actual': actual}
    args.out.write_text(json.dumps(output, indent=2) + '\n')
    if differences:
        raise RuntimeError('Original PostgreSQL table fingerprints changed: ' + ', '.join(differences))
    print(f'All {len(expected)} original PostgreSQL fingerprints remain unchanged; report: {args.out}')


if __name__ == '__main__':
    main()
