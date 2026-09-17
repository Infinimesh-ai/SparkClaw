// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { api } from "../api/client";
import type { EmailMessage } from "../api/email";
import { dictionaries } from "../i18n";
import { EmailBody } from "./emailBody";
let container: HTMLElement, root: ReturnType<typeof createRoot>;
const mail = { id: "mail", body_available: true } as EmailMessage;
beforeEach(() => { container=document.createElement("div"); document.body.append(container); root=createRoot(container); });
afterEach(() => { act(() => root.unmount());container.remove();vi.restoreAllMocks(); });
it("fetches the parsed preview only when expanded and reuses it on reopening", async () => {
 const read=vi.spyOn(api,"emailPreview").mockResolvedValue({version:1,id:"mail",subject:"",from:"",to:[],cc:[],attachments:[],header_lines:["Date: Mon, 7 Sep 2026 10:00:00 +0800"],body_text:"<script>unsafe()</script>\nThe original text"});
 await act(async()=>root.render(<EmailBody mail={mail} text={dictionaries.en}/>));
 expect(read).not.toHaveBeenCalled();expect(container.querySelector("details")!.open).toBe(false);
 const toggle=async(open:boolean)=>act(async()=>{const details=container.querySelector("details")!;details.open=open;details.dispatchEvent(new Event("toggle"));});
 await toggle(true);
 expect(read).toHaveBeenCalledTimes(1);expect(read).toHaveBeenCalledWith("mail",expect.any(AbortSignal));
 // Sender-controlled text is rendered as text, never interpreted as markup.
 expect(container.textContent).toContain("<script>unsafe()</script>");expect(container.querySelector("script")).toBeNull();
 expect(container.querySelector(".emailHeaderLines")?.textContent).toContain("Date: Mon, 7 Sep 2026");
 await toggle(false);await toggle(true);expect(read).toHaveBeenCalledTimes(1);
});
