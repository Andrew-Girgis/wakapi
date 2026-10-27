"""Fixtures for incompatible and evolving provider accounting conventions."""
from __future__ import annotations

import json
from pathlib import Path
import sqlite3
import tempfile
import unittest

from .audit import import_copy, reconcile, summarize, transcript_references
from .sources import canonical, claude, codex, opencode, pi, number
from .wakatime import observations


class LedgerTests(unittest.TestCase):
    def test_claude_streaming_deduplicates_by_message_and_request(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'session.jsonl'
            with path.open('w') as f:
                for time, out in [('2026-09-01T00:00:00Z', 1), ('2026-09-01T00:00:01Z', 50)]:
                    f.write(json.dumps({'timestamp':time, 'message':{'role':'assistant', 'id':'msg1',
                        'model':'opus','usage':{'input_tokens':2,'output_tokens':out,
                        'cache_creation_input_tokens':7,'cache_read_input_tokens':100}},
                        'requestId':'request1'})+'\n')
            issues=[];result=list(claude(Path(tmp),'mac',issues))
            self.assertEqual(len(result),1)
            self.assertEqual((result[0].input,result[0].cache_read,result[0].cache_write,result[0].output), (2,100,7,50))
            self.assertEqual(issues,[])

    def test_claude_copied_history_covers_both_sessions_once(self):
        with tempfile.TemporaryDirectory() as tmp:
            for session in ('original','fork'):
                item={'timestamp':'2026-09-01T00:00:00Z','sessionId':session,'cwd':'/tmp/'+session,
                      'message':{'role':'assistant','id':'same','model':'opus',
                      'usage':{'input_tokens':2,'output_tokens':5,'cache_read_input_tokens':100}},
                      'requestId':'r1'}
                (Path(tmp)/(session+'.jsonl')).write_text(json.dumps(item)+'\n')
            issues=[];raw=list(claude(Path(tmp),'Pythia',issues))
            self.assertEqual(len(raw),2)
            self.assertNotEqual(raw[0].project,raw[1].project)
            self.assertEqual(raw[0].fingerprint,raw[1].fingerprint)
            reconciled,problems=reconcile(raw)
            self.assertEqual((len(reconciled),len(problems)),(1,0))
            refs=transcript_references(raw,reconciled)
            self.assertEqual({e.session_key for e in refs},{'original','fork'})
            self.assertEqual(summarize(reconciled,[],refs)['grand_total'],107)

    def test_pi_total_and_reasoning_not_counted_twice(self):
        with tempfile.TemporaryDirectory() as tmp:
            (Path(tmp)/'session.jsonl').write_text(json.dumps({'type':'message','id':'a',
                'timestamp':'2026-09-01T00:00:00Z','message':{'role':'assistant','model':'m',
                'usage':{'input':10,'output':12,'cacheRead':100,'cacheWrite':5,
                'reasoning':8,'totalTokens':127}}})+'\n')
            issues=[];one,=pi(Path(tmp),'mac',issues)
            self.assertEqual((one.total,one.reasoning), (127,8));self.assertEqual(issues,[])

    def test_codex_interleaved_fork_counts_each_proven_branch_once(self):
        with tempfile.TemporaryDirectory() as tmp:
            points=[(100,90,10,100,90,10),(200,180,20,100,90,10),
                    (150,135,15,50,45,5),(300,270,30,100,90,10),
                    (250,225,25,100,90,10)]
            rows=[{'type':'session_meta','payload':{'id':'fork'}}]
            for i,(inp,cache,out,li,lc,lo) in enumerate(points):
                rows.append({'type':'event_msg','timestamp':f'2026-09-01T00:00:0{i}Z',
                    'payload':{'type':'token_count','info':{
                        'total_token_usage':{'input_tokens':inp,'cached_input_tokens':cache,'output_tokens':out},
                        'last_token_usage':{'input_tokens':li,'cached_input_tokens':lc,'output_tokens':lo}}}})
            path=Path(tmp)/'rollout.jsonl'
            path.write_text(''.join(json.dumps(row)+'\n' for row in rows[:3]))
            prefix=list(codex(Path(tmp),'mac',[]))
            path.write_text(''.join(json.dumps(row)+'\n' for row in rows))
            issues=[];result=list(codex(Path(tmp),'mac',issues))
            self.assertEqual(issues,[])
            self.assertEqual((len(result),sum(e.total for e in result)),(5,495))
            old={e.key:e.fingerprint for e in prefix}
            full={e.key:e.fingerprint for e in result}
            self.assertEqual({k:full[k] for k in old},old)
            self.assertTrue(all(':fork:' in e.key for e in result))
            # One missing predecessor invalidates the entire fork reconstruction.
            rows.pop(1)
            (Path(tmp)/'rollout.jsonl').write_text(''.join(json.dumps(row)+'\n' for row in rows))
            issues=[];list(codex(Path(tmp),'mac',issues))
            self.assertTrue(any('manual reconciliation' in issue for issue in issues))

    def test_codex_complete_response_ledger_overrides_interleaved_counters(self):
        with tempfile.TemporaryDirectory() as tmp:
            rows=[{'type':'session_meta','payload':{'id':'fork'}}]
            state={'a':{'input_tokens':0,'cached_input_tokens':0,'output_tokens':0},
                   'b':{'input_tokens':0,'cached_input_tokens':0,'output_tokens':0}}
            points=[('a',100,90,10),('a',100,90,10),('b',30,20,5),
                    ('a',50,40,5),('b',30,20,5)]
            for i,(thread,inp,cache,out) in enumerate(points):
                at=f'2026-09-01T00:00:0{i}Z'
                u={'input_tokens':inp,'cached_input_tokens':cache,'cache_write_input_tokens':0,
                   'output_tokens':out,'reasoning_output_tokens':0,'total_tokens':inp+out}
                for k in ('input_tokens','cached_input_tokens','output_tokens'):state[thread][k]+=u[k]
                cumulative={**state[thread],'cache_write_input_tokens':0,'reasoning_output_tokens':0,
                            'total_tokens':state[thread]['input_tokens']+state[thread]['output_tokens']}
                rows.append({'type':'token_usage_record','timestamp':at,'ordinal':i+1,
                    'payload':{'thread_id':thread,'response_id':thread+str(i),'turn_id':thread,
                        'session_id':'fork','usage':u,'turn_token_usage':cumulative,
                        'thread_token_usage':cumulative}})
                rows.append({'type':'event_msg','timestamp':at,'ordinal':i+6,
                    'payload':{'type':'token_count','info':{
                        'total_token_usage':cumulative,'last_token_usage':u}}})
            (Path(tmp)/'rollout.jsonl').write_text(''.join(json.dumps(row)+'\n' for row in rows))
            issues=[];result=list(codex(Path(tmp),'mac',issues))
            self.assertEqual(issues,[])
            self.assertEqual((len(result),sum(e.total for e in result)),(5,345))
            self.assertTrue(all(':response:' in e.key for e in result))
            duplicate=next(row for row in rows if row['type']=='token_usage_record')
            rows.insert(rows.index(duplicate)+1,duplicate.copy())
            (Path(tmp)/'rollout.jsonl').write_text(''.join(json.dumps(row)+'\n' for row in rows))
            issues=[];again=list(codex(Path(tmp),'mac',issues))
            self.assertEqual((len(again),sum(e.total for e in again),issues),(5,345,[]))
            # An incomplete per-response log must not be mistaken for full coverage.
            rows=[row for row in rows if not (row['type']=='token_usage_record' and
                  row['payload']['response_id']=='a3')]
            (Path(tmp)/'rollout.jsonl').write_text(''.join(json.dumps(row)+'\n' for row in rows))
            issues=[];list(codex(Path(tmp),'mac',issues))
            self.assertTrue(any('per-response usage' in issue for issue in issues))

    def test_codex_uses_cumulative_deltas_inclusive_cached(self):
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp)/'rollout.jsonl'
            rows=[{'type':'session_meta','payload':{'id':'s'}},
                *[{'type':'event_msg','timestamp':f'2026-09-01T00:00:0{i}Z','payload':{'type':'token_count',
                    'info':{'total_token_usage':{'input_tokens':inp,'cached_input_tokens':cached,'output_tokens':out}}}}
                for i,(inp,cached,out) in enumerate([(100,90,10),(100,90,10),(200,180,20)])]]
            path.write_text(''.join(json.dumps(row)+'\n' for row in rows))
            issues=[];result=list(codex(Path(tmp),'mac',issues))
            self.assertEqual(len(result),2)
            self.assertEqual(sum(r.total for r in result),220)
            self.assertEqual(sum(r.cache_read for r in result),180)
            self.assertEqual(issues,[])

    def test_codex_proven_new_epoch_adds_first_turn_only_once(self):
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp)/'rollout.jsonl'
            points=[(100,90,10,100,90,10),(200,180,20,100,90,10),
                    (50,40,5,50,40,5),(100,80,12,50,40,7)]
            rows=[{'type':'session_meta','payload':{'id':'s'}}]
            for i,(inp,cached,out,li,lc,lo) in enumerate(points):
                rows.append({'type':'event_msg','timestamp':f'2026-09-01T00:00:0{i}Z',
                    'payload':{'type':'token_count','info':{
                        'total_token_usage':{'input_tokens':inp,'cached_input_tokens':cached,'output_tokens':out},
                        'last_token_usage':{'input_tokens':li,'cached_input_tokens':lc,'output_tokens':lo}}}})
            path.write_text(''.join(json.dumps(row)+'\n' for row in rows))
            issues=[];result=list(codex(Path(tmp),'mac',issues))
            self.assertEqual(issues,[])
            self.assertEqual(sum(r.total for r in result),332)  # epoch 1: 220; epoch 2: 112
            self.assertEqual(len(result),4)
            self.assertEqual(len(set(e.key for e in result)),4)

    def test_opencode_both_reasoning_conventions(self):
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp)/'db';db=sqlite3.connect(path)
            db.execute('create table message (id text, time_created integer, data text, session_id text)')
            db.execute('create table session (id text, directory text)')
            db.execute("insert into session values('session1', '/tmp/myproject')")
            for i,(out,reason,total) in enumerate([(20,5,130),(20,5,135),(-2,20,128),(0,20,129)]):
                u={'role':'assistant','modelID':'m','tokens':{'input':10,'output':out,
                   'cache':{'read':100,'write':0},'reasoning':reason,'total':total}}
                db.execute('insert into message values(?,?,?,?)',(str(i),1000+i,json.dumps(u),'session1'))
            db.commit();db.close()
            issues=[];result=list(opencode(path,'mac',issues))
            self.assertEqual([e.total for e in result],[130,135,128,129])
            self.assertEqual([e.reasoning for e in result],[5,5,20,20])
            self.assertEqual([e.project for e in result],['myproject']*4)
            self.assertEqual([e.output for e in result],[20,25,18,19])
            self.assertEqual(len(issues),1)  # unexplained 1-token adjustment, but total preserved
            self.assertEqual(json.loads(result[2].raw_usage_json)['output'],-2)

    def test_opencode_explicit_total_with_matching_step_finish_is_bounded(self):
        with tempfile.TemporaryDirectory() as tmp:
            path=Path(tmp)/'db';db=sqlite3.connect(path)
            db.execute('create table message (id text, time_created integer, data text, session_id text)')
            db.execute('create table session (id text, directory text)')
            db.execute('create table part (message_id text, data text)')
            db.execute("insert into session values('s','/tmp/test')")
            u={'total':57554,'input':8563,'output':0,'reasoning':1961,
               'cache':{'write':0,'read':47104}}
            db.execute('insert into message values(?,?,?,?)',('m',1000,
                json.dumps({'role':'assistant','modelID':'kimi-k2.6','tokens':u}),'s'))
            db.execute('insert into part values(?,?)',('m',json.dumps({'type':'step-finish','tokens':u})))
            db.commit();db.close()
            issues=[];notes=[];event,=opencode(path,'Pythia',issues,notes)
            self.assertEqual(issues,[])
            self.assertEqual(event.total,57554)
            self.assertEqual((event.output,event.reasoning),(1887,1961))
            self.assertEqual(len(notes),1)
            self.assertIn('74 tokens',notes[0])

    def test_wakatime_reference_is_only_counted_without_transcript_session(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp)
            day={'date':'2026-05-20','heartbeats':[
                {'id':'hb1','time':1779278400,'ai_session':'s1','ai_input_tokens':100,
                 'ai_cached_input_tokens':0,'ai_output_tokens':10,'category':'AI Coding',
                 'user_agent_id':'ua','machine_name_id':'mac','project':'wakapi'},
                {'id':'hb1','time':1779278400,'ai_session':'s1','ai_input_tokens':100,
                 'ai_cached_input_tokens':0,'ai_output_tokens':10,'category':'AI Coding',
                 'user_agent_id':'ua','machine_name_id':'mac','project':'wakapi'},
                {'id':'hb2','time':1779278401,'ai_session':'s2','ai_input_tokens':200,
                 'ai_cached_input_tokens':0,'ai_output_tokens':20,'category':'AI Coding',
                 'user_agent_id':'ua','machine_name_id':'mac','project':'wakapi'}]}
            files=[('export.json',{'days':[day]}),('agents.json',[{'id':'ua','editor':'Claude Code','ai_model':'Opus'}]),
                   ('machines.json',[{'id':'mac','value':'Mac'}])]
            for name,content in files:(root/name).write_text(json.dumps(content))
            issues=[];observed=list(observations(root/'export.json',root/'agents.json',root/'machines.json',1791759696000,issues))
            transcript=canonical('claude','msg',1779278400000,'Pythia','wakapi','Opus',1,500,0,5,0,session_key='s1')
            unique,conflicts=reconcile(observed+[transcript]);report=summarize(unique,issues+conflicts)
            self.assertEqual(issues+conflicts,[])
            self.assertEqual(len(unique),3)
            self.assertEqual(report['transcript_total'],506)
            self.assertEqual(report['fallback_total'],220)
            self.assertEqual(report['unknown_input_tokens'],200)
            self.assertEqual(report['excluded_wakatime_tokens'],110)
            db=sqlite3.connect(root/'copy.rehearsal.sqlite3');db.execute('create table heartbeats(id integer)')
            self.assertEqual(import_copy(db,'u1',unique)['inserted'],3)
            totals=db.execute("""SELECT sum(input_tokens+cache_read_tokens+cache_write_tokens+unknown_input_tokens+output_tokens)
                FROM token_usage_events AS e WHERE source!='wakatime' OR NOT EXISTS
                (SELECT 1 FROM token_usage_session_refs AS t WHERE t.user_id=e.user_id AND t.session_key=e.session_key AND t.source!='wakatime')""").fetchone()[0]
            self.assertEqual(totals,report['grand_total']);db.close()

    def test_wakatime_large_unmatched_observation_requires_review(self):
        w=canonical('wakatime','hb',1779278400000,'Pythia','','m',0,0,0,1,0,
                    session_key='unmatched',unknown_input=1_000_001)
        report=summarize([w],[])
        self.assertEqual(report['issues_count'],1)
        self.assertEqual(report['large_fallback_events'],1)
        self.assertEqual(report['large_fallback_tokens'],1_000_002)
        self.assertEqual(report['quarantined_fallback_tokens'],1_000_002)
        self.assertEqual(report['grand_total'],0)
        matched=canonical('claude','msg',1779278400000,'Pythia','','m',1,0,0,0,0,
                          session_key='unmatched')
        self.assertEqual(summarize([w,matched],[])['issues_count'],0)
        self.assertEqual(summarize([w,matched],[])['grand_total'],1)

    def test_wakatime_non_target_editor_is_excluded(self):
        with tempfile.TemporaryDirectory() as tmp:
            root=Path(tmp);heartbeat={'id':'hb','time':1779278400,'ai_session':'unmatched',
                'ai_input_tokens':200,'ai_cached_input_tokens':0,'ai_output_tokens':20,
                'category':'AI Coding','user_agent_id':'ua','machine_name_id':'mac'}
            for name,body in [('export.json',{'days':[{'heartbeats':[heartbeat]}]}),
                              ('agents.json',[{'id':'ua','editor':'Unknown Editor'}]),
                              ('machines.json',[{'id':'mac','value':'Mac'}])]:
                (root/name).write_text(json.dumps(body))
            skipped={};issues=[]
            events=list(observations(root/'export.json',root/'agents.json',root/'machines.json',
                                     1791759696000,issues,skipped))
            self.assertEqual(events,[])
            self.assertEqual(skipped,{'events':1,'tokens':220})
            self.assertEqual(issues,[])

    def test_copied_pi_message_covers_both_sessions_without_duplicate_tokens(self):
        a=canonical('pi','same-msg',1000,'Pythia','project','model',1,100,0,5,0,session_key='original')
        b=canonical('pi','same-msg',1000,'omphalos','project','model',1,100,0,5,0,session_key='fork')
        w=canonical('wakatime','hb1',1000,'omphalos','project','model',0,0,0,10,0,
                    session_key='fork',unknown_input=100)
        self.assertEqual(a.fingerprint,b.fingerprint)
        reconciled,issues=reconcile([a,b,w]);self.assertEqual(issues,[])
        refs=transcript_references([a,b,w],reconciled)
        report=summarize(reconciled,issues,refs)
        self.assertEqual(report['grand_total'],106)
        self.assertEqual(report['fallback_total'],0)
        self.assertEqual(report['session_refs'],2)
        with tempfile.TemporaryDirectory() as tmp:
            db=sqlite3.connect(Path(tmp)/'copy.rehearsal.sqlite3')
            import_copy(db,'user',reconciled,refs)
            self.assertEqual(db.execute('select count(*) from token_usage_events').fetchone()[0],2)
            self.assertEqual(db.execute('select count(*) from token_usage_session_refs').fetchone()[0],2)
            db.close()

    def test_invalid_numeric_accounting_is_not_coerced(self):
        for value in (True, 1.5, 2.0, [], '1.2', -1):
            with self.assertRaises((ValueError,TypeError)):number(value)
        self.assertEqual(number('123'),123)

    def test_conflicting_snapshots_have_no_arbitrary_counted_winner(self):
        a=canonical('pi','same',1000,'mac','','m',1,100,0,1,0,session_key='s')
        b=canonical('pi','same',1000,'other','','m',1,200,0,1,0,session_key='s')
        result,issues=reconcile([a,b,a])
        self.assertEqual(result,[])
        self.assertEqual(len(issues),1)
        self.assertEqual(transcript_references([a,b],result),[])

    def test_offline_import_does_not_add_new_coding_representation(self):
        with tempfile.TemporaryDirectory() as tmp:
            db=sqlite3.connect(Path(tmp)/'copy.db')
            a=canonical('codex','s:0:counter',1000,'mac','','m',1,100,0,1,0,session_key='s')
            b=canonical('codex','s:response:id',1000,'mac','','m',1,100,0,1,0,session_key='s')
            import_copy(db,'user',[a])
            with self.assertRaisesRegex(RuntimeError,'representation changed'):import_copy(db,'user',[b])
            self.assertEqual(db.execute('select count(*) from token_usage_events').fetchone()[0],1)
            db.close()

    def test_ledger_is_additive_idempotent_and_conflicts_quarantined(self):
        with tempfile.TemporaryDirectory() as tmp:
            db=sqlite3.connect(Path(tmp)/'copy.db');db.execute('create table heartbeats (id integer primary key, data text)');db.execute("insert into heartbeats values (1, 'untouched')")
            e=canonical('pi','msg',1000,'mac','','m',1,90,0,10,0)
            self.assertEqual(import_copy(db,'user',[e])['inserted'],1)
            self.assertEqual(import_copy(db,'user',[e])['unchanged'],1)
            changed=canonical('pi','msg',1001,'mac','','m',1,100,0,10,0)
            self.assertEqual(import_copy(db,'user',[changed])['updated'],1)
            regression=canonical('pi','msg',1001,'mac','','m',1,95,0,10,0)
            self.assertEqual(import_copy(db,'user',[regression])['conflicts'],1)
            self.assertEqual(db.execute('select sum(input_tokens+cache_read_tokens+cache_write_tokens+output_tokens) from token_usage_events').fetchone()[0],111)
            self.assertEqual(db.execute('select count(*) from token_usage_conflicts').fetchone()[0],1)
            self.assertEqual(db.execute('select data from heartbeats').fetchone()[0],'untouched')
            db.close()


if __name__ == '__main__':
    unittest.main()
