"""Validate the actual release image on a fresh backup COPY before deployment.

Requires both container health and authenticated ledger API success, then checks
created tables, original schema, every heartbeat field, and SQLite integrity.
It never mounts a live production volume. No credentials enter command lines.
"""
from __future__ import annotations

import argparse
import base64
import gzip
import hashlib
import json
from pathlib import Path
import os
import shutil
import sqlite3
import subprocess
import tempfile
import time
import urllib.request

from .collect import open_request

SCHEMA_QUERY = "select type,name,sql from sqlite_master where sql is not null and name not like 'token_usage_%' and tbl_name not like 'token_usage_%' order by type,name"
TABLES = {'token_usage_events','token_usage_session_refs','token_usage_quarantined_sessions',
          'token_usage_accounting_modes','token_usage_conflicts'}


def check_running(state: dict) -> bool:
    if state.get('Status') != 'running':
        raise RuntimeError('release container failed to stay running; deployment blocked')
    return (state.get('Health') or {}).get('Status') == 'healthy'


def docker(*args: str) -> str:
    return subprocess.check_output(['docker',*args],text=True).strip()


def heartbeat_digest(db: sqlite3.Connection) -> str:
    return hashlib.sha256(json.dumps(db.execute('select * from heartbeats order by id').fetchall()).encode()).hexdigest()


def validate(image: str, backup: Path, port: int) -> dict:
    with tempfile.TemporaryDirectory(prefix='wakapi-image-validation-') as tmp:
        root=Path(tmp);database=root/'wakapi.db';name='wakapi-release-check-'+str(os.getpid())
        with gzip.open(backup,'rb') as source,database.open('wb') as target:
            shutil.copyfileobj(source,target)
        database.chmod(0o600)
        with sqlite3.connect(database) as db:
            schema=db.execute(SCHEMA_QUERY).fetchall();heartbeats=heartbeat_digest(db)
            row=db.execute('select api_key from users limit 1').fetchone()
            if not row or not row[0]:
                raise RuntimeError('backup lacks an existing authenticated test user')
            key=row[0]
            db.execute("update users set wakatime_api_key=''")  # COPY only
        ownership_prepared=False
        try:
            docker('run','--rm','-u','0:0','-v',str(root)+':/copy','python:3.13-slim',
                'python','-c',"import os;os.chown('/copy',65532,65532);os.chown('/copy/wakapi.db',65532,65532)")
            ownership_prepared=True
            docker('run','-d','--name',name,'-p',f'127.0.0.1:{port}:3000','-v',str(root)+':/data',
                '-e','WAKAPI_IMPORT_ENABLED=false','-e','WAKAPI_ALLOW_SIGNUP=false',
                '-e','WAKAPI_PASSWORD_SALT=isolated-test-only',image)
            healthy=False
            for _ in range(60):
                state=json.loads(docker('inspect','-f','{{json .State}}',name))
                if check_running(state):
                    healthy=True;break
                time.sleep(1)
            if not healthy:
                raise RuntimeError('release container never became healthy; deployment blocked')
            headers={'Authorization':'Basic '+base64.b64encode(key.encode()).decode()}
            request=urllib.request.Request(f'http://127.0.0.1:{port}/api/usage/summary?from_ms=0&to_ms='+str(int(time.time()*1000)),headers=headers)
            with open_request(request,30) as response:
                summary=json.load(response)
            if summary.get('events') != 0 or summary.get('total_tokens') != 0:
                raise RuntimeError('expected a fresh empty ledger on the restored copy')
        finally:
            subprocess.run(['docker','rm','-f',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
            if ownership_prepared:
                docker('run','--rm','-u','0:0','-v',str(root)+':/copy','python:3.13-slim','python','-c',
                    f"import os;os.chown('/copy',{os.getuid()},{os.getgid()});[(os.chown('/copy/'+f,{os.getuid()},{os.getgid()})) for f in os.listdir('/copy')]")
        with sqlite3.connect(database) as db:
            actual={name for (name,) in db.execute("select name from sqlite_master where type='table'")}
            result={'healthy_and_authenticated_api':True,'new_tables_created':TABLES<=actual,
                    'original_schema_unchanged':db.execute(SCHEMA_QUERY).fetchall()==schema,
                    'all_heartbeat_fields_unchanged':heartbeat_digest(db)==heartbeats,
                    'integrity':db.execute('pragma integrity_check').fetchone()[0]}
        if not all(result[k] for k in ('new_tables_created','original_schema_unchanged','all_heartbeat_fields_unchanged')) or result['integrity']!='ok':
            raise RuntimeError('release database validation failed; deployment blocked')
        return result


def main() -> int:
    cli=argparse.ArgumentParser(description=__doc__)
    cli.add_argument('--image',required=True)
    cli.add_argument('--backup',type=Path,required=True)
    cli.add_argument('--port',type=int,default=3197)
    args=cli.parse_args()
    if not args.backup.is_file() or not args.backup.name.endswith('.gz') or not 1024<=args.port<=65535:
        cli.error('private gzip backup and unprivileged loopback port required')
    print(json.dumps(validate(args.image,args.backup,args.port)))
    return 0


if __name__=='__main__':
    raise SystemExit(main())
