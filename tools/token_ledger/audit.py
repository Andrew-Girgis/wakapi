"""Reproducible transcript audit and guarded *isolated-copy* rehearsal.

Usage: python3 -m tools.token_ledger.audit --source PATH --machine Pythia --report PATH
Writes only the requested report by default; --commit requires --database and --user-id.
Do not run --commit against a live SQLite database. This command has no network access.
"""
from __future__ import annotations

import argparse
from collections import defaultdict
from datetime import datetime, timezone
import json
import hashlib
from pathlib import Path
import sqlite3
import sys

from .sources import Usage, PARSER_VERSION, scan
from .collect import accounting_mode
from .wakatime import observations

SCHEMA = Path(__file__).with_name('schema.sql')


def reconcile(events: list[Usage]) -> tuple[list[Usage], list[str]]:
    indexed: dict[tuple[str, str], Usage] = {}
    problems: list[str] = []
    disputed: set[tuple[str, str]] = set()
    for event in events:
        key = event.source, event.key
        if key in disputed:
            continue
        old = indexed.get(key)
        if old is not None and old.fingerprint != event.fingerprint:
            problems.append(f'{event.source}:{event.key}: conflicting records across snapshots/machines')
            # Fail closed: neither conflicting observation is a verified winner.
            disputed.add(key)
            del indexed[key]
            continue
        if old is None:
            indexed[key] = event
    return list(indexed.values()), problems


def evidence_digest(events: list[Usage]) -> str:
    # Include aliases and attribution, not just a plausible grand total.
    rows = sorted((e.source,e.key,e.fingerprint,e.session_key,e.machine,e.project) for e in events)
    return hashlib.sha256(json.dumps(rows,separators=(',', ':')).encode()).hexdigest()


def transcript_references(raw_events: list[Usage], reconciled: list[Usage]) -> list[Usage]:
    canonical = {(e.source,e.key):e.fingerprint for e in reconciled}
    return [e for e in raw_events if e.source != 'wakatime' and e.session_key
            and canonical.get((e.source,e.key)) == e.fingerprint]


def summarize(events: list[Usage], issues: list[str], references: list[Usage] | None = None) -> dict:
    by_source: dict[str, dict] = defaultdict(lambda: {'events': 0, 'input': 0, 'cache_read': 0,
        'cache_write': 0, 'unknown_input': 0, 'output': 0, 'total': 0, 'first_ms': None, 'last_ms': None})
    for event in events:
        row = by_source[event.source]
        row['events'] += 1
        for key in ('input', 'cache_read', 'cache_write', 'unknown_input', 'output', 'total'):
            row[key] += getattr(event, key)
        row['first_ms'] = min(row['first_ms'] or event.occurred_ms, event.occurred_ms)
        row['last_ms'] = max(row['last_ms'] or event.occurred_ms, event.occurred_ms)
    transcript_sessions = {e.session_key for e in (references if references is not None else events)
                           if e.source != 'wakatime' and e.session_key}
    candidate_fallback = [e for e in events if e.source == 'wakatime'
                          and e.session_key and e.session_key not in transcript_sessions]
    large_fallback = [e for e in candidate_fallback if e.total > 1_000_000]
    quarantined_sessions = {e.session_key for e in large_fallback}
    quarantined = [e for e in candidate_fallback if e.session_key in quarantined_sessions]
    fallback = [e for e in candidate_fallback if e.session_key not in quarantined_sessions]
    counted = [e for e in events if e.source != 'wakatime'] + fallback
    findings = list(issues)
    for session in sorted(quarantined_sessions):
        findings.append(f'wakatime:{session}: fallback heartbeat exceeds 1M tokens; verify per-event provenance')
    return {'parser_version': PARSER_VERSION, 'generated_at': datetime.now(timezone.utc).isoformat(),
        'sources': dict(by_source), 'grand_total': sum(e.total for e in counted),
        'transcript_total': sum(e.total for e in counted if e.source != 'wakatime'),
        'fallback_total': sum(e.total for e in fallback), 'fallback_events': len(fallback),
        'large_fallback_events': len(large_fallback), 'large_fallback_tokens': sum(e.total for e in large_fallback),
        'quarantined_fallback_events': len(quarantined), 'quarantined_fallback_tokens': sum(e.total for e in quarantined),
        'excluded_wakatime_tokens': sum(e.total for e in events if e.source == 'wakatime')-sum(e.total for e in candidate_fallback),
        'unknown_input_tokens': sum(e.unknown_input for e in counted),
        'session_refs': len({(e.source,e.key,e.session_key) for e in (references if references is not None else events)
                             if e.source != 'wakatime' and e.session_key}),
        'issues_count': len(findings), 'issues': findings[:300],
        'issues_truncated': max(0, len(findings)-300)}


