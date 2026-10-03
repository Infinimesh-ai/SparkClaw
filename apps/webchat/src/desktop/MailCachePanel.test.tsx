// @vitest-environment jsdom
import {act} from "react";
import {createRoot} from "react-dom/client";
import {afterEach,describe,expect,it,vi} from "vitest";
import {MailCachePanel} from "./MailCachePanel";
afterEach(()=>{delete window.sparkclawMailSync;vi.unstubAllGlobals();});
describe("mail cache panel",()=>{
 it("saves a selected attachment only into the current local conversation",async()=>{
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT",true);const save=vi.fn(async()=>({id:"local-file"}));const changed=vi.fn();
  window.sparkclawMailSync={catalog:async()=>[{id:"box",address:"owner@example.com",provider:"gmail"}],refreshCatalog:vi.fn(),sync:vi.fn(),saveAttachment:save,read:async()=>({mailbox_id:"box",sequence:1,synced_at:"",messages:[{id:"a",subject:"Mail",from:"sender@example.com",summary:"",body_text:"",body_truncated:false,attachments:[{id:"part-1",name:"report.txt",size:10,available:true}]}]})};
  const host=document.createElement("div");const root=createRoot(host);
  try{await act(async()=>root.render(<MailCachePanel language="en" conversationID="conversation-current" onFileSaved={changed}/>));const button=[...host.querySelectorAll<HTMLButtonElement>("button")].find(b=>b.textContent==="Copy to device conversation")!;
   await act(async()=>button.click());expect(save).toHaveBeenCalledWith("box","a","part-1","conversation-current");expect(changed).toHaveBeenCalledOnce();expect(host.textContent).toContain("Attachment verified and copied");}
  finally{await act(async()=>root.unmount());}
 });
 it("reads its scoped cache without a network call and keeps readable data on sync failure",async()=>{
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT",true);const refresh=vi.fn();const sync=vi.fn(async()=>{throw new Error("Offline");});
  window.sparkclawMailSync={catalog:async()=>[{id:"box",address:"owner@example.com",provider:"gmail"}],refreshCatalog:refresh,sync,read:async()=>({mailbox_id:"box",sequence:1,synced_at:"2026-10-03T00:00:00Z",messages:[{id:"a",subject:"Stored subject",from:"sender@example.com",summary:"Stored summary",body_text:"<script>text stays inert</script>",body_truncated:false,attachments:[]}]})};
  const host=document.createElement("div");const root=createRoot(host);
  try{await act(async()=>root.render(<MailCachePanel language="en"/>));expect(host.textContent).toContain("Stored subject");expect(host.textContent).toContain("Device cache");expect(refresh).not.toHaveBeenCalled();expect(sync).not.toHaveBeenCalled();expect(host.querySelector("script")).toBeNull();
   const button=[...host.querySelectorAll<HTMLButtonElement>("button")].find(b=>b.textContent==="Sync mail")!;await act(async()=>button.click());expect(host.textContent).toContain("Offline");expect(host.textContent).toContain("Stored summary");expect(sync).toHaveBeenCalledWith("box");}
  finally{await act(async()=>root.unmount());}
 });
});
