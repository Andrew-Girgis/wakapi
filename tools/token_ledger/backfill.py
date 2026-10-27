"""Replay an audited frozen snapshot through authenticated ingest, never live SQL.

Defaults to queue-only. --send is explicit; credentials come from a protected
WakaTime config, not arguments. A report with zero blocking issues and an exact
evidence digest is required. Use a separate fresh queue for each endpoint.
"""
from __future__ import annotations

import argparse
import configparser
import fcntl
import json
import os
from pathlib import Path
import sqlite3
import sys

from .audit import evidence_digest, reconcile, summarize, transcript_references
from .collect import deliver_bulk, stage
from .sources import PARSER_VERSION, Usage, scan
from .wakatime import observations


def audited_batch(raw: list[Usage], issues: list[str], notes: list[str], report: dict) -> list[Usage]:
    events, conflicts = reconcile(raw)
    references = transcript_references(raw, events)
    actual = summarize(events, issues + conflicts, references)
    if (actual['issues_count'] or report.get('issues_count') != 0 or
            report.get('parser_version') != PARSER_VERSION or
            report.get('evidence_sha256') != evidence_digest(raw) or
            report.get('classification_notes_count') != len(notes) or
            report.get('grand_total') != actual['grand_total'] or
            report.get('sources') != actual['sources']):
        raise RuntimeError('backfill evidence does not match a passing audit; re-audit before delivery')
    # Replays of canonical observations are no-ops; copied-session refs still
    # reach the service so their WakaTime observations cannot become fallback.
    return events + references


def main() -> int:
    cli = argparse.ArgumentParser(description=__doc__)
    cli.add_argument('--source', type=Path, action='append', required=True)
    cli.add_argument('--machine', action='append', required=True)
    cli.add_argument('--audit-report', type=Path, required=True)
    cli.add_argument('--queue', type=Path, required=True)
    cli.add_argument('--wakatime-export', type=Path)
    cli.add_argument('--wakatime-user-agents', type=Path)
    cli.add_argument('--wakatime-machines', type=Path)
    cli.add_argument('--wakatime-config', type=Path, default=Path.home()/'.wakatime.cfg')
    cli.add_argument('--send', action='store_true')
    cli.add_argument('--limit', type=int, default=1000, help='bounded maximum delivered per invocation')
    args = cli.parse_args()
    if len(args.source) != len(args.machine) or not 1 <= args.limit <= 100_000:
        cli.error('invalid sources or delivery limit')
    report = json.loads(args.audit_report.read_text())
    cutoff = report.get('cutoff_ms')
    if not isinstance(cutoff, int) or cutoff <= 0:
        cli.error('passing audit must have a fixed UTC cutoff')
    if args.wakatime_export and not (args.wakatime_user_agents and args.wakatime_machines):
        cli.error('export requires user-agent and machine metadata')
    raw: list[Usage] = [];issues: list[str] = [];notes: list[str] = []
    for root, machine in zip(args.source,args.machine):
        raw.extend(scan(root,machine,issues,notes))
    if args.wakatime_export:
        raw.extend(observations(args.wakatime_export,args.wakatime_user_agents,
                                args.wakatime_machines,cutoff,issues))
    raw = [e for e in raw if e.occurred_ms < cutoff]
    batch = audited_batch(raw,issues,notes,report)
    args.queue.parent.mkdir(parents=True,exist_ok=True,mode=0o700)
    os.chmod(args.queue.parent,0o700)
    with (args.queue.parent/'collector.lock').open('a+') as lock:
        try:
            fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
        except BlockingIOError:
            raise RuntimeError('collector already running') from None
        with sqlite3.connect(args.queue) as db:
            os.chmod(args.queue,0o600)
            db.execute('PRAGMA journal_mode=WAL')
            db.execute('CREATE TABLE IF NOT EXISTS backfill_manifest (id INTEGER PRIMARY KEY CHECK(id=1), evidence_sha256 TEXT NOT NULL)')
            previous = db.execute('SELECT evidence_sha256 FROM backfill_manifest').fetchone()
            if previous and previous[0] != report['evidence_sha256']:
                raise RuntimeError('backfill queue belongs to different evidence; audited migration required')
            db.execute('INSERT OR IGNORE INTO backfill_manifest VALUES(1,?)',(report['evidence_sha256'],))
            db.commit()
            result = {'staged':stage(db,batch,1),'blocking_issues':0,
                      'bounded_classification_notes':len(notes),'expected_tokens':report['grand_total']}
            if result['staged']['conflicts']:
                raise RuntimeError('backfill queue contains conflicts; delivery disabled')
            if args.send:
                conf = configparser.ConfigParser(interpolation=None)
                if not conf.read(args.wakatime_config):
                    cli.error('WakaTime config not found')
                result['delivery'] = deliver_bulk(db,conf.get('settings','api_url'),
                                                  conf.get('settings','api_key'),args.limit,100)
        print(json.dumps(result))  # Counts only. The private report contains source identities.
    return 0


if __name__ == '__main__':
    sys.exit(main())
