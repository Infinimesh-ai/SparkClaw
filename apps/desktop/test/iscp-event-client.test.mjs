import test from 'node:test';import assert from 'node:assert/strict';import {ISCPEventClient} from '../src/main/iscp-event-client.mjs';
test('events never ACK before atomic projection persistence and only wake original execution reconciliation',async()=>{
 let stored={cursor:'',revision:0,snapshot:{}},diskFails=true;const calls=[];let reconciles=0;
 const store={eventProjection:()=>stored,pending:()=>[{request_id:'original'}],commitEventProjection:(_scope,value)=>{if(diskFails)throw new Error('disk failed');stored={cursor:value.cursor,revision:value.revision,snapshot:value.snapshot};}};
 const auth={generation:1,status:{state:'connected',capabilities:{events:true}},invokeISCP:async(operation,body)=>{calls.push(operation);if(operation==='events.snapshot'){assert.deepEqual(body.request_ids,['original']);return{cursor:'cursor-1',revision:1,epoch:'epoch',reset:true,snapshot:{executions:[{request_id:'original',state:'running'}]}};}if(operation==='events.ack')assert.equal(stored.cursor,body.cursor);return{};}};
 const client=new ISCPEventClient({auth,store,execution:{reconcilePending:async()=>{reconciles++;}},getIdentity:()=>({owner_id:'owner'})});
 await assert.rejects(client.poll(),/disk/);assert.equal(calls.includes('events.ack'),false);diskFails=false;await client.poll();assert.equal(calls.filter(call=>call==='events.ack').length,1);assert.equal(reconciles,1);
});

test('lost ACK replays the persisted packet and state invalidations wake every projection',async()=>{
 const packet={cursor:'packet',revision:2,epoch:'epoch',events:[{sequence:2,category:'state',reason:'snapshot_changed'}],snapshot:{executions:[]}};
 let stored={cursor:'prior',revision:1,epoch:'epoch',snapshot:{}},commits=0,acks=0;const wakes=[];
 const store={eventProjection:()=>stored,pending:()=>[],commitEventProjection:(_scope,value)=>{commits++;stored=value;}};
 const auth={generation:1,status:{state:'connected',capabilities:{events:true}},invokeISCP:async(op)=>{if(op==='events.pull')return packet;if(op==='events.ack'){acks++;if(acks===1)throw new Error('ACK lost');return{};}throw new Error('Unexpected operation');}};
 const client=new ISCPEventClient({auth,store,execution:{reconcilePending:async()=>{}},getIdentity:()=>({owner_id:'owner'}),onEvents:value=>wakes.push(value)});
 await assert.rejects(client.poll(),/ACK lost/);await client.poll();assert.equal(commits,1);assert.equal(acks,2);assert.deepEqual(wakes,[{categories:['tasks','approvals','notifications','settings','email']}]);
 packet.snapshot={executions:['conflicting']};await assert.rejects(client.poll(),/replay differs/);assert.equal(acks,2);
});

test('cursor gaps request a bounded new snapshot and explicitly reset within the same Gateway epoch',async()=>{
 let stored={cursor:'expired',revision:9,epoch:'epoch',snapshot:{old:true}},committed;
 const store={eventProjection:()=>stored,pending:()=>Array.from({length:60},(_,i)=>({request_id:String(i)})),commitEventProjection:(_scope,value)=>{committed=value;stored=value;}};
 const auth={generation:1,status:{state:'connected',capabilities:{events:true}},invokeISCP:async(op,body)=>{if(op==='events.pull')throw Object.assign(new Error('gap'),{code:'cursor_gap'});if(op==='events.snapshot'){assert.equal(body.request_ids.length,32);return{cursor:'fresh',revision:1,epoch:'epoch',reset:true,events:[],snapshot:{executions:[]}};}return{};}};
 const client=new ISCPEventClient({auth,store,execution:{reconcilePending:async()=>{}},getIdentity:()=>({owner_id:'owner'})});await client.poll();assert.equal(committed.reset,true);assert.equal(committed.revision,1);
});
