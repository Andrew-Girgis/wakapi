"""Read-only, versioned transcript adapters. No API calls and no source mutations."""
from __future__ import annotations

from collections import defaultdict
from dataclasses import dataclass
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import sqlite3
from typing import Iterator

PARSER_VERSION = 9


@dataclass(frozen=True)
class Usage:
    source: str
    key: str
    session_key: str
    occurred_ms: int
    machine: str
    project: str
    model: str
    input: int
    cache_read: int
    cache_write: int
    unknown_input: int
    output: int
    reasoning: int
    fingerprint: str
    raw_usage_json: str

    @property
    def total(self) -> int:
        return self.input + self.cache_read + self.cache_write + self.unknown_input + self.output


def stamp(value: str | int | None) -> int:
    if isinstance(value, int):
        return value
    if not value:
        raise ValueError('missing timestamp')
    return int(datetime.fromisoformat(value.replace('Z', '+00:00')).timestamp() * 1000)


def number(value: object) -> int:
    if value is None:
        return 0
    if isinstance(value, bool) or not isinstance(value, (int, str)):
        raise ValueError('token count must be an integer')
    result = int(value)
    if result < 0:
        raise ValueError('negative token count')
    return result


def project_name(directory: str | None) -> str:
    if not directory or directory in ('/', str(Path.home())):
        return ''
    return Path(directory).name


def usage_numbers(value: dict) -> dict:
    """Retain source-reported numeric accounting only, never prompts or secrets."""
    return {k: (usage_numbers(v) if isinstance(v, dict) else v) for k, v in value.items()
            if isinstance(v, dict) or (isinstance(v, int) and not isinstance(v, bool))}


def canonical(source: str, key: str, at: int, machine: str, project: str,
              model: str, inp: int, read: int, write: int, out: int, reasoning: int,
              raw_usage: dict | None = None, *, session_key: str = '', unknown_input: int = 0) -> Usage:
    if not key or at <= 0 or min(inp, read, write, unknown_input, out, reasoning) < 0:
        raise ValueError(f'invalid usage: {source} {key}')
    # Reasoning is a gross source metric. After a provider's negative output
    # adjustment it can exceed net output; it is never added to total again.
    raw_json = json.dumps(usage_numbers(raw_usage or {}), sort_keys=True, separators=(',', ':'))
    # Machine is excluded: a copied transcript must have the same identity and accounting.
    # Forks/copies may carry the identical usage event in a different session.
    # Its fingerprint must remain stable; all session references are stored separately.
    identity_session = session_key if source == 'wakatime' else ''
    # Claude copies of a message may be reopened from another working directory.
    # Project labels belong to the first observed event, not its usage identity.
    identity_project = '' if source == 'claude' else project
    fields = [source, key, identity_session, at, identity_project, model, inp, read, write, unknown_input, out, reasoning, raw_json]
    digest = hashlib.sha256(json.dumps(fields, separators=(',', ':')).encode()).hexdigest()
    return Usage(source, key, session_key, at, machine, project, model, inp, read, write, unknown_input, out, reasoning, digest, raw_json)


def records(directory: Path, issues: list[str]) -> Iterator[tuple[Path, dict]]:
    for path in sorted(directory.rglob('*.jsonl')):
        with path.open('r', errors='replace') as stream:
            for line_no, line in enumerate(stream, 1):
                try:
                    item = json.loads(line)
                    if isinstance(item, dict):
                        yield path, item
                except (UnicodeError, json.JSONDecodeError) as e:
                    issues.append(f'{path.name}:{line_no}: malformed JSON ({type(e).__name__})')


