"""Audited backfill and observation gates must verify evidence, not just totals."""
import io
import json
import sqlite3
import plistlib
from pathlib import Path
import unittest
from unittest.mock import Mock, patch
from unittest.mock import patch

from .audit import evidence_digest, reconcile, summarize, transcript_references
from .backfill import audited_batch
from .collect import stage, pin_delivery_target
from .observe import compare_source_queue, verify_receipts
from .sources import canonical, PARSER_VERSION
from .gate import assess, DAY_MS
from .install import render_jobs
from .validate_image import check_running
from .daily import retry_busy


class RolloutTests(unittest.TestCase):
    def test_backfill_requires_exact_evidence_and_preserves_aliases(self):
        a=canonical('pi','id',1791759695000,'Pythia','','m',1,100,0,1,0,session_key='a')
        b=canonical('pi','id',1791759695000,'omphalos','','m',1,100,0,1,0,session_key='b')
        raw=[a,b];events,issues=reconcile(raw)
        refs=transcript_references(raw,events)
        report=summarize(events,issues,refs)
        report.update(evidence_sha256=evidence_digest(raw),classification_notes_count=0)
        batch=audited_batch(raw,[],[],report)
        self.assertEqual({e.session_key for e in batch},{'a','b'})
        # Same plausible total but different event evidence is insufficient.
        altered=canonical('pi','different-id',1791759695000,'Pythia','','m',1,100,0,1,0,session_key='a')
        with self.assertRaisesRegex(RuntimeError,'does not match'):
            audited_batch([altered],[],[],report)
        for bad in (dict(report,issues_count=1),dict(report,parser_version=PARSER_VERSION-1)):
            with self.assertRaises(RuntimeError):audited_batch(raw,[],[],bad)

    def test_queue_source_comparison_checks_window_alias_fingerprint_and_ack(self):
        db=sqlite3.connect(':memory:');at=1791759696000
        a=canonical('pi','id',at,'Pythia','','m',1,100,0,1,0,session_key='a')
        b=canonical('pi','other',at+1000,'Pythia','','m',1,100,0,1,0,session_key='b')
        stage(db,[a,b],at)
        counts,receipts=compare_source_queue(db,[a,b],at,at+1000)
        self.assertEqual(counts['source_events'],1)
        self.assertEqual(counts['queue_not_sent'],1)
        self.assertEqual(len(receipts),1)
        with db:db.execute("update pending_usage set status='sent'")
        counts,_=compare_source_queue(db,[a,b],at,at+1000)
        self.assertEqual(counts['queue_not_sent'],0)
        changed=canonical('pi','id',at,'Pythia','','m',1,200,0,1,0,session_key='a')
        counts,_=compare_source_queue(db,[changed],at,at+1000)
        self.assertEqual(counts['queue_mismatched'],1)
        db.close()

    def test_receipt_totals_require_complete_valid_acknowledgements(self):
        receipt={'source':'pi','source_key':'id','session_key':'s','raw_sha256':'a'*64}
        with patch('tools.token_ledger.observe.open_request',return_value=io.BytesIO(
                json.dumps({'matched':1,'missing':0,'mismatched':0,'conflicts':0}).encode())):
            self.assertEqual(verify_receipts('http://127.0.0.1/api','fixture',[receipt])['matched'],1)
        with patch('tools.token_ledger.observe.open_request',return_value=io.BytesIO(
                json.dumps({'matched':0,'missing':0,'mismatched':0,'conflicts':0}).encode())):
            with self.assertRaisesRegex(RuntimeError,'acknowledgement'):
                verify_receipts('http://127.0.0.1/api','fixture',[receipt])

    def test_observation_gate_requires_seven_actual_unchanged_daily_windows(self):
        start=1791849600000
        start-=start%DAY_MS
        reports=[]
        for n in range(7):
            for machine in ('Pythia','omphalos'):
                reports.append({'machine':machine,'from_ms':start+n*DAY_MS,'to_ms':start+(n+1)*DAY_MS,
                    'passed':True,'source_issues':0,'preview_matches_summary':True,'queue_missing':0,
                    'queue_mismatched':0,'queue_not_sent':0,'queue_blockers':0,'queue_status':{'sent':1},
                    'source_events':1,'receipts':{'matched':1,'missing':0,'mismatched':0,'conflicts':0},
                    'parser_version':PARSER_VERSION,'implementation_sha256':'a'*64,
                    'per_source':{('pi','codex','claude','opencode')[n%4]:{'events':1,'tokens':100}}})
        machines=['Pythia','omphalos'];now=start+7*DAY_MS
        self.assertTrue(assess(reports,machines,start,7,now)['passed'])
        self.assertFalse(assess(reports,machines,start,7,now-1)['passed'])
        self.assertFalse(assess(reports[:-1],machines,start,7,now)['passed'])
        quiet=[dict(r,per_source={'pi':{'events':1,'tokens':100}}) for r in reports]
        self.assertFalse(assess(quiet,machines,start,7,now)['passed'])
        reports[-1]['implementation_sha256']='b'*64
        self.assertFalse(assess(reports,machines,start,7,now)['passed'])
        reports[-1]['implementation_sha256']='a'*64
        reports[-1]['receipts']['mismatched']=1
        self.assertFalse(assess(reports,machines,start,7,now)['passed'])

    def test_scheduled_observation_retries_only_temporary_lock_contention(self):
        operation=Mock(side_effect=[75,75,0])
        with patch('tools.token_ledger.daily.time.sleep'):
            self.assertEqual(retry_busy(operation),0)
        self.assertEqual(operation.call_count,3)
        failed_gate=Mock(return_value=1)
        self.assertEqual(retry_busy(failed_gate),1)
        self.assertEqual(failed_gate.call_count,1)
        with patch('tools.token_ledger.daily.time.sleep'):
            self.assertEqual(retry_busy(lambda:75,attempts=2),75)

    def test_image_gate_requires_running_healthy_container(self):
        for state in ({'Status':'restarting','ExitCode':126}, {'Status':'exited','ExitCode':0}):
            with self.assertRaisesRegex(RuntimeError,'deployment blocked'):check_running(state)
        self.assertFalse(check_running({'Status':'running','Health':{'Status':'starting'}}))
        self.assertTrue(check_running({'Status':'running','Health':{'Status':'healthy'}}))

    def test_jobs_are_additive_and_do_not_enable_delivery_by_default(self):
        for platform in ('linux','darwin'):
            jobs=render_jobs(platform,'/usr/bin/python3',Path('/private/release'),'Pythia',
                             Path('/private/state'),1,DAY_MS,False)
            self.assertTrue(all('token-ledger' in name for name in jobs))
            self.assertTrue(all(b'--send' not in body for body in jobs.values()))
            self.assertTrue(all(b'api_key' not in body for body in jobs.values()))
            if platform=='darwin':
                collector=plistlib.loads(jobs['net.wakapi.token-ledger.collector.plist'])
                self.assertEqual(collector['StartInterval'],300)
                self.assertEqual(collector['Umask'],0o077)
        enabled=render_jobs('linux','/usr/bin/python3',Path('/private/release'),'omphalos',
                            Path('/private/state'),1,DAY_MS,True)
        self.assertIn(b'--send',enabled['wakapi-token-ledger-collector.service'])
        self.assertIn(b'--collect-since-ms',enabled['wakapi-token-ledger-observation.service'])
        self.assertNotIn(b'ExecStartPre',enabled['wakapi-token-ledger-observation.service'])

    def test_observation_waits_for_dns_instead_of_restarting(self):
        jobs=render_jobs('linux','/usr/bin/python3',Path('/private/release'),'omphalos',
                         Path('/private/state'),1,DAY_MS,True,'host.example.ts.net')
        observation=jobs['wakapi-token-ledger-observation.service'].decode()
        self.assertIn('getent hosts host.example.ts.net',observation)
        self.assertIn('TimeoutStartSec=15min',observation)
        self.assertNotIn('Restart=',observation)
        self.assertNotIn(b'ExecStartPre',jobs['wakapi-token-ledger-collector.service'])
        with self.assertRaises(ValueError):
            render_jobs('linux','/usr/bin/python3',Path('/r'),'m',Path('/s'),1,DAY_MS,True,'x; rm -rf /')

    def test_checkpoints_are_bound_to_one_safe_endpoint(self):
        db=sqlite3.connect(':memory:')
        stage(db,[],1)
        pin_delivery_target(db,'http://127.0.0.1:3199/api')
        with self.assertRaisesRegex(RuntimeError,'different endpoint'):
            pin_delivery_target(db,'https://production.example/api')
        for bad in ('http://production.example/api','https://user:password@example/api','https://example/api?key=secret'):
            with self.assertRaises(ValueError):pin_delivery_target(db,bad)
        db.close()


if __name__ == '__main__':
    unittest.main()