def import_copy(db: sqlite3.Connection, user_id: str, events: list[Usage],
                references: list[Usage] | None = None) -> dict:
    """Only additive ledger tables. Existing rows are immutable; conflicting revisions are quarantined."""
    db.executescript(SCHEMA.read_text())
    added = updated = duplicate = conflict = refs_added = 0
    now = int(datetime.now(timezone.utc).timestamp() * 1000)
    with db:
        for e in events:
            if e.source == 'codex' and e.session_key:
                identity = (user_id,e.source,e.session_key)
                mode = accounting_mode(e)
                prior = db.execute('SELECT mode FROM token_usage_accounting_modes WHERE user_id=? AND source=? AND session_key=?', identity).fetchone()
                keys = db.execute('SELECT source_key FROM token_usage_events WHERE user_id=? AND source=? AND session_key=?', identity).fetchall()
                existing_modes = {'response' if ':response:' in key else 'fork' if ':fork:' in key else 'cumulative' for key, in keys}
                if (prior and prior[0] != mode) or existing_modes - {mode}:
                    raise RuntimeError('accounting representation changed; use a fresh isolated copy or audited migration')
                db.execute('INSERT OR IGNORE INTO token_usage_accounting_modes VALUES (?,?,?,?)', (*identity,mode))
            row = db.execute('''SELECT raw_sha256, occurred_ms, machine, project, model, session_key,
                input_tokens, cache_read_tokens, cache_write_tokens, unknown_input_tokens,
                output_tokens, reasoning_tokens, parser_version
                FROM token_usage_events WHERE user_id=? AND source=? AND source_key=?''',
                (user_id, e.source, e.key)).fetchone()
            if row:
                if row[0] == e.fingerprint:
                    duplicate += 1
                elif (row[2:6] == (e.machine, e.project, e.model, e.session_key)
                      and e.occurred_ms >= row[1] and PARSER_VERSION >= row[12]
                      and all(new >= old for new, old in zip(
                          (e.input, e.cache_read, e.cache_write, e.unknown_input, e.output, e.reasoning), row[6:12]))):
                    db.execute('''UPDATE token_usage_events SET occurred_ms=?, input_tokens=?,
                        cache_read_tokens=?, cache_write_tokens=?, unknown_input_tokens=?, output_tokens=?, reasoning_tokens=?,
                        parser_version=?, raw_sha256=?, raw_usage_json=?, observed_ms=?
                        WHERE user_id=? AND source=? AND source_key=?''',
                        (e.occurred_ms, e.input, e.cache_read, e.cache_write, e.unknown_input, e.output, e.reasoning,
                         PARSER_VERSION, e.fingerprint, e.raw_usage_json, now, user_id, e.source, e.key))
                    updated += 1
                else:
                    db.execute('INSERT OR IGNORE INTO token_usage_conflicts VALUES (?,?,?,?,?,?,?)',
                        (user_id, e.source, e.key, row[0], e.fingerprint, 'revised source record', now))
                    conflict += 1
                continue
            db.execute('''INSERT INTO token_usage_events
                (user_id,source,source_key,session_key,occurred_ms,machine,project,model,input_tokens,
                 cache_read_tokens,cache_write_tokens,unknown_input_tokens,output_tokens,reasoning_tokens,
                 parser_version,raw_sha256,raw_usage_json,observed_ms)
                VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)''',
                (user_id, e.source, e.key, e.session_key, e.occurred_ms, e.machine, e.project, e.model,
                 e.input, e.cache_read, e.cache_write, e.unknown_input, e.output, e.reasoning,
                 PARSER_VERSION, e.fingerprint, e.raw_usage_json, now))
            added += 1
        for e in references if references is not None else events:
            if e.source == 'wakatime' or not e.session_key:
                continue
            existing = db.execute('SELECT raw_sha256 FROM token_usage_events WHERE user_id=? AND source=? AND source_key=?',
                                  (user_id,e.source,e.key)).fetchone()
            if existing is None or existing[0] != e.fingerprint:
                continue  # no reference for disputed or rejected accounting
            cursor = db.execute('INSERT OR IGNORE INTO token_usage_session_refs VALUES (?,?,?,?)',
                (user_id,e.source,e.key,e.session_key))
            refs_added += cursor.rowcount
        covered = {e.session_key for e in (references if references is not None else events)
                   if e.source != 'wakatime' and e.session_key}
        for session in {e.session_key for e in events if e.source == 'wakatime'
                        and e.session_key not in covered and e.total > 1_000_000}:
            db.execute('INSERT OR IGNORE INTO token_usage_quarantined_sessions VALUES (?,?,?,?,?)',
                       (user_id,'wakatime',session,'individual fallback observation exceeds 1M tokens','open'))
    return {'inserted': added, 'updated': updated, 'unchanged': duplicate,
            'conflicts': conflict, 'session_refs_inserted': refs_added}


