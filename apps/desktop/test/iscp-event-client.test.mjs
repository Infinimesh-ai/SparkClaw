import test from 'node:test';import assert from 'node:assert/strict';import {ISCPEventClient} from '../src/main/iscp-event-client.mjs';
test('events never ACK before atomic projection persistence and only wake original execution reconciliation',async()=>{
 let stored={cursor:'',revision:0,snapshot:{}},diskFails=true;const calls=[];let reconciles=0;
 const store={eventProjection:()=>stored,pending:()=>[{request_id:'original'}],commitEventProjection:(_scope,value)=>{if(diskFails)throw new Error('disk failed');stored={cursor:value.cursor,revision:value.revision,snapshot:value.snapshot};}};
 const auth={generation:1,status:{state:'connected',capabilities:{events:true}},invokeISCP:async(operation,body)=>{calls.push(operation);if(operation==='events.snapshot'){assert.deepEqual(body.request_ids,['original']);return{cursor:'cursor-1',revision:1,epoch:'epoch',reset:true,snapshot:{executions:[{request_id:'original',state:'running'}]}};}if(operation==='events.ack')assert.equal(stored.cursor,body.cursor);return{};}};
 const client=new ISCPEventClient({auth,store,execution:{reconcilePending:async()=>{reconciles++;}},getIdentity:()=>({owner_id:'owner'})});
 await assert.rejects(client.poll(),/disk/);assert.equal(calls.includes('events.ack'),false);diskFails=false;await client.poll();assert.equal(calls.filter(call=>call==='events.ack').length,1);assert.equal(reconciles,1);
});
