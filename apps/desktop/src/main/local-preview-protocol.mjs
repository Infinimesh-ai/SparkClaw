import fs from 'node:fs/promises';
import path from 'node:path';

export const PREVIEW_ORIGIN = 'sparkclaw-preview://viewer';
export const PREVIEW_CSP = "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src blob: data:; font-src 'self' blob:; connect-src 'self'; worker-src 'self'; object-src 'none'; frame-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors sparkclaw-app://workbench";
const mime = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.mjs': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8', '.wasm': 'application/wasm', '.bcmap': 'application/octet-stream', '.pfb': 'application/octet-stream', '.ttf': 'font/ttf', '.otf': 'font/otf' };
const headers = { 'content-security-policy': PREVIEW_CSP, 'x-content-type-options': 'nosniff', 'cache-control': 'no-store', 'referrer-policy': 'no-referrer', 'permissions-policy': 'camera=(), microphone=(), geolocation=(), clipboard-read=(), clipboard-write=(), fullscreen=(), display-capture=()' };
export function isPreviewURL(raw) {
  try { const u = new URL(raw); return u.protocol === 'sparkclaw-preview:' && u.hostname === 'viewer' && !u.port && !u.username && !u.password; } catch { return false; }
}

// Integrate into the existing session's sole onBeforeRequest callback. CSP is
// the first boundary; this independently blocks requests issued by the frame.
export function previewRequestAllowed({ url, initiator, referrer, frame, resourceType }) {
  const fromPreview = [initiator, referrer, frame?.url].some(isPreviewURL);
  if (isPreviewURL(url)) return !fromPreview || resourceType !== 'subFrame';
  if (!fromPreview) return true;
  return resourceType === 'image' && /^(blob:|data:)/u.test(url);
}

export function createLocalPreviewProtocolHandler(directory) {
  const root = path.resolve(directory);
  return async function handle(request) {
    if (request.method !== 'GET' || !isPreviewURL(request.url)) return new Response(null, { status: 403, headers });
    const url = new URL(request.url);
    let name;
    try { name = decodeURIComponent(url.pathname); } catch { return new Response(null, { status: 400, headers }); }
    if (url.search || !/^\/(?:index\.html|assets\/[\w./-]+|cmaps\/[\w.-]+|standard_fonts\/[\w.-]+|wasm\/[\w.-]+)$/u.test(name) || name.split('/').some(p => p === '..' || p === '.')) {
      return new Response(null, { status: 404, headers });
    }
    const type = mime[path.extname(name)];
    if (!type) return new Response(null, { status: 404, headers });
    try {
      const canonicalRoot = await fs.realpath(root);
      const target = await fs.realpath(path.join(root, name));
      if (!target.startsWith(canonicalRoot + path.sep)) return new Response(null, { status: 403, headers });
      const stat = await fs.stat(target);
      if (!stat.isFile() || stat.size > 32 * 1024 * 1024) return new Response(null, { status: 403, headers });
      return new Response(await fs.readFile(target), { status: 200, headers: { ...headers, 'content-type': type } });
    } catch { return new Response(null, { status: 404, headers }); }
  };
}

// Document frames never become workbench frames, and cannot create windows.
// The trusted top frame retains the application's existing navigation policy.
export function guardLocalPreviewNavigation(webContents) {
  const navigate = (event, details) => {
    const frame = details?.frame ?? event.frame;
    const source = frame?.url;
    const destination = details?.url ?? event.url;
    if (isPreviewURL(source) || isPreviewURL(destination)) {
      if (details?.isMainFrame || event.isMainFrame || !isPreviewURL(destination) || (isPreviewURL(source) && new URL(destination).pathname !== '/index.html')) event.preventDefault();
    }
  };
  webContents.on('will-frame-navigate', navigate);
  return () => { if (!webContents.isDestroyed()) webContents.removeListener('will-frame-navigate', navigate); };
}
