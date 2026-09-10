import test from 'node:test';
import assert from 'node:assert/strict';
import {parseQQMailList,parseQQMailFolders,qqDiscoveryPosition,qqDiscoveryContinuation} from '../../../scripts/email/lib/qqmail-list.mjs';

const options={account_address:'owner@example.test',lane:'recent_inbound',interval_start:'2026-09-08T00:00:00Z',interval_end:'2026-09-08T01:00:00Z',continuation:''};
const list=rows=>({head:{ret:0,time:1788860000},body:{list:rows,total_num:rows.length}});
test('QQ uses server receipt time and treats an absent unread bit as read',()=>{
 const result=parseQQMailList(list([{emailid:'mail~1',dirid:1,totime:1788852647,fromtime:1688852647},{emailid:'mail2',dirid:2000,totime:1788852117,unread:1}]));
 assert.equal(result.rows[0].received_at,new Date(1788852647000).toISOString());assert.equal(result.rows[0].unread,false);
 assert.equal(result.rows[1].unread,true);assert.equal(result.rows[1].folder,'qq:2000');
});
test('QQ rejects missing or invalid receipt evidence and malformed locators',()=>{
 assert.equal(parseQQMailList({head:{ret:0},body:{list:[],total_num:0}}),null);
 const result=parseQQMailList(list([{emailid:'mail',dirid:1,fromtime:1788852647},{emailid:'other',dirid:1,totime:1789999999},{emailid:'<script>',dirid:1,totime:1788852647},{emailid:'badflag',dirid:1,totime:1788852647,unread:2}]));
 assert.equal(result.rows.length,0);assert.equal(result.unsupported_rows,4);
});
test('QQ ordinary folder inventory excludes special folders and fences unknown custom types',()=>{
 const result=parseQQMailFolders({head:{ret:0},body:{list:{sys_list:[1,2,3,4,5,6,8,16].map(dirid=>({dirid})),personal_list:[{dirid:2000,folder_type:3},{dirid:2001,folder_type:4},{dirid:2000,folder_type:3}]}}});
 assert.deepEqual(result.folders,[{id:1,folder:'inbox'},{id:2000,folder:'qq:2000'}]);assert.equal(result.unsupported,1);
});
test('QQ durable continuation rotates ordinary folders, deepens native replay and binds the interval/account',()=>{
 const folders=[{id:1},{id:2000}],initial=qqDiscoveryPosition(options);
 const page=qqDiscoveryContinuation(initial,'a'.repeat(64),50,true,folders);
 assert.deepEqual(qqDiscoveryPosition({...options,continuation:page}),{...initial,s:'a'.repeat(64),o:50});
 const second=qqDiscoveryPosition({...options,continuation:qqDiscoveryContinuation(initial,'a'.repeat(64),50,false,folders)});
 assert.equal(second.f,2000);assert.equal(second.d,4);
 const next=qqDiscoveryPosition({...options,continuation:qqDiscoveryContinuation(second,'a'.repeat(64),50,false,folders)});
 assert.equal(next.f,1);assert.equal(next.d,8);
 assert.equal(qqDiscoveryPosition({...options,account_address:'other@example.test',continuation:page}).o,0);
 assert.equal(qqDiscoveryPosition({...options,interval_end:'2026-09-08T02:00:00Z',continuation:page}).o,0);
});
