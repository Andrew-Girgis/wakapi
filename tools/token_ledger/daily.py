"""Catch up fixed UTC observation windows after wake/restart; never cut over.

Use from a daily systemd/launchd job after collection. Existing matching passing
reports are skipped. Failed attempts are retained; a gate never forgets them.
"""
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import json
from pathlib import Path
import re
import sys
import time

from . import observe, collect
from .gate import DAY_MS, implementation_digest


def retry_busy(operation, attempts: int = 30) -> int:
    """Retry ONLY lock contention, never a failed source/accounting gate."""
    for attempt in range(attempts):
        try:
            result=operation()
        except observe.ObservationBusy:
            result=75
        if result!=75:
            return result
        if attempt+1<attempts:
            time.sleep(2)
    return 75


def main() -> int:
    cli=argparse.ArgumentParser(description=__doc__)
    cli.add_argument('--machine',required=True)
    cli.add_argument('--first-day-ms',type=int,required=True)
    cli.add_argument('--queue',type=Path,required=True)
    cli.add_argument('--collect-since-ms',type=int,help='explicitly deliver backlog before verifying')
    cli.add_argument('--report-dir',type=Path,required=True)
    cli.add_argument('--wakatime-config',type=Path,default=Path.home()/'.wakatime.cfg')
    args=cli.parse_args()
    if not re.fullmatch(r'[A-Za-z0-9_-]+',args.machine) or args.first_day_ms<=0 or args.first_day_ms%DAY_MS:
        cli.error('safe machine label and UTC midnight start required')
    if args.collect_since_ms is not None:
        old_argv=sys.argv
        try:
            sys.argv=['collect','--machine',args.machine,'--since-ms',str(args.collect_since_ms),
                '--queue',str(args.queue),'--wakatime-config',str(args.wakatime_config),'--send','--limit','10000']
            if retry_busy(collect.main)!=0:
                return 1
        finally:
            sys.argv=old_argv
    midnight=int(datetime.now(timezone.utc).replace(hour=0,minute=0,second=0,microsecond=0).timestamp()*1000)
    if midnight<=args.first_day_ms:
        print(json.dumps({'waiting_for_first_complete_day':True,'first_day_ms':args.first_day_ms}))
        return 0
    args.report_dir.mkdir(parents=True,exist_ok=True,mode=0o700)
    existing=[json.loads(p.read_text()) for p in args.report_dir.glob('*.json')]
    digest=implementation_digest();failures=0
    # Bounded catch-up: operators investigate longer offline periods explicitly.
    for start in range(max(args.first_day_ms,midnight-14*DAY_MS),midnight,DAY_MS):
        end=start+DAY_MS
        if any(r.get('machine')==args.machine and r.get('from_ms')==start and r.get('to_ms')==end
               and r.get('passed') is True and r.get('implementation_sha256')==digest for r in existing):
            continue
        stamp=datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')
        report=args.report_dir/(args.machine+'-'+str(start)+'-'+stamp+'.json')
        old_argv=sys.argv
        try:
            sys.argv=['observe','--machine',args.machine,'--from-ms',str(start),'--to-ms',str(end),
                '--queue',str(args.queue),'--report',str(report),'--wakatime-config',str(args.wakatime_config)]
            failures+=retry_busy(observe.main)!=0
        finally:
            sys.argv=old_argv
    return 1 if failures else 0


if __name__=='__main__':
    sys.exit(main())