def claude(root: Path, machine: str, issues: list[str]) -> Iterator[Usage]:
    latest: dict[tuple[str, str], tuple[str, Usage]] = {}
    revisions: set[tuple[str, str]] = set()
    for path, item in records(root, issues):
        msg = item.get('message') or {}
        if msg.get('role') != 'assistant' or not isinstance(msg.get('usage'), dict):
            continue
        u = msg['usage']
        key = msg.get('id') or item.get('uuid')
        if not key:
            issues.append(f'{path.name}: Claude usage without id')
            continue
        key = str(key) + ':' + str(item.get('requestId') or '')
        try:
            event = canonical('claude', key, stamp(item.get('timestamp')), machine,
                project_name(item.get('cwd')), str(msg.get('model') or ''),
                number(u.get('input_tokens')), number(u.get('cache_read_input_tokens')),
                number(u.get('cache_creation_input_tokens')), number(u.get('output_tokens')),
                number((u.get('output_tokens_details') or {}).get('thinking_tokens')), u,
                session_key=str(item.get('sessionId') or item.get('session_id') or ''))
        except (ValueError, TypeError) as e:
            issues.append(f'{path.name}: invalid Claude usage ({e})')
            continue
        identity = (key, event.session_key)
        old = latest.get(identity)
        if old and old[1].fingerprint != event.fingerprint:
            # Streaming Claude messages commonly repeat the *same* message ID with an
            # increasing output count. Count the final snapshot, never both snapshots.
            previous = old[1]
            if event.occurred_ms >= previous.occurred_ms:
                if any(getattr(event, f) < getattr(previous, f) for f in
                       ('input', 'cache_read', 'cache_write', 'output')) and identity not in revisions:
                    issues.append(f'claude:{key}: non-monotonic usage revision; review final value')
                    revisions.add(identity)
        if old is None or event.occurred_ms > old[1].occurred_ms or (
                event.occurred_ms == old[1].occurred_ms and event.total >= old[1].total):
            latest[identity] = (str(item.get('timestamp')), event)
    yield from (pair[1] for pair in latest.values())


def pi(root: Path, machine: str, issues: list[str]) -> Iterator[Usage]:
    # Pi message IDs are stable across branches; copied histories are deduped by this ID.
    directories: dict[Path, str] = {}
    sessions: dict[Path, str] = {}
    for path, item in records(root, issues):
        if item.get('type') == 'session':
            directories[path] = project_name(item.get('cwd'))
            sessions[path] = str(item.get('id') or '')
        if item.get('type') != 'message':
            continue
        msg = item.get('message') or {}
        if msg.get('role') != 'assistant' or not isinstance(msg.get('usage'), dict):
            continue
        u = msg['usage']
        try:
            event = canonical('pi', str(item.get('id') or ''), stamp(item.get('timestamp')),
                machine, directories.get(path, ''), str(msg.get('model') or ''), number(u.get('input')),
                number(u.get('cacheRead')), number(u.get('cacheWrite')),
                number(u.get('output')), number(u.get('reasoning')), u,
                session_key=sessions.get(path, ''))
            if u.get('totalTokens') is not None and event.total != number(u['totalTokens']):
                issues.append(f'pi:{event.key}: reported total differs from sum of buckets')
            yield event
        except (ValueError, TypeError) as e:
            issues.append(f'{path.name}: invalid Pi usage ({e})')


CODEX_BUCKETS = ('input_tokens', 'cached_input_tokens', 'cache_write_input_tokens', 'output_tokens')
CODEX_RECORD_BUCKETS = CODEX_BUCKETS + ('reasoning_output_tokens', 'total_tokens')


def codex_response_usage(session: str, machine: str, samples: list[tuple],
                         records: list[tuple[int, int, dict, str, str]]) -> list[Usage] | None:
    """Use complete, independently persisted per-response usage, never its running totals."""
    if not records:
        return None
    by_thread: dict[str, dict[str, int]] = defaultdict(lambda: {k: 0 for k in CODEX_RECORD_BUCKETS})
    by_turn: dict[tuple[str, str], dict[str, int]] = defaultdict(lambda: {k: 0 for k in CODEX_RECORD_BUCKETS})
    identities: dict[tuple[str, str], tuple] = {}
    events = []
    try:
        for at, _, record, project, model in sorted(records, key=lambda row: (row[0], row[1])):
            thread = str(record.get('thread_id') or '')
            response = str(record.get('response_id') or '')
            turn = str(record.get('turn_id') or '')
            # session_meta.id is a thread ID; runtime session_id may differ.
            # Child-thread records may retain the parent session association.
            if not thread or not response or not turn or (
                    thread != session and str(record.get('session_id')) != session):
                return None
            u = record['usage']
            values = {k: number(u.get(k)) for k in CODEX_RECORD_BUCKETS}
            if values['cached_input_tokens'] + values['cache_write_input_tokens'] > values['input_tokens']:
                return None
            if values['reasoning_output_tokens'] > values['output_tokens'] or (
                values['total_tokens'] != values['input_tokens'] + values['output_tokens']):
                return None
            ident = (thread, response)
            signature = (tuple(values.values()),
                json.dumps(record['turn_token_usage'], sort_keys=True),
                json.dumps(record['thread_token_usage'], sort_keys=True))
            if ident in identities:
                if identities[ident] != signature:
                    return None
                continue  # copied rollout, not another model response
            identities[ident] = signature
            for k, v in values.items():
                by_thread[thread][k] += v
                by_turn[(thread, turn)][k] += v
            if any(by_thread[thread][k] != number(record['thread_token_usage'].get(k)) or
                   by_turn[(thread, turn)][k] != number(record['turn_token_usage'].get(k))
                   for k in CODEX_RECORD_BUCKETS):
                return None  # missing record, reordering, or seeded counter
            key = session + ':response:' + hashlib.sha256((thread+'\0'+response).encode()).hexdigest()[:24]
            events.append(canonical('codex', key, at, machine, project, model,
                values['input_tokens']-values['cached_input_tokens']-values['cache_write_input_tokens'],
                values['cached_input_tokens'], values['cache_write_input_tokens'],
                values['output_tokens'], values['reasoning_output_tokens'],
                {'usage': u, 'turn': record['turn_token_usage'], 'thread': record['thread_token_usage']},
                session_key=session))
        # Old unrecorded usage cannot be discarded: the complete response ledger
        # must cover every source-reported cumulative bucket from this session.
        distinct_deltas = {(tuple(number(s[1].get(k)) for k in CODEX_BUCKETS),
                            tuple(number(s[2].get(k)) for k in CODEX_BUCKETS)) for s in samples}
        for i, k in enumerate(CODEX_BUCKETS):
            total = sum(t[k] for t in by_thread.values())
            if total < max((state[i] for state, _ in distinct_deltas), default=0) or total < sum(
                    delta[i] for _, delta in distinct_deltas):
                return None
        return events
    except (ValueError, TypeError, KeyError, AttributeError):
        return None


