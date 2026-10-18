import { app, BrowserWindow, protocol, session } from "electron";
import fs from "node:fs/promises";
import path from "node:path";

const output = process.env.SPARKCLAW_VISUAL_OUTPUT;
if (!output) throw new Error("SPARKCLAW_VISUAL_OUTPUT must identify an isolated fixture directory");
app.setName("SparkX isolated experience visual qualification");
app.setPath("userData", path.join(output, "profile"));
protocol.registerSchemesAsPrivileged([{ scheme: "sparkx-visual", privileges: { standard: true, secure: true, supportFetchAPI: true } }]);
app.whenReady().then(async () => {
  app.dock?.hide();
  let window;
  const errors = [], rows = [], screenshots = [];
  try {
    const [script, css] = await Promise.all(["fixture.js", "fixture.css"].map(name => fs.readFile(path.join(output, "ui", name), "utf8")));
    const html = `<html><head><meta charset="utf-8"><style>${css}</style></head><body><div id="root"></div><script>${script}</script></body></html>`;
    session.defaultSession.webRequest.onBeforeRequest(({ url }, callback) => callback({ cancel: !url.startsWith("sparkx-visual://fixture/") }));
    session.defaultSession.protocol.handle("sparkx-visual", () => new Response(html, { headers: { "content-type": "text/html; charset=utf-8" } }));
    window = new BrowserWindow({ show: false, width: 1440, height: 900, webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false, backgroundThrottling: false } });
    window.webContents.on("console-message", ({ level, message }) => { if (level === "error") errors.push(message); });
    await window.loadURL("sparkx-visual://fixture/index.html");
    const evaluate = expression => window.webContents.executeJavaScript(expression);
    const settle = () => evaluate("new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))");
    while (!(await evaluate("Boolean(window.experienceVisual)"))) await new Promise(resolve => setTimeout(resolve, 20));
    const scenes = ["empty", "short", "long", "mixed", "attachments", "schedules", "mail", "files", "notifications", "settings", "appearance", "devices", "models-tools", "permissions", "connections", "memory", "approvals", "timeline"];
    const dimensions = [[1180, 720], [1440, 900], [1920, 1080]];
    for (const [width, height] of dimensions) {
      window.setContentSize(width, height);
      for (const zoom of width === 1180 ? [1, 1.25, 1.5, 2] : [1]) {
        window.webContents.setZoomFactor(zoom);
        for (const theme of ["light", "dark"]) for (const size of ["default", "large"]) {
          await evaluate(`window.experienceVisual.setAppearance(${JSON.stringify(theme)},${JSON.stringify(size)})`);
          for (const scene of scenes) {
            await evaluate(`window.experienceVisual.setScene(${JSON.stringify(scene)})`);
            await settle();
            if (scene === "mail") { await evaluate("document.querySelector('.emailRenderPreview').open=true"); await settle(); }
            const result = await evaluate(`(() => {
              const rgba=value=>{const p=(value.match(/[\\d.]+/g)||[]).map(Number);return [p.slice(0,3).map(c=>value.startsWith('color(srgb')?c*255:c),p[3]??1];};
              const rgb=value=>rgba(value)[0];
              const luminance=value=>rgb(value).map(c=>c/255).map(c=>c<=.04045?c/12.92:((c+.055)/1.055)**2.4).reduce((sum,c,index)=>sum+c*[.2126,.7152,.0722][index],0);
              const contrast=(a,b)=>{const x=luminance(a),y=luminance(b);return (Math.max(x,y)+.05)/(Math.min(x,y)+.05);};
              const background=e=>{const stack=[];for(let n=e;n;n=n.parentElement)stack.unshift(n);let result=[255,255,255];for(const n of stack){const [color,alpha]=rgba(getComputedStyle(n).backgroundColor);result=result.map((c,i)=>color[i]*alpha+c*(1-alpha));}return 'rgb('+result.join(', ')+')';};
              const rect = selector => document.querySelector(selector)?.getBoundingClientRect();
              const input=document.querySelector('.composer textarea'), message=document.querySelector('.message.assistant .messageContent p');
              const i=input && getComputedStyle(input), m=message && getComputedStyle(message);
              const frame=rect('.composer'), outer=rect('.message.assistant');
              const overflow=[...document.querySelectorAll('.settingsPageMain,.workbenchPage,.chatColumn,.composer,.emailPopup,.emailDetailPane')].filter(e=>e.scrollWidth>e.clientWidth+2).map(e=>({element:e.className,client:e.clientWidth,scroll:e.scrollWidth}));
              const controls=[...document.querySelectorAll('.composer button,.settingsPageContent button,.settingsPageContent select')].filter(e=>e.offsetParent!==null);
              const clipped=controls.filter(e=>{const r=e.getBoundingClientRect();return r.left < -1 || r.right > innerWidth+1 || (e.closest('.composer') && r.bottom>innerHeight+1);}).map(e=>e.className);
              const colors=['.workspace','.sidebar','.messageContent','.settingsSurfaceCopy small','.settingsPageHeader p','.sendButton','.composer','.emailThreadCopy b','.emailStructuredContent','.notificationItem strong','.buttonRow .approve','.buttonRow .reject'].flatMap(selector=>{const e=document.querySelector(selector);if(!e)return [];const c=getComputedStyle(e),bg=background(e);return [{selector,color:c.color,background:bg,border:c.borderColor,font:c.fontSize,line:c.lineHeight,contrast:contrast(c.color,bg)}];});
              const preference={theme:document.documentElement.dataset.resolvedTheme,size:document.documentElement.dataset.textSize,selectedSize:document.querySelector('select[aria-label="文字大小"]')?.value};
              return {preference,viewport:{width:innerWidth,height:innerHeight,dpr:devicePixelRatio},bodyOverflow:document.documentElement.scrollWidth>innerWidth+1,overflow,clipped,alignment:input&&message?Math.abs(message.getBoundingClientRect().left-input.getBoundingClientRect().left-parseFloat(i.paddingLeft)):null,frame:frame?.width,messageFrame:outer?.width,bodyFont:m?.fontSize,inputFont:i?.fontSize,line:i?.lineHeight,cssZoom:getComputedStyle(document.querySelector('.workbench')).zoom,header:rect('.topbar')?.height,sidebar:rect('.sidebar')?.width,colors};
            })()`);
            rows.push({ width, height, zoom, theme, size, scene, ...result });
            if (result.preference.theme !== theme || result.preference.size !== size || (scene === "appearance" && result.preference.selectedSize !== size) || result.colors.some(color => color.contrast < 4.5) || result.bodyOverflow || result.overflow.length || result.clipped.length || (zoom === 1 && result.alignment !== null && result.alignment > 2)) errors.push(JSON.stringify(rows.at(-1)));
            if ((width === 1440 && zoom === 1) || (width === 1180 && zoom === 2 && ["long", "mail", "appearance", "permissions"].includes(scene))) {
              const name = `${scene}-${theme}-${size}-${width}-${zoom}.png`;
              await fs.writeFile(path.join(output, name), (await window.webContents.capturePage()).toPNG()); screenshots.push(name);
            }
          }
        }
      }
    }
    // Verify shared WebChat type/layout without the desktop host selector.
    window.setContentSize(1440, 900); window.webContents.setZoomFactor(1);
    await evaluate("window.experienceVisual.setScene('short')"); await settle();
    const webChat = await evaluate("document.querySelector('.workspace').classList.remove('desktopWorkbench'); ({frame:document.querySelector('.composer').getBoundingClientRect().width,body:getComputedStyle(document.querySelector('.messageContent')).fontSize})");
    // The fixture uses production preference setters with an isolated localStorage profile.

    const environment = await evaluate("({font:getComputedStyle(document.body).fontFamily,platform:navigator.platform,ua:navigator.userAgent})");
    await fs.writeFile(path.join(output, "report.json"), JSON.stringify({ output, environment, webChat, rows, screenshots, errors }, null, 2));
    console.log(JSON.stringify({ output, cases: rows.length, screenshots: screenshots.length, errors: errors.slice(0, 20), errorCount: errors.length }));
    if (errors.length) process.exitCode = 1;
  } catch (error) {
    console.error(error.stack); process.exitCode = 1;
  } finally { window?.destroy(); app.exit(process.exitCode ?? 0); }
});
