"""Collector checkpoint, retry, correction, and conflict regression tests."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import sqlite3
import threading
import unittest

from .collect import deliver, deliver_bulk, stage
from .sources import canonical


class FakeServer(BaseHTTPRequestHandler):
    failures = 0
    accepted = set()

    def log_message(self, *args):
        pass

    def do_POST(self):
        if FakeServer.failures:
            FakeServer.failures -= 1
            self.send_response(503);self.end_headers();return
        length=int(self.headers.get('content-length',0))
        events=json.loads(self.rfile.read(length))
        inserted=unchanged=0
        for event in events:
            key=(event['source'],event['source_key'],event['raw_sha256'])
            duplicate=key in FakeServer.accepted
            FakeServer.accepted.add(key)
            inserted+=not duplicate;unchanged+=duplicate
        body=json.dumps({'inserted':inserted,'unchanged':unchanged,'updated':0}).encode()
        self.send_response(200);self.end_headers();self.wfile.write(body)


class CollectorTests(unittest.TestCase):
    def test_bulk_retry_after_lost_ack(self):
        db=sqlite3.connect(':memory:');at=1791759696000
        events=[canonical('pi',str(n),at+n,'mac','','m',1,100,0,1,0,session_key='s') for n in range(11)]
        stage(db,events,at)
        server=ThreadingHTTPServer(('127.0.0.1',0),FakeServer)
        t=threading.Thread(target=server.serve_forever,daemon=True);t.start()
        try:
            FakeServer.failures=1;FakeServer.accepted=set()
            url=f'http://127.0.0.1:{server.server_port}'
            with self.assertRaises(RuntimeError):deliver_bulk(db,url,'test-key',11,5)
            self.assertEqual(db.execute("select count(*) from pending_usage where status='pending'").fetchone()[0],11)
            result=deliver_bulk(db,url,'test-key',11,5)
            self.assertEqual((result['sent'],result['remaining']),(11,0))
            with db:db.execute("update pending_usage set status='pending' where source_key='0'")
            self.assertEqual(deliver_bulk(db,url,'test-key',1,5)['sent'],1)
            self.assertEqual(len(FakeServer.accepted),11)
        finally:
            server.shutdown();server.server_close();t.join();db.close()

    def test_copied_session_is_queued_as_second_reference(self):
        db=sqlite3.connect(':memory:');at=1791759696000
        a=canonical('pi','one',at,'Pythia','','m',1,100,0,1,0,session_key='a')
        b=canonical('pi','one',at,'omphalos','','m',1,100,0,1,0,session_key='b')
        self.assertEqual(a.fingerprint,b.fingerprint)
        self.assertEqual(stage(db,[a,b],at)['new'],2)
        self.assertEqual(db.execute('select count(*) from pending_usage').fetchone()[0],2)
        self.assertEqual(stage(db,[a,b],at)['new'],0)
        db.close()

    def test_mode_switch_is_persistently_blocked_before_any_insert(self):
        db=sqlite3.connect(':memory:');at=1791759696000
        initial=canonical('codex','s:0:counter',at,'mac','','m',1,100,0,1,0,session_key='s')
        stage(db,[initial],at)
        with db:db.execute("UPDATE pending_usage SET status='sent'")
        other=canonical('pi','safe',at,'mac','','m',1,0,0,1,0)
        changed=canonical('codex','s:response:one',at,'mac','','m',1,100,0,1,0,session_key='s')
        with self.assertRaisesRegex(RuntimeError,'representation changed'):
            stage(db,[other,changed],at)
        self.assertEqual(db.execute('select count(*) from pending_usage').fetchone()[0],1)
        with self.assertRaisesRegex(RuntimeError,'collector blockers'):
            deliver_bulk(db,'http://127.0.0.1:1/api','dummy')
        db.close()

    def test_old_sent_parser_rows_require_explicit_migration(self):
        db=sqlite3.connect(':memory:');at=1791759696000
        e=canonical('pi','msg',at,'mac','','m',1,0,0,1,0)
        stage(db,[e],at)
        with db:db.execute("update pending_usage set status='sent',payload=json_set(payload,'$.parser_version',1)")
        with self.assertRaisesRegex(RuntimeError,'outdated queue'):
            stage(db,[e],at)
        db.close()

    def test_outdated_queue_fails_closed(self):
        db=sqlite3.connect(':memory:')
        at=1791759696000
        stage(db,[canonical('pi','old',at,'mac','','m',1,0,0,1,0)],at)
        with db:
            db.execute("UPDATE pending_usage SET payload=json_remove(payload,'$.raw_usage_json')")
        with self.assertRaisesRegex(RuntimeError,'outdated queued usage'):
            deliver(db,'http://127.0.0.1:1/api','dummy-key')
        self.assertEqual(db.execute("select status from pending_usage").fetchone()[0],'pending')
        db.close()

    def test_retry_queue_and_late_revision(self):
        db=sqlite3.connect(':memory:');at=1791759696000
        initial=canonical('claude','msg:request',at,'mac','wakapi','opus',2,30,5,1,0)
        self.assertEqual(stage(db,[initial],at)['new'],1)
        self.assertEqual(stage(db,[initial],at)['unchanged'],1)
        server=ThreadingHTTPServer(('127.0.0.1',0),FakeServer)
        t=threading.Thread(target=server.serve_forever,daemon=True);t.start()
        try:
            FakeServer.failures=1;FakeServer.accepted=set()
            url=f'http://127.0.0.1:{server.server_port}'
            with self.assertRaises(RuntimeError):deliver(db,url,'test-key')
            self.assertEqual(db.execute("select status from pending_usage").fetchone()[0],'pending')
            self.assertEqual(deliver(db,url,'test-key')['sent'],1)
            revision=canonical('claude','msg:request',at+1000,'mac','wakapi','opus',2,30,5,50,0)
            self.assertEqual(stage(db,[revision],at)['revised'],1)
            self.assertEqual(deliver(db,url,'test-key')['sent'],1)
            self.assertEqual(db.execute("select status from pending_usage").fetchone()[0],'sent')
            regression=canonical('claude','msg:request',at+2000,'mac','wakapi','opus',2,20,5,50,0)
            self.assertEqual(stage(db,[regression],at)['conflicts'],1)
            self.assertEqual(db.execute("select status from pending_usage").fetchone()[0],'conflict')
        finally:
            server.shutdown();server.server_close();t.join();db.close()


if __name__=='__main__':unittest.main()
