"""Durable, replay-safe transcript collector (standard library only).

Runs one pass; a systemd/launchd timer repeats it. By default it queues only; the
--send switch enables network delivery. Never send historical usage until audited.
"""
from __future__ import annotations

import argparse
import base64
import configparser
import fcntl
import json
import os
from pathlib import Path
import sqlite3
import sys
import urllib.error
import urllib.request
import urllib.parse
import hashlib

from .sources import PARSER_VERSION, Usage, scan_live

QUEUE_SCHEMA = '''
CREATE TABLE IF NOT EXISTS pending_usage (
  source TEXT NOT NULL, source_key TEXT NOT NULL, session_key TEXT NOT NULL, fingerprint TEXT NOT NULL,
  payload TEXT NOT NULL, status TEXT NOT NULL CHECK(status IN ('pending','sent','conflict')),
  last_error TEXT NOT NULL DEFAULT '', PRIMARY KEY(source, source_key, session_key)
);
CREATE INDEX IF NOT EXISTS idx_pending_usage_status ON pending_usage(status);
CREATE TABLE IF NOT EXISTS delivery_target (
  id INTEGER PRIMARY KEY CHECK(id=1), target_sha256 TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS accounting_modes (
  source TEXT NOT NULL, session_key TEXT NOT NULL, mode TEXT NOT NULL,
  PRIMARY KEY(source,session_key)
);
CREATE TABLE IF NOT EXISTS collector_blockers (
  source TEXT NOT NULL, session_key TEXT NOT NULL, reason TEXT NOT NULL,
  PRIMARY KEY(source,session_key)
);
'''


def accounting_mode(event: Usage) -> str:
    if event.source != 'codex':
        return 'message'
    if ':response:' in event.key:
        return 'response'
    if ':fork:' in event.key:
        return 'fork'
    return 'cumulative'


def stage(db: sqlite3.Connection, events: list[Usage], since_ms: int) -> dict[str, int]:
    db.executescript(QUEUE_SCHEMA)
    if 'session_key' not in {r[1] for r in db.execute('PRAGMA table_info(pending_usage)')}:
        raise RuntimeError('outdated queue schema; preserve and migrate before collecting')
    counts = {'new': 0, 'revised': 0, 'unchanged': 0, 'conflicts': 0, 'before_cutoff': 0}
    for source, session, body in db.execute('SELECT source,session_key,payload FROM pending_usage'):
        prior_payload = json.loads(body)
        if prior_payload.get('parser_version') != PARSER_VERSION:
            raise RuntimeError('outdated queue requires audited migration before staging')
        if source == 'codex' and session:
            key = prior_payload['source_key']
            mode = 'response' if ':response:' in key else 'fork' if ':fork:' in key else 'cumulative'
            with db:
                db.execute('INSERT OR IGNORE INTO accounting_modes VALUES (?,?,?)', (source,session,mode))
    # Pin accounting representation before staging anything. A later parser
    # discovery must not add a second representation of already sent usage.
    modes: dict[tuple[str, str], str] = {}
    for e in events:
        if e.source == 'codex' and e.session_key and e.occurred_ms >= since_ms:
            identity = (e.source, e.session_key)
            mode = accounting_mode(e)
            prior = db.execute('SELECT mode FROM accounting_modes WHERE source=? AND session_key=?', identity).fetchone()
            if (prior and prior[0] != mode) or (identity in modes and modes[identity] != mode):
                with db:
                    db.execute('INSERT OR REPLACE INTO collector_blockers VALUES (?,?,?)',
                               (*identity,'accounting representation changed; audited migration required'))
                raise RuntimeError('accounting representation changed; preserve queue and reconcile before delivery')
            modes[identity] = mode
    with db:
        for identity, mode in modes.items():
            db.execute('INSERT OR IGNORE INTO accounting_modes VALUES (?,?,?)', (*identity,mode))
        for e in events:
            if e.occurred_ms < since_ms:
                counts['before_cutoff'] += 1
                continue
            body = json.dumps({'source': e.source, 'source_key': e.key, 'session_key': e.session_key,
                'occurred_ms': e.occurred_ms,
                'machine': e.machine, 'project': e.project, 'model': e.model,
                'input_tokens': e.input, 'cache_read_tokens': e.cache_read,
                'cache_write_tokens': e.cache_write, 'unknown_input_tokens': e.unknown_input,
                'output_tokens': e.output,
                'reasoning_tokens': e.reasoning, 'parser_version': PARSER_VERSION,
                'raw_sha256': e.fingerprint, 'raw_usage_json': e.raw_usage_json}, separators=(',', ':'))
            old = db.execute('SELECT fingerprint, payload, status FROM pending_usage WHERE source=? AND source_key=? AND session_key=?',
                             (e.source, e.key, e.session_key)).fetchone()
            if old is None:
                db.execute('INSERT INTO pending_usage VALUES (?,?,?,?,?,?,?)',
                           (e.source, e.key, e.session_key, e.fingerprint, body, 'pending', ''))
                counts['new'] += 1
            elif old[0] == e.fingerprint:
                counts['unchanged'] += 1
            else:
                prev = json.loads(old[1])
                if (e.machine == prev['machine'] and e.project == prev['project'] and e.model == prev['model']
                    and e.session_key == prev.get('session_key','')
                    and e.occurred_ms >= prev['occurred_ms'] and
                    all(n >= prev.get(k,0) for k,n in [('input_tokens',e.input),('cache_read_tokens',e.cache_read),
                         ('cache_write_tokens',e.cache_write),('unknown_input_tokens',e.unknown_input),
                         ('output_tokens',e.output),('reasoning_tokens',e.reasoning)])):
                    db.execute('UPDATE pending_usage SET fingerprint=?,payload=?,status=?,last_error=? WHERE source=? AND source_key=? AND session_key=?',
                               (e.fingerprint, body, 'pending', '', e.source, e.key, e.session_key))
                    counts['revised'] += 1
                else:
                    db.execute('UPDATE pending_usage SET status=?,last_error=? WHERE source=? AND source_key=? AND session_key=?',
                               ('conflict', 'non-monotonic source revision', e.source, e.key, e.session_key))
                    counts['conflicts'] += 1
    return counts


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None  # never forward an Authorization header to a redirect target


