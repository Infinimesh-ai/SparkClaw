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
 await assert.rejects(client.poll(),/ACK lost/);assert.deepEqual(wakes,[{categories:['tasks','approvals','notifications','settings','email']}]);await client.poll();assert.equal(commits,1);assert.equal(acks,2);assert.deepEqual(wakes,[{categories:['tasks','approvals','notifications','settings','email']},{categories:['tasks','approvals','notifications','settings','email']}]);
 packet.snapshot={executions:['conflicting']};await assert.rejects(client.poll(),/replay differs/);assert.equal(acks,2);
});

test('cursor gaps request a bounded new snapshot and explicitly reset within the same Gateway epoch',async()=>{
 let stored={cursor:'expired',revision:9,epoch:'epoch',snapshot:{old:true}},committed;
 const store={eventProjection:()=>stored,pending:()=>Array.from({length:60},(_,i)=>({request_id:String(i)})),commitEventProjection:(_scope,value)=>{committed=value;stored=value;}};
 const auth={generation:1,status:{state:'connected',capabilities:{events:true}},invokeISCP:async(op,body)=>{if(op==='events.pull')throw Object.assign(new Error('gap'),{code:'cursor_gap'});if(op==='events.snapshot'){assert.equal(body.request_ids.length,32);return{cursor:'fresh',revision:1,epoch:'epoch',reset:true,events:[],snapshot:{executions:[]}};}return{};}};
 const client=new ISCPEventClient({auth,store,execution:{reconcilePending:async()=>{}},getIdentity:()=>({owner_id:'owner'})});await client.poll();assert.equal(committed.reset,true);assert.equal(committed.revision,1);
});

test('a slow event ACK never delays lookup or notification and still serializes the next poll',async()=>{
 let stored={cursor:'',revision:0,epoch:'',snapshot:{}},release,pulls=0,acks=0,reconciles=0,wakes=0;
 const held=new Promise(resolve=>{release=resolve;});
 const store={eventProjection:()=>stored,pending:()=>[],commitEventProjection:(_scope,value)=>{stored=value;}};
 const auth={generation:1,status:{state:'connected',capabilities:{events:true}},invokeISCP:async op=>{
  if(op==='events.snapshot'){pulls++;return{cursor:'first',revision:1,epoch:'epoch',reset:true,snapshot:{executions:[]}};}
  if(op==='events.ack'){acks++;assert.equal(stored.cursor,'first');await held;return{};}throw new Error('Unexpected operation');
 }};
 const client=new ISCPEventClient({auth,store,execution:{reconcilePending:async options=>{assert.equal(options.wake,true);reconciles++;}},getIdentity:()=>({owner_id:'owner'}),onEvents:()=>wakes++});
 const polling=client.poll();await new Promise(resolve=>setImmediate(resolve));
 assert.equal(reconciles,1);assert.equal(wakes,1);assert.equal(acks,1);
 await client.poll();assert.equal(pulls,1);release();await polling;
});

for(const boundary of ['generation','lifetime','identity']){
 test(`an event response after ${boundary} change cannot persist, notify or ACK`,async()=>{
  let release,commits=0,acks=0,wakes=0,reconciles=0,identity={owner_id:'owner'};
  const held=new Promise(resolve=>{release=resolve;});
  const store={eventProjection:()=>({cursor:'',revision:0,epoch:'',snapshot:{}}),pending:()=>[],commitEventProjection:()=>commits++};
  const auth={generation:1,status:{state:'connected',capabilities:{events:true}},invokeISCP:async op=>{
   if(op==='events.snapshot'){await held;return{cursor:'first',revision:1,epoch:'epoch',reset:true,snapshot:{executions:[]}};}acks++;return{};
  }};
  const client=new ISCPEventClient({auth,store,execution:{reconcilePending:async()=>reconciles++},getIdentity:()=>identity,onEvents:()=>wakes++});
  const polling=client.poll();const rejected=assert.rejects(polling,/authentication changed/);
  if(boundary==='generation')auth.generation++;else if(boundary==='lifetime')client.close();else identity={owner_id:'other'};
  release();await rejected;assert.equal(commits,0);assert.equal(acks,0);assert.equal(wakes,0);assert.equal(reconciles,0);
 });
}

test('restarting after event persistence and a lost ACK replays and reconciles the same durable packet',async()=>{
 let stored={cursor:'prior',revision:1,epoch:'epoch',snapshot:{}},commits=0,acks=0,reconciles=0;
 const packet={cursor:'next',revision:2,epoch:'epoch',events:[{sequence:2,category:'tasks'}],snapshot:{executions:[]}};
 const store={eventProjection:()=>stored,pending:()=>[],commitEventProjection:(_scope,value)=>{stored=value;commits++;}};
 const auth={generation:1,status:{state:'connected',capabilities:{events:true}},invokeISCP:async(op,body)=>{
  if(op==='events.pull'){assert.ok(['prior','next'].includes(body.cursor));return packet;}
  if(op==='events.ack'){acks++;if(acks===1)throw new Error('ACK lost');return{};}throw new Error('Unexpected operation');
 }};
 const options={auth,store,execution:{reconcilePending:async()=>reconciles++},getIdentity:()=>({owner_id:'owner'})};
 const original=new ISCPEventClient(options);await assert.rejects(original.poll(),/ACK lost/);original.close();
 await new ISCPEventClient(options).poll();assert.equal(commits,1);assert.equal(acks,2);assert.equal(reconciles,2);
});

test('idle polling does not create ACK receipts and an uncertain ACK keeps its operation ID',async()=>{
 let stored={cursor:'prior',revision:1,epoch:'epoch',snapshot:{}},fail=true;
 let packet={cursor:'next',revision:2,epoch:'epoch',events:[{sequence:2,category:'tasks'}],snapshot:{executions:[]}};
 const acks=[];
 const store={eventProjection:()=>stored,pending:()=>[],commitEventProjection:(_scope,value)=>{stored=value;}};
 const auth={generation:1,status:{state:'connected',capabilities:{events:true}},invokeISCP:async(op,body,options)=>{
  if(op==='events.pull')return packet;
  assert.equal(op,'events.ack');acks.push({cursor:body.cursor,id:options.operationID});
  if(fail){fail=false;throw new Error('ACK lost');}return{};
 }};
 const options={auth,store,execution:{reconcilePending:async()=>{}},getIdentity:()=>({deployment_id:'deployment',owner_id:'owner',client_id:'client'})};
 const client=new ISCPEventClient(options);
 await assert.rejects(client.poll(),/ACK lost/);await client.poll();
 assert.equal(acks.length,2);assert.equal(acks[0].id,acks[1].id);
 for(let i=0;i<20;i++)await client.poll();
 assert.equal(acks.length,2,'unchanged acknowledged cursor needs no further ACK');
 packet={...packet,cursor:'newer',revision:3,events:[{sequence:3,category:'settings'}]};
 await client.poll();assert.equal(acks.length,3);assert.notEqual(acks[2].id,acks[1].id);
 auth.generation++;await client.poll();assert.equal(acks.length,4,'new connection confirms its current packet');
 client.close();await new ISCPEventClient(options).poll();assert.equal(acks.length,5,'restart safely confirms durable projection once');
});
