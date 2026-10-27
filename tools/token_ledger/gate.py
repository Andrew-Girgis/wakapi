"""Fail-closed seven-day observation gate. Elapsed time alone is not validation.

Requires a successful fixed-window report for every expected host on each of
seven consecutive UTC days, with one unchanged parser/implementation. This
command never deploys or changes a dashboard; it only reports gate status.
"""
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import sys

DAY_MS = 86_400_000


def implementation_digest() -> str:
    digest = hashlib.sha256()
    root = Path(__file__).parent
    for name in ('sources.py','collect.py','audit.py','backfill.py','observe.py','daily.py','gate.py'):
        digest.update(name.encode());digest.update((root/name).read_bytes())
    return digest.hexdigest()


def assess(reports: list[dict], machines: list[str], start: int, days: int, now: int) -> dict:
    end = start + days*DAY_MS
    expected = {(machine,start+n*DAY_MS,start+(n+1)*DAY_MS) for machine in machines for n in range(days)}
    successes=set();versions=set();failures=0;activity={machine:set() for machine in machines}
    required_tools={'claude','codex','pi','opencode'}
    for r in reports:
        identity=(r.get('machine'),r.get('from_ms'),r.get('to_ms'))
        if identity not in expected:
            continue
        # Re-read all counters; a stale/corrupt `passed:true` is insufficient.
        receipts=r.get('receipts') or {}
        failed=(r.get('passed') is not True or r.get('source_issues') != 0 or
                r.get('preview_matches_summary') is not True or
                any(r.get(k) != 0 for k in ('queue_missing','queue_mismatched','queue_not_sent','queue_blockers')) or
                any(receipts.get(k) != 0 for k in ('missing','mismatched','conflicts')) or
                receipts.get('matched') != r.get('source_events') or
                any((r.get('queue_status') or {}).get(k,0) for k in ('pending','conflict')) or
                not r.get('implementation_sha256'))
        if failed:
            failures+=1
            continue
        successes.add(identity)
        versions.add((r.get('parser_version'),r['implementation_sha256']))
        for source,bucket in (r.get('per_source') or {}).items():
            if bucket.get('events',0)>0:activity[identity[0]].add(source)
    missing=len(expected-successes)
    unobserved=required_tools-set().union(*activity.values())
    result={'unobserved_tools':sorted(unobserved),'required_reports':len(expected),'passing_windows':len(successes),'missing_windows':missing,
            'failed_reports':failures,'implementation_versions':len(versions),'period_elapsed':now>=end,
            'observed_tools':{k:sorted(v) for k,v in activity.items()}}
    result['passed']=now>=end and not missing and not failures and len(versions)==1 and not unobserved
    return result


def main() -> int:
    cli=argparse.ArgumentParser(description=__doc__)
    cli.add_argument('--report-dir',type=Path,required=True)
    cli.add_argument('--machine',action='append',required=True)
    cli.add_argument('--from-ms',type=int,required=True,help='UTC midnight beginning the observation period')
    cli.add_argument('--days',type=int,default=7)
    args=cli.parse_args()
    if args.days<7 or args.from_ms<=0 or args.from_ms%DAY_MS:
        cli.error('at least seven days beginning at UTC midnight are required')
    reports=[json.loads(path.read_text()) for path in args.report_dir.rglob('*.json')]
    result=assess(reports,list(set(args.machine)),args.from_ms,args.days,
                  int(datetime.now(timezone.utc).timestamp()*1000))
    print(json.dumps(result))
    return 0 if result['passed'] else 1


if __name__ == '__main__':
    sys.exit(main())