def open_request(request: urllib.request.Request, timeout: int):
    return urllib.request.build_opener(NoRedirect).open(request,timeout=timeout)


def validate_api_url(api_url: str) -> None:
    parsed = urllib.parse.urlsplit(api_url)
    if parsed.username or parsed.password or parsed.query or parsed.fragment or not parsed.hostname:
        raise ValueError('invalid API URL')
    if parsed.scheme != 'https' and not (parsed.scheme == 'http' and parsed.hostname in ('127.0.0.1','localhost','::1')):
        raise ValueError('HTTPS required except for an isolated loopback test')


def pin_delivery_target(db: sqlite3.Connection, api_url: str) -> None:
    validate_api_url(api_url)
    digest = hashlib.sha256(api_url.rstrip('/').encode()).hexdigest()
    old = db.execute('SELECT target_sha256 FROM delivery_target WHERE id=1').fetchone()
    if old and old[0] != digest:
        raise RuntimeError('queue is bound to a different endpoint; preserve checkpoints and use a fresh queue')
    with db:
        db.execute('INSERT OR IGNORE INTO delivery_target VALUES(1,?)',(digest,))


def ensure_delivery_safe(db: sqlite3.Connection) -> None:
    if db.execute("SELECT count(*) FROM collector_blockers").fetchone()[0]:
        raise RuntimeError('unresolved collector blockers; delivery disabled')


def deliver(db: sqlite3.Connection, api_url: str, key: str, limit: int = 100) -> dict[str, int]:
    ensure_delivery_safe(db)
    pin_delivery_target(db,api_url)
    counts = {'sent': 0, 'conflicts': 0, 'remaining': 0}
    # Never send rows queued by an incompatible older adapter. Preserve them
    # until a separately validated migration or reconciliation replaces them.
    for (body,) in db.execute("SELECT payload FROM pending_usage WHERE status='pending'"):
        item = json.loads(body)
        if item.get('parser_version') != PARSER_VERSION or 'raw_usage_json' not in item:
            raise RuntimeError('outdated queued usage requires audited migration before delivery')
    auth = 'Basic ' + base64.b64encode(key.encode()).decode()
    for source, ident, session, body in db.execute('SELECT source,source_key,session_key,payload FROM pending_usage WHERE status=? ORDER BY rowid LIMIT ?',
                                          ('pending',limit)).fetchall():
        request = urllib.request.Request(api_url.rstrip('/')+'/usage-write/events', data=('['+body+']').encode(),
            headers={'Authorization': auth, 'Content-Type': 'application/json'}, method='POST')
        try:
            with open_request(request, timeout=20) as response:
                result = json.load(response)
                if result.get('inserted',0)+result.get('updated',0)+result.get('unchanged',0) != 1:
                    raise ValueError('missing acknowledgement for usage event')
        except urllib.error.HTTPError as e:
            code = e.code
            e.close()
            if code == 409:
                with db:
                    db.execute('UPDATE pending_usage SET status=?,last_error=? WHERE source=? AND source_key=? AND session_key=?',
                               ('conflict', 'remote identity conflict',source,ident,session))
                counts['conflicts'] += 1
                continue
            # Do not advance queue checkpoints on authentication/network/server errors.
            raise RuntimeError(f'usage delivery failed with HTTP {code}') from None
        with db:
            db.execute('UPDATE pending_usage SET status=?,last_error=? WHERE source=? AND source_key=? AND session_key=?',
                       ('sent','',source,ident,session))
        counts['sent'] += 1
    counts['remaining'] = db.execute("SELECT count(*) FROM pending_usage WHERE status='pending'").fetchone()[0]
    return counts


