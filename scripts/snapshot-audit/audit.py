#!/usr/bin/env python3
"""Read-only schema and aggregate audit of the isolated restored snapshot containers.

Never selects application text, credentials, notification payloads or user rows.
Outputs schema identifiers, counts and timestamp extrema only. Failures abort the audit.
"""
import argparse
import csv
import io
import json
import pathlib
import subprocess


def identifier(value, engine):
    quote = '`' if engine == 'mariadb' else '"'
    return quote + value.replace(quote, quote * 2) + quote


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--compose', required=True)
    p.add_argument('--engine', choices=['mariadb','postgres'], action='append', help='Audit only selected engine(s); default both')
    p.add_argument('--project', required=True)
    p.add_argument('--env-file', required=True, help='Explicit Compose environment file; never inherit the repository .env')
    p.add_argument('--output', required=True)
    p.add_argument('--mariadb-database', default='fpfss')
    p.add_argument('--postgres-database', default='fpfss')
    p.add_argument('--postgres-user', default='postgres')
    p.add_argument('--mariadb-service', default='mariadb')
    p.add_argument('--postgres-service', default='postgres')
    args = p.parse_args()
    output = pathlib.Path(args.output)
    output.mkdir(parents=True, exist_ok=True)
    (output / 'audit-complete.json').unlink(missing_ok=True)
    compose = ['docker', 'compose', '--env-file', args.env_file, '-f', args.compose, '-p', args.project, 'exec', '-T']
    def query(engine, sql):
        if engine == 'mariadb':
            command = compose + [args.mariadb_service, 'sh', '-c', 'export MYSQL_PWD="${MARIADB_ROOT_PASSWORD:-$MYSQL_ROOT_PASSWORD}"; exec mariadb -uroot --batch --raw --skip-column-names "$1"', 'audit', args.mariadb_database]
        else:
            command = compose + [args.postgres_service, 'psql', '-X', '-q', '-v', 'ON_ERROR_STOP=1', '-U', args.postgres_user, '-d', args.postgres_database, '-A', '-t', '-F', '\t']
        result = subprocess.run(command, input=sql+';\n', text=True, capture_output=True)
        if result.returncode:
            # Error diagnostics can contain row contents: retain only query identity.
            raise RuntimeError(f'{engine} audit query failed (exit {result.returncode}); no server error payload displayed')
        return [line.split('\t') for line in result.stdout.splitlines() if line]
    def save(name, header, rows):
        with (output / (name+'.tsv')).open('w', newline='') as f:
            writer = csv.writer(f, delimiter='\t'); writer.writerow(header); writer.writerows(rows)
    engines = args.engine or ['mariadb', 'postgres']
    for engine in engines:
        scope = 'table_schema=DATABASE()' if engine == 'mariadb' else "table_schema='public'"
        tables = [r[0] for r in query(engine, 'SELECT table_name FROM information_schema.tables WHERE '+scope+" AND table_type='BASE TABLE' ORDER BY table_name")]
        if engine == 'mariadb':
            server = query(engine, 'SELECT VERSION(), @@character_set_server, @@collation_server, @@time_zone, @@system_time_zone, @@sql_mode, @@group_concat_max_len')
            save(engine+'-server', ['version','charset','collation','timezone','system_timezone','sql_mode','group_concat_max_len'],server)
            save(engine+'-tables', ['table','engine','collation','data_bytes','index_bytes','auto_increment'], query(engine,'SELECT table_name,engine,table_collation,data_length,index_length,auto_increment FROM information_schema.tables WHERE '+scope+' ORDER BY table_name'))
            colquery = 'SELECT table_name,column_name,column_type,is_nullable,column_default,character_set_name,collation_name,datetime_precision,character_maximum_length,numeric_precision,numeric_scale FROM information_schema.columns WHERE '+scope+' ORDER BY table_name,ordinal_position'
            save(engine+'-indexes',['table','index','non_unique','sequence','column','prefix_length','type'],query(engine,'SELECT table_name,index_name,non_unique,seq_in_index,column_name,sub_part,index_type FROM information_schema.statistics WHERE '+scope+' ORDER BY table_name,index_name,seq_in_index'))
            fks = query(engine,'SELECT table_name,constraint_name,column_name,referenced_table_name,referenced_column_name FROM information_schema.key_column_usage WHERE '+scope+' AND referenced_table_name IS NOT NULL ORDER BY table_name,constraint_name,ordinal_position')
        else:
            save(engine+'-server',['version','encoding','collation','ctype','timezone'],query(engine,"SELECT version(),pg_encoding_to_char(encoding),datcollate,datctype,current_setting('TimeZone') FROM pg_database WHERE datname=current_database()"))
            save(engine+'-column-types',['table','column','type_schema','type_name'],query(engine,"SELECT table_name,column_name,udt_schema,udt_name FROM information_schema.columns WHERE table_schema='public' ORDER BY table_name,ordinal_position"))
            colquery = 'SELECT table_name,column_name,data_type,is_nullable,column_default,character_set_name,collation_name,datetime_precision,character_maximum_length,numeric_precision,numeric_scale FROM information_schema.columns WHERE '+scope+' ORDER BY table_name,ordinal_position'
            save(engine+'-indexes',['table','index','definition'],query(engine,"SELECT tablename,indexname,indexdef FROM pg_indexes WHERE schemaname='public' ORDER BY tablename,indexname"))
            fks=query(engine,"SELECT ct.relname,c.conname,ca.attname,pt.relname,pa.attname FROM pg_constraint c JOIN pg_class ct ON ct.oid=c.conrelid JOIN pg_namespace ns ON ns.oid=ct.relnamespace JOIN pg_class pt ON pt.oid=c.confrelid CROSS JOIN LATERAL unnest(c.conkey,c.confkey) WITH ORDINALITY keys(child,parent,ord) JOIN pg_attribute ca ON ca.attrelid=ct.oid AND ca.attnum=keys.child JOIN pg_attribute pa ON pa.attrelid=pt.oid AND pa.attnum=keys.parent WHERE c.contype='f' AND ns.nspname='public' ORDER BY ct.relname,c.conname,keys.ord")
        columns=query(engine,colquery)
        save(engine+'-columns',['table','column','type','nullable','default','charset','collation','datetime_precision','character_maximum_length','numeric_precision','numeric_scale'],columns)
        save(engine+'-foreign-keys',['table','constraint','column','parent_table','parent_column'],fks)
        counts=[]; nulls=[]; dates=[]
        for table in tables:
            qt=identifier(table,engine)
            counts += [[table]+r for r in query(engine,'SELECT COUNT(*) FROM '+qt)]
            cols=[r for r in columns if r[0]==table]
            # A single aggregate scan per table for null counts and timestamp properties.
            exprs=[]; labels=[]
            for _,col,typ,*_ in cols:
                qc=identifier(col,engine)
                exprs.append(f'COUNT(*)-COUNT({qc})'); labels.append(('null',col))
                if any(t in typ.lower() for t in ('timestamp','datetime','date')) or (typ.lower().startswith('bigint') and (col.endswith('_at') or col in ('date_added','date_modified'))):
                    cast = f'CAST({qc} AS CHAR)' if engine=='mariadb' else f'{qc}::text'
                    exprs.extend([f'MIN({qc})', f'MAX({qc})'])
                    labels.extend([('min',col),('max',col)])
                    condition=f"{cast} LIKE '0000-%'" if engine=='mariadb' else f"{cast} IN ('infinity','-infinity')"
                    exprs.append(f'SUM(CASE WHEN {condition} THEN 1 ELSE 0 END)'); labels.append(('invalid',col))
                    if any(t in typ.lower() for t in ('timestamp','datetime')):
                        fractional = f'MICROSECOND({qc})<>0' if engine=='mariadb' else f'EXTRACT(MICROSECONDS FROM {qc})::numeric % 1000000 <> 0'
                        exprs.append(f'SUM(CASE WHEN {fractional} THEN 1 ELSE 0 END)'); labels.append(('fractional_seconds',col))
            vals=query(engine,'SELECT '+','.join(exprs)+' FROM '+qt)[0]
            for (kind,col),value in zip(labels,vals):
                (nulls if kind=='null' else dates).append([table,col,kind,value])
        save(engine+'-row-counts',['table','rows'],counts)
        save(engine+'-nulls',['table','column','metric','value'],nulls)
        save(engine+'-timestamps',['table','column','metric','value'],dates)
        if 'schema_migrations' in tables:
            save(engine+'-migrations',['version','dirty'],query(engine,'SELECT version,dirty FROM schema_migrations'))
        grouped={}
        for table,name,col,parent,pcol in fks:
            grouped.setdefault((table,name,parent),[]).append((col,pcol))
        orphan=[]
        for (table,name,parent),pairs in grouped.items():
            present=' AND '.join('c.'+identifier(c,engine)+' IS NOT NULL' for c,_ in pairs)
            match=' AND '.join('c.'+identifier(c,engine)+'=p.'+identifier(pc,engine) for c,pc in pairs)
            sql=f'SELECT COUNT(*) FROM {identifier(table,engine)} c WHERE {present} AND NOT EXISTS (SELECT 1 FROM {identifier(parent,engine)} p WHERE {match})'
            orphan += [[table,name,parent]+r for r in query(engine,sql)]
        save(engine+'-orphans',['table','constraint','parent','orphan_rows'],orphan)
    if 'postgres' in engines:
        logical_sql = pathlib.Path(__file__).with_name('postgres-logical-references.sql').read_text()
        save('postgres-logical-references', ['relationship','missing_parent_rows'], query('postgres',logical_sql))
    checks = {
        'history_comment_counts': 'SELECT COUNT(*),COALESCE(MAX(n),0),COALESCE(SUM(CASE WHEN n>50 THEN 1 ELSE 0 END),0) FROM (SELECT COUNT(*) n FROM comment WHERE deleted_at IS NULL GROUP BY fk_submission_id) x',
        'cache_duplicates': 'SELECT COUNT(*),COALESCE(SUM(n-1),0) FROM (SELECT COUNT(*) n FROM submission_cache GROUP BY fk_submission_id HAVING COUNT(*)>1) x',
        'subscription_duplicates': 'SELECT COUNT(*),COALESCE(SUM(n-1),0) FROM (SELECT COUNT(*) n FROM submission_notification_subscription GROUP BY fk_user_id,fk_submission_id HAVING COUNT(*)>1) x',
        'preference_duplicates': 'SELECT COUNT(*),COALESCE(SUM(n-1),0) FROM (SELECT COUNT(*) n FROM notification_settings GROUP BY fk_user_id,fk_action_id HAVING COUNT(*)>1) x',
        'metadata_duplicates': 'SELECT COUNT(*),COALESCE(SUM(n-1),0) FROM (SELECT COUNT(*) n FROM curation_meta GROUP BY fk_submission_file_id HAVING COUNT(*)>1) x',
        'eligible_missing_cache': 'SELECT COUNT(*) FROM submission s WHERE deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM submission_cache c WHERE c.fk_submission_id=s.id)',
        'deleted_submission_cache': 'SELECT COUNT(*) FROM submission_cache c JOIN submission s ON s.id=c.fk_submission_id WHERE s.deleted_at IS NOT NULL',
        'eligible_no_live_files': 'SELECT COUNT(*) FROM submission s WHERE deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM submission_file f WHERE f.fk_submission_id=s.id AND f.deleted_at IS NULL)',
        'comment_timestamp_ties': 'SELECT COUNT(*),COALESCE(SUM(n-1),0) FROM (SELECT COUNT(*) n FROM comment WHERE deleted_at IS NULL GROUP BY fk_submission_id,created_at HAVING COUNT(*)>1) x',
        'file_timestamp_ties': 'SELECT COUNT(*),COALESCE(SUM(n-1),0) FROM (SELECT COUNT(*) n FROM submission_file WHERE deleted_at IS NULL GROUP BY fk_submission_id,created_at HAVING COUNT(*)>1) x',
        'cross_file_comment_ties': 'SELECT COUNT(*) FROM comment c WHERE c.deleted_at IS NULL AND EXISTS (SELECT 1 FROM submission_file f WHERE f.fk_submission_id=c.fk_submission_id AND f.deleted_at IS NULL AND f.created_at=c.created_at)',
        'comment_deleted_before_created': 'SELECT COUNT(*) FROM comment WHERE deleted_at<created_at',
        'file_deleted_before_created': 'SELECT COUNT(*) FROM submission_file WHERE deleted_at<created_at',
        'notification_sent_before_created': 'SELECT COUNT(*) FROM submission_notification WHERE sent_at<created_at',
        'auth_session_rows': 'SELECT COUNT(*) FROM session',
        'auth_oauth_client_rows': 'SELECT COUNT(*) FROM oauth_client',
    }
    if 'mariadb' in engines:
        save('mariadb-business-checks',['check','count','excess_rows_if_duplicate_check_or_max_history','histories_above_50'],[[name]+r for name,sql in checks.items() for r in query('mariadb',sql)])
        pointers = []
        for column, table in [('fk_oldest_file_id','submission_file'), ('fk_newest_file_id','submission_file'), ('fk_newest_comment_id','comment')]:
            sql = f'SELECT COALESCE(SUM(c.{column} IS NOT NULL AND p.id IS NULL),0),COALESCE(SUM(p.id IS NOT NULL AND NOT (p.fk_submission_id <=> c.fk_submission_id)),0),COALESCE(SUM(p.deleted_at IS NOT NULL),0) FROM submission_cache c LEFT JOIN {table} p ON p.id=c.{column}'
            pointers += [[column]+r for r in query('mariadb',sql)]
        save('mariadb-cache-pointers',['column','missing_target','wrong_parent','deleted_target'],pointers)
    (output/'audit-complete.json').write_text(json.dumps({'project':args.project,'read_only':True,'engines':engines,'outputs':'schema and aggregates only'},indent=2)+'\n')
    print('Audit completed: '+str(output))

if __name__=='__main__':
    main()
