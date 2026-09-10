#!/usr/bin/env python3
"""Compare two completed importer result directories, without reading row data."""
import hashlib
import json
from pathlib import Path
import sys


def compare(left_dir, right_dir):
    left = json.loads((left_dir / 'manifest.json').read_text())
    right = json.loads((right_dir / 'manifest.json').read_text())
    assert left['Complete'] and right['Complete'], 'Both imports must have committed successfully'
    assert left['SourceVersion'] == right['SourceVersion'], 'Source migration changed'
    assert left['SourceDatabase'] == right['SourceDatabase'], 'Source database changed'
    assert left['Tables'].keys() == right['Tables'].keys(), 'Table mapping changed'
    for name, before in left['Tables'].items():
        after = right['Tables'][name]
        for key in ('SourceRows', 'ImportedRows', 'DiscardedRows', 'SourceSHA256', 'TargetSHA256', 'SequenceNext'):
            assert before[key] == after[key], f'{name}.{key} changed'
        assert after['SourceSHA256'] == after['TargetSHA256'], f'{name} reconciliation failed'
    for report in (left, right):
        assert report['ExistingBefore'] == report['ExistingAfter'], 'Original PostgreSQL rows changed during import'
    # A later target schema may add lookup tables for submission collation. Every
    # original catalog table seen in the earlier rehearsal must remain identical.
    for name, fingerprint in left['ExistingBefore'].items():
        assert right['ExistingBefore'].get(name) == fingerprint, f'Original PostgreSQL {name} differs between targets'
    for name in left['Checks'].keys() & right['Checks'].keys():
        assert left['Checks'][name] == right['Checks'][name], f'Audit {name} changed'
    maps = [hashlib.sha256((d / 'discarded-subscriptions.jsonl').read_bytes()).hexdigest() for d in (left_dir, right_dir)]
    assert maps[0] == maps[1], 'Discarded-to-retained subscription mapping changed'
    return {'complete': True, 'tables': len(right['Tables']),
            'source_rows': sum(t['SourceRows'] for t in right['Tables'].values()),
            'imported_rows': sum(t['ImportedRows'] for t in right['Tables'].values()),
            'discarded_rows': sum(t['DiscardedRows'] for t in right['Tables'].values()),
            'mapping_sha256': maps[0], 'target_databases': [left['TargetDatabase'], right['TargetDatabase']]}


if __name__ == '__main__':
    if len(sys.argv) != 3:
        sys.exit('Usage: compare.py FIRST/results SECOND/results')
    print(json.dumps(compare(Path(sys.argv[1]), Path(sys.argv[2])), indent=2))
