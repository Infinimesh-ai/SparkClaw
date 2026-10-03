// @vitest-environment jsdom
import {act} from "react";
import {createRoot} from "react-dom/client";
import {afterEach,describe,expect,it,vi} from "vitest";
import {MailCachePanel} from "./MailCachePanel";
afterEach(()=>{delete window.sparkclawMailSync;vi.unstubAllGlobals();});
describe("mail cache panel",()=>{
 it("reads its scoped cache without a network call and keeps readable data on sync failure",async()=>{
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT",true);const refresh=vi.fn();const sync=vi.fn(async()=>{throw new Error("Offline");});
  window.sparkclawMailSync={catalog:async()=>[{id:"box",address:"owner@example.com",provider:"gmail"}],refreshCatalog:refresh,sync,read:async()=>({mailbox_id:"box",sequence:1,synced_at:"2026-10-03T00:00:00Z",messages:[{id:"a",subject:"Stored subject",from:"sender@example.com",summary:"Stored summary",body_text:"<script>text stays inert</script>",body_truncated:false,attachments:[]}]})};
  const host=document.createElement("div");const root=createRoot(host);
  try{await act(async()=>root.render(<MailCachePanel language="en"/>));expect(host.textContent).toContain("Stored subject");expect(host.textContent).toContain("Device cache");expect(refresh).not.toHaveBeenCalled();expect(sync).not.toHaveBeenCalled();expect(host.querySelector("script")).toBeNull();
   const button=[...host.querySelectorAll<HTMLButtonElement>("button")].find(b=>b.textContent==="Sync mail")!;await act(async()=>button.click());expect(host.textContent).toContain("Offline");expect(host.textContent).toContain("Stored summary");expect(sync).toHaveBeenCalledWith("box");}
  finally{await act(async()=>root.unmount());}
 });
});
