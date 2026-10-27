"""Generate additive collector/observer jobs. --enable and --send are explicit.

Never changes existing heartbeat/sync jobs, credentials, or the dashboard.
Run from an immutable private release directory, not a changing working tree.
"""
from __future__ import annotations

import argparse
import os
from pathlib import Path
import plistlib
import shlex
import subprocess
import sys

from .gate import DAY_MS


def render_jobs(platform: str, python: str, release: Path, machine: str, state: Path,
                since: int, first_day: int, send: bool, wait_host: str = '') -> dict[str,bytes]:
    queue=state/'live/queue.sqlite3';reports=state/'observations'
    collector=[python,'-m','tools.token_ledger.collect','--machine',machine,'--since-ms',str(since),'--queue',str(queue)]
    daily=[python,'-m','tools.token_ledger.daily','--machine',machine,'--first-day-ms',str(first_day),
           '--queue',str(queue),'--report-dir',str(reports)]
    if send:
        collector+=['--send']
        daily+=['--collect-since-ms',str(since)]
    if platform=='darwin':
        jobs={}
        for name,args,calendar in [('collector',collector,None),('observation',daily,{'Hour':3,'Minute':15})]:
            label='net.wakapi.token-ledger.'+name
            body={'Label':label,'ProgramArguments':args,'WorkingDirectory':str(release),'RunAtLoad':True,
                  'StandardOutPath':str(state/(name+'.log')),'StandardErrorPath':str(state/(name+'.error.log')),
                  'Umask':0o077}
            if calendar:body['StartCalendarInterval']=calendar
            else:body['StartInterval']=300
            jobs[label+'.plist']=plistlib.dumps(body)
        return jobs
    if platform!='linux':
        raise ValueError('only macOS launchd and Linux user systemd are supported')
    jobs={}
    for name,args in [('collector',collector),('observation',daily)]:
        unit='wakapi-token-ledger-'+name
        # These are systemd argument quotes, not a shell command.
        command=' '.join(shlex.quote(arg) for arg in args)
        jobs[unit+'.service']=(f'[Unit]\nDescription=Wakapi token ledger {name}\n\n[Service]\nType=oneshot\n'
            f'WorkingDirectory={shlex.quote(str(release))}\nExecStart={command}\nUMask=0077\nSuccessExitStatus=75\n'
            +(wait_for_host(wait_host) if name=='observation' and wait_host else '')).encode()
        schedule='OnBootSec=3min\nOnUnitActiveSec=5min\n' if name=='collector' else 'OnCalendar=*-*-* 07:15:00 UTC\nPersistent=true\n'
        jobs[unit+'.timer']=(f'[Unit]\nDescription=Wakapi token ledger {name} schedule\n\n[Timer]\n{schedule}'
            f'Unit={unit}.service\n\n[Install]\nWantedBy=timers.target\n').encode()
    return jobs


def wait_for_host(host: str) -> str:
    """The catch-up run after boot starts before DNS works. Wait for the API host instead of
    restarting: a failed observation writes a failed report, and the gate counts every one."""
    if not host.replace('.','').replace('-','').isalnum():
        raise ValueError('wait host must be a plain host name')
    return (f"ExecStartPre=/bin/sh -c 'for i in $$(seq 120); do getent hosts {host} >/dev/null && exit 0; "
            "sleep 5; done; exit 1'\nTimeoutStartSec=15min\n")


def main() -> int:
    cli=argparse.ArgumentParser(description=__doc__)
    cli.add_argument('--machine',required=True)
    cli.add_argument('--since-ms',type=int,required=True)
    cli.add_argument('--first-day-ms',type=int,required=True)
    cli.add_argument('--state-dir',type=Path,required=True)
    cli.add_argument('--output-dir',type=Path)
    cli.add_argument('--send',action='store_true')
    cli.add_argument('--enable',action='store_true')
    cli.add_argument('--wait-for-host',default='',help='Linux: observation waits for this API host to resolve')
    args=cli.parse_args()
    if args.since_ms<=0 or args.first_day_ms%DAY_MS or args.first_day_ms<args.since_ms:
        cli.error('audited cutoff and subsequent UTC midnight required')
    release=Path(__file__).resolve().parents[2]
    state=args.state_dir.resolve();state.mkdir(parents=True,exist_ok=True,mode=0o700);os.chmod(state,0o700)
    output=args.output_dir or (Path.home()/'Library/LaunchAgents' if sys.platform=='darwin' else Path.home()/'.config/systemd/user')
    output.mkdir(parents=True,exist_ok=True)
    jobs=render_jobs(sys.platform,sys.executable,release,args.machine,state,args.since_ms,args.first_day_ms,args.send,
                     args.wait_for_host)
    for name,body in jobs.items():
        path=output/name
        if path.exists() and path.read_bytes()!=body:
            cli.error('existing token-ledger job differs; preserve it and review before replacing')
        path.write_bytes(body);path.chmod(0o600)
    if args.enable:
        if args.output_dir:
            cli.error('--enable requires the standard OS job directory')
        if sys.platform=='darwin':
            for name in jobs:
                subprocess.run(['launchctl','bootstrap','gui/'+str(os.getuid()),str(output/name)],check=True)
        else:
            subprocess.run(['systemctl','--user','daemon-reload'],check=True)
            subprocess.run(['systemctl','--user','enable','--now',
                'wakapi-token-ledger-collector.timer','wakapi-token-ledger-observation.timer'],check=True)
    print('generated '+str(len(jobs))+' additive jobs; delivery '+('enabled' if args.send else 'disabled')+
          '; schedules '+('enabled' if args.enable else 'not started'))
    return 0


if __name__=='__main__':
    sys.exit(main())
