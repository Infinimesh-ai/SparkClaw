import crypto from "node:crypto";

const WORLD = 1004;
const hash = (value) => crypto.createHash("sha256").update(typeof value === "string" ? value : JSON.stringify(value)).digest("hex");
const execute = (record, code) => record.webContents.executeJavaScriptInIsolatedWorld(WORLD, [{ code }]);

// Every script is owned by this module. The wire supplies only bounded data;
// it cannot select a JS expression, debugger method, selector or file path.
export async function nativePageCommand(record, operation, args, binding, fence) {
  fence();
  if (operation === "navigate") {
    await record.webContents.loadURL(args.url);
    fence();
    return { page_id: binding.page_id, url: record.webContents.getURL(), title: record.webContents.getTitle(), page_generation: binding.page_generation };
  }
  if (operation === "read") {
    const max = Math.min(args.max_chars || 16384, 24000);
    const page = await execute(record, `(()=>({url:location.href,final_url:location.href,title:document.title,text:(document.body?.innerText||'').slice(0,${max}),ready_state:document.readyState,lang:document.documentElement.lang,rendered:true,content_type:'text/html'}))()`);
    fence(); return { ...page, page_id: binding.page_id, untrusted: true };
  }
  if (operation === "snapshot") {
    const snapshotID = `snapshot_${crypto.randomUUID().replaceAll("-", "")}`;
    const result = await execute(record, `(()=>{
      const snapshotID=${JSON.stringify(snapshotID)};const refs=new Map();const controls=[];
      const elements=[...document.querySelectorAll('button,a[href],input,textarea,select,[role="button"],[role="link"],[role="textbox"]')].slice(0,120);
      for(const element of elements){const box=element.getBoundingClientRect();if(!box.width||!box.height)continue;
        const style=getComputedStyle(element);if(style.visibility==='hidden'||style.display==='none')continue;
        const tag=element.tagName.toLowerCase(),type=(element.type||'').toLowerCase();if(['password','file','hidden'].includes(type))continue;
        const name=(element.getAttribute('aria-label')||element.labels?.[0]?.innerText||element.innerText||element.getAttribute('placeholder')||'').trim().slice(0,180);
        const role=element.getAttribute('role')||(tag==='a'?'link':tag==='select'?'combobox':['input','textarea'].includes(tag)?'textbox':'button');
        const uid=snapshotID+':e'+(controls.length+1);refs.set(uid,element);controls.push({ref:uid,uid,role,name,label:name,tag,type,visible:true,enabled:!element.disabled,clickable:['button','link'].includes(role),editable:role==='textbox',value_kind:'text',box:{x:box.x,y:box.y,width:box.width,height:box.height}});
      }
      globalThis.__sparkclawBrowserRefs={snapshotID,refs,url:location.href};
      return {url:location.href,title:document.title,controls,text:(document.body?.innerText||'').slice(0,12000)};
    })()`);
    fence();
    const digest = hash(result); const contentDigest = hash(`${result.title}\n${result.text}`);
    const aria = result.controls.map((control) => `[ref=${control.ref}] ${control.role} ${control.name}`).join("\n");
    const controls = result.controls.map((control, index) => ({ ...control, ordinal: index + 1, short_ref: `e${index + 1}`, fingerprint: hash(control), snapshot_id: snapshotID }));
    const snapshot = { schema_version: "browser_interaction_snapshot_v1", snapshot_id: snapshotID, page_id: binding.page_id, page_generation: binding.page_generation,
      session_generation: binding.page_generation, url: result.url, title: result.title, digest, content_digest: contentDigest, controls_total: controls.length, controls_returned: controls.length,
      controls, refs: controls, action_refs: controls.filter((control) => control.clickable).map((control) => control.ref), aria, truncated: false, browser_page_auth_state: "unknown", browser_page_auth_confidence: "unknown" };
    return { snapshot_id: snapshotID, page_id: binding.page_id, digest, content_digest: contentDigest, snapshot, text: `Page: ${result.url}\n${aria}`, browser_page_auth_state: "unknown" };
  }
  if (operation === "click") {
    const position = await execute(record, `(()=>{${lookup(args)} const box=element.getBoundingClientRect();const x=box.x+box.width/2,y=box.y+box.height/2;
      if(element.disabled||box.width<=0||box.height<=0||x<0||y<0||x>=innerWidth||y>=innerHeight)throw Error('Element is unavailable');
      const top=document.elementFromPoint(x,y);if(!top||!(top===element||element.contains(top)))throw Error('Element is obscured');return {x,y};})()`);
    fence();
    // A shielded unfocused task view cannot consume sendInputEvent reliably.
    // Main uses only these fixed native input commands on the fenced contents;
    // the broker never exposes a debugger method or parameter escape hatch.
    // The task input shield deliberately prevents owner mouse input. Execute
    // the fixed native DOM activation on this exact referenced embedded node;
    // no selector, script or debugger method is accepted from the command.
    await execute(record, `(()=>{${lookup(args)} if(element.disabled)throw Error('Element is disabled');globalThis.__sparkclawBrowserRefs=null;HTMLElement.prototype.click.call(element);return true;})()`);
    fence();
    return { page_id: binding.page_id, snapshot_id: args.snapshot_id, clicked: args.ref };
  }
  if (operation === "fill" || operation === "select") {
    fence();
    await execute(record, `(()=>{${lookup(args)} const value=${JSON.stringify(args.value)};const tag=element.tagName.toLowerCase();
      if(element.disabled||element.readOnly)throw Error('Element is not editable');
      if(${JSON.stringify(operation)}==='select') {if(tag!=='select'||![...element.options].some(option=>option.value===value&&!option.disabled))throw Error('Invalid option');element.value=value;}
      else {if(!['input','textarea'].includes(tag)||tag==='input'&&!['text','search','email','url','tel','number',''].includes(element.type))throw Error('Field is not a reversible text draft');
        const prototype=tag==='input'?HTMLInputElement.prototype:HTMLTextAreaElement.prototype;Object.getOwnPropertyDescriptor(prototype,'value').set.call(element,value);}
      globalThis.__sparkclawBrowserRefs=null;element.dispatchEvent(new Event('input',{bubbles:true}));element.dispatchEvent(new Event('change',{bubbles:true}));return true;})()`);
    fence(); return { page_id: binding.page_id, snapshot_id: args.snapshot_id, typed: args.ref, draft_only: true };
  }
  if (operation === "screenshot") {
    const capture = await record.webContents.capturePage(); fence();
    const data = capture.resize({ width: 480 }).toPNG();
    if (data.length > 64 << 10) throw new Error("Screenshot exceeds the bounded output budget");
    return { page_id: binding.page_id, data: data.toString("base64"), mimeType: "image/png" };
  }
  if (operation === "wait") { await new Promise((resolve) => setTimeout(resolve, args.milliseconds)); fence(); return { page_id: binding.page_id, waited: true }; }
  throw new Error("Browser operation is unavailable");
}
function lookup(args) {
  return `const state=globalThis.__sparkclawBrowserRefs;if(!state||state.snapshotID!==${JSON.stringify(args.snapshot_id)}||state.url!==location.href)throw Error('Snapshot is stale');
    const element=state.refs.get(${JSON.stringify(args.ref)});if(!element||!element.isConnected)throw Error('Reference is stale');`;
}
