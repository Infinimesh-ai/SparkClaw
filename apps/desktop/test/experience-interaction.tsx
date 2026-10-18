import { useLayoutEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { MessageBubble } from "../../webchat/src/components/messages";
import { ComposerSurface } from "../../webchat/src/components/composer";
import { useMessageScroll } from "../../webchat/src/hooks/useMessageScroll";
import { dictionaries } from "../../webchat/src/i18n";
import type { VoiceInputModel } from "../../webchat/src/hooks/useVoiceInput";
import "../../webchat/src/styles/app.css";
const voice = { state:"idle",active:false,devices:[],refreshDevices:async()=>{},stopPreview:async()=>{} } as unknown as VoiceInputModel;
function Fixture() {
  const [session,setSession]=useState("a"),[revision,setRevision]=useState(0),[empty,setEmpty]=useState(false),[text,setText]=useState("");
  const scroll=useMessageScroll(session),ref=useRef<HTMLTextAreaElement>(null);
  useLayoutEffect(()=>{ Object.assign(window,{experienceInteraction:{setSession,setRevision,setEmpty,setText}}); },[]);
  return <div className="shell workbench sidebarCollapsed"><main className="workspace"><header className="topbar">Fixture</header><section className="chatColumn">
    <div ref={scroll.listRef} className="messageList">{!empty && Array.from({length:20},(_,i)=><MessageBubble key={i} message={{id:`${i}`,session_id:session,role:"assistant",content:`${i}: 中文阅读、输入与焦点验证。`.repeat(i===19 ? 30+revision*20 : 20),created_at:"2026-10-18T00:00:00Z"}} streamStatuses={[]} text={dictionaries.zh} language="zh" onFeedback={async()=>{}} />)}</div>
    {scroll.away && <div className="messageScrollAction"><button id="latest" className="messageLatest" onClick={scroll.resume}>回到最新</button></div>}
    <ComposerSurface text={dictionaries.zh} language="zh" activeSession={session} activeInput={text} activeAttachments={[]} busy={false} voice={voice} composerInputRef={ref} filesEnabled={false} mailEnabled={false} onInputChange={setText} onSend={()=>setText("")} onUploadDocument={async()=>{}} onChooseDocument={()=>{}} onOpenAttachment={()=>{}} onRemoveAttachment={()=>{}} />
  </section></main></div>;
}
createRoot(document.getElementById("root")!).render(<Fixture/>);
