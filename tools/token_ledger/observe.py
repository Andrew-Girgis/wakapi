"""Read-only source -> queue -> ledger receipt -> shadow-preview verification.

Run after the collector on each host. The UTC window is fixed; default end is
last midnight, not a moving 'now'. Reports contain counts only, not source IDs,
transcripts or credentials. A failed comparison exits nonzero and blocks cutover.
"""
from __future__ import annotations

import argparse
import base64
from collections import Counter, defaultdict
import configparser
from datetime import datetime, timezone
import fcntl
import json
import os
from pathlib import Path
import sqlite3
import sys
import urllib.parse
import urllib.request

from .collect import open_request, validate_api_url
from .sources import PARSER_VERSION, Usage, scan_live
from .gate import implementation_digest


class ObservationBusy(RuntimeError):
    pass


def compare_source_queue(db: sqlite3.Connection, events: list[Usage], start: int, end: int) -> tuple[dict, list[dict]]:
    unique = {(e.source,e.key,e.session_key):e for e in events if start <= e.occurred_ms < end}
    counts = {'source_events':len(unique),'queue_missing':0,'queue_mismatched':0,'queue_not_sent':0}
    per_source = defaultdict(lambda:{'events':0,'tokens':0})
    receipts = []
    for (source,key,session), event in sorted(unique.items()):
        per_source[source]['events'] += 1
        per_source[source]['tokens'] += event.total
        row = db.execute('SELECT fingerprint,status FROM pending_usage WHERE source=? AND source_key=? AND session_key=?',
                         (source,key,session)).fetchone()
        if row is None:
            counts['queue_missing'] += 1
        else:
            counts['queue_mismatched'] += row[0] != event.fingerprint
            counts['queue_not_sent'] += row[1] != 'sent'
        receipts.append({'source':source,'source_key':key,'session_key':session,'raw_sha256':event.fingerprint})
    counts['per_source'] = dict(per_source)
    counts['queue_status'] = dict(db.execute('SELECT status,count(*) FROM pending_usage GROUP BY status').fetchall())
    counts['queue_blockers'] = db.execute('SELECT count(*) FROM collector_blockers').fetchone()[0]
    return counts,receipts


def verify_receipts(api_url: str, key: str, receipts: list[dict]) -> dict:
    validate_api_url(api_url)
    headers = {'Authorization':'Basic '+base64.b64encode(key.encode()).decode(),'Content-Type':'application/json'}
    totals = Counter({'matched':0,'missing':0,'mismatched':0,'conflicts':0})
    for index in range(0,len(receipts),200):
        batch = receipts[index:index+200]
        req = urllib.request.Request(api_url.rstrip('/')+'/usage/receipts',headers=headers,
            data=json.dumps(batch,separators=(',',':')).encode(),method='POST')
        with open_request(req,30) as response:
            ack = json.load(response)
        if (any(type(ack.get(k)) is not int or ack[k]<0 for k in totals) or
                sum(ack[k] for k in ('matched','missing','mismatched')) != len(batch)):
            raise RuntimeError('invalid receipt acknowledgement')
        totals.update({k:ack[k] for k in totals})
    return dict(totals)


def preview_parity(api_url: str, key: str, start: int, end: int) -> bool:
    validate_api_url(api_url)
    headers = {'Authorization':'Basic '+base64.b64encode(key.encode()).decode()}
    direct_query = urllib.parse.urlencode({'from_ms':start,'to_ms':end})
    dashboard_query = urllib.parse.urlencode({'from':datetime.fromtimestamp(start/1000,timezone.utc).isoformat(),
                                              'to':datetime.fromtimestamp(end/1000,timezone.utc).isoformat()})
    values=[]
    for suffix in ('/usage/summary?'+direct_query,'/dashboard/ledger?'+dashboard_query):
        with open_request(urllib.request.Request(api_url.rstrip('/')+suffix,headers=headers),30) as response:
            values.append(json.load(response))
    return values[0] == values[1]


def main() -> int:
    cli = argparse.ArgumentParser(description=__doc__)
    cli.add_argument('--machine',required=True)
    cli.add_argument('--from-ms',type=int,required=True)
    cli.add_argument('--to-ms',type=int)
    cli.add_argument('--queue',type=Path,required=True)
    cli.add_argument('--report',type=Path,required=True)
    cli.add_argument('--wakatime-config',type=Path,default=Path.home()/'.wakatime.cfg')
    args = cli.parse_args()
    now = datetime.now(timezone.utc)
    end = args.to_ms if args.to_ms is not None else int(now.replace(hour=0,minute=0,second=0,microsecond=0).timestamp()*1000)
    if args.from_ms <= 0 or end <= args.from_ms or end > int(now.timestamp()*1000):
        cli.error('invalid frozen window')
    if not args.queue.is_file() or args.queue.is_symlink():
        cli.error('existing private collector queue required')
    with (args.queue.parent/'collector.lock').open('a+') as lock:
        try:
            fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
        except BlockingIOError:
            raise ObservationBusy('collector already running; retry verification') from None
        issues=[];notes=[]
        events=list(scan_live(Path.home(),args.machine,issues,notes))
        with sqlite3.connect(args.queue.resolve().as_uri()+'?mode=ro',uri=True) as db:
            db.execute('BEGIN')
            comparison,receipts=compare_source_queue(db,events,args.from_ms,end)
        report={'parser_version':PARSER_VERSION,'implementation_sha256':implementation_digest(),
                'checked_at':now.isoformat(),'machine':args.machine,
                'from_ms':args.from_ms,'to_ms':end,'source_issues':len(issues),
                'bounded_classification_notes':len(notes),**comparison}
        conf=configparser.ConfigParser(interpolation=None)
        if not conf.read(args.wakatime_config):
            cli.error('WakaTime config not found')
        api_url=conf.get('settings','api_url');key=conf.get('settings','api_key')
        report['receipts']=verify_receipts(api_url,key,receipts)
        report['preview_matches_summary']=preview_parity(api_url,key,args.from_ms,end)
        report['passed']=not (issues or any(comparison[k] for k in ('queue_missing','queue_mismatched','queue_not_sent','queue_blockers'))
            or comparison['queue_status'].get('conflict',0) or comparison['queue_status'].get('pending',0)
            or any(report['receipts'][k] for k in ('missing','mismatched','conflicts'))
            or not report['preview_matches_summary'])
    args.report.parent.mkdir(parents=True,exist_ok=True,mode=0o700)
    # Atomic replacement avoids a partially-written report looking like a pass.
    temporary=args.report.with_name(args.report.name+'.tmp')
    fd=os.open(temporary,os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600)
    with os.fdopen(fd,'w') as stream:json.dump(report,stream,indent=2)
    os.replace(temporary,args.report)
    print(json.dumps(report))
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    sys.exit(main())
