// @vitest-environment jsdom
import {act} from "react";
import {createRoot} from "react-dom/client";
import {afterEach,expect,it,vi} from "vitest";
import {api} from "../../api/client";
import type {AIPlatformLoginOverview} from "../../api/types";
import {dictionaries} from "../../i18n";
import {AIPlatformLoginSettings} from "./settingsAIPlatforms";
const overview: AIPlatformLoginOverview={browser_state:"ready",profile_id:"default",providers:["chatgpt","claude","gemini","grok"].map(p=>({provider:p as "chatgpt"|"claude"|"gemini"|"grok",state:"unchecked"}))};
afterEach(()=>vi.restoreAllMocks());
it("offers four login/check cards without script state; check-all continues after errors",async()=>{
 vi.spyOn(api,"aiPlatformLogins").mockResolvedValue(overview);
 const check=vi.spyOn(api,"checkAIPlatformLogin").mockImplementation(async p=>{if(p==="claude")throw new Error("check unavailable");return overview;});
 const login=vi.spyOn(api,"openAIPlatformLogin").mockResolvedValue(overview);
 const container=document.createElement("div");const root=createRoot(container);
 try{
  await act(async()=>root.render(<AIPlatformLoginSettings text={dictionaries.en} language="en"/>));
  expect(container.querySelectorAll("article")).toHaveLength(4);
  expect(container.textContent).not.toMatch(/script readiness|Tampermonkey|脚本就绪/i);
  const buttons=()=>[...container.querySelectorAll("button")];
  await act(async()=>buttons().find(b=>b.textContent==="Check all")!.click());
  expect(check.mock.calls.map(c=>c[0])).toEqual(["chatgpt","claude","gemini","grok"]);
  expect(container.textContent).toContain("check unavailable");
  await act(async()=>buttons().find(b=>b.textContent==="Open login page")!.click());
  expect(login).toHaveBeenCalledWith("chatgpt");
  expect(container.textContent).toContain(dictionaries.en.settings.aiPlatformOpened);
  expect(container.textContent).not.toContain("Signed in");
 }finally{await act(async()=>root.unmount());}
});
