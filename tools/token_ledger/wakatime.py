"""Raw WakaTime export as lower-priority, per-session observations.

Never add WakaTime counts to transcript counts for the same session. Before
6 Aug 2026 input includes an unknown share of cache reads: it is deliberately
recorded in unknown_input_tokens, not incorrectly priced as uncached input.
"""
from __future__ import annotations

from collections.abc import Iterator
import json
from pathlib import Path

from .sources import Usage, canonical, number

CACHE_SPLIT_FROM_MS = 1785974400000  # 6 Aug 2026, 00:00 UTC
TARGET_EDITORS = {'claude code', 'codex', 'codex cli', 'codex vscode',
                  'opencode', 'opencode cli', 'pi'}


def machine_label(value: str) -> str:
    if value in ('Mac', 'Mac.localdomain', 'Andrews-Macbook-Air.local', 'Pythia.local'):
        return 'Pythia'
    return value or 'unknown'


def observations(export: Path, user_agents: Path, machines: Path,
                 cutoff_ms: int, issues: list[str],
                 unattributed: dict[str, int] | None = None) -> Iterator[Usage]:
    if cutoff_ms <= 0:
        raise ValueError('fixed UTC cutoff required')
    data = json.loads(export.read_text())
    agents = {str(row['id']): row for row in json.loads(user_agents.read_text())}
    names = {str(row['id']): row for row in json.loads(machines.read_text())}
    skipped_keys: set[str] = set()
    for day in data['days']:
        for h in day['heartbeats']:
            try:
                at = int(float(h['time'])*1000)
                if at >= cutoff_ms:
                    continue
                raw = {k: number(h.get(k)) for k in
                    ('ai_input_tokens', 'ai_cached_input_tokens', 'ai_output_tokens')}
                if not any(raw.values()):
                    continue
                key = str(h.get('id') or '')
                session = str(h.get('ai_session') or '')
                if not key or not session:
                    issues.append(f'wakatime:{key or "missing"}: AI usage without heartbeat/session ID')
                    continue
                agent = agents.get(str(h.get('user_agent_id'))) or {}
                host = names.get(str(h.get('machine_name_id'))) or {}
                # An unmatched heartbeat from another editor is not evidence of
                # usage by one of the four target tools. Exclude it fail-closed,
                # including when its user-agent metadata is missing.
                if str(agent.get('editor') or '').lower() not in TARGET_EDITORS:
                    if unattributed is not None and key not in skipped_keys:
                        skipped_keys.add(key)
                        unattributed['events'] = unattributed.get('events', 0) + 1
                        unattributed['tokens'] = unattributed.get('tokens', 0) + sum(raw.values())
                    continue
                if str(h.get('category') or '').lower() != 'ai coding':
                    issues.append(f'wakatime:{key}: token usage outside AI coding category')
                model = str(agent.get('ai_model') or '')
                version = str(agent.get('ai_model_version') or '')
                if version:
                    model = (model + ' ' + version).strip()
                unclassified = raw['ai_input_tokens'] if at < CACHE_SPLIT_FROM_MS else 0
                inp = 0 if unclassified else raw['ai_input_tokens']
                yield canonical('wakatime', key, at, machine_label(str(host.get('value') or '')),
                    str(h.get('project') or ''), model, inp, raw['ai_cached_input_tokens'], 0,
                    raw['ai_output_tokens'], 0, raw, session_key=session,
                    unknown_input=unclassified)
            except (ValueError, TypeError, KeyError) as err:
                issues.append(f'wakatime:{h.get("id") or "missing"}: invalid usage ({err})')