def codex_fork_usage(session: str, machine: str, samples: list[tuple]) -> list[Usage] | None:
    """Recover interleaved forks only if every distinct counter has a proven parent.

    `last_token_usage` is counted once for a NEW cumulative state; a repeated
    last value on a rate-limit-only snapshot has no new state and adds zero.
    """
    nodes: dict[tuple[int, ...], tuple[int, tuple[int, ...], dict, dict, str, str]] = {}
    all_states: set[tuple[int, ...]] = set()
    try:
        for at, cumulative, last, _, project, model in sorted(samples, key=lambda row: row[0]):
            state = tuple(number(cumulative.get(k)) for k in CODEX_BUCKETS)
            delta = tuple(number(last.get(k)) for k in CODEX_BUCKETS)
            all_states.add(state)
            if not any(delta):
                continue
            if delta[1] + delta[2] > delta[0] or any(d > s for d, s in zip(delta, state)):
                return None
            if state in nodes and nodes[state][1] != delta:
                return None
            nodes.setdefault(state, (at, delta, cumulative, last, project, model))
        zero = (0,) * len(CODEX_BUCKETS)
        if not nodes or all_states - set(nodes) - {zero}:
            return None  # an unexplained state is not evidence of usage
        roots = 0
        for state, (at, delta, *_rest) in nodes.items():
            parent = tuple(s-d for s, d in zip(state, delta))
            if parent == zero:
                roots += 1
            elif parent not in nodes or nodes[parent][0] > at:
                return None
        if roots != 1:
            return None  # more than one root may be unrelated, overlapping epochs
        events = []
        for state, (at, delta, cumulative, last, project, model) in nodes.items():
            key = session + ':fork:' + hashlib.sha256(json.dumps(state).encode()).hexdigest()[:24]
            reasoning = number(last.get('reasoning_output_tokens'))
            if reasoning > delta[3]:
                return None
            events.append(canonical('codex', key, at, machine, project, model,
                delta[0]-delta[1]-delta[2], delta[1], delta[2], delta[3], reasoning,
                {'cumulative': cumulative, 'last': last}, session_key=session))
        return events
    except (ValueError, TypeError, KeyError):
        return None


