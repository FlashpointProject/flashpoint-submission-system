#!/usr/bin/env python3
"""Compare live MariaDB and disposable PostgreSQL LIKE semantics.

Supply Maria container, PostgreSQL container and disposable PostgreSQL database.
Requires migration0015 there. SQL reads expressions only, never source records.
"""
import argparse
import json
import subprocess

p = argparse.ArgumentParser()
p.add_argument('--maria-container', required=True)
p.add_argument('--postgres-container', required=True)
p.add_argument('--postgres-database', required=True)
p.add_argument('--weights', help='Optional weights.sql TSV: check all BMP equality keys')
a = p.parse_args()
cases = [
    ('', ''), ('', '%'), ('', '_'), ('abc','a%'), ('abc','A_C'),
    ('abc','ab'), ('abc','%b%'), ('abc','%d%'), ('abc','%%a%%c%%'),
    ('Résumé','%resume%'), ('Straße','%strasse%'), ('ß','ss'), ('ß','s'),
    ('ß','_'), ('ß','ẞ'), ('æ','ae'), ('æ','ǽ'), ('œ','oe'), ('ø','o'),
    ('é','e'), ('e\u0301','é'), ('e\u0301','e_'), ('é','e_'),
    ('\u0301','\u0300'), ('\u0301',''), ('ı','I'), ('İ','i'), ('Σ','ς'),
    ('Ａ','a'), ('ａ','A'), ('ＡＢＣ','abc'), ('😀','😃'), ('😀','_'),
    ('𐀀','😀'), ('a ','a'), ('a','a '), ('a ','a_'), (' a','a'),
    ('100%','100\\%'), ('100x','100\\%'), ('a_b','a\\_b'),
    ('a\\b','a\\\\b'), ('a\\','a\\'), ('[a]','[a]'), ('-^-','-^-'),
    ('a.b','a.b'), ('a$b','a$b'), ('a\nb','a_b'), ('a\nb','a%b'),
    ('a\n','a'), ('K','k'), ('Å','å'), ('ﬁ','fi'), ('\u0001','\u0002'),
    (None,'%'), ('x',None), (None,None)
]
# Verify every multi-member class against its canonical character in both
# directions. The generator mapping includes case/accent variants and old UCA
# ignorables. Include metacharacters through explicitly escaped patterns.
import re
from pathlib import Path
migration = (Path(__file__).resolve().parents[2] / 'postgres_migrations/0015_submission_text_semantics.up.sql').read_text()
keys = json.loads(re.search(r'\$keys\$(.*?)\$keys\$', migration).group(1))
def literal(s):
    return ''.join('\\' + c if c in '\\%_' else c for c in s)
for cp, canonical in keys.items():
    if cp != canonical:
        x,y=chr(int(cp)),chr(int(canonical))
        cases.extend([(x,literal(y)),(y,literal(x))])
def maria_text(s):
    return 'NULL' if s is None else "CONVERT(UNHEX('%s') USING utf8mb4) COLLATE utf8mb4_unicode_ci" % s.encode().hex()
def pg_text(s):
    return 'NULL' if s is None else "convert_from(decode('%s','hex'),'UTF8')" % s.encode().hex()
def run(container, command, sql):
    return subprocess.run(['docker','exec','-i',container,*command],input=sql,text=True,capture_output=True,check=True).stdout.splitlines()
msql='\n'.join('SELECT %s LIKE %s;' % (maria_text(x),maria_text(y)) for x,y in cases)
psql='\n'.join("SELECT COALESCE(submission_like(%s,%s)::integer::text,'NULL');" % (pg_text(x),pg_text(y)) for x,y in cases)
m=run(a.maria_container,['mariadb','-uroot','-psnapshot-root','--default-character-set=utf8mb4','-N'],msql)
g=run(a.postgres_container,['psql','-XAt','-U','snapshot_admin','-d',a.postgres_database,'-v','ON_ERROR_STOP=1'],psql)
assert len(m)==len(g)==len(cases),(len(m),len(g),len(cases))
failures=[{'value':v,'pattern':pattern,'maria':x,'postgres':y} for (v,pattern),x,y in zip(cases,m,g) if x!=y]
msql='\n'.join('SELECT %s = %s;' % (maria_text(x),maria_text(y)) for x,y in cases)
psql='\n'.join("SELECT COALESCE(submission_equal(%s,%s)::integer::text,'NULL');" % (pg_text(x),pg_text(y)) for x,y in cases)
m=run(a.maria_container,['mariadb','-uroot','-psnapshot-root','--default-character-set=utf8mb4','-N'],msql)
g=run(a.postgres_container,['psql','-XAt','-U','snapshot_admin','-d',a.postgres_database,'-v','ON_ERROR_STOP=1'],psql)
assert len(m)==len(g)==len(cases)
failures.extend({'equality_value':v,'pattern':pattern,'maria':x,'postgres':y} for (v,pattern),x,y in zip(cases,m,g) if x!=y)
key_cases=0
if a.weights:
    expected={}
    general_expected={}
    for line in Path(a.weights).read_text().splitlines():
        cp,weight,general_weight=line.split('\t')
        while weight.endswith('0209'):
            weight=weight[:-4]
        expected[int(cp)]=weight.lower()
        while general_weight.endswith('0020'):
            general_weight=general_weight[:-4]
        general_expected[int(cp)]=general_weight.lower()
    psql="SELECT i::text || ':' || encode(submission_text_key(chr(i)), 'hex') FROM generate_series(1,65535) i WHERE i NOT BETWEEN 55296 AND 57343 ORDER BY i"
    actual=run(a.postgres_container,['psql','-XAt','-U','snapshot_admin','-d',a.postgres_database,'-v','ON_ERROR_STOP=1'],psql)
    assert len(actual)==len(expected)
    for line in actual:
        cp,weight=line.split(':')
        key_cases+=1
        if expected[int(cp)]!=weight:
            failures.append({'codepoint':int(cp),'maria_key':expected[int(cp)],'postgres_key':weight})
    psql="SELECT i::text || ':' || encode(submission_general_text_key(chr(i)), 'hex') FROM generate_series(1,65535) i WHERE i NOT BETWEEN 55296 AND 57343 ORDER BY i"
    actual=run(a.postgres_container,['psql','-XAt','-U','snapshot_admin','-d',a.postgres_database,'-v','ON_ERROR_STOP=1'],psql)
    assert len(actual)==len(general_expected)
    for line in actual:
        cp,weight=line.split(':')
        key_cases+=1
        if general_expected[int(cp)]!=weight:
            failures.append({'general_codepoint':int(cp),'maria_key':general_expected[int(cp)],'postgres_key':weight})
print(json.dumps({'like_cases':len(cases),'equality_cases':len(cases),'key_cases':key_cases,'mismatches':len(failures),'failures':failures[:30]},ensure_ascii=True,indent=2))
raise SystemExit(bool(failures))