def main() -> int:
    cli = argparse.ArgumentParser(description=__doc__)
    cli.add_argument('--source', type=Path, action='append', required=True, help='snapshot source/ directory (repeat for each machine)')
    cli.add_argument('--machine', action='append', required=True, help='corresponding machine label')
    cli.add_argument('--report', type=Path, required=True)
    cli.add_argument('--wakatime-export', type=Path, help='private raw heartbeat export; lower-priority per-session fallback')
    cli.add_argument('--wakatime-user-agents', type=Path)
    cli.add_argument('--wakatime-machines', type=Path)
    cli.add_argument('--cutoff-ms', type=int, help='exclude observations after frozen source snapshot')
    cli.add_argument('--database', type=Path, help='isolated, writable Wakapi database COPY')
    cli.add_argument('--user-id', help='existing Wakapi user ID, needed only with --commit')
    cli.add_argument('--commit', action='store_true', help='write additive ledger tables to isolated database COPY')
    cli.add_argument('--allow-incomplete', action='store_true', help='rehearsal only: import verified rows despite reported source issues')
    args = cli.parse_args()
    if len(args.source) != len(args.machine) or (args.commit and (not args.database or not args.user_id)):
        cli.error('one machine per source; --commit requires --database and --user-id')
    if args.wakatime_export and not (args.wakatime_user_agents and args.wakatime_machines and args.cutoff_ms):
        cli.error('WakaTime export requires agents, machines and fixed UTC cutoff')
    issues: list[str] = []
    classification_notes: list[str] = []
    events: list[Usage] = []
    unattributed: dict[str, int] = {}
    for directory, machine in zip(args.source, args.machine):
        events.extend(scan(directory, machine, issues, classification_notes))
    if args.wakatime_export:
        events.extend(observations(args.wakatime_export, args.wakatime_user_agents,
                                   args.wakatime_machines, args.cutoff_ms, issues, unattributed))
    # The fixed cutoff applies equally to transcripts and WakaTime observations.
    if args.cutoff_ms:
        events = [e for e in events if e.occurred_ms < args.cutoff_ms]
    raw_events = events
    events, conflicts = reconcile(raw_events)
    issues.extend(conflicts)
    references = transcript_references(raw_events, events)
    report = summarize(events, issues, references)
    report['evidence_sha256'] = evidence_digest(raw_events)
    report['cutoff_ms'] = args.cutoff_ms
    report['excluded_unattributed_wakatime'] = unattributed
    report['classification_notes'] = classification_notes
    report['classification_notes_count'] = len(classification_notes)
    if args.commit:
        # Require an unmistakably named, existing, ordinary offline rehearsal copy.
        # This tool is not an online production migration mechanism.
        if args.database.is_symlink() or not args.database.is_file() or not args.database.name.endswith('.rehearsal.sqlite3'):
            cli.error('write target must be an existing nonsymlink *.rehearsal.sqlite3 copy')
        if report['issues_count'] and not args.allow_incomplete:
            cli.error(f"{report['issues_count']} unresolved issues; use --allow-incomplete only for a clearly labelled rehearsal")
        db = sqlite3.connect(args.database)
        try:
            db.execute('PRAGMA busy_timeout=5000')
            db.execute('PRAGMA foreign_keys=ON')
            if db.execute('SELECT count(*) FROM users WHERE id=?', (args.user_id,)).fetchone()[0] != 1:
                cli.error('unknown user ID in target Wakapi copy')
            report['import'] = import_copy(db, args.user_id, events, references)
        finally:
            db.close()
    args.report.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    args.report.write_text(json.dumps(report, indent=2))
    args.report.chmod(0o600)
    print(json.dumps({k: v for k, v in report.items() if k not in ('issues', 'classification_notes')}, indent=2))
    return 2 if report['issues_count'] else 0


if __name__ == '__main__':
    sys.exit(main())