def codex(root: Path | list[Path], machine: str, issues: list[str]) -> Iterator[Usage]:
    snapshots: dict[str, list[tuple[int, dict, dict, str, str, str]]] = defaultdict(list)
    response_records: dict[str, list[tuple[int, int, dict, str, str]]] = defaultdict(list)
    roots = [root] if isinstance(root, Path) else root
    for path in sorted({path for directory in roots for path in directory.rglob('*.jsonl')}):
        session = ''
        project = ''
        model = ''
        with path.open('r', errors='replace') as stream:
            for line_no, line in enumerate(stream, 1):
                try:
                    item = json.loads(line)
                except (UnicodeError, json.JSONDecodeError) as e:
                    issues.append(f'{path.name}:{line_no}: malformed JSON ({type(e).__name__})')
                    continue
                payload = item.get('payload') or {}
                if item.get('type') == 'session_meta':
                    session = str(payload.get('id') or '')
                    project = project_name(payload.get('cwd'))
                if item.get('type') == 'turn_context':
                    project = project_name(payload.get('cwd')) or project
                    model = str(payload.get('model') or model)
                if item.get('type') == 'token_usage_record' and session:
                    try:
                        response_records[session].append((stamp(item.get('timestamp')),
                            number(item.get('ordinal') or line_no), payload, project, model))
                    except (TypeError, ValueError):
                        issues.append(f'{path.name}:{line_no}: invalid per-response usage timestamp')
                    continue
                if payload.get('type') != 'token_count':
                    continue
                cumulative = (payload.get('info') or {}).get('total_token_usage')
                if not isinstance(cumulative, dict):
                    continue
                if not session:
                    issues.append(f'{path.name}:{line_no}: Codex usage without session ID')
                    continue
                try:
                    last = (payload.get('info') or {}).get('last_token_usage') or {}
                    snapshots[session].append((stamp(item.get('timestamp')), cumulative, last, path.name, project, model))
                except (TypeError, ValueError) as e:
                    issues.append(f'{path.name}:{line_no}: invalid timestamp ({e})')
    for session in snapshots.keys() | response_records.keys():
        samples = snapshots.get(session, [])
        ordered = sorted(samples, key=lambda row: row[0])
        recorded = response_records.get(session, [])
        # Select by available evidence, not by whether a regression has appeared
        # yet. A later fork must not re-key usage already delivered from a prefix.
        if recorded:
            recovered = codex_response_usage(session, machine, ordered, recorded)
            if recovered is None:
                issues.append(f'codex:{session}: incomplete/conflicting per-response usage; retry or reconcile')
            else:
                yield from recovered
            continue
        recovered = codex_fork_usage(session, machine, ordered)
        if recovered is not None:
            yield from recovered
            continue
        previous_state = {k: 0 for k in CODEX_BUCKETS}
        signatures: set[tuple[int, ...]] = set()
        ambiguous = False
        try:
            for _, cumulative, last, *_ in ordered:
                now = {k: number(cumulative.get(k)) for k in previous_state}
                signature = tuple(now.values())
                if signature in signatures:
                    continue
                signatures.add(signature)
                if any(now[k] < previous_state[k] for k in previous_state):
                    if all(now[k] == number(last.get(k)) for k in previous_state):
                        previous_state = {k: 0 for k in previous_state}
                    else:
                        ambiguous = True
                previous_state = now
        except (ValueError, TypeError):
            pass  # original parser below reports the invalid counter
        if ambiguous:
            issues.append(f'codex:{session}: cumulative counter reset/regression; manual reconciliation required')
            continue  # no partial high-water-mark masquerading as complete usage
        previous = {'input_tokens': 0, 'cached_input_tokens': 0,
                    'cache_write_input_tokens': 0, 'output_tokens': 0}
        seen = set()
        epoch = 0
        reset_reported = False
        for at, cumulative, last, filename, project, model in sorted(samples, key=lambda row: row[0]):
            try:
                now = {k: number(cumulative.get(k)) for k in previous}
            except (ValueError, TypeError) as e:
                issues.append(f'{filename}: invalid Codex counter ({e})')
                continue
            signature = tuple(now.values())
            if signature in seen:
                continue
            seen.add(signature)
            if any(now[k] < previous[k] for k in previous):
                # A genuine new epoch begins with exactly the last turn's usage
                # in every bucket. This is independently checkable from Codex's
                # last_token_usage; arbitrary decreases/branch interleavings are
                # not safe to reinterpret as resets.
                if all(now[k] == number(last.get(k)) for k in previous):
                    epoch += 1
                    previous = {k: 0 for k in previous}
                    seen = {signature}
                else:
                    if not reset_reported:
                        issues.append(f'codex:{session}: cumulative counter reset/regression; manual reconciliation required')
                        reset_reported = True
                    continue
            change = {k: now[k] - previous[k] for k in previous}
            previous = now
            if not any(change.values()):
                continue
            # Codex input is inclusive of cached input. Reasoning is included in output.
            inp = change['input_tokens'] - change['cached_input_tokens'] - change['cache_write_input_tokens']
            if inp < 0:
                issues.append(f'codex:{session}: cached input exceeds inclusive input')
                continue
            key = session + ':' + str(epoch) + ':' + hashlib.sha256(json.dumps(signature).encode()).hexdigest()[:24]
            try:
                yield canonical('codex', key, at, machine, project, model, inp,
                    change['cached_input_tokens'], change['cache_write_input_tokens'],
                    change['output_tokens'], 0, {'cumulative': cumulative, 'last': last},
                    session_key=session)
            except ValueError as e:
                issues.append(f'{filename}: invalid Codex usage ({e})')


