#!/usr/bin/env python3
"""Read-only EXPLAIN ANALYZE for submission DAL candidates on an imported snapshot.

Run while other benchmarks are idle. Each query has a 90-second timeout; the
entire run stops on a timeout or error. Output is plans/timing, not row content.
"""
import argparse
import json
import pathlib
import re
import subprocess
import time

ROOT = pathlib.Path(__file__).resolve().parents[2]


def extract_query(source, method):
    start = source.index('func (d *postgresSubmissionDAL) ' + method + '(')
    end = source.find('\nfunc ', start + 1)
    body = source[start:end if end != -1 else None]
    match = re.search(r'\.Query(?:Row)?Context\(dbs\.Ctx\(\), `([\s\S]*?)`', body)
    if not match:
        raise ValueError('SQL not found for ' + method)
    return match.group(1).strip().rstrip(';')


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument('--target', required=True)
    ap.add_argument('--project', required=True)
    ap.add_argument('--user-id', type=int, required=True)
    ap.add_argument('--out', type=pathlib.Path, required=True)
    ap.add_argument('--repetitions', type=int, default=3)
    args = ap.parse_args()
    if not re.fullmatch(r'submission_import_[a-z0-9_]+', args.target):
        ap.error('target must be a disposable submission_import_* database')
    if not args.project.startswith('fpfss-snapshot-'):
        ap.error('project must be a snapshot project')
    if args.repetitions < 1:
        ap.error('repetitions must be positive')
    args.out.mkdir(parents=True, exist_ok=False)
    source = (ROOT / 'database/submission_postgres.go').read_text()
    queries = {name: extract_query(source, name) for name in (
        'GetTotalCommentsCount', 'GetTotalUserCount', 'GetTotalSubmissionFilesize',
        'GetCommentsByUserIDAndAction')}
    queries['GetCommentsByUserIDAndAction'] = queries['GetCommentsByUserIDAndAction'].replace('?', str(args.user_id), 1).replace('?', "'approve'", 1)
    count_source = (ROOT / 'database/submission_count.go').read_text()
    queries['CountCommentsByUserIDAndAction'] = extract_query(count_source, 'CountCommentsByUserIDAndAction').replace('?', str(args.user_id), 1).replace('?', "'approve'", 1)
    queries['PreviousCommentsByUserIDAndAction'] = f'''SELECT id, message, created_at FROM
      (SELECT id, message, (SELECT name FROM action WHERE id=comment.fk_action_id) AS action, created_at
       FROM comment WHERE fk_user_id={args.user_id} AND deleted_at IS NULL) AS t
      WHERE submission_text_key(action)=submission_text_key('approve') ORDER BY created_at DESC'''
    # Keep the potentially expensive whole-catalog query last.
    queries['GetAllSimilarityAttributes'] = extract_query(source, 'GetAllSimilarityAttributes')
    command = ['docker', 'compose', '--env-file', str(ROOT / 'scripts/snapshot-db/snapshot.env'),
               '-f', str(ROOT / 'scripts/snapshot-db/compose.yml'), '-p', args.project,
               'exec', '-T', 'postgres', 'psql', '-X', '-q', '-A', '-t', '-v', 'ON_ERROR_STOP=1',
               '-U', 'snapshot_admin', '-d', args.target]
    report = {'target': args.target, 'project': args.project, 'repetitions': args.repetitions,
              'timeout_seconds': 90, 'scope': 'EXPLAIN ANALYZE execution; excludes DAL row decoding/network transfer', 'results': []}
    for name, query in queries.items():
        (args.out / (name + '.sql')).write_text(query + ';\n')
        for repetition in range(args.repetitions + 1):
            sql = "BEGIN READ ONLY; SET LOCAL statement_timeout='90s'; EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) " + query + '; ROLLBACK;'
            started = time.monotonic()
            try:
                result = subprocess.run(command, input=sql, text=True, capture_output=True, timeout=100, check=True)
                plan = json.loads(result.stdout)
            except (subprocess.SubprocessError, ValueError) as error:
                report['error'] = {'query': name, 'repetition': repetition, 'details': str(error), 'stderr': getattr(error, 'stderr', None)}
                (args.out / 'report.json').write_text(json.dumps(report, indent=2))
                raise SystemExit('Benchmark stopped: ' + name + '; see report.json')
            (args.out / f'{name}-{repetition}.json').write_text(json.dumps(plan, indent=2))
            row = {'query': name, 'repetition': repetition, 'warm': repetition > 0,
                   'execution_ms': plan[0]['Execution Time'], 'planning_ms': plan[0]['Planning Time'],
                   'wall_seconds': time.monotonic() - started}
            report['results'].append(row)
            print(json.dumps(row), flush=True)
            (args.out / 'report.json').write_text(json.dumps(report, indent=2))
    report['complete'] = True
    (args.out / 'report.json').write_text(json.dumps(report, indent=2))


if __name__ == '__main__':
    main()