def deliver_bulk(db: sqlite3.Connection, api_url: str, key: str,
                 limit: int = 1000, batch_size: int = 100) -> dict[str, int]:
    """Checkpoint complete batches; resolve a 409 one event at a time.

    A lost batch acknowledgement is harmless: the server treats every event as
    idempotent. Never advance checkpoints on 400/401/5xx or a missing ACK.
    """
    ensure_delivery_safe(db)
    if limit < 1 or batch_size < 1 or batch_size > 200:
        raise ValueError('invalid bulk delivery limits')
    pin_delivery_target(db,api_url)
    for (body,) in db.execute("SELECT payload FROM pending_usage WHERE status='pending'"):
        item = json.loads(body)
        if item.get('parser_version') != PARSER_VERSION or 'raw_usage_json' not in item:
            raise RuntimeError('outdated queued usage requires audited migration before delivery')
    counts = {'sent': 0, 'conflicts': 0, 'remaining': 0}
    rows = db.execute('SELECT source,source_key,session_key,payload FROM pending_usage WHERE status=? ORDER BY rowid LIMIT ?',
                      ('pending',limit)).fetchall()
    auth = 'Basic ' + base64.b64encode(key.encode()).decode()
    for start in range(0,len(rows),batch_size):
        batch = rows[start:start+batch_size]
        payload = '['+','.join(row[3] for row in batch)+']'
        request = urllib.request.Request(api_url.rstrip('/')+'/usage-write/events',
            data=payload.encode(), headers={'Authorization':auth,'Content-Type':'application/json'}, method='POST')
        try:
            with open_request(request,timeout=30) as response:
                ack = json.load(response)
                if sum(ack.get(k,0) for k in ('inserted','updated','unchanged')) != len(batch):
                    raise ValueError('incomplete bulk acknowledgement')
        except urllib.error.HTTPError as error:
            code=error.code;error.close()
            if code != 409:
                raise RuntimeError(f'bulk usage delivery failed with HTTP {code}') from None
            # Server commits the unconflicted subset. Re-send singly to identify
            # the conflicting identity; replayed peers are acknowledged unchanged.
            for _ in batch:
                result=deliver(db,api_url,key,1)
                counts['sent']+=result['sent']
                counts['conflicts']+=result['conflicts']
            continue
        with db:
            for source,ident,session,_ in batch:
                db.execute('UPDATE pending_usage SET status=?,last_error=? WHERE source=? AND source_key=? AND session_key=?',
                           ('sent','',source,ident,session))
        counts['sent']+=len(batch)
    counts['remaining']=db.execute("SELECT count(*) FROM pending_usage WHERE status='pending'").fetchone()[0]
    return counts


def main() -> int:
    cli = argparse.ArgumentParser(description=__doc__)
    cli.add_argument('--machine', required=True)
    cli.add_argument('--since-ms', type=int, required=True, help='audited cutover lower bound (milliseconds UTC)')
    cli.add_argument('--queue', type=Path, required=True)
    cli.add_argument('--send', action='store_true', help='enable delivery to Wakapi (default is queue only)')
    cli.add_argument('--wakatime-config', type=Path, default=Path.home()/'.wakatime.cfg')
    cli.add_argument('--limit', type=int, default=1000, help='bounded maximum delivered per pass (up to 10000)')
    args = cli.parse_args()
    if args.since_ms <= 0 or args.limit < 1 or args.limit > 10_000:
        cli.error('invalid since-ms or limit')
    args.queue.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(args.queue.parent, 0o700)
    # Nonblocking process lock: two timers must never advance the queue together.
    with (args.queue.parent/'collector.lock').open('a+') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            print('collector already running', file=sys.stderr)
            return 75  # transient lock contention, distinct from a failed accounting gate
        db = sqlite3.connect(args.queue)
        os.chmod(args.queue,0o600)
        db.execute('PRAGMA busy_timeout=5000')
        db.execute('PRAGMA journal_mode=WAL')
        home = Path.home();issues: list[str] = [];notes: list[str] = []
        events = list(scan_live(home,args.machine,issues,notes))
        staged = stage(db,events,args.since_ms)
        result: dict = {'staged': staged, 'source_issues': len(issues), 'bounded_classification_notes': len(notes),
                        'delivery_blocked': bool(issues or staged['conflicts'])}
        if args.send and not issues and not staged['conflicts']:
            config = configparser.ConfigParser(interpolation=None)
            if not config.read(args.wakatime_config):
                cli.error('WakaTime config not found')
            api_url = config.get('settings','api_url')
            key = config.get('settings','api_key')
            result['delivery'] = deliver_bulk(db,api_url,key,args.limit,100)
        result['unresolved'] = (db.execute("SELECT count(*) FROM pending_usage WHERE status='conflict'").fetchone()[0]
                                + db.execute('SELECT count(*) FROM collector_blockers').fetchone()[0])
        db.close()
        print(json.dumps(result))  # Counts only; no credentials, raw transcript, or source identity.
        return 1 if result['unresolved'] or issues else 0


if __name__ == '__main__':
    sys.exit(main())
