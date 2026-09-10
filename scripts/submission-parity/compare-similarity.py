#!/usr/bin/env python3
"""Compare ordered similarity tuples without retaining their contents.

Both SQL statements run read-only on an isolated snapshot. PostgreSQL COPY
serializes row JSON deterministically; SHA-256 includes row order and NULLs.
Run with other database jobs idle. Each statement has a 90-second timeout.
"""
import argparse
import hashlib
import json
import pathlib
import re
import runpy
import subprocess
import time

ROOT = pathlib.Path(__file__).resolve().parents[2]


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument('--target', required=True)
    ap.add_argument('--project', required=True)
    ap.add_argument('--previous-sql', required=True, type=pathlib.Path)
    ap.add_argument('--out', required=True, type=pathlib.Path)
    args = ap.parse_args()
    if not re.fullmatch(r'submission_import_[a-z0-9_]+', args.target):
        ap.error('target must be a disposable submission_import_* database')
    if not args.project.startswith('fpfss-snapshot-'):
        ap.error('project must be a snapshot project')
    extract = runpy.run_path(str(ROOT / 'scripts/submission-parity/benchmark-other.py'))['extract_query']
    current = extract((ROOT / 'database/submission_postgres.go').read_text(), 'GetAllSimilarityAttributes')
    previous = args.previous_sql.read_text().strip().rstrip(';')
    args.out.mkdir(parents=True, exist_ok=False)
    command = ['docker', 'compose', '--env-file', str(ROOT / 'scripts/snapshot-db/snapshot.env'),
               '-f', str(ROOT / 'scripts/snapshot-db/compose.yml'), '-p', args.project,
               'exec', '-T', 'postgres', 'psql', '-X', '-q', '-A', '-t', '-v', 'ON_ERROR_STOP=1',
               '-U', 'snapshot_admin', '-d', args.target]
    report = {'target': args.target, 'project': args.project, 'results': {}, 'equal': False}
    for name, query in [('previous', previous), ('current', current)]:
        (args.out / (name + '.sql')).write_text(query + ';\n')
        # COPY's text escaping preserves embedded tabs, newlines and backslashes;
        # row_to_json preserves NULL versus empty strings and original spelling.
        sql = "BEGIN READ ONLY; SET LOCAL statement_timeout='90s'; COPY (SELECT row_to_json(similarity_tuple) FROM (" + query + ") AS similarity_tuple) TO STDOUT; ROLLBACK;"
        started = time.monotonic()
        try:
            result = subprocess.run(command, input=sql.encode(), capture_output=True, timeout=100, check=True)
        except subprocess.SubprocessError as error:
            report['error'] = {'query': name, 'details': str(error), 'stderr': str(getattr(error, 'stderr', None))}
            (args.out / 'report.json').write_text(json.dumps(report, indent=2))
            raise SystemExit('Comparison stopped; see report.json')
        report['results'][name] = {'rows': result.stdout.count(b'\n'),
                                   'ordered_sha256': hashlib.sha256(result.stdout).hexdigest(),
                                   'wall_seconds': time.monotonic() - started}
        del result
        (args.out / 'report.json').write_text(json.dumps(report, indent=2))
    a, b = report['results']['previous'], report['results']['current']
    report['equal'] = a['rows'] == b['rows'] and a['ordered_sha256'] == b['ordered_sha256']
    (args.out / 'report.json').write_text(json.dumps(report, indent=2))
    print(json.dumps(report, indent=2))
    if not report['equal']:
        raise SystemExit('Ordered similarity tuples differ')


if __name__ == '__main__':
    main()