def opencode(database: Path, machine: str, issues: list[str],
             classification_notes: list[str] | None = None) -> Iterator[Usage]:
    db = sqlite3.connect(f'file:{database}?mode=ro', uri=True)
    try:
        for ident, at, data, directory, session in db.execute('SELECT m.id, m.time_created, m.data, s.directory, m.session_id FROM message m LEFT JOIN session s ON s.id=m.session_id'):
            try:
                msg = json.loads(data)
                if msg.get('role') != 'assistant' or not isinstance(msg.get('tokens'), dict):
                    continue
                u = msg['tokens'];cache = u.get('cache') or {}
                inp = number(u.get('input'));read = number(cache.get('read'))
                write = number(cache.get('write'));reasoning = number(u.get('reasoning'))
                # OpenCode has used both conventions: reasoning included in output,
                # or additional to output (occasionally with a negative net output).
                # Its reported total disambiguates; do not assume one version.
                out = int(u.get('output') or 0)
                reported = u.get('total')
                base = inp + read + write + out
                if reported is None:
                    # OpenCode can create unfinished assistant placeholders with no usage.
                    if base == reasoning == 0:
                        continue
                    issues.append(f'opencode:{ident}: nonzero usage without reported total')
                    continue
                if number(reported) == base and out >= 0:
                    # Reasoning already included in output; source can include
                    # more gross reasoning than net output after corrections.
                    pass
                elif number(reported) == base + reasoning and out + reasoning >= 0:
                    out += reasoning
                elif number(reported) >= inp + read + write:
                    # Source's reported total fixes the numerical accounting;
                    # its output/reasoning classification may still be unclear.
                    out = number(reported) - inp - read - write
                    corroborated = False
                    if classification_notes is not None:
                        try:
                            for (part_data,) in db.execute('SELECT data FROM part WHERE message_id=?', (ident,)):
                                part = json.loads(part_data)
                                if part.get('type') == 'step-finish' and part.get('tokens') == u:
                                    corroborated = True
                                    break
                        except (sqlite3.OperationalError, ValueError, TypeError):
                            pass
                    if corroborated:
                        classification_notes.append(f'opencode:{ident}: source total corroborated by step-finish; '
                            f'output/reasoning classification differs by {abs(out - (int(u.get("output") or 0) + reasoning))} tokens')
                    else:
                        issues.append(f'opencode:{ident}: total requires unexplained output correction')
                else:
                    issues.append(f'opencode:{ident}: total cannot be reconciled')
                    continue
                event = canonical('opencode', ident, number(at), machine, project_name(directory),
                    str(msg.get('modelID') or (msg.get('model') or {}).get('id') or ''),
                    inp, read, write, out, reasoning, u, session_key=session)
                yield event
            except (ValueError, TypeError, KeyError, AttributeError) as e:
                issues.append(f'opencode:{ident}: invalid usage ({e})')
    finally:
        db.close()


def scan(source_dir: Path, machine: str, issues: list[str],
         classification_notes: list[str] | None = None) -> Iterator[Usage]:
    for name, adapter in (('claude', claude), ('pi', pi)):
        if (source_dir / name).is_dir():
            yield from adapter(source_dir / name, machine, issues)
    roots = [source_dir/name for name in ('codex','codex-archive') if (source_dir/name).is_dir()]
    if roots:
        yield from codex(roots, machine, issues)
    if (source_dir / 'opencode.db').exists():
        yield from opencode(source_dir / 'opencode.db', machine, issues, classification_notes)


def scan_live(home: Path, machine: str, issues: list[str], notes: list[str]) -> Iterator[Usage]:
    for adapter, root in ((claude, home/'.claude/projects'), (pi, home/'.pi/agent/sessions')):
        if root.exists():
            yield from adapter(root,machine,issues)
    codex_roots = [root for root in (home/'.codex/sessions',home/'.codex/archived_sessions') if root.exists()]
    if codex_roots:
        yield from codex(codex_roots,machine,issues)
    database = home/'.local/share/opencode/opencode.db'
    if database.exists():
        yield from opencode(database,machine,issues,notes)
