#!/usr/bin/env python3
"""Compare saved search SQL under transactional index/statistics experiments.

DDL and EXPLAIN ANALYZE run only on disposable imported databases and roll back.
Use a JSON configuration with phases [{name, ddl}], and workloads [{name, path}].
Runs first plus three warm executions in both custom and generic prepared modes.
No result rows are retained. Run without concurrent database jobs.
"""
import argparse,json,pathlib,re,subprocess
ROOT=pathlib.Path(__file__).resolve().parents[2]
def literal(v):
    if v is None:return 'NULL'
    if type(v) is bool:return 'TRUE' if v else 'FALSE'
    if type(v) in (int,float):return str(v)
    if isinstance(v,str):return "'"+v.replace("'","''")+"'"
    raise ValueError('unsupported parameter type')
def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--config',type=pathlib.Path,required=True);p.add_argument('--out',type=pathlib.Path,required=True)
    p.add_argument('--target',required=True);p.add_argument('--project',required=True)
    a=p.parse_args()
    if not re.fullmatch(r'submission_import_[a-z0-9_]+',a.target) or not re.fullmatch(r'fpfss-snapshot-[a-z0-9-]+',a.project):p.error('disposable snapshot target required')
    cfg=json.loads(a.config.read_text());a.out.mkdir(parents=True,exist_ok=False)
    (a.out/'config.json').write_text(json.dumps(cfg,indent=2))
    cmd=['docker','compose','--env-file',str(ROOT/'scripts/snapshot-db/snapshot.env'),'-f',str(ROOT/'scripts/snapshot-db/compose.yml'),'-p',a.project,'exec','-T','postgres','psql','-X','-qAt','-v','ON_ERROR_STOP=1','-U','snapshot_admin','-d',a.target]
    report={'complete':False,'target':a.target,'project':a.project,'results':[]}
    for phase in cfg['phases']:
        statements=["BEGIN; SET LOCAL statement_timeout='90s'; SET LOCAL standard_conforming_strings=on;",phase.get('ddl','')]
        labels=[]
        for mode in cfg.get('modes', ['force_custom_plan','force_generic_plan']):
            if mode not in ('force_custom_plan', 'force_generic_plan'):raise ValueError('unsupported plan mode')
            statements.append('SET LOCAL plan_cache_mode='+mode+';')
            for w in cfg['workloads']:
                q=json.loads((ROOT/w['path']).read_text());args=','.join(literal(v) for v in q['Args'])
                statements.append('PREPARE candidate AS '+q['SQL']+';')
                for i in range(4):
                    statements.append('EXPLAIN (ANALYZE,BUFFERS,WAL,FORMAT JSON) EXECUTE candidate('+args+');')
                    labels.append((w['name'],mode,i))
                statements.append('DEALLOCATE candidate;')
        statements.append('ROLLBACK;')
        sql='\n'.join(statements);(a.out/(phase['name']+'.sql')).write_text(sql)
        try:
            result=subprocess.run(cmd,input=sql,text=True,capture_output=True,check=True,timeout=600)
            raw=result.stdout; decoder=json.JSONDecoder();plans=[]
            while raw.strip():
                raw=raw.lstrip();v,n=decoder.raw_decode(raw);plans.append(v);raw=raw[n:]
            assert len(plans)==len(labels)
            for label,plan in zip(labels,plans):
                name,mode,i=label
                row={'phase':phase['name'],'name':name,'mode':mode,'repetition':i,'execution_ms':plan[0]['Execution Time'],'planning_ms':plan[0]['Planning Time']}
                report['results'].append(row)
                (a.out/f"{phase['name']}-{name}-{mode}-{i}.json").write_text(json.dumps(plan,indent=2))
                print(json.dumps(row),flush=True)
        except Exception as e:
            report['error']=str(e);report['stderr']=getattr(e,'stderr',None)
            (a.out/'report.json').write_text(json.dumps(report,indent=2));raise
        (a.out/'report.json').write_text(json.dumps(report,indent=2))
    report['complete']=True;(a.out/'report.json').write_text(json.dumps(report,indent=2))
if __name__=='__main__':main()
