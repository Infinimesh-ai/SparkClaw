"use strict";

const { contextBridge, ipcRenderer, webFrame } = require("electron");

const MANAGED_SCRIPTS = [Object.assign({"id":"80700eb2-0fe8-46ea-902f-f4463134057d","file":"revivalstack.user.js","version":"3.1.0-sparkclaw.1","sha256":"a1ce2c17d0058a433659e2985c3d393f0181fa4eaa31c94f4a1eed7dc528738c","matches":[{"origin":"https://chat.openai.com","pathPrefix":"/"},{"origin":"https://chatgpt.com","pathPrefix":"/"},{"origin":"https://claude.ai","pathPrefix":"/"},{"origin":"https://www.copilot.com","pathPrefix":"/"},{"origin":"https://gemini.google.com","pathPrefix":"/"},{"origin":"https://grok.com","pathPrefix":"/"}],"origins":["https://chat.openai.com","https://chatgpt.com","https://claude.ai","https://gemini.google.com","https://grok.com","https://www.copilot.com"],"runAt":"document-end","grants":["GM_getValue","GM_setValue","GM_registerMenuCommand"],"noframes":true},{source:"// ==UserScript==\n// @name         SparkClaw AI Chat Exporter (RevivalStack fork)\n// @namespace    https://github.com/Infinimesh-ai/SparkClaw\n// @version      3.1.0-sparkclaw.1\n// @description  Export your ChatGPT, Claude, Copilot, Gemini or Grok chat into a properly and elegantly formatted Markdown or JSON.\n// @author       Mic Mejia (Refactored by Google Gemini)\n// @homepage     https://github.com/micmejia\n// @license      MIT License\n// @match        https://chat.openai.com/*\n// @match        https://chatgpt.com/*\n// @match        https://claude.ai/*\n// @match        https://www.copilot.com/*\n// @match        https://gemini.google.com/*\n// @match        https://grok.com/*\n// @grant        GM_getValue\n// @grant        GM_setValue\n// @grant        GM_registerMenuCommand\n// @noframes\n// ==/UserScript==\n\n(function () {\n  \"use strict\";\n\n  // BEGIN SPARKCLAW BATCH\n// SparkClaw additions to RevivalStack (MIT).\nconst SPARKCLAW_BATCH_PROVIDERS = {\n  chatgpt: { host: 'chatgpt.com', history: '/', path: /^\\/(?:g\\/[^/]+\\/)?c\\/([\\w-]+)\\/?$/ },\n  claude: { host: 'claude.ai', history: '/recents', path: /^\\/chat\\/([\\w-]+)\\/?$/ },\n  gemini: { host: 'gemini.google.com', history: '/app', path: /^\\/(?:u\\/\\d+\\/)?app\\/([\\w-]+)\\/?$/ },\n  grok: { host: 'grok.com', history: '/history', path: /^\\/c\\/([\\w-]+)\\/?$/ }\n};\nfunction batchConversation(provider, href) {\n  try {\n    const config = SPARKCLAW_BATCH_PROVIDERS[provider], url = new URL(href, 'https://' + config.host);\n    const match = config.path.exec(url.pathname);\n    if (url.protocol !== 'https:' || url.host !== config.host || url.username || url.password || !match) return null;\n    return { id: match[1], url: url.origin + url.pathname.replace(/\\/$/, '') };\n  } catch { return null; }\n}\nfunction batchCollection(provider, href) {\n  try {\n    const config = SPARKCLAW_BATCH_PROVIDERS[provider], url = new URL(href, 'https://' + config.host);\n    if (url.protocol !== 'https:' || url.host !== config.host || url.username || url.password || url.search || url.hash) return null;\n    // Follow only observed history/collection links, never destructive menus,\n    // arbitrary external links, account selectors or guessed private APIs.\n    if (!/^\\/(?:projects?(?:\\/[\\w-]+)?|g\\/[\\w-]+(?:\\/project)?|archived(?:-chats)?|archive|recents|history)\\/?$/.test(url.pathname)) return null;\n    return url.origin + url.pathname.replace(/\\/$/, '');\n  } catch { return null; }\n}\nasync function scanBatchTimeline(provider, io) {\n  const rows = new Map(), collections = new Set();\n  let stable = 0;\n  for (let step = 0; step < 2000; step++) {\n    io.check();\n    const snapshot = await io.snapshot();\n    if (snapshot.blocked) throw new Error('timeline_login_or_loading_blocked');\n    let added = 0;\n    for (const href of snapshot.collections || []) { const url = batchCollection(provider, href); if (url && !collections.has(url)) { collections.add(url); added++; } }\n    for (const item of snapshot.items) {\n      const ref = batchConversation(provider, item.url);\n      if (!ref) continue;\n      const updated = Number.isFinite(Date.parse(item.updated)) ? new Date(item.updated).toISOString() : null;\n      if (!rows.has(ref.id)) { rows.set(ref.id, { ...ref, title: item.title || '', updated, rank: rows.size }); added++; }\n      else if (updated && (!rows.get(ref.id).updated || updated > rows.get(ref.id).updated)) rows.get(ref.id).updated = updated;\n    }\n    if (snapshot.end && !added && !snapshot.busy) stable++; else stable = 0;\n    if (stable >= 5 && (rows.size || snapshot.empty || step >= 29)) {\n      if (!rows.size && !snapshot.empty && !collections.size) throw new Error('timeline_not_found');\n      return { schema: 'sparkclaw.timeline.v1', provider, coverage: 'visible-history', complete: false,\n        collections: [...collections],\n        warning: 'UI exhaustion cannot prove account-wide coverage; archived/project/hidden conversations may be absent.',\n        conversations: [...rows.values()].sort((a, b) => a.updated && b.updated ? a.updated.localeCompare(b.updated) || a.id.localeCompare(b.id) : a.updated ? -1 : b.updated ? 1 : b.rank - a.rank) };\n    }\n    await io.advance();\n  }\n  throw new Error('timeline_scan_limit');\n}\nasync function discoverBatchHistory(provider, initial, scanURL, check) {\n  const rows = new Map(), visited = new Set(), pending = [initial];\n  const sources = [], errors = [];\n  while (pending.length) {\n    check(); const url = pending.shift();\n    if (visited.has(url)) continue;\n    if (visited.size >= 1000) throw new Error('history_collection_limit');\n    visited.add(url);\n    try {\n      const timeline = await scanURL(url);\n      if (timeline.provider !== provider || !Array.isArray(timeline.conversations)) throw new Error('timeline_invalid');\n      sources.push({ url, count: timeline.conversations.length, coverage: timeline.coverage });\n      for (const item of timeline.conversations) {\n        const ref = batchConversation(provider, item.url);\n        if (!ref || ref.id !== item.id) throw new Error('timeline_identity_invalid');\n        const previous = rows.get(item.id);\n        if (!previous || (item.updated && (!previous.updated || item.updated > previous.updated))) rows.set(item.id, item);\n      }\n      for (const href of timeline.collections || []) {\n        const collection = batchCollection(provider, href);\n        if (collection && !visited.has(collection) && !pending.includes(collection)) pending.push(collection);\n      }\n    } catch (error) { check(); errors.push({url,error:error.message}); }\n  }\n  if (!sources.length) throw new Error(errors[0]?.error || 'timeline_not_found');\n  return { schema: 'sparkclaw.timeline.v1', provider, coverage:'visible-history', complete:false,\n    sources, errors, warning:'Only discovered history and collection links were scanned. Hidden archives, projects, and virtualized messages may be absent.',\n    conversations:[...rows.values()].sort((a,b)=>a.updated && b.updated ? a.updated.localeCompare(b.updated) || a.id.localeCompare(b.id) : a.updated ? -1 : b.updated ? 1 : 0) };\n}\n// Reads only rendered UI. No private website API, token, or cookie dependencies.\nfunction batchTimelineSnapshot(provider, doc = document) {\n  const visible = node => {\n    const rect = node.getBoundingClientRect(), style = doc.defaultView.getComputedStyle(node);\n    return rect.width > 0 && rect.height > 0 && style.display !== 'none' && style.visibility !== 'hidden';\n  };\n  const roots = [...doc.querySelectorAll('nav, [role=\"navigation\"], aside, side-navigation, [data-sidebar=\"sidebar\"]'), !!batchCollection(provider, doc.location.href) ? doc.querySelector('main') : null].filter(Boolean).filter((root, index, all) => !all.some((other, i) => i !== index && other.contains(root)));\n  const items = [];\n  for (const root of roots) {\n    for (const node of root.querySelectorAll('a[href], [data-conversation-id]')) {\n      const id = node.getAttribute('data-conversation-id');\n      const url = node.getAttribute('href') || (provider === 'gemini' && id ? '/app/' + id : '');\n      if (!url) continue;\n      const row = node.closest('li, [role=\"listitem\"]') || node;\n      items.push({ url, title: node.textContent.trim(), updated: row.querySelector('time[datetime]')?.getAttribute('datetime') || row.getAttribute('data-update-time') });\n    }\n  }\n  const scrollers = roots.flatMap(root => [root, ...root.querySelectorAll('*')]).filter(node => node.scrollHeight > node.clientHeight + 4 && node.clientHeight > 0 && /auto|scroll/.test(doc.defaultView.getComputedStyle(node).overflowY));\n  const more = roots.flatMap(root => [...root.querySelectorAll('button')]).find(node => /^(load more|show more|加载更多|显示更多)$/i.test(node.textContent.trim()) && !node.disabled && node.getAttribute('aria-disabled') !== 'true' && visible(node));\n  const collections = roots.flatMap(root => [...root.querySelectorAll('a[href]')]).map(node => node.getAttribute('href')).filter(href => batchCollection(provider, href));\n  return { items, collections, end: !more && scrollers.every(node => node.scrollTop + node.clientHeight >= node.scrollHeight - 4),\n    busy: roots.some(root => [...root.querySelectorAll('[aria-busy=\"true\"], [role=\"progressbar\"]')].some(visible)),\n    empty: roots.some(root => /^(no conversations|no chats|暂无对话|暂无聊天)$/i.test(root.textContent.trim())),\n    blocked: !!doc.querySelector('input[type=\"password\"]'), scrollers, more };\n}\nasync function runTimelineBatch({ provider, timeline, ledger, capture, save, verify, commit, check, progress = () => {} }) {\n  const result = { exported: [], skipped: [], failed: [] };\n  if (timeline.provider !== provider || !Array.isArray(timeline.conversations)) throw new Error('timeline_invalid');\n  for (const item of timeline.conversations) {\n    check();\n    const ref = batchConversation(provider, item.url);\n    if (!ref || ref.id !== item.id) throw new Error('timeline_identity_invalid');\n    const previous = ledger[item.id];\n    if (previous && item.updated && previous.updated === item.updated && await verify(previous)) { result.skipped.push(item.id); continue; }\n    try {\n      const text = await capture(item);\n      check();\n      const doc = JSON.parse(text);\n      if (doc.author !== provider || batchConversation(provider, doc.url)?.id !== item.id || !doc.exporter || !Array.isArray(doc.messages) || !doc.messages.length || doc.messages.some(m => !['user', 'ai'].includes(m.author) || typeof m.content !== 'string')) throw new Error('conversation_export_invalid');\n      const contentBytes = new TextEncoder().encode(JSON.stringify({ title: doc.title, messages: doc.messages }));\n      const contentHash = [...new Uint8Array(await crypto.subtle.digest('SHA-256', contentBytes))].map(x => x.toString(16).padStart(2, '0')).join('');\n      if (previous?.contentHash === contentHash && await verify(previous)) {\n        const record = { ...previous, updated: item.updated };\n        await commit(item.id, record); ledger[item.id] = record;\n        result.skipped.push(item.id); continue;\n      }\n      const receipt = await save(item, text);\n      // File persistence, read-back verification, then checkpoint; failures retry.\n      if (!await verify(receipt)) throw new Error('saved_file_verification_failed');\n      check();\n      const record = { ...receipt, contentHash, updated: item.updated, url: item.url };\n      await commit(item.id, record); ledger[item.id] = record;\n      result.exported.push(item.id);\n    } catch (error) { result.failed.push({ id: item.id, error: error.message }); }\n    progress(result);\n  }\n  return result;\n}\nfunction installTimelineBridge(captureCurrent, exportCurrent) {\n  const provider = Object.keys(SPARKCLAW_BATCH_PROVIDERS).find(p => SPARKCLAW_BATCH_PROVIDERS[p].host === location.hostname);\n  if (!provider) return;\n  if (document.querySelector('#sparkclaw-ai-export-bridge')) return;\n  const bridge = document.createElement('output');\n  bridge.id = 'sparkclaw-ai-export-bridge';\n  bridge.hidden = true;\n  bridge.dataset.state = 'ready';\n  document.documentElement.appendChild(bridge);\n  let busy = false;\n  const check = () => {};\n  const pause = ms => new Promise(resolve => setTimeout(resolve, ms));\n  const scan = async () => {\n    for (const node of batchTimelineSnapshot(provider).scrollers) node.scrollTop = 0;\n    await pause(1000);\n    return scanBatchTimeline(provider, { check, snapshot: () => batchTimelineSnapshot(provider), advance: async () => {\n    const view = batchTimelineSnapshot(provider); if (view.more) view.more.click();\n    for (const node of view.scrollers) node.scrollTop += Math.max(100, node.clientHeight * 0.8);\n    await pause(1000);\n  } }); };\n  bridge.addEventListener('sparkclaw-ai-export-command', async () => {\n    if (busy) { bridge.dataset.state = 'failed'; bridge.textContent = 'bridge_busy'; return; }\n    busy = true;\n    bridge.dataset.state = 'working';\n    bridge.textContent = '';\n    try {\n      if (bridge.dataset.command === 'timeline.scan') bridge.textContent = JSON.stringify(await scan());\n      else if (bridge.dataset.command === 'conversation.capture') {\n        // Wait for the rendered transcript to stabilize. This does not prove\n        // that a platform exposed every historical message.\n        let last = '', stable = 0, text;\n        for (let n = 0; n < 90; n++) {\n          if (document.querySelector('[data-testid=\"stop-button\"], button[aria-label=\"Stop streaming\"], button[aria-label=\"Stop response\"]')) throw new Error('conversation_generating');\n          text = captureCurrent();\n          if (!text) { stable = 0; await pause(1000); continue; }\n          const data = JSON.parse(text), body = JSON.stringify(data.messages);\n          if (data.messages?.length && body === last) stable++; else stable = 0;\n          if (stable >= 3) { bridge.textContent = text; break; }\n          last = body;\n          for (const node of document.querySelectorAll('main, main *')) if (node.clientHeight > 0 && node.scrollHeight > node.clientHeight + 4 && /auto|scroll/.test(getComputedStyle(node).overflowY)) node.scrollTop = 0;\n          await pause(1000);\n        }\n        if (!bridge.textContent) throw new Error('conversation_not_ready');\n      } else if (bridge.dataset.command === 'conversation.export-json') exportCurrent();\n      else throw new Error('bridge_command_invalid');\n      bridge.dataset.state = 'ready';\n    } catch (error) {\n      bridge.dataset.state = 'failed';\n      bridge.textContent = error.message;\n    } finally { busy = false; }\n  });\n}\n\n  // END SPARKCLAW BATCH\n\n  // --- Global Constants ---\n  const EXPORTER_VERSION = \"3.1.0-sparkclaw.1\";\n  const EXPORT_CONTAINER_ID = \"export-controls-container\";\n  const OUTLINE_CONTAINER_ID = \"export-outline-container\"; // ID for the outline div\n  const DOM_READY_TIMEOUT = 1000;\n  const EXPORT_BUTTON_TITLE_PREFIX = `AI Chat Exporter v${EXPORTER_VERSION}`;\n  const ALERT_CONTAINER_ID = \"exporter-alert-container\";\n  const HIDE_ALERT_FLAG = \"exporter_hide_scroll_alert\"; // Local Storage flag\n  const ALERT_AUTO_CLOSE_DURATION = 30000; // 30 seconds\n  const OUTLINE_COLLAPSED_STATE_KEY = \"outline_is_collapsed\"; // Local Storage key for collapsed state\n  const AUTOSCROLL_INITIAL_DELAY = 2000; // Initial delay before starting auto-scroll (X seconds)\n  const OUTLINE_TITLE_ID = \"ai-chat-exporter-outline-title\";\n  const OUTPUT_FILE_FORMAT_DEFAULT = \"{platform}_{title}_{timestampLocal}\";\n  const GM_OUTPUT_FILE_FORMAT = \"aiChatExporter_fileFormat\";\n  const GM_CONTROLS_HORIZONTAL_POSITION = \"aiChatExporter_horizontalPosition\";\n  const GM_CONTROLS_VERTICAL_POSITION = \"aiChatExporter_verticalPosition\";\n  const GM_CONTROLS_CHAT_TITLE_PREFIX = \"aiChatExporter_chatTitlePrefix\";\n  const DEFAULT_CHAT_TITLE_PREFIX = \"✓ \";\n\n  GM_registerMenuCommand(\"Set Gemini Chat Title Prefix\", () => {\n    const currentPrefix = GM_getValue(\n      GM_CONTROLS_CHAT_TITLE_PREFIX,\n      DEFAULT_CHAT_TITLE_PREFIX\n    );\n    const newPrefix = prompt(\n      \"Enter the prefix you want to use: \\n\" +\n        \"You only need to set this if you use the 'Set Gemini Chat Title Prefix' Tampermonkey script.\",\n      currentPrefix\n    );\n    if (newPrefix !== null && newPrefix !== currentPrefix) {\n      GM_setValue(GM_CONTROLS_CHAT_TITLE_PREFIX, newPrefix);\n      alert(\n        `Prefix updated to \"${newPrefix}\". Please refresh page to apply to future exports.`\n      );\n    }\n  });\n\n  // --- 1. Horizontal Position Command ---\n  GM_registerMenuCommand(\"Set Horizontal Position\", () => {\n    const screenWidth =\n      document.documentElement.clientWidth || window.innerWidth || 0;\n    const currentPos = GM_getValue(GM_CONTROLS_HORIZONTAL_POSITION, 20);\n\n    const promptMsg =\n      `Set horizontal position (right) in px.\\n\\n` +\n      `Current: ${currentPos}px\\n` +\n      `Screen Width: ${screenWidth}px\\n\\n` +\n      `Note: Invalid input will reset position to 20px. \\n` +\n      `You must refresh the page after for changes to take effect.`;\n\n    const input = prompt(promptMsg, currentPos);\n    if (input !== null) {\n      const parsed = parseInt(input, 10);\n      const isValid = !isNaN(parsed) && parsed >= 0;\n      const finalVal = isValid ? parsed : 20;\n\n      if (isValid && screenWidth > 0 && parsed > screenWidth - 60) {\n        alert(\n          `Warning: ${parsed}px is very large for your current screen width (${screenWidth}px). The controls will likely be hidden off-screen.`\n        );\n      }\n\n      GM_setValue(GM_CONTROLS_HORIZONTAL_POSITION, finalVal);\n      if (isValid)\n        alert(\n          `Horizontal position set to ${finalVal}px. Please refresh to apply.`\n        );\n    }\n  });\n\n  // --- 2. Vertical Position Command ---\n  GM_registerMenuCommand(\"Set Vertical Position\", () => {\n    const screenHeight =\n      document.documentElement.clientHeight || window.innerHeight || 0;\n    const currentPos = GM_getValue(GM_CONTROLS_VERTICAL_POSITION, 20);\n\n    const promptMsg =\n      `Set vertical position (bottom) in px.\\n\\n` +\n      `Current: ${currentPos}px\\n` +\n      `Screen Height: ${screenHeight}px\\n\\n` +\n      `Note: Invalid input will reset position to 20px. \\n` +\n      `You must refresh the page after for changes to take effect.`;\n\n    const input = prompt(promptMsg, currentPos);\n    if (input !== null) {\n      const parsed = parseInt(input, 10);\n      const isValid = !isNaN(parsed) && parsed >= 0;\n      const finalVal = isValid ? parsed : 20;\n\n      if (isValid && screenHeight > 0 && parsed > screenHeight - 100) {\n        alert(\n          `Warning: ${parsed}px is very high. The controls might be pushed off the top of the browser window.`\n        );\n      }\n\n      GM_setValue(GM_CONTROLS_VERTICAL_POSITION, finalVal);\n      if (isValid)\n        alert(\n          `Vertical position set to ${finalVal}px. Please refresh to apply.`\n        );\n    }\n  });\n\n  // --- 3. Dynamic Style Calculation ---\n  const hPos = GM_getValue(GM_CONTROLS_HORIZONTAL_POSITION, 20);\n  const vPos = GM_getValue(GM_CONTROLS_VERTICAL_POSITION, 20);\n\n  const savedHorizontalPos = `${hPos}px`;\n  const savedVerticalPos = `${vPos}px`;\n  const outlineVerticalPos = `${vPos + 50}px`; // Always +50px above main buttons\n\n  const FONT_STACK = `system-ui, -apple-system, BlinkMacSystemFont, \"Segoe UI\", Roboto, \"Helvetica Neue\", Arial, \"Noto Sans\", sans-serif, \"Apple Color Emoji\", \"Segoe UI Emoji\", \"Segoe UI Symbol\", \"Noto Color Emoji\"`;\n\n  const COMMON_CONTROL_PROPS = {\n    position: \"fixed\",\n    bottom: savedVerticalPos,\n    right: savedHorizontalPos,\n    zIndex: \"9999\",\n    boxShadow: \"0 2px 8px rgba(0,0,0,0.2)\",\n    fontSize: \"14px\",\n    cursor: \"pointer\",\n    borderRadius: \"8px\",\n    display: \"flex\",\n    alignItems: \"center\",\n    fontFamily: FONT_STACK,\n  };\n\n  const OUTLINE_CONTAINER_PROPS = {\n    position: \"fixed\",\n    bottom: outlineVerticalPos,\n    right: savedHorizontalPos,\n    zIndex: \"9998\",\n    boxShadow: \"0 2px 8px rgba(0,0,0,0.2)\",\n    fontSize: \"12px\",\n    borderRadius: \"8px\",\n    backgroundColor: \"#fff\",\n    color: \"#333\",\n    maxHeight: \"350px\",\n    width: \"300px\",\n    padding: \"10px\",\n    border: \"1px solid #ddd\",\n    fontFamily: FONT_STACK,\n    display: \"flex\",\n    flexDirection: \"column\",\n    transition:\n      \"max-height 0.3s ease-in-out, padding 0.3s ease-in-out, opacity 0.3s ease-in-out\",\n    opacity: \"1\",\n    transformOrigin: \"bottom right\",\n  };\n\n  const OUTLINE_CONTAINER_COLLAPSED_PROPS = {\n    maxHeight: \"30px\", // Height when collapsed\n    padding: \"5px 10px\",\n    overflow: \"hidden\",\n    opacity: \"0.9\",\n  };\n\n  const OUTLINE_HEADER_PROPS = {\n    display: \"flex\",\n    justifyContent: \"space-between\",\n    alignItems: \"center\",\n    marginBottom: \"5px\",\n    paddingBottom: \"5px\",\n    borderBottom: \"1px solid #eee\",\n    fontWeight: \"bold\",\n    cursor: \"pointer\", // Indicates it's clickable to collapse/expand\n  };\n\n  const OUTLINE_TITLE_PROPS = {\n    display: \"flex\",\n    justifyContent: \"space-between\",\n    alignItems: \"center\",\n    marginBottom: \"5px\",\n    paddingBottom: \"5px\",\n    borderBottom: \"1px solid #eee\",\n    wordWrap: \"break-word\" /* Ensures long titles wrap */,\n  };\n\n  // Styles for the \"Select all\" section\n  const SELECT_ALL_CONTAINER_PROPS = {\n    display: \"flex\",\n    alignItems: \"center\",\n    padding: \"5px 0\",\n    marginBottom: \"5px\",\n    borderBottom: \"1px solid #eee\",\n  };\n\n  // Styles for the search bar\n  const SEARCH_INPUT_PROPS = {\n    width: \"calc(100% - 20px)\", // Full width minus padding\n    padding: \"6px 10px\",\n    margin: \"5px 0 10px 0\",\n    border: \"1px solid #ddd\",\n    borderRadius: \"4px\",\n    fontSize: \"12px\",\n    fontFamily: FONT_STACK,\n  };\n\n  const NO_MATCH_MESSAGE_PROPS = {\n    textAlign: \"center\",\n    fontStyle: \"italic\",\n    fontWeight: \"bold\",\n    color: \"#666\",\n    padding: \"10px 0\",\n  };\n\n  const OUTLINE_ITEM_PROPS = {\n    display: \"flex\",\n    alignItems: \"center\",\n    marginBottom: \"3px\",\n    lineHeight: \"1.3\",\n  };\n\n  const OUTLINE_CHECKBOX_PROPS = {\n    marginRight: \"5px\",\n    cursor: \"pointer\",\n  };\n\n  const OUTLINE_TOGGLE_BUTTON_PROPS = {\n    background: \"none\",\n    border: \"none\",\n    fontSize: \"16px\",\n    cursor: \"pointer\",\n    padding: \"0 5px\",\n    color: \"#5b3f87\",\n  };\n\n  const BUTTON_BASE_PROPS = {\n    height: \"32px\", // 4px shorter than our previous 36px target\n    display: \"inline-flex\",\n    alignItems: \"center\",\n    justifyContent: \"center\",\n    padding: \"0 15px\", // Increased from 14px to 15px for extra width\n    backgroundColor: \"#5b3f87\",\n    color: \"white\",\n    border: \"none\",\n    cursor: \"pointer\",\n    borderRadius: \"6px\", // Slightly tighter radius for shorter buttons\n    fontSize: \"12px\", // Slightly smaller font to fit the 32px height\n    fontWeight: \"600\",\n    boxSizing: \"border-box\",\n    lineHeight: \"1\",\n    flexShrink: \"0\", // Prevents Gemini from squeezing buttons\n  };\n\n  const BUTTON_SPACING_PROPS = {\n    marginLeft: \"8px\",\n  };\n\n  // --- Alert Styles ---\n  // Note: max-width for ALERT_PROPS will be dynamically set\n  const ALERT_PROPS = {\n    position: \"fixed\",\n    top: \"20px\",\n    left: \"50%\",\n    transform: \"translateX(-50%)\",\n    zIndex: \"10000\",\n    backgroundColor: \"rgba(91, 63, 135, 0.9)\", // Shade of #5b3f87 with transparency\n    color: \"white\",\n    padding: \"15px 20px\",\n    borderRadius: \"8px\",\n    boxShadow: \"0 2px 10px rgba(0,0,0,0.2)\",\n    display: \"flex\",\n    flexDirection: \"column\", // Changed to column for title, message and checkbox\n    justifyContent: \"space-between\",\n    alignItems: \"flex-start\", // Align items to the start for better layout\n    fontSize: \"14px\",\n    opacity: \"1\",\n    transition: \"opacity 0.5s ease-in-out\",\n    fontFamily: FONT_STACK,\n  };\n\n  const ALERT_MESSAGE_ROW_PROPS = {\n    display: \"flex\",\n    justifyContent: \"space-between\",\n    alignItems: \"center\",\n    width: \"100%\",\n    marginBottom: \"10px\", // Space between message and checkbox\n  };\n\n  const ALERT_CLOSE_BUTTON_PROPS = {\n    background: \"none\",\n    border: \"none\",\n    color: \"white\",\n    fontSize: \"20px\",\n    cursor: \"pointer\",\n    marginLeft: \"15px\", // Add margin to push it right\n    lineHeight: \"1\", // Align 'x' vertically\n  };\n\n  const ALERT_CHECKBOX_CONTAINER_PROPS = {\n    display: \"flex\",\n    alignItems: \"center\",\n    width: \"100%\",\n  };\n\n  const ALERT_CHECKBOX_PROPS = {\n    marginRight: \"5px\",\n  };\n\n  // --- Hostname-Specific Selectors & Identifiers ---\n  const CHATGPT = \"chatgpt\";\n  const CHATGPT_HOSTNAMES = [\"chat.openai.com\", \"chatgpt.com\"];\n  const CHATGPT_TITLE_REPLACE_TEXT = \" - ChatGPT\";\n  const CHATGPT_ARTICLE_SELECTOR = \"section[data-testid^='conversation-turn-']\";\n  const CHATGPT_HEADER_SELECTOR = \"h4\"; // Targets the hidden \"You said:\" / \"ChatGPT said:\"\n  const CHATGPT_TEXT_DIV_SELECTOR = \".markdown, .whitespace-pre-wrap\";\n  const CHATGPT_USER_MESSAGE_INDICATOR = \"you said\";\n  const CHATGPT_POPUP_DIV_CLASS = \"popover\";\n  const CHATGPT_BUTTON_SPECIFIC_CLASS = \"text-sm\";\n\n  const GEMINI = \"gemini\";\n  const GEMINI_HOSTNAMES = [\"gemini.google.com\"];\n  const GEMINI_TITLE_REPLACE_TEXT = \"Gemini - \";\n  const GEMINI_MESSAGE_ITEM_SELECTOR = \"user-query, model-response\";\n  const GEMINI_SIDEBAR_ACTIVE_CHAT_SELECTOR =\n    'a[data-test-id=\"conversation\"].selected .conversation-title';\n  const GEMINI_TOPBAR_ACTIVE_CHAT_SELECTOR =\n    \"conversation-actions .conversation-title\";\n\n  const CLAUDE = \"claude\";\n  const CLAUDE_HOSTNAMES = [\"claude.ai\"];\n  const CLAUDE_MESSAGE_SELECTOR =\n    \".font-claude-response:not(#markdown-artifact), [data-testid='user-message']\";\n  const CLAUDE_USER_MESSAGE_SELECTOR = '[data-testid=\"user-message\"]';\n  const CLAUDE_THINKING_BLOCK_CLASS = \"transition-all\";\n  const CLAUDE_ARTIFACT_BLOCK_CELL = \".artifact-block-cell\";\n\n  const COPILOT = \"copilot\";\n  const COPILOT_HOSTNAMES = [\"www.copilot.com\"];\n  const COPILOT_MESSAGE_SELECTOR = \".group\\\\/user-message, .group\\\\/ai-message\";\n  const COPILOT_USER_MESSAGE_SELECTOR = \".group\\\\/user-message\";\n  const COPILOT_BOT_MESSAGE_SELECTOR = \".group\\\\/ai-message\";\n\n  const GROK = \"grok\";\n  const GROK_HOSTNAMES = [\"grok.com\"];\n  const GROK_MESSAGE_SELECTOR = \"div[id^='response-']\";\n  const GROK_CONTENT_SELECTOR = \".response-content-markdown\";\n  const GROK_USER_INDICATOR = \".items-end\";\n\n  const HOSTNAME = window.location.hostname;\n  const CURRENT_PLATFORM = (() => {\n    if (CHATGPT_HOSTNAMES.some((host) => HOSTNAME.includes(host))) {\n      return CHATGPT;\n    }\n    if (CLAUDE_HOSTNAMES.some((host) => HOSTNAME.includes(host))) {\n      return CLAUDE;\n    }\n    if (COPILOT_HOSTNAMES.some((host) => HOSTNAME.includes(host))) {\n      return COPILOT;\n    }\n    if (GEMINI_HOSTNAMES.some((host) => HOSTNAME.includes(host))) {\n      return GEMINI;\n    }\n    if (GROK_HOSTNAMES.some((host) => HOSTNAME.includes(host))) {\n      return GROK;\n    }\n    return \"unknown\";\n  })();\n\n  // --- Markdown Formatting Constants ---\n  const DEFAULT_CHAT_TITLE = \"chat\";\n  const MARKDOWN_TOC_PLACEHOLDER_LINK = \"#table-of-contents\";\n  const MARKDOWN_BACK_TO_TOP_LINK = `___\\n###### [top](${MARKDOWN_TOC_PLACEHOLDER_LINK})\\n`;\n\n  // Parents of <p> tags where newlines should be suppressed or handled differently\n  // LI is handled separately in the paragraph rule for single newlines.\n  const PARAGRAPH_FILTER_PARENT_NODES = [\"TH\", \"TR\"];\n\n  // Styles for the scrollable message list div\n  const MESSAGE_LIST_PROPS = {\n    overflowY: \"auto\", // Enable vertical scrolling for this specific div\n    flexGrow: \"1\", // Allow it to grow and take available space\n    paddingRight: \"5px\", // Add some padding for scrollbar visibility\n  };\n\n  // --- Inlined Turndown.js (v7.1.2) - BEGIN ---\n  // Customized TurndownService to handle specific chat DOM structures\n  class TurndownService {\n    constructor(options = {}) {\n      this.rules = [];\n      this.options = {\n        headingStyle: \"atx\",\n        hr: \"___\",\n        bulletListMarker: \"-\",\n        codeBlockStyle: \"fenced\",\n        ...options,\n      };\n    }\n\n    addRule(key, rule) {\n      this.rules.push({ key, ...rule });\n    }\n\n    turndown(rootNode) {\n      let output = \"\";\n\n      const process = (node) => {\n        if (node.nodeType === Node.TEXT_NODE) return node.nodeValue;\n        if (node.nodeType !== Node.ELEMENT_NODE) return \"\";\n\n        const rule = this.rules.find(\n          (r) =>\n            (typeof r.filter === \"string\" &&\n              r.filter === node.nodeName.toLowerCase()) ||\n            (Array.isArray(r.filter) &&\n              r.filter.includes(node.nodeName.toLowerCase())) ||\n            (typeof r.filter === \"function\" && r.filter(node))\n        );\n\n        const content = Array.from(node.childNodes)\n          .map((n) => process(n))\n          .join(\"\");\n\n        if (rule) return rule.replacement(content, node, this.options);\n        return content;\n      };\n\n      let parsedRootNode = rootNode;\n      if (typeof rootNode === \"string\") {\n        const parser = new DOMParser();\n        const doc = parser.parseFromString(rootNode, \"text/html\");\n        parsedRootNode = doc.body || doc.documentElement;\n      }\n\n      output = Array.from(parsedRootNode.childNodes)\n        .map((n) => process(n))\n        .join(\"\");\n      // Clean up excessive newlines (more than two)\n      return output.trim().replace(/\\n{3,}/g, \"\\n\\n\");\n    }\n  }\n  // --- Inlined Turndown.js - END ---\n\n  // --- Utility Functions ---\n  const Utils = {\n    /**\n     * Converts a string into a URL-friendly slug.\n     * @param {string} str The input text.\n     * @returns {string} The slugified string.\n     */\n    slugify(str, toLowerCase = true, maxLength = 120) {\n      if (typeof str !== \"string\") {\n        return \"invalid-filename\"; // Handle non-string input gracefully\n      }\n      if (toLowerCase) {\n        str = str.toLocaleLowerCase();\n      }\n      return str\n        .replace(/[^a-zA-Z0-9\\-_.+]+/g, \"-\")\n        .replace(/-+/g, \"-\")\n        .replace(/^-|-$/g, \"\")\n        .replace(/^$/, \"invalid-filename\")\n        .slice(0, maxLength);\n    },\n\n    /**\n     * Formats a Date object into a local time string with UTC offset.\n     * @param {Date} d The Date object.\n     * @returns {string} The formatted local time string.\n     */\n    formatLocalTime(d) {\n      const pad = (n) => String(n).padStart(2, \"0\");\n      const tzOffsetMin = -d.getTimezoneOffset();\n      const sign = tzOffsetMin >= 0 ? \"+\" : \"-\";\n      const absOffset = Math.abs(tzOffsetMin);\n      const offsetHours = pad(Math.floor(absOffset / 60));\n      const offsetMinutes = pad(absOffset % 60);\n      return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(\n        d.getDate()\n      )}T${pad(d.getHours())}-${pad(d.getMinutes())}-${pad(\n        d.getSeconds()\n      )}${sign}${offsetHours}${offsetMinutes}`;\n    },\n\n    /**\n     * Truncates a string to a given maximum length, adding \"…\" if truncated.\n     * @param {string} str The input string.\n     * @param {number} [len=70] The maximum length.\n     * @returns {string} The truncated string.\n     */\n    truncate(str, len = 70) {\n      return str.length <= len ? str : str.slice(0, len).trim() + \"…\";\n    },\n\n    /**\n     * Escapes Markdown special characters in a string.\n     * @param {string} text The input string.\n     * @returns {string} The string with Markdown characters escaped.\n     */\n    escapeMd(text) {\n      return text.replace(/[|\\\\`*_{}\\[\\]()#+\\-!>]/g, \"\\\\/*__SPARKCLAW_MANAGED_SCRIPTS__*/\");\n    },\n\n    /**\n     * Downloads text content as a file.\n     * @param {string} filename The name of the file to download.\n     * @param {string} text The content to save.\n     * @param {string} [mimeType='text/plain;charset=utf-8'] The MIME type.\n     */\n    downloadFile(filename, text, mimeType = \"text/plain;charset=utf-8\") {\n      const blob = new Blob([text], { type: mimeType });\n      const url = URL.createObjectURL(blob);\n      const a = document.createElement(\"a\");\n      a.href = url;\n      a.download = filename;\n      a.click();\n      URL.revokeObjectURL(url);\n    },\n\n    /**\n     * Applies a set of CSS properties to an element.\n     * @param {HTMLElement} element The HTML element to style.\n     * @param {object} styles An object where keys are CSS property names (camelCase) and values are their values.\n     */\n    applyStyles(element, styles) {\n      for (const prop in styles) {\n        element.style[prop] = styles[prop];\n      }\n    },\n\n    /**\n     * Formats a filename string based on provided format and chat data.\n     *\n     * @param {string} format - The format string with placeholders (e.g., \"{platform}_{tag1}_{title}_{timestamp}.md\").\n     * @param {string} title - The cleaned title of the chat.\n     * @param {string[]} tags - An array of tags for the chat.\n     * @param {string} ext - The file extenstion without leading dot.\n     * @returns {string} The formatted filename.\n     */\n    formatFileName(format, title, tags, ext) {\n      // Ensure tags is an array\n      const tagsArray = Array.isArray(tags) ? tags : [];\n\n      const replacements = {\n        \"{exporter}\": EXPORTER_VERSION,\n        \"{platform}\": CURRENT_PLATFORM,\n        \"{title}\": title.slice(0, 70).toLocaleLowerCase(),\n        \"{timestamp}\": new Date().toISOString(),\n        \"{timestampLocal}\": Utils.formatLocalTime(new Date()),\n        \"{tags}\": tagsArray.join(\"-\").toLocaleLowerCase(), // Comma separated string of all tags\n      };\n\n      // Add individual tags (tag1 to tag9)\n      for (let i = 0; i < 9; i++) {\n        const tagName = `{tag${i + 1}}`;\n        replacements[tagName] = tagsArray[i]\n          ? tagsArray[i].toLocaleLowerCase()\n          : \"\"; // Use tag if it exists, otherwise empty string\n      }\n\n      let formattedFilename = format;\n      for (const placeholder in replacements) {\n        if (replacements.hasOwnProperty(placeholder)) {\n          // Replace all occurrences of the placeholder with its value\n          formattedFilename = formattedFilename\n            .split(placeholder)\n            .join(replacements[placeholder]);\n        }\n      }\n\n      return Utils.slugify(\n        `${formattedFilename.replace(/(_+|-+)$/, \"\")}.${ext}`,\n        false\n      );\n    },\n\n    /**\n     * Parses a raw chat title to extract tags and the cleaned main title.\n     * Tags starting with '#' followed by one or more digits are ignored.\n     *\n     * @param {string} rawTitle - The raw chat title string, e.g., \"#aice #plan #tech #50731 Browser Storage Options Comparison\".\n     * @returns {{title: string, tags: string[]}} An object containing the cleaned title and an array of extracted tags.\n     */\n    parseChatTitleAndTags(rawTitle) {\n      const tags = [];\n      let cleanedTitle = rawTitle.trim();\n\n      // Regular expression to find tags at the beginning of the string:\n      // # (hash)\n      // \\S+ (one or more non-whitespace characters)\n      // ^ (start of string)\n      // (\\s*#\\S+)* (zero or more occurrences of space and then a tag)\n      const tagRegex = /(^|\\s+)#(\\S+)/g;\n      let match;\n\n      // Iterate over all matches to extract tags\n      while ((match = tagRegex.exec(cleanedTitle)) !== null) {\n        const fullTag = match[0].trim(); // e.g., \"#aice\", \" #plan\"\n        const tagName = match[2]; // e.g., \"aice\", \"plan\"\n\n        // Check if the tag is numeric (e.g., #50731)\n        if (!/^\\d+$/.test(tagName)) {\n          tags.push(tagName);\n        }\n      }\n\n      // Remove all tags from the title string, including the numeric ones,\n      // to get the final cleaned title.\n      // This regex matches a hash, followed by one or more non-whitespace characters,\n      // optionally followed by a space, only if it appears at the beginning or after a space.\n      cleanedTitle = cleanedTitle.replace(/(^|\\s+)#\\S+/g, \" \").trim();\n\n      // Remove any extra spaces that might result from tag removal\n      cleanedTitle = cleanedTitle.replace(/\\s+/g, \" \").trim();\n\n      return {\n        title: cleanedTitle,\n        tags: tags,\n      };\n    },\n\n    /**\n     * Returns the current URL without query parameters or hash fragments.\n     * Robust for ChatGPT, Claude, Copilot, and Gemini.\n     */\n    getCleanUrl() {\n      try {\n        return window.location.origin + window.location.pathname;\n      } catch (e) {\n        return window.location.href; // Fallback to full URL on unexpected error\n      }\n    },\n  };\n\n  // --- Core Export Logic ---\n  const ChatExporter = {\n    _currentChatData: null, // Store the last extracted chat data\n    _selectedMessageIds: new Set(), // Store IDs of selected messages for export\n\n    /**\n     * Extracts chat data from ChatGPT's DOM structure.\n     * @param {Document} doc - The Document object.\n     * @returns {object|null} The standardized chat data, or null.\n     */\n    extractChatGPTChatData(doc) {\n      const articles = [...doc.querySelectorAll(CHATGPT_ARTICLE_SELECTOR)];\n      if (articles.length === 0) return null;\n\n      let title =\n        doc.title.replace(CHATGPT_TITLE_REPLACE_TEXT, \"\").trim() ||\n        DEFAULT_CHAT_TITLE;\n      const messages = [];\n      let chatIndex = 1;\n\n      for (const article of articles) {\n        const turnType = article.getAttribute(\"data-turn\");\n        const header =\n          article.querySelector(CHATGPT_HEADER_SELECTOR)?.textContent?.trim() ||\n          \"\";\n\n        const isUser =\n          turnType === \"user\" ||\n          header.toLowerCase().includes(CHATGPT_USER_MESSAGE_INDICATOR);\n        const author = isUser ? \"user\" : \"ai\";\n\n        // CRITICAL FIX: Target exactly the content container to ignore action buttons\n        const contentTarget = article.querySelector(\n          \".markdown, .whitespace-pre-wrap\"\n        );\n        const contentHtml = contentTarget || article;\n\n        const contentText = contentHtml.innerText.trim();\n\n        if (!contentText) continue;\n\n        const messageId = `${author}-${chatIndex}-${Date.now()}-${Math.random()\n          .toString(36)\n          .substring(2, 9)}`;\n\n        messages.push({\n          id: messageId,\n          author: author,\n          contentHtml: contentHtml, // Pass the clean container to Turndown\n          contentText: contentText,\n          timestamp: new Date(),\n          originalIndex: chatIndex,\n        });\n\n        if (!isUser) chatIndex++;\n      }\n\n      const _parsedTitle = Utils.parseChatTitleAndTags(title);\n\n      return {\n        _raw_title: title,\n        title: _parsedTitle.title,\n        tags: _parsedTitle.tags,\n        author: CURRENT_PLATFORM,\n        messages: messages,\n        messageCount: messages.filter((m) => m.author === \"user\").length,\n        exportedAt: new Date(),\n        exporterVersion: EXPORTER_VERSION,\n        threadUrl: Utils.getCleanUrl(),\n      };\n    },\n\n    /**\n     * Extracts chat data from Claude's DOM structure.\n     * @param {Document} doc - The Document object.\n     * @returns {object|null} The standardized chat data, or null.\n     */\n    extractClaudeChatData(doc) {\n      const messageItems = [...doc.querySelectorAll(CLAUDE_MESSAGE_SELECTOR)];\n      if (messageItems.length === 0) return null;\n\n      const messages = [];\n      let chatIndex = 1;\n      // --- Strip the autogenerated '- Claude' suffix ---\n      let rawTitle = doc.title || DEFAULT_CHAT_TITLE;\n      const chatTitle = rawTitle.replace(/\\s-\\sClaude$/, \"\").trim();\n\n      messageItems.forEach((item) => {\n        // const isUser = item.classList.contains(CLAUDE_USER_MESSAGE_CLASS);\n        const isUser = item.matches(CLAUDE_USER_MESSAGE_SELECTOR);\n        const author = isUser ? \"user\" : \"ai\";\n\n        let messageContentHtml = null;\n        let messageContentText = \"\";\n\n        if (isUser) {\n          // For user messages, the entire div is the content\n          messageContentHtml = item;\n          messageContentText = item.innerText.trim();\n        } else {\n          // For Claude messages, we need to filter out \"thinking\" blocks\n          const claudeResponseContent = document.createElement(\"div\");\n          Array.from(item.children).forEach((child) => {\n            const isThinkingBlock = child.className.includes(\n              CLAUDE_THINKING_BLOCK_CLASS\n            );\n            const isArtifactBlock =\n              (child.className.includes(\"pt-3\") &&\n                child.className.includes(\"pb-3\")) ||\n              child.querySelector(CLAUDE_ARTIFACT_BLOCK_CELL);\n\n            // Only consider non-thinking, non-artifact blocks\n            if (!isThinkingBlock && !isArtifactBlock) {\n              const contentGrid = child.querySelector(\".grid-cols-1\");\n              if (contentGrid) {\n                // We will use the existing TurndownService to process this content\n                claudeResponseContent.appendChild(contentGrid.cloneNode(true));\n              }\n            }\n          });\n          messageContentHtml = claudeResponseContent;\n          messageContentText = claudeResponseContent.innerText.trim();\n        }\n\n        if (messageContentText) {\n          const messageId = `${author}-${chatIndex}-${Date.now()}-${Math.random()\n            .toString(36)\n            .substring(2, 9)}`;\n\n          messages.push({\n            id: messageId,\n            author: author,\n            contentHtml: messageContentHtml,\n            contentText: messageContentText,\n            timestamp: new Date(),\n            originalIndex: chatIndex,\n          });\n\n          if (!isUser) chatIndex++;\n        }\n      });\n\n      const _parsedTitle = Utils.parseChatTitleAndTags(chatTitle);\n\n      return {\n        _raw_title: chatTitle,\n        title: _parsedTitle.title,\n        tags: _parsedTitle.tags,\n        author: CURRENT_PLATFORM,\n        messages: messages,\n        messageCount: messages.filter((m) => m.author === \"user\").length,\n        exportedAt: new Date(),\n        exporterVersion: EXPORTER_VERSION,\n        threadUrl: Utils.getCleanUrl(),\n      };\n    },\n\n    /**\n     * Extracts chat data from Copilot's DOM structure.\n     * @param {Document} doc - The Document object.\n     * @returns {object|null} The standardized chat data, or null.\n     */\n    extractCopilotChatData(doc) {\n      const messageItems = [...doc.querySelectorAll(COPILOT_MESSAGE_SELECTOR)];\n      if (messageItems.length === 0) return null;\n\n      const messages = [];\n      let chatIndex = 1;\n\n      let rawTitle = \"\";\n      const selected = doc.querySelector(\n        '[role=\"option\"][aria-selected=\"true\"]'\n      );\n      if (selected) {\n        rawTitle =\n          selected.querySelector(\"p\")?.textContent.trim() ||\n          (selected.getAttribute(\"aria-label\") || \"\")\n            .split(\",\")\n            .slice(1)\n            .join(\",\")\n            .trim();\n      }\n      if (!rawTitle) {\n        rawTitle = (doc.title || \"\")\n          .replace(/^\\s*Microsoft[_\\s-]*Copilot.*$/i, \"\")\n          .replace(/\\s*[-–|]\\s*Copilot.*$/i, \"\")\n          .trim();\n      }\n      if (!rawTitle) rawTitle = \"Copilot Conversation\";\n\n      for (const item of messageItems) {\n        const isUser = item.matches(COPILOT_USER_MESSAGE_SELECTOR);\n        const author = isUser ? \"user\" : \"ai\";\n\n        // Target the specific inner content wrappers based on the new DOM\n        const messageContentElem = isUser\n          ? item.querySelector('[data-content=\"user-message\"]')\n          : item.querySelector(\".group\\\\/ai-message-item\");\n\n        if (!messageContentElem) continue;\n\n        const messageId = `${author}-${chatIndex}-${Date.now()}-${Math.random()\n          .toString(36)\n          .substring(2, 9)}`;\n\n        messages.push({\n          id: messageId,\n          author: author,\n          contentHtml: isUser\n            ? messageContentElem\n            : messageContentElem.cloneNode(true),\n          contentText: messageContentElem.innerText.trim(),\n          timestamp: new Date(),\n          originalIndex: chatIndex,\n        });\n\n        if (author === \"ai\") chatIndex++;\n      }\n\n      const _parsedTitle = Utils.parseChatTitleAndTags(rawTitle);\n\n      return {\n        _raw_title: rawTitle,\n        title: _parsedTitle.title,\n        tags: _parsedTitle.tags,\n        author: COPILOT,\n        messages: messages,\n        messageCount: messages.filter((m) => m.author === \"user\").length,\n        exportedAt: new Date(),\n        exporterVersion: EXPORTER_VERSION,\n        threadUrl: Utils.getCleanUrl(),\n      };\n    },\n\n    /**\n     * Extracts chat data from Gemini's DOM structure.\n     * @param {Document} doc - The Document object.\n     * @returns {object|null} The standardized chat data, or null.\n     */\n    extractGeminiChatData(doc) {\n      const messageItems = [\n        ...doc.querySelectorAll(GEMINI_MESSAGE_ITEM_SELECTOR),\n      ];\n      if (messageItems.length === 0) return null;\n\n      let title = DEFAULT_CHAT_TITLE;\n\n      // 1. Prioritize title from sidebar if available and not generic\n      const sidebarActiveChatItem = doc.querySelector(\n        GEMINI_SIDEBAR_ACTIVE_CHAT_SELECTOR\n      );\n\n      // 2. Fallback to topbar title\n      const topbarActiveChatItem = doc.querySelector(\n        GEMINI_TOPBAR_ACTIVE_CHAT_SELECTOR\n      );\n\n      if (sidebarActiveChatItem && sidebarActiveChatItem.textContent.trim()) {\n        title = sidebarActiveChatItem.textContent.trim();\n      } else if (\n        topbarActiveChatItem &&\n        topbarActiveChatItem.textContent.trim()\n      ) {\n        title = topbarActiveChatItem.textContent.trim();\n      } else {\n        title = doc.title;\n      }\n\n      // 3. Clean up the title for the filename\n      // Remove the generic \"Gemini - \" prefix if present\n      if (title.startsWith(GEMINI_TITLE_REPLACE_TEXT)) {\n        title = title.replace(GEMINI_TITLE_REPLACE_TEXT, \"\").trim();\n      }\n\n      // 4. Specifically strip the configurable chat title prefix\n      // This ensures the filename doesn't include the status mark/prefix\n      const configuredPrefix = GM_getValue(\n        GM_CONTROLS_CHAT_TITLE_PREFIX,\n        DEFAULT_CHAT_TITLE_PREFIX\n      );\n\n      if (configuredPrefix && title.startsWith(configuredPrefix.trim())) {\n        // Escape prefix for regex safety (handles characters like [, *, +, etc.)\n        const escapedPrefix = configuredPrefix\n          .trim()\n          .replace(/[.*+?^${}()|[\\]\\\\]/g, \"\\\\/*__SPARKCLAW_MANAGED_SCRIPTS__*/\");\n        const prefixRegex = new RegExp(`^${escapedPrefix}\\\\s*`);\n        title = title.replace(prefixRegex, \"\").trim();\n      }\n\n      const messages = [];\n      let chatIndex = 1;\n\n      for (const item /* @type {HTMLElement} */ of messageItems) {\n        let author = \"\";\n        let messageContentElem = null;\n\n        const tagName = item.tagName.toLowerCase();\n\n        if (tagName === \"user-query\") {\n          author = \"user\";\n          messageContentElem = item.querySelector(\"div.query-content\");\n        } else if (tagName === \"model-response\") {\n          author = \"ai\";\n          messageContentElem = item.querySelector(\"message-content\");\n        }\n\n        if (!messageContentElem) continue;\n\n        // Assign a unique ID to each message. This is crucial for selection.\n        const messageId = `${author}-${chatIndex}-${Date.now()}-${Math.random()\n          .toString(36)\n          .substring(2, 9)}`;\n\n        messages.push({\n          id: messageId, // Unique ID\n          author: author,\n          contentHtml: messageContentElem, // Store the direct DOM Element\n          contentText: messageContentElem.innerText\n            .replace(/^you said\\s+/i, \"\")\n            .trim(),\n          timestamp: new Date(),\n          originalIndex: chatIndex, // Keep original index for outline\n        });\n\n        if (author === \"ai\") chatIndex++;\n      }\n\n      // Final fallback to the first user message if title is still default\n      if (\n        title === DEFAULT_CHAT_TITLE &&\n        messages.length > 0 &&\n        messages[0].author === \"user\"\n      ) {\n        const firstUserMessage = messages[0].contentText;\n        const words = firstUserMessage\n          .split(/\\s+/)\n          .filter((word) => word.length > 0);\n        if (words.length > 0) {\n          let generatedTitle = words.slice(0, 7).join(\" \");\n          generatedTitle = generatedTitle.replace(/[,.;:!?\\-+]$/, \"\").trim();\n          if (generatedTitle.length < 5 && words.length > 1) {\n            generatedTitle = words\n              .slice(0, Math.min(words.length, 10))\n              .join(\" \");\n            generatedTitle = generatedTitle.replace(/[,.;:!?\\-+]$/, \"\").trim();\n          }\n          title = generatedTitle || DEFAULT_CHAT_TITLE;\n        }\n      }\n\n      const _parsedTitle = Utils.parseChatTitleAndTags(title);\n\n      return {\n        _raw_title: title,\n        title: _parsedTitle.title,\n        tags: _parsedTitle.tags,\n        author: CURRENT_PLATFORM,\n        messages: messages,\n        messageCount: messages.filter((m) => m.author === \"user\").length, // Count user messages as questions\n        exportedAt: new Date(),\n        exporterVersion: EXPORTER_VERSION,\n        threadUrl: Utils.getCleanUrl(),\n      };\n    },\n\n    /**\n     * Extracts chat data from Grok's DOM structure.\n     * @param {Document} doc - The Document object.\n     * @returns {object|null} The standardized chat data, or null.\n     */\n    extractGrokChatData(doc) {\n      const messageItems = [...doc.querySelectorAll(GROK_MESSAGE_SELECTOR)];\n      if (messageItems.length === 0) return null;\n\n      const messages = [];\n      let chatIndex = 1;\n      // --- Strip the autogenerated '- Grok' suffix ---\n      let rawTitle = doc.title || DEFAULT_CHAT_TITLE;\n      const chatTitle = rawTitle.replace(/\\s-\\sGrok$/, \"\").trim();\n\n      for (const item of messageItems) {\n        const isUser = item.matches(GROK_USER_INDICATOR);\n        const author = isUser ? \"user\" : \"ai\";\n        const messageContentElem = item.querySelector(GROK_CONTENT_SELECTOR);\n\n        if (!messageContentElem) continue;\n\n        // Use the narrower .response-content-markdown for the text preview (outline/TOC),\n        // but the full .message-bubble for HTML conversion (so code blocks are included).\n        const textSourceElem = isUser\n          ? messageContentElem\n          : item.querySelector(\".response-content-markdown\") ||\n            messageContentElem;\n\n        const messageId = `${author}-${chatIndex}-${Date.now()}-${Math.random()\n          .toString(36)\n          .substring(2, 9)}`;\n\n        const clonedContent = isUser\n          ? messageContentElem\n          : messageContentElem.cloneNode(true);\n        const cbInClone = clonedContent.querySelectorAll\n          ? clonedContent.querySelectorAll('[data-testid=\"code-block\"]')\n          : [];\n\n        messages.push({\n          id: messageId,\n          author: author,\n          contentHtml: clonedContent,\n          contentText: textSourceElem.innerText.trim(),\n          timestamp: new Date(),\n          originalIndex: chatIndex,\n        });\n\n        if (author === \"ai\") chatIndex++;\n      }\n\n      const _parsedTitle = Utils.parseChatTitleAndTags(chatTitle);\n\n      return {\n        _raw_title: chatTitle,\n        title: _parsedTitle.title,\n        tags: _parsedTitle.tags,\n        author: GROK,\n        messages: messages,\n        messageCount: messages.filter((m) => m.author === \"user\").length,\n        exportedAt: new Date(),\n        exporterVersion: EXPORTER_VERSION,\n        threadUrl: Utils.getCleanUrl(),\n      };\n    },\n\n    /**\n     * Converts standardized chat data to Markdown format.\n     * This function now expects a pre-filtered `chatData`.\n     * @param {object} chatData - The standardized chat data (already filtered).\n     * @param {TurndownService} turndownServiceInstance - Configured TurndownService.\n     * @returns {{output: string, fileName: string}} Markdown string and filename.\n     */\n    formatToMarkdown(chatData, turndownServiceInstance) {\n      let toc = \"\";\n      let content = \"\";\n      let exportChatIndex = 0; // Initialize to 0 for sequential user message numbering\n\n      chatData.messages.forEach((msg) => {\n        if (msg.author === \"user\") {\n          exportChatIndex++; // Increment only for user messages\n          const preview = Utils.truncate(\n            msg.contentText.replace(/\\s+/g, \" \"),\n            70\n          );\n          toc += `- [${exportChatIndex}: ${Utils.escapeMd(\n            preview\n          )}](#chat-${exportChatIndex})\\n`;\n          content +=\n            `## chat-${exportChatIndex}\\n\\n> ` +\n            msg.contentText.replace(/\\n/g, \"\\n> \") +\n            \"\\n\\n\";\n        } else {\n          let markdownContent;\n          try {\n            markdownContent = turndownServiceInstance.turndown(msg.contentHtml);\n          } catch (e) {\n            console.error(\n              `Error converting AI message ${msg.id} to Markdown:`,\n              e\n            );\n            markdownContent = `[CONVERSION ERROR: Failed to render this section. Original content below]\\n\\n\\`\\`\\`\\n${msg.contentText}\\n\\`\\`\\`\\n`;\n          }\n          content += markdownContent + \"\\n\\n\" + MARKDOWN_BACK_TO_TOP_LINK;\n        }\n        // Removed the incorrect increment logic from here\n      });\n\n      const localTime = Utils.formatLocalTime(chatData.exportedAt);\n\n      const yaml = `---\\ntitle: \"${chatData.title.replaceAll(\n        '\"',\n        '\\\\\"'\n      )}\"\\ntags: [${chatData.tags.join(\", \")}]\\nauthor: ${\n        chatData.author\n      }\\ncount: ${\n        chatData.messageCount\n      }\\nexporter: ${EXPORTER_VERSION}\\ndate: ${localTime}\\nurl: ${\n        chatData.threadUrl\n      }\\n---\\n`;\n      const tocBlock = `## Table of Contents\\n\\n${toc.trim()}\\n\\n`;\n\n      const finalOutput =\n        yaml + `\\n# ${chatData.title}\\n\\n` + tocBlock + content.trim() + \"\\n\\n\";\n\n      const fileName = Utils.formatFileName(\n        GM_getValue(GM_OUTPUT_FILE_FORMAT, OUTPUT_FILE_FORMAT_DEFAULT),\n        chatData.title,\n        chatData.tags,\n        \"md\"\n      );\n      return { output: finalOutput, fileName: fileName };\n    },\n\n    /**\n     * Converts standardized chat data to JSON format.\n     * This function now expects a pre-filtered `chatData`.\n     * @param {object} chatData - The standardized chat data (already filtered).\n     * @param {TurndownService} turndownServiceInstance - Configured TurndownService.\n     * @returns {{output: string, fileName: string}} JSON string and filename.\n     */\n    formatToJSON(chatData, turndownServiceInstance) {\n      const processMessageContent = function (msg) {\n        if (msg.author === \"user\") {\n          return msg.contentText;\n        } else {\n          let markdownContent;\n          try {\n            markdownContent = turndownServiceInstance.turndown(msg.contentHtml);\n          } catch (e) {\n            console.error(\n              `Error converting AI message ${msg.id} to Markdown:`,\n              e\n            );\n            markdownContent = `[CONVERSION ERROR: Failed to render this section.]: ${msg.contentText}`;\n          }\n          return markdownContent;\n        }\n      };\n      const jsonOutput = {\n        title: chatData.title,\n        tags: chatData.tags,\n        author: chatData.author,\n        count: chatData.messageCount,\n        exporter: EXPORTER_VERSION,\n        date: chatData.exportedAt.toISOString(),\n        url: chatData.threadUrl,\n        messages: chatData.messages.map((msg) => ({\n          id: msg.id.split(\"-\").slice(0, 2).join(\"-\"), // Keep the ID for reference in JSON\n          author: msg.author,\n          content: processMessageContent(msg),\n        })),\n      };\n\n      const fileName = Utils.formatFileName(\n        GM_getValue(GM_OUTPUT_FILE_FORMAT, OUTPUT_FILE_FORMAT_DEFAULT),\n        chatData.title,\n        chatData.tags,\n        \"json\"\n      );\n\n      return {\n        output: JSON.stringify(jsonOutput, null, 2),\n        fileName: fileName,\n      };\n    },\n\n    /**\n     * This function setups the rules for turndownServiceInstance\n     * @param {TurndownService} turndownServiceInstance - Configured TurndownService.\n     */\n    setupTurndownRules(turndownServiceInstance) {\n      if (CURRENT_PLATFORM === CHATGPT) {\n        turndownServiceInstance.addRule(\"chatgptRemoveReactions\", {\n          filter: (node) =>\n            node.nodeName === \"DIV\" &&\n            // Check for the language div (2nd child).\n            node.querySelector(\n              ':scope > div:nth-child(1) > button[data-testid=\"copy-turn-action-button\"]'\n            ),\n          replacement: () => \"\",\n        });\n        turndownServiceInstance.addRule(\"chatgptRemoveH6ChatGPTSaid\", {\n          filter: (node) =>\n            node.nodeName === \"H6\" &&\n            node.classList.contains(\"sr-only\") &&\n            node.textContent.trim().toLowerCase().startsWith(\"chatgpt said\"),\n          replacement: () => \"\",\n        });\n      }\n\n      if (CURRENT_PLATFORM === COPILOT) {\n        turndownServiceInstance.addRule(\"copilotRemoveReactions\", {\n          filter: (node) =>\n            node.matches('[data-testid=\"message-item-reactions\"]'),\n          replacement: () => \"\",\n        });\n\n        // This single rule handles the entire Copilot code block structure.\n        turndownServiceInstance.addRule(\"copilotCodeBlock\", {\n          filter: function (node, options) {\n            // Filter for the grandparent div of the pre element using more concise CSS selectors.\n            return (\n              node.nodeName === \"DIV\" &&\n              // Removed strict child combinator (>) to handle new nested flex div\n              node.querySelector(\":scope > div:nth-child(1) span\") &&\n              // Check for the code block div (3rd child) with a direct <pre> child.\n              node.querySelector(\":scope > div:nth-child(2) > div > pre\")\n            );\n          },\n          replacement: function (content, node) {\n            // Get the language from the span inside the first child div.\n            const languageNode = node.querySelector(\n              \":scope > div:nth-child(1) span\"\n            );\n            const language = languageNode\n              ? languageNode.textContent.trim().toLowerCase()\n              : \"\";\n\n            // Get the code content from the pre > code element within the second child div.\n            const codeNode = node.querySelector(\n              \":scope > div:nth-child(2) > div > pre > code\"\n            );\n            if (!codeNode) return \"\";\n\n            const codeText = codeNode.textContent || \"\";\n\n            return \"\\n\\n```\" + language + \"\\n\" + codeText + \"\\n```\\n\\n\";\n          },\n        });\n\n        turndownServiceInstance.addRule(\"copilotFooterLinks\", {\n          filter: function (node, options) {\n            // Footer links for each message is an <a> with children: span, img, and span\n            // Use the last span content as text\n            return (\n              node.nodeName === \"A\" &&\n              node.querySelector(\":scope > span:nth-child(1)\") &&\n              node.querySelector(\":scope > img:nth-child(2)\") &&\n              node.querySelector(\":scope > span:nth-child(3)\")\n            );\n          },\n          replacement: function (content, node) {\n            // Get the link text from last span.\n            const lastSpan = node.querySelector(\":scope > span:nth-child(3)\");\n            const linkText = lastSpan\n              ? lastSpan.textContent.trim()\n              : node.getAttribute(\"href\");\n\n            return `[${linkText}](${node.getAttribute(\"href\")}) `;\n          },\n        });\n      }\n\n      turndownServiceInstance.addRule(\"lineBreak\", {\n        filter: \"br\",\n        replacement: () => \"  \\n\",\n      });\n\n      turndownServiceInstance.addRule(\"heading\", {\n        filter: [\"h1\", \"h2\", \"h3\", \"h4\", \"h5\", \"h6\"],\n        replacement: (content, node) => {\n          const hLevel = Number(node.nodeName.charAt(1));\n          return `\\n\\n${\"#\".repeat(hLevel)} ${content}\\n\\n`;\n        },\n      });\n\n      // Custom rule for list items to ensure proper nesting and markers\n      turndownServiceInstance.addRule(\"customLi\", {\n        filter: \"li\",\n        replacement: function (content, node) {\n          let processedContent = content.trim();\n\n          // Heuristic: If content contains multiple lines and the second line\n          // looks like a list item, ensure a double newline for nested lists.\n          if (processedContent.length > 0) {\n            const lines = processedContent.split(\"\\n\");\n            if (lines.length > 1 && /^\\s*[-*+]|^[0-9]+\\./.test(lines[1])) {\n              processedContent = lines.join(\"\\n\\n\").trim();\n            }\n          }\n\n          let listItemMarkdown;\n          if (node.parentNode.nodeName === \"UL\") {\n            let indent = \"\";\n            let liAncestorCount = 0;\n            let parent = node.parentNode;\n\n            // Calculate indentation for nested unordered lists\n            while (parent) {\n              if (parent.nodeName === \"LI\") {\n                liAncestorCount++;\n              }\n              parent = parent.parentNode;\n            }\n            for (let i = 0; i < liAncestorCount; i++) {\n              indent += \"    \"; // 4 spaces per nesting level\n            }\n            listItemMarkdown = `${indent}${turndownServiceInstance.options.bulletListMarker} ${processedContent}`;\n          } else if (node.parentNode.nodeName === \"OL\") {\n            // Get the correct index for ordered list items\n            const siblings = Array.from(node.parentNode.children).filter(\n              (child) => child.nodeName === \"LI\"\n            );\n            const index = siblings.indexOf(node);\n            listItemMarkdown = `${index + 1}. ${processedContent}`;\n          } else {\n            listItemMarkdown = processedContent; // Fallback\n          }\n          // Always add a newline after each list item for separation\n          return listItemMarkdown + \"\\n\";\n        }.bind(turndownServiceInstance),\n      });\n\n      if (CURRENT_PLATFORM === CLAUDE) {\n        // This single rule handles the entire Claude code block structure.\n        turndownServiceInstance.addRule(\"claudeCodeBlock\", {\n          filter: function (node, options) {\n            // Filter for the grandparent div of the pre element using more concise CSS selectors.\n            return (\n              node.nodeName === \"DIV\" &&\n              // Check for the language div (2nd child).\n              node.querySelector(\":scope > div:nth-child(2)\") &&\n              // Check for the code block div (3rd child) with a direct <pre> child.\n              node.querySelector(\":scope > div:nth-child(3) > pre > code\")\n            );\n          },\n          replacement: function (content, node) {\n            // Get the language from the second child div.\n            const languageNode = node.querySelector(\n              \":scope > div:nth-child(2)\"\n            );\n            const language = languageNode\n              ? languageNode.textContent.trim().toLowerCase()\n              : \"\";\n\n            // Get the code content from the pre > code element within the third child div.\n            const codeNode = node.querySelector(\n              \":scope > div:nth-child(3) > pre > code\"\n            );\n            if (!codeNode) return \"\";\n\n            const codeText = codeNode.textContent || \"\";\n\n            return \"\\n\\n```\" + language + \"\\n\" + codeText + \"\\n```\\n\\n\";\n          },\n        });\n      }\n\n      if (CURRENT_PLATFORM === GROK) {\n        // Fix for single-line preview (Preserve pre-wrap newlines)\n        turndownServiceInstance.addRule(\"grokPreserveNewlines\", {\n          filter: (node) => {\n            return (\n              node.nodeName === \"P\" &&\n              node.getAttribute(\"style\")?.includes(\"white-space: pre-wrap\")\n            );\n          },\n          replacement: (content) => {\n            // Adds two spaces at the end of lines to force a hard break in MD\n            const preservedContent = content.trim().replace(/\\n/g, \"\\n\\n\");\n            return `\\n\\n${preservedContent}\\n\\n`;\n          },\n        });\n\n        turndownServiceInstance.addRule(\"grokCodeBlock\", {\n          filter: function (node) {\n            return (\n              node.nodeName === \"DIV\" &&\n              node.getAttribute(\"data-testid\") === \"code-block\"\n            );\n          },\n          replacement: function (content, node) {\n            const langSpan = node.querySelector(\".font-mono.text-xs\");\n            const rawLang = langSpan\n              ? langSpan.textContent.trim().toLowerCase()\n              : \"\";\n            const lang = rawLang === \"text\" ? \"\" : rawLang;\n\n            // Case 1: <pre> was rendered and the pre rule already produced\n            // a fenced block inside content — extract it and re-wrap with correct lang.\n            const existingFence = content.match(/```[\\w]*\\n([\\s\\S]*?)\\n```/);\n            if (existingFence) {\n              const codeText = existingFence[1].trim();\n              if (codeText) return `\\n\\`\\`\\`${lang}\\n${codeText}\\n\\`\\`\\`\\n\\n`;\n            }\n\n            // Case 2: Grok lazy-renders <pre><code> for off-screen blocks.\n            // The code text is still in `content` as raw concatenated text nodes,\n            // prefixed by the lang label and Grok UI button labels.\n            // Strip them from the start to isolate the code.\n            let codeText = content;\n            if (langSpan) {\n              const langLabel = langSpan.textContent.trim();\n              if (codeText.startsWith(langLabel)) {\n                codeText = codeText.slice(langLabel.length);\n              }\n            }\n            // Strip leading Grok UI button labels (vary by block type)\n            codeText = codeText.replace(/^(Collapse|Run|Copy|Wrap)+/, \"\");\n            codeText = codeText.trim();\n\n            if (!codeText) return \"\";\n            return `\\n\\`\\`\\`${lang}\\n${codeText}\\n\\`\\`\\`\\n\\n`;\n          },\n        });\n      }\n\n      turndownServiceInstance.addRule(\"code\", {\n        filter: \"code\",\n        replacement: (content, node) => {\n          if (node.parentNode.nodeName === \"PRE\") return content;\n\n          // Prevent wrapping in single backticks if the code is inside a Grok code block\n          if (\n            typeof CURRENT_PLATFORM !== \"undefined\" &&\n            CURRENT_PLATFORM === GROK\n          ) {\n            if (node.closest && node.closest('[data-testid=\"code-block\"]')) {\n              return content;\n            }\n          }\n\n          return `\\`${content}\\``;\n        },\n      });\n\n      // Rule for preformatted code blocks\n      turndownServiceInstance.addRule(\"pre\", {\n        filter: \"pre\",\n        replacement: (content, node) => {\n          let lang = \"\";\n          let codeText = \"\";\n\n          // 1. GEMINI STRICT ISOLATION\n          if (\n            typeof CURRENT_PLATFORM !== \"undefined\" &&\n            CURRENT_PLATFORM === GEMINI\n          ) {\n            const geminiCodeBlockParent = node.closest(\".code-block\");\n            if (geminiCodeBlockParent) {\n              const geminiLanguageSpan = geminiCodeBlockParent.querySelector(\n                \".code-block-decoration span\"\n              );\n              if (geminiLanguageSpan && geminiLanguageSpan.textContent.trim()) {\n                lang = geminiLanguageSpan.textContent.trim();\n              }\n            }\n          }\n\n          // 2. CHATGPT STRICT ISOLATION (Optimized for CodeMirror 6)\n          if (\n            typeof CURRENT_PLATFORM !== \"undefined\" &&\n            CURRENT_PLATFORM === CHATGPT\n          ) {\n            const chatgptLanguageDiv = node.querySelector(\n              \".text-token-text-primary, .flex.items-center.text-token-text-secondary, .text-xs.font-sans\"\n            );\n            if (chatgptLanguageDiv) {\n              const firstSpan = chatgptLanguageDiv.querySelector(\"span\");\n              lang = firstSpan\n                ? firstSpan.textContent.trim()\n                : chatgptLanguageDiv.textContent\n                    .replace(/Copy code|Run/gi, \"\")\n                    .trim();\n            }\n\n            const cmContent = node.querySelector(\".cm-content\");\n            if (cmContent) {\n              // .innerText is better than .textContent here because it\n              // respects the line breaks generated by the editor's divs/blocks\n              codeText = cmContent.innerText;\n            }\n          }\n\n          // 3. UNIVERSAL FALLBACK (Claude, Copilot, and standard markdown)\n          const codeElement = node.querySelector(\"code\");\n\n          // Only extract from <code> if platform-specific logic (ChatGPT/Gemini) didn't already find code\n          if (!codeText && codeElement) {\n            codeText = codeElement.textContent;\n          }\n\n          // If no code text was found via any method, return the original content\n          if (!codeText && !codeElement) return content;\n\n          // Standard language detection from <code> class if not already set by header logic\n          if (!lang && codeElement && codeElement.className) {\n            const match = codeElement.className.match(/language-(\\w+)/);\n            if (match) lang = match[1];\n          }\n\n          // Final cleanup and fence construction\n          const cleanCode = (codeText || \"\").trim();\n          const safeLang = (lang || \"\").toLowerCase();\n\n          let prefix = \"\\n\";\n          let prevSibling = node.previousElementSibling;\n          if (prevSibling && prevSibling.nodeName === \"P\") {\n            let parentLi = prevSibling.closest(\"li\");\n            if (parentLi && parentLi.contains(node)) {\n              prefix = \"\\n\\n\";\n            }\n          }\n\n          return `${prefix}\\`\\`\\`${safeLang}\\n${cleanCode}\\n\\`\\`\\`\\n`;\n        },\n      });\n\n      turndownServiceInstance.addRule(\"strong\", {\n        filter: [\"strong\", \"b\"],\n        replacement: (content) => `**${content}**`,\n      });\n\n      turndownServiceInstance.addRule(\"em\", {\n        filter: [\"em\", \"i\"],\n        replacement: (content) => `_${content}_`,\n      });\n\n      turndownServiceInstance.addRule(\"blockQuote\", {\n        filter: \"blockquote\",\n        replacement: (content) =>\n          content\n            .trim()\n            .split(\"\\n\")\n            .map((l) => `> ${l}`)\n            .join(\"\\n\"),\n      });\n\n      turndownServiceInstance.addRule(\"link\", {\n        filter: \"a\",\n        replacement: (content, node) =>\n          `[${content}](${node.getAttribute(\"href\")})`,\n      });\n\n      turndownServiceInstance.addRule(\"strikethrough\", {\n        filter: (node) => node.nodeName === \"DEL\",\n        replacement: (content) => `~~${content}~~`,\n      });\n\n      // Rule for HTML tables to Markdown table format\n      turndownServiceInstance.addRule(\"table\", {\n        filter: \"table\",\n        replacement: function (content, node) {\n          const headerRows = Array.from(node.querySelectorAll(\"thead tr\"));\n          const bodyRows = Array.from(node.querySelectorAll(\"tbody tr\"));\n          const footerRows = Array.from(node.querySelectorAll(\"tfoot tr\"));\n\n          let allRowsContent = [];\n\n          const getRowCellsContent = (rowElement) => {\n            const cells = Array.from(rowElement.querySelectorAll(\"th, td\"));\n            return cells.map((cell) =>\n              cell.textContent.replace(/\\s+/g, \" \").trim()\n            );\n          };\n\n          if (headerRows.length > 0) {\n            allRowsContent.push(getRowCellsContent(headerRows[0]));\n          }\n\n          bodyRows.forEach((row) => {\n            allRowsContent.push(getRowCellsContent(row));\n          });\n\n          footerRows.forEach((row) => {\n            allRowsContent.push(getRowCellsContent(row));\n          });\n\n          if (allRowsContent.length === 0) {\n            return \"\";\n          }\n\n          const isFirstRowAHeader = headerRows.length > 0;\n          const maxCols = Math.max(...allRowsContent.map((row) => row.length));\n\n          const paddedRows = allRowsContent.map((row) => {\n            const paddedRow = [...row];\n            while (paddedRow.length < maxCols) {\n              paddedRow.push(\"\");\n            }\n            return paddedRow;\n          });\n\n          let markdownTable = \"\";\n\n          if (isFirstRowAHeader) {\n            markdownTable += \"| \" + paddedRows[0].join(\" | \") + \" |\\n\";\n            markdownTable += \"|\" + Array(maxCols).fill(\"---\").join(\"|\") + \"|\\n\";\n            for (let i = 1; i < paddedRows.length; i++) {\n              markdownTable += \"| \" + paddedRows[i].join(\" | \") + \" |\\n\";\n            }\n          } else {\n            for (let i = 0; i < paddedRows.length; i++) {\n              markdownTable += \"| \" + paddedRows[i].join(\" | \") + \" |\\n\";\n              if (i === 0) {\n                markdownTable +=\n                  \"|\" + Array(maxCols).fill(\"---\").join(\"|\") + \"|\\n\";\n              }\n            }\n          }\n\n          return markdownTable.trim();\n        },\n      });\n\n      // Universal rule for paragraph tags with a fix for list item newlines\n      turndownServiceInstance.addRule(\"paragraph\", {\n        filter: \"p\",\n        replacement: (content, node) => {\n          if (!content.trim()) return \"\"; // Ignore empty paragraphs\n\n          let currentNode = node.parentNode;\n          while (currentNode) {\n            // If inside TH or TR (table headers/rows), suppress newlines.\n            if (PARAGRAPH_FILTER_PARENT_NODES.includes(currentNode.nodeName)) {\n              return content;\n            }\n            // If inside an LI (list item), add a single newline for proper separation.\n            if (currentNode.nodeName === \"LI\") {\n              return content + \"\\n\";\n            }\n            currentNode = currentNode.parentNode;\n          }\n          // For all other cases, add double newlines for standard paragraph separation.\n          return `\\n\\n${content}\\n\\n`;\n        },\n      });\n\n      // ChatGPT-specific rules for handling unique elements/classes\n      if (CURRENT_PLATFORM === CHATGPT) {\n        turndownServiceInstance.addRule(\"popup-div\", {\n          filter: (node) =>\n            node.nodeName === \"DIV\" &&\n            node.classList.contains(CHATGPT_POPUP_DIV_CLASS),\n          replacement: (content) => {\n            // Convert HTML content of popups to a code block\n            const textWithLineBreaks = content\n              .replace(/<br\\s*\\/?>/gi, \"\\n\")\n              .replace(/<\\/(p|div|h[1-6]|ul|ol|li)>/gi, \"\\n\")\n              .replace(/<(?:p|div|h[1-6]|ul|ol|li)[^>]*>/gi, \"\\n\")\n              .replace(/<\\/?[^>]+(>|$)/g, \"\")\n              .replace(/\\n+/g, \"\\n\");\n            return \"\\n```\\n\" + textWithLineBreaks + \"\\n```\\n\";\n          },\n        });\n        turndownServiceInstance.addRule(\"buttonWithSpecificClass\", {\n          filter: (node) =>\n            node.nodeName === \"BUTTON\" &&\n            node.classList.contains(CHATGPT_BUTTON_SPECIFIC_CLASS),\n          replacement: (content) =>\n            content.trim() ? `__${content}__\\n\\n` : \"\",\n        });\n        // turndownServiceInstance.addRule(\"remove-img\", {\n        //   filter: \"img\",\n        //   replacement: () => \"\", // Remove image tags\n        // });\n      }\n\n      // Gemini specific rule to remove language labels from being processed as content\n      if (CURRENT_PLATFORM === GEMINI) {\n        turndownServiceInstance.addRule(\"geminiCodeLanguageLabel\", {\n          filter: (node) =>\n            node.nodeName === \"SPAN\" &&\n            node.closest(\".code-block-decoration\") &&\n            node.textContent.trim().length > 0, // Ensure it's not an an empty span\n          replacement: () => \"\", // Replace with empty string\n        });\n      }\n\n      turndownServiceInstance.addRule(\"images\", {\n        filter: (node) => node.nodeName === \"IMG\",\n        replacement: (content, node) => {\n          const src = node.getAttribute(\"src\") || \"\";\n          const alt = node.alt || \"\";\n          return src ? `![${alt}](${src})` : \"\";\n        },\n      });\n\n      // Generic rule for subscript\n      turndownServiceInstance.addRule(\"sub\", {\n        filter: [\"sub\"],\n        replacement: (content) => {\n          return content.trim() ? `<sub>${content}</sub>` : \"\";\n        },\n      });\n\n      // Generic rule for superscript\n      turndownServiceInstance.addRule(\"sup\", {\n        filter: [\"sup\"],\n        replacement: (content) => {\n          return content.trim() ? `<sup>${content}</sup>` : \"\";\n        },\n      });\n    },\n\n    /**\n     * Main export orchestrator. Extracts data, configures Turndown, and formats.\n     * This function now filters messages based on _selectedMessageIds and visibility.\n     * @param {string} format - The desired output format ('markdown' or 'json').\n     */\n    initiateExport(format) {\n      // Use the _currentChatData that matches the outline's IDs\n      const rawChatData = ChatExporter._currentChatData;\n      let turndownServiceInstance = null;\n\n      if (!rawChatData || rawChatData.messages.length === 0) {\n        alert(\"No messages found to export.\");\n        return;\n      }\n\n      // --- Refresh ChatExporter._selectedMessageIds from current UI state and visibility ---\n      ChatExporter._selectedMessageIds.clear(); // Clear previous state\n      const outlineContainer = document.querySelector(\n        `#${OUTLINE_CONTAINER_ID}`\n      );\n      if (outlineContainer) {\n        // Only consider checkboxes that are checked AND visible\n        const checkedVisibleCheckboxes = outlineContainer.querySelectorAll(\n          \".outline-item-checkbox:checked\"\n        ); // This will only return visible ones if their parent `itemDiv` is hidden with `display:none` as `querySelectorAll` won't find them\n\n        checkedVisibleCheckboxes.forEach((cb) => {\n          // Ensure the parent element is actually visible before adding to selected\n          const parentItemDiv = cb.closest(\"div\");\n          if (\n            parentItemDiv &&\n            window.getComputedStyle(parentItemDiv).display !== \"none\" &&\n            cb.dataset.messageId\n          ) {\n            ChatExporter._selectedMessageIds.add(cb.dataset.messageId);\n          }\n        });\n\n        // Also, manually add AI responses that follow selected *and visible* user messages.\n        const visibleUserMessageIds = new Set();\n        checkedVisibleCheckboxes.forEach((cb) => {\n          const parentItemDiv = cb.closest(\"div\");\n          if (\n            parentItemDiv &&\n            window.getComputedStyle(parentItemDiv).display !== \"none\" &&\n            cb.dataset.messageId\n          ) {\n            visibleUserMessageIds.add(cb.dataset.messageId);\n          }\n        });\n\n        rawChatData.messages.forEach((msg, index) => {\n          if (msg.author === \"ai\") {\n            let prevUserMessageId = null;\n            for (let i = index - 1; i >= 0; i--) {\n              if (rawChatData.messages[i].author === \"user\") {\n                prevUserMessageId = rawChatData.messages[i].id;\n                break;\n              }\n            }\n            if (\n              prevUserMessageId &&\n              visibleUserMessageIds.has(prevUserMessageId)\n            ) {\n              ChatExporter._selectedMessageIds.add(msg.id);\n            }\n          }\n        });\n      }\n      // --- End Refresh ---\n\n      // --- Filter messages based on selection ---\n      const filteredMessages = rawChatData.messages.filter((msg) =>\n        ChatExporter._selectedMessageIds.has(msg.id)\n      );\n\n      if (filteredMessages.length === 0) {\n        alert(\n          \"No messages selected or visible for export. Please check at least one question in the outline or clear your search filter.\"\n        );\n        return;\n      }\n\n      // Create a new chatData object for the filtered export\n      // Also, re-calculate messageCount for the filtered set\n      const chatDataForExport = {\n        ...rawChatData,\n        // Match the starlight-tags plugin requirements:\n        // 1. Lowercase everything\n        // 2. Remove any character that isn't a-z, 0-9, -, or _\n        // 3. Remove all spaces\n        tags: (rawChatData.tags || [])\n          .map((tag) =>\n            tag\n              .toLowerCase()\n              .replace(/\\s+/g, \"\")\n              .replace(/[^a-z0-9_-]/g, \"\")\n          )\n          .filter((tag) => tag.length > 0),\n        messages: filteredMessages,\n        messageCount: filteredMessages.filter((m) => m.author === \"user\")\n          .length,\n        exportedAt: new Date(), // Set current timestamp just before export\n      };\n\n      let fileOutput = null;\n      let fileName = null;\n      let mimeType = \"\";\n\n      turndownServiceInstance = new TurndownService();\n      ChatExporter.setupTurndownRules(turndownServiceInstance);\n\n      if (format === \"markdown\") {\n        // Pass the filtered chat data to formatToMarkdown\n        const markdownResult = ChatExporter.formatToMarkdown(\n          chatDataForExport,\n          turndownServiceInstance\n        );\n        fileOutput = markdownResult.output;\n        fileName = markdownResult.fileName;\n        mimeType = \"text/markdown;charset=utf-8\";\n      } else if (format === \"json\") {\n        // Pass the filtered chat data to formatToJSON\n        const jsonResult = ChatExporter.formatToJSON(\n          chatDataForExport,\n          turndownServiceInstance\n        );\n        fileOutput = jsonResult.output;\n        fileName = jsonResult.fileName;\n        mimeType = \"application/json;charset=utf-8\";\n      } else {\n        alert(\"Invalid export format selected.\");\n        return;\n      }\n\n      if (fileOutput && fileName) {\n        Utils.downloadFile(fileName, fileOutput, mimeType);\n      }\n    },\n  };\n\n  // --- Injected CSS for Theme Overrides ---\n  function injectThemeOverrideStyles() {\n    const styleElement = document.createElement(\"style\");\n    styleElement.id = \"ai-chat-exporter-theme-overrides\";\n    styleElement.textContent = `\n      /* Always ensure the outline container and its children have a light theme */\n      #${OUTLINE_CONTAINER_ID} {\n        background-color: #fff !important;\n        color: #333 !important;\n      }\n\n      /* Force the search input to have a light background and text color */\n      #${OUTLINE_CONTAINER_ID} #outline-search-input {\n        background-color: #fff !important;\n        color: #333 !important;\n        border: 1px solid #ddd !important;\n      }\n\n      /* --- Special rule for Gemini's search box on dark theme --- */\n      /* Gemini's dark theme selector is very specific, so we need to match or exceed it. */\n      .dark-theme #${OUTLINE_CONTAINER_ID} #outline-search-input {\n        background-color: #fff !important;\n        color: #333 !important;\n      }\n\n      /* Force scrollbar to be light for all browsers */\n      /* For WebKit (Chrome, Safari, Gemini, ChatGPT) */\n      #${OUTLINE_CONTAINER_ID} ::-webkit-scrollbar {\n        width: 8px;\n        background-color: #f1f1f1; /* Light track color */\n      }\n\n      #${OUTLINE_CONTAINER_ID} ::-webkit-scrollbar-thumb {\n        background-color: #c1c1c1; /* Light thumb color */\n        border-radius: 4px;\n      }\n\n      /* For Firefox */\n      #${OUTLINE_CONTAINER_ID} {\n        scrollbar-color: #c1c1c1 #f1f1f1 !important; /* Light thumb and track */\n        scrollbar-width: thin !important;\n      }\n    `;\n    document.head.appendChild(styleElement);\n  }\n\n  // --- UI Management ---\n  const UIManager = {\n    /**\n     * Stores the timeout ID for the alert's auto-hide.\n     * @type {number|null}\n     */\n    alertTimeoutId: null,\n    _outlineIsCollapsed: false, // State for the outline collapse\n    _lastProcessedChatUrl: null, // Track the last processed chat URL for Gemini\n    _initialListenersAttached: false, // Track if the URL change handlers are initialized\n    autoScrollEnabled: GM_getValue(\"gm_auto_scroll_enabled\", true),\n\n    /**\n     * Installs SparkClaw's non-visual, fixed-command automation bridge.\n     */\n    installAutomationBridge() {\n      const serializeCurrent = () => {\n        const extract = {\n          chatgpt: ChatExporter.extractChatGPTChatData,\n          claude: ChatExporter.extractClaudeChatData,\n          gemini: ChatExporter.extractGeminiChatData,\n          grok: ChatExporter.extractGrokChatData,\n        }[CURRENT_PLATFORM];\n        if (!extract) throw new Error(\"batch_provider_unsupported\");\n        const data = extract.call(ChatExporter, document);\n        if (!data || !data.messages?.length) return null;\n        const turndown = new TurndownService();\n        ChatExporter.setupTurndownRules(turndown);\n        return ChatExporter.formatToJSON(\n          { ...data, exportedAt: new Date() },\n          turndown\n        );\n      };\n      installTimelineBridge(\n        () => serializeCurrent()?.output || null,\n        () => {\n          const result = serializeCurrent();\n          if (!result) throw new Error(\"conversation_not_ready\");\n          Utils.downloadFile(\n            result.fileName,\n            result.output,\n            \"application/json;charset=utf-8\"\n          );\n        }\n      );\n    },\n\n    /**\n     * Adds and manages the collapsible outline div.\n     */\n    addOutlineControls() {\n      let outlineContainer = document.querySelector(`#${OUTLINE_CONTAINER_ID}`);\n      if (!outlineContainer) {\n        outlineContainer = document.createElement(\"div\");\n        outlineContainer.id = OUTLINE_CONTAINER_ID;\n        document.body.appendChild(outlineContainer);\n      }\n\n      // Apply base styles\n      Utils.applyStyles(outlineContainer, OUTLINE_CONTAINER_PROPS);\n\n      // Apply collapsed styles if state is collapsed\n      if (UIManager._outlineIsCollapsed) {\n        Utils.applyStyles(outlineContainer, OUTLINE_CONTAINER_COLLAPSED_PROPS);\n      }\n\n      UIManager.generateOutlineContent();\n    },\n\n    /**\n     * Generates and updates the content of the outline div.\n     * This function should be called whenever the chat data changes.\n     */\n    generateOutlineContent() {\n      const outlineContainer = document.querySelector(\n        `#${OUTLINE_CONTAINER_ID}`\n      );\n      if (!outlineContainer) return;\n\n      // Extract fresh chat data\n      let freshChatData = null;\n      switch (CURRENT_PLATFORM) {\n        case CHATGPT:\n          freshChatData = ChatExporter.extractChatGPTChatData(document);\n          break;\n        case CLAUDE:\n          freshChatData = ChatExporter.extractClaudeChatData(document);\n          break;\n        case COPILOT:\n          freshChatData = ChatExporter.extractCopilotChatData(document);\n          break;\n        case GEMINI:\n          freshChatData = ChatExporter.extractGeminiChatData(document);\n          break;\n        case GROK:\n          freshChatData = ChatExporter.extractGrokChatData(document);\n          break;\n        default:\n          outlineContainer.style.display = \"none\"; // Hide if not supported\n          return;\n      }\n\n      // Check if chat data has changed significantly to warrant a re-render\n      // Compare message count and content of the last few messages as a heuristic\n      // This is to avoid regenerating the outline on every minor DOM change.\n      const hasDataChanged =\n        !ChatExporter._currentChatData || // No previous data\n        !freshChatData || // No new data\n        freshChatData._raw_title !== ChatExporter._currentChatData._raw_title ||\n        freshChatData.messages.length !==\n          ChatExporter._currentChatData.messages.length ||\n        (freshChatData.messages.length > 0 &&\n          ChatExporter._currentChatData.messages.length > 0 &&\n          freshChatData.messages[freshChatData.messages.length - 1]\n            .contentText !==\n            ChatExporter._currentChatData.messages[\n              ChatExporter._currentChatData.messages.length - 1\n            ].contentText);\n\n      if (!hasDataChanged) {\n        // If data hasn't changed, just ensure visibility based on message presence\n        outlineContainer.style.display =\n          freshChatData && freshChatData.messages.length > 0 ? \"flex\" : \"none\";\n        return; // No need to regenerate content\n      }\n\n      // Update stored chat data\n      ChatExporter._currentChatData = freshChatData;\n\n      // Hide if no messages after update\n      if (\n        !ChatExporter._currentChatData ||\n        ChatExporter._currentChatData.messages.length === 0\n      ) {\n        outlineContainer.style.display = \"none\";\n        return;\n      } else {\n        outlineContainer.style.display = \"flex\";\n      }\n\n      // Clear existing content safely to avoid TrustedHTML error\n      while (outlineContainer.firstChild) {\n        outlineContainer.removeChild(outlineContainer.firstChild);\n      }\n\n      // Reset selections and check all by default (only on fresh rebuild)\n      ChatExporter._selectedMessageIds.clear();\n\n      // Header for Chat Outline (always visible)\n      const headerDiv = document.createElement(\"div\");\n      Utils.applyStyles(headerDiv, OUTLINE_HEADER_PROPS);\n      headerDiv.title = `AI Chat Exporter v${EXPORTER_VERSION}`;\n      headerDiv.onclick = UIManager.toggleOutlineCollapse; // Only this div handles collapse\n\n      const headerSpan = document.createElement(\"span\");\n      headerSpan.textContent = \"AI Chat Exporter: Chat Outline\";\n      headerDiv.appendChild(headerSpan);\n\n      const toggleButton = document.createElement(\"button\");\n      toggleButton.id = \"outline-toggle-btn\";\n      toggleButton.textContent = UIManager._outlineIsCollapsed ? \"▲\" : \"▼\"; // Up/Down arrow\n      Utils.applyStyles(toggleButton, OUTLINE_TOGGLE_BUTTON_PROPS);\n      headerDiv.appendChild(toggleButton);\n\n      outlineContainer.appendChild(headerDiv);\n\n      const titleDiv = document.createElement(\"div\");\n      Utils.applyStyles(titleDiv, OUTLINE_TITLE_PROPS);\n      titleDiv.textContent = freshChatData.title || DEFAULT_CHAT_TITLE;\n      titleDiv.title = \"tags: \" + freshChatData.tags.join(\", \");\n      titleDiv.id = OUTLINE_TITLE_ID;\n      outlineContainer.appendChild(titleDiv);\n\n      // New: Select All checkbox and label section (below header)\n      const selectAllContainer = document.createElement(\"div\");\n      Utils.applyStyles(selectAllContainer, SELECT_ALL_CONTAINER_PROPS);\n      selectAllContainer.id = \"outline-select-all-container\"; // For easier hiding/showing\n\n      const masterCheckbox = document.createElement(\"input\");\n      masterCheckbox.type = \"checkbox\";\n      masterCheckbox.id = \"outline-select-all\";\n      masterCheckbox.checked = true; // Default to checked\n      Utils.applyStyles(masterCheckbox, OUTLINE_CHECKBOX_PROPS);\n      // masterCheckbox.onchange will be set later after updateSelectedCountDisplay is defined and elements exist\n      selectAllContainer.appendChild(masterCheckbox);\n\n      const selectAllLabel = document.createElement(\"span\");\n      selectAllContainer.appendChild(selectAllLabel); // Append label here, content set later\n      outlineContainer.appendChild(selectAllContainer);\n\n      // Search Bar\n      const searchInput = document.createElement(\"input\");\n      searchInput.type = \"text\";\n      searchInput.id = \"outline-search-input\";\n      searchInput.placeholder =\n        \"Search text or regex in user queries & AI responses.\";\n      Utils.applyStyles(searchInput, SEARCH_INPUT_PROPS);\n      outlineContainer.appendChild(searchInput);\n\n      const noMatchMessage = document.createElement(\"div\");\n      noMatchMessage.id = \"outline-no-match-message\";\n      noMatchMessage.textContent = \"Your search text didn't match any items\";\n      Utils.applyStyles(noMatchMessage, NO_MATCH_MESSAGE_PROPS);\n      noMatchMessage.style.display = \"none\"; // Hidden by default\n      outlineContainer.appendChild(noMatchMessage);\n\n      const hr = document.createElement(\"hr\"); // Horizontal rule\n      hr.style.cssText =\n        \"border: none; border-top: 1px solid #eee; margin: 5px 0;\";\n      outlineContainer.appendChild(hr);\n\n      // List of messages\n      const messageListDiv = document.createElement(\"div\");\n      messageListDiv.id = \"outline-message-list\";\n      Utils.applyStyles(messageListDiv, MESSAGE_LIST_PROPS);\n\n      let userQuestionCount = 0; // This will be 'y' (total items)\n\n      const updateSelectedCountDisplay = () => {\n        const totalUserMessages = userQuestionCount; // 'y'\n        let selectedAndVisibleMessages = 0;\n\n        // Only count if the outline is not collapsed\n        if (!UIManager._outlineIsCollapsed) {\n          const allCheckboxes = outlineContainer.querySelectorAll(\n            \".outline-item-checkbox\"\n          );\n          allCheckboxes.forEach((checkbox) => {\n            // Check if the checkbox is checked AND its parent div is visible due to search filter\n            const parentItemDiv = checkbox.closest(\"div\");\n            if (\n              checkbox.checked &&\n              parentItemDiv &&\n              window.getComputedStyle(parentItemDiv).display !== \"none\"\n            ) {\n              selectedAndVisibleMessages++;\n            }\n          });\n        }\n\n        // Clear existing content safely\n        while (selectAllLabel.firstChild) {\n          selectAllLabel.removeChild(selectAllLabel.firstChild);\n        }\n\n        // Create a strong element for bold text\n        const strongElement = document.createElement(\"strong\");\n        strongElement.appendChild(\n          document.createTextNode(\"Items to export:  \")\n        );\n        strongElement.appendChild(\n          document.createTextNode(selectedAndVisibleMessages.toString())\n        );\n        strongElement.appendChild(document.createTextNode(\" out of \"));\n        strongElement.appendChild(\n          document.createTextNode(totalUserMessages.toString())\n        );\n\n        selectAllLabel.appendChild(strongElement);\n      };\n\n      // Store references to the actual itemDiv elements for easy access during search\n      const outlineItemElements = new Map(); // Map<messageId, itemDiv>\n\n      ChatExporter._currentChatData.messages.forEach((msg, index) => {\n        if (msg.author === \"user\") {\n          userQuestionCount++; // Increment 'y'\n          const itemDiv = document.createElement(\"div\");\n          Utils.applyStyles(itemDiv, OUTLINE_ITEM_PROPS);\n          itemDiv.dataset.userMessageId = msg.id; // Store user message ID for search lookup\n\n          const checkbox = document.createElement(\"input\");\n          checkbox.type = \"checkbox\";\n          checkbox.checked = true; // Default to checked\n          checkbox.className = \"outline-item-checkbox\"; // Add class for easy selection\n          checkbox.dataset.messageId = msg.id; // Store message ID on checkbox\n          Utils.applyStyles(checkbox, OUTLINE_CHECKBOX_PROPS);\n          checkbox.onchange = (e) => {\n            // Update master checkbox state based on individual checkboxes\n            const allVisibleCheckboxes = Array.from(\n              outlineContainer.querySelectorAll(\n                \".outline-item-checkbox:not([style*='display: none'])\"\n              )\n            );\n            const allVisibleChecked = allVisibleCheckboxes.every(\n              (cb) => cb.checked\n            );\n            masterCheckbox.checked = allVisibleChecked;\n            updateSelectedCountDisplay(); // Update count on individual checkbox change\n          };\n          itemDiv.appendChild(checkbox);\n\n          const itemText = document.createElement(\"span\");\n          itemText.textContent = `${userQuestionCount}: ${Utils.truncate(\n            msg.contentText,\n            40\n          )}`; // Truncate to 40\n          itemText.style.cursor = \"pointer\"; // Set cursor to hand\n          itemText.style.textDecoration = \"none\"; // Remove underline\n          itemText.title = `${userQuestionCount}: ${Utils.truncate(\n            msg.contentText.replace(/\\n+/g, \"\\n\"),\n            140\n          )}`; // Truncate to 140 // Add tooltip\n\n          // Add hover effect\n          itemText.onmouseover = () => {\n            itemText.style.backgroundColor = \"#f0f0f0\"; // Light gray background on hover\n            itemText.style.color = \"#5b3f87\"; // Change text color on hover\n          };\n          itemText.onmouseout = () => {\n            itemText.style.backgroundColor = \"transparent\"; // Revert background on mouse out\n            itemText.style.color = \"#333\"; // Revert text color on mouse out (assuming default is #333, adjust if needed)\n          };\n\n          itemText.onclick = () => {\n            // Find the original message element using the stored contentHtml reference\n            const messageElement = ChatExporter._currentChatData.messages.find(\n              (m) => m.id === msg.id\n            )?.contentHtml;\n            // console.log(\"clicked on message\", msg.id, messageElement);\n            if (messageElement) {\n              messageElement.scrollIntoView({\n                behavior: \"smooth\",\n                block: \"start\",\n              });\n            }\n          };\n          itemDiv.appendChild(itemText);\n\n          messageListDiv.appendChild(itemDiv);\n          outlineItemElements.set(msg.id, itemDiv);\n\n          // Add to selected IDs by default (will be refreshed on export anyway)\n          ChatExporter._selectedMessageIds.add(msg.id);\n        } else {\n          // For AI responses, if they follow a selected user message, also add them to selected IDs\n          // This is a pre-population, actual selection is determined on export.\n          const prevUserMessage = ChatExporter._currentChatData.messages.find(\n            (m, i) =>\n              i < ChatExporter._currentChatData.messages.indexOf(msg) &&\n              m.author === \"user\"\n          );\n          if (\n            prevUserMessage &&\n            ChatExporter._selectedMessageIds.has(prevUserMessage.id)\n          ) {\n            ChatExporter._selectedMessageIds.add(msg.id);\n          }\n        }\n      });\n\n      // Now set the master checkbox onchange after userQuestionCount is final\n      masterCheckbox.onchange = (e) => {\n        const isChecked = e.target.checked;\n        // Only toggle visible checkboxes\n        const visibleCheckboxes = outlineContainer.querySelectorAll(\n          \".outline-item-checkbox:not([style*='display: none'])\"\n        );\n        visibleCheckboxes.forEach((cb) => {\n          cb.checked = isChecked;\n        });\n        updateSelectedCountDisplay(); // Update count on master checkbox change\n      };\n\n      outlineContainer.appendChild(messageListDiv);\n\n      // Initial call to set the display text once all checkboxes are rendered and userQuestionCount is final\n      // This call is now placed AFTER messageListDiv (containing all checkboxes) is appended to outlineContainer.\n      updateSelectedCountDisplay();\n\n      // --- Search Bar Logic ---\n      searchInput.oninput = () => {\n        const searchText = searchInput.value.trim(); // Get the raw input text\n        let anyMatchFound = false;\n        let searchRegex;\n        let regexError = false;\n\n        // Reset previous error message and style\n        noMatchMessage.textContent = \"Your search text didn't match any items\";\n        noMatchMessage.style.color = \"#7e7e7e\"; // Default color\n\n        if (searchText === \"\") {\n          // If search text is empty, no regex is needed, all items will be shown\n        } else {\n          try {\n            // Create a RegExp object from the search input.\n            // The 'i' flag is added by default for case-insensitive search.\n            // Users can still specify other flags (e.g., /pattern/gi) directly in the input.\n            searchRegex = new RegExp(searchText, \"i\");\n          } catch (e) {\n            regexError = true;\n            // Display an error message for invalid regex\n            noMatchMessage.textContent = `Invalid regex: ${e.message}`;\n            noMatchMessage.style.color = \"red\"; // Make error message red\n            noMatchMessage.style.display = \"block\";\n            messageListDiv.style.display = \"none\";\n\n            // Hide all outline items if there's a regex error\n            outlineItemElements.forEach((itemDiv) => {\n              itemDiv.style.display = \"none\";\n            });\n            masterCheckbox.checked = false; // No valid visible items\n            updateSelectedCountDisplay(); // Update the count display\n            return; // Exit the function early if regex is invalid\n          }\n        }\n\n        const messages = ChatExporter._currentChatData.messages;\n        const userMessageMap = new Map();\n\n        // Group user messages with their immediate AI responses\n        for (let i = 0; i < messages.length; i++) {\n          const msg = messages[i];\n          if (msg.author === \"user\") {\n            const userMsg = msg;\n            let aiMsg = null;\n            if (i + 1 < messages.length && messages[i + 1].author === \"ai\") {\n              aiMsg = messages[i + 1];\n            }\n            userMessageMap.set(userMsg.id, { user: userMsg, ai: aiMsg });\n          }\n        }\n\n        outlineItemElements.forEach((itemDiv, userMsgId) => {\n          const userAiPair = userMessageMap.get(userMsgId);\n          let match = false;\n\n          if (userAiPair) {\n            const userContent = userAiPair.user.contentText;\n            const aiContent = userAiPair.ai ? userAiPair.ai.contentText : \"\";\n\n            if (searchText === \"\") {\n              match = true; // If search box is empty, consider it a match (show all)\n            } else if (searchRegex) {\n              // Use regex.test() for matching against content\n              if (\n                searchRegex.test(userContent) ||\n                searchRegex.test(aiContent)\n              ) {\n                match = true;\n              }\n            }\n          }\n\n          if (match) {\n            itemDiv.style.display = \"flex\";\n            anyMatchFound = true;\n          } else {\n            itemDiv.style.display = \"none\";\n          }\n        });\n\n        // Show/hide no match message and adjust message list visibility\n        if (searchText !== \"\" && !anyMatchFound && !regexError) {\n          noMatchMessage.style.display = \"block\";\n          messageListDiv.style.display = \"none\";\n        } else if (searchText === \"\" || anyMatchFound) {\n          noMatchMessage.style.display = \"none\";\n          if (!UIManager._outlineIsCollapsed) {\n            // Only show message list if outline is expanded\n            // Keep this as a fallback if messageListDiv display is not primarily controlled by flexGrow\n            messageListDiv.style.display = \"block\";\n          }\n        }\n\n        // After filtering, update master checkbox and count display based on visible items\n        const visibleCheckboxes = outlineContainer.querySelectorAll(\n          \".outline-item-checkbox:not([style*='display: none'])\"\n        );\n        const allVisibleChecked =\n          visibleCheckboxes.length > 0 &&\n          Array.from(visibleCheckboxes).every((cb) => cb.checked);\n        masterCheckbox.checked = allVisibleChecked;\n        updateSelectedCountDisplay();\n      };\n      // --- End Search Bar Logic ---\n\n      // Ensure visibility based on collapse state\n      if (UIManager._outlineIsCollapsed) {\n        titleDiv.style.display = \"none\";\n        selectAllContainer.style.display = \"none\";\n        searchInput.style.display = \"none\";\n        noMatchMessage.style.display = \"none\";\n        hr.style.display = \"none\";\n        messageListDiv.style.display = \"none\";\n      } else {\n        titleDiv.style.display = \"flex\";\n        selectAllContainer.style.display = \"flex\";\n        searchInput.style.display = \"block\";\n        // noMatchMessage and messageListDiv display will be handled by searchInput.oninput\n        hr.style.display = \"block\";\n      }\n    },\n\n    /**\n     * Toggles the collapse state of the outline div.\n     */\n    toggleOutlineCollapse() {\n      UIManager._outlineIsCollapsed = !UIManager._outlineIsCollapsed;\n      // New: Save the new state to localStorage\n      localStorage.setItem(\n        OUTLINE_COLLAPSED_STATE_KEY,\n        UIManager._outlineIsCollapsed.toString()\n      );\n\n      const outlineContainer = document.querySelector(\n        `#${OUTLINE_CONTAINER_ID}`\n      );\n      const titleDiv = document.querySelector(`#${OUTLINE_TITLE_ID}`);\n      const selectAllContainer = document.querySelector(\n        \"#outline-select-all-container\"\n      );\n      const searchInput = document.querySelector(\"#outline-search-input\");\n      const noMatchMessage = document.querySelector(\n        \"#outline-no-match-message\"\n      );\n      const hr = outlineContainer.querySelector(\"hr\");\n      const messageListDiv = document.querySelector(\"#outline-message-list\");\n      const toggleButton = document.querySelector(\"#outline-toggle-btn\");\n\n      if (UIManager._outlineIsCollapsed) {\n        Utils.applyStyles(outlineContainer, {\n          ...OUTLINE_CONTAINER_PROPS,\n          ...OUTLINE_CONTAINER_COLLAPSED_PROPS,\n        });\n        if (titleDiv) titleDiv.style.display = \"none\";\n        if (selectAllContainer) selectAllContainer.style.display = \"none\";\n        if (searchInput) searchInput.style.display = \"none\";\n        if (noMatchMessage) noMatchMessage.style.display = \"none\";\n        if (hr) hr.style.display = \"none\";\n        if (messageListDiv) messageListDiv.style.display = \"none\";\n        if (toggleButton) toggleButton.textContent = \"▲\";\n      } else {\n        Utils.applyStyles(outlineContainer, OUTLINE_CONTAINER_PROPS);\n        if (titleDiv) titleDiv.style.display = \"flex\";\n        if (selectAllContainer) selectAllContainer.style.display = \"flex\";\n        if (searchInput) searchInput.style.display = \"block\";\n        // noMatchMessage and messageListDiv display depend on search state, not just collapse\n        if (hr) hr.style.display = \"block\";\n        // Trigger a re-evaluation of search filter if it was active\n        const currentSearchText = searchInput\n          ? searchInput.value.toLowerCase().trim()\n          : \"\";\n        if (currentSearchText !== \"\") {\n          searchInput.dispatchEvent(new Event(\"input\")); // Re-run search filter\n        } else {\n          // If no search text, ensure all messages are visible\n          if (messageListDiv) messageListDiv.style.display = \"block\";\n          const allItems = outlineContainer.querySelectorAll(\n            \".outline-item-checkbox\"\n          );\n          allItems.forEach((cb) => {\n            const parentDiv = cb.closest(\"div\");\n            if (parentDiv) parentDiv.style.display = \"flex\";\n          });\n          if (noMatchMessage) noMatchMessage.style.display = \"none\";\n        }\n        if (toggleButton) toggleButton.textContent = \"▼\";\n      }\n    },\n\n    /**\n     * Attempts to auto-scroll the Gemini chat to the top to load all messages.\n     * This function uses an iterative approach to handle dynamic loading.\n     */\n    autoScrollToTop: async function () {\n      if (UIManager.autoScrollEnabled === false) return; // Exit early if disabled\n\n      if (CURRENT_PLATFORM !== GEMINI) {\n        // console.log(\"autoScrollToTop: Not on a Gemini hostname. Returning early.\");\n        return;\n      }\n\n      // Track the current URL to avoid re-scrolling the same chat repeatedly\n      const currentUrl = Utils.getCleanUrl();\n\n      // New: Check if we have already effectively started auto-scrolling for this URL.\n      // UIManager._lastProcessedChatUrl will be null initially, or explicitly reset by handleUrlChange for new URLs.\n      // It will be set to currentUrl *after* the initial message element is found.\n      if (UIManager._lastProcessedChatUrl === currentUrl) {\n        console.log(\n          \"Auto-scroll already initiated or completed for this URL. Skipping.\"\n        );\n        return;\n      }\n\n      // console.log(`Auto-scroll triggered for new URL: ${currentUrl}`);\n\n      let scrollableElement =\n        document.querySelector('[data-test-id=\"chat-history-container\"]') || // **PRIMARY TARGET (CONFIRMED BY LOGS)**\n        document.querySelector(\"#chat-history\") || // Fallback to chat history div by ID\n        document.querySelector(\"main\") || // Fallback to main element\n        document.documentElement; // Final fallback to the document's root element\n\n      if (!scrollableElement) {\n        return;\n      }\n\n      const AUTOSCROLL_MAT_PROGRESS_BAR_POLL_INTERVAL = 50;\n      const AUTOSCROLL_MAT_PROGRESS_BAR_APPEAR_TIMEOUT = 3000;\n      const AUTOSCROLL_MAT_PROGRESS_BAR_DISAPPEAR_TIMEOUT = 5000;\n      const AUTOSCROLL_REPEAT_DELAY = 500;\n      const AUTOSCROLL_MAX_RETRY = 3;\n      const MESSAGE_ELEMENT_APPEAR_TIMEOUT = 5000;\n\n      let previousMessageCount = -1;\n      let retriesForProgressBar = 0;\n\n      const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));\n\n      const waitForElementToAppear = async (\n        selector,\n        timeoutMs,\n        checkInterval = AUTOSCROLL_MAT_PROGRESS_BAR_POLL_INTERVAL\n      ) => {\n        const startTime = Date.now();\n        return new Promise((resolve) => {\n          const interval = setInterval(() => {\n            const element = document.querySelector(selector);\n            if (element) {\n              clearInterval(interval);\n              resolve(element);\n            } else if (Date.now() - startTime > timeoutMs) {\n              clearInterval(interval);\n              resolve(null);\n            }\n          }, checkInterval);\n        });\n      };\n\n      const waitForElementToDisappear = async (\n        selector,\n        timeoutMs,\n        checkInterval = AUTOSCROLL_MAT_PROGRESS_BAR_POLL_INTERVAL\n      ) => {\n        const startTime = Date.now();\n        return new Promise((resolve) => {\n          const interval = setInterval(() => {\n            const element = document.querySelector(selector);\n            if (\n              !element ||\n              (element.offsetWidth === 0 && element.offsetHeight === 0)\n            ) {\n              clearInterval(interval);\n              resolve(true);\n            } else if (Date.now() - startTime > timeoutMs) {\n              clearInterval(interval);\n              console.warn(\n                `waitForElementToDisappear: Timeout waiting for '${selector}' to disappear.`\n              );\n              resolve(false);\n            }\n          }, checkInterval);\n        });\n      };\n\n      // --- Wait for initial chat messages to appear ---\n      // This is crucial for new chat loads from sidebar clicks.\n      // console.log(\"Waiting for initial chat message elements...\");\n      const initialMessageElement = await waitForElementToAppear(\n        GEMINI_MESSAGE_ITEM_SELECTOR,\n        MESSAGE_ELEMENT_APPEAR_TIMEOUT\n      );\n\n      if (!initialMessageElement) {\n        //   \"Timeout waiting for chat messages to appear. Auto-scroll cannot proceed.\"\n        console.error(\n          \"Initial chat message elements did not appear within timeout.\"\n        );\n        // If initial messages don't appear, this URL was not successfully processed for auto-scroll.\n        // So, reset _lastProcessedChatUrl to null to allow a retry or a different trigger for this URL.\n        UIManager._lastProcessedChatUrl = null; // Add this line\n        return;\n      }\n      // console.log(\"Initial chat message elements found. Starting scroll loop.\");\n\n      // Mark this URL as processed *only after* initial messages are found.\n      // This ensures that autoScrollToTop will proceed if called for a new URL,\n      // and will block subsequent calls for the *same* URL until _lastProcessedChatUrl is reset by handleUrlChange.\n      UIManager._lastProcessedChatUrl = currentUrl; // Move this line from the beginning to here.\n\n      // --- IMPORTANT: Attach URL change listeners here after initial chat message elements appears ---\n      if (!UIManager._initialListenersAttached) {\n        // Only attach them once\n        UIManager.initUrlChangeObserver();\n        UIManager._initialListenersAttached = true; // Mark that they are attached\n      }\n\n      while (true) {\n        scrollableElement.scrollTop = 0;\n        await delay(50); // Small delay after scroll\n\n        // console.log(\"Scrolling to top, checking for progress bar...\");\n        const progressBarElement = await waitForElementToAppear(\n          \"mat-progress-bar.mdc-linear-progress--indeterminate\",\n          AUTOSCROLL_MAT_PROGRESS_BAR_APPEAR_TIMEOUT\n        );\n\n        if (progressBarElement) {\n          retriesForProgressBar = 0; // Reset retries if progress bar appeared\n          // console.log(\"Progress bar appeared. Waiting for it to disappear...\");\n          const disappeared = await waitForElementToDisappear(\n            \"mat-progress-bar.mdc-linear-progress--indeterminate\",\n            AUTOSCROLL_MAT_PROGRESS_BAR_DISAPPEAR_TIMEOUT\n          );\n          if (!disappeared) {\n            console.warn(\n              \"autoScrollToTop: mat-progress-bar did not disappear within expected time.\"\n            );\n          }\n        } else {\n          // If progress bar doesn't appear, increment retry count\n          retriesForProgressBar++;\n\n          if (retriesForProgressBar > AUTOSCROLL_MAX_RETRY) {\n            break;\n          }\n          await delay(AUTOSCROLL_REPEAT_DELAY);\n          continue; // Continue loop to try scrolling again\n        }\n\n        const currentChatData = ChatExporter.extractGeminiChatData(document);\n        const currentMessageCount = currentChatData\n          ? currentChatData.messages.length\n          : 0;\n\n        if (currentMessageCount > previousMessageCount) {\n          previousMessageCount = currentMessageCount;\n          retriesForProgressBar = 0; // Reset retries if new messages found\n        } else {\n          // No new messages detected after a scroll attempt (and progress bar check)\n          // If we had messages before, and now no new ones, it means we reached the top.\n          // console.log(\"autoScrollToTop: No NEW messages detected after this load cycle. Checking for termination conditions.\");\n          if (previousMessageCount !== -1) {\n            // console.log(\"autoScrollToTop: Assuming end of chat due to no new messages after loading.\");\n            break;\n          }\n        }\n\n        await delay(AUTOSCROLL_REPEAT_DELAY);\n      }\n\n      // console.log(\"autoScrollToTop: Auto-scroll process complete. Final message count:\", previousMessageCount);\n      UIManager.addOutlineControls();\n    },\n\n    /**\n     * Handles URL changes to trigger auto-scroll for new Gemini chats.\n     * This will only be attached AFTER the initial page load auto-scroll finishes.\n     */\n    handleUrlChange: function () {\n      const newUrl = Utils.getCleanUrl();\n      // console.log(\n      //   \"URL Change Detected (popstate or customHistoryChange):\",\n      //   newUrl\n      // );\n\n      const isGeminiChatUrl =\n        GEMINI_HOSTNAMES.some((host) => newUrl.includes(host)) &&\n        newUrl.includes(\"/app\");\n\n      if (isGeminiChatUrl) {\n        // Trigger auto-scroll for valid Gemini chat URLs.\n        setTimeout(() => {\n          UIManager.autoScrollToTop();\n        }, 100); // Small delay to allow DOM to update before triggering\n      } else {\n        console.log(\n          \"URL is not a Gemini chat URL. Skipping auto-scroll for:\",\n          newUrl\n        );\n      }\n    },\n\n    /**\n     * Initializes a MutationObserver to ensure the controls are always present\n     * and to regenerate the outline on DOM changes.\n     */\n    initObserver() {\n      const observer = new MutationObserver((mutations) => {\n        // Only re-add export controls if they are missing\n        if (!document.querySelector(`#${EXPORT_CONTAINER_ID}`)) {\n          UIManager.installAutomationBridge();\n        }\n        // Always ensure outline controls are present and regenerate content on changes\n        // This covers new messages, and for Gemini, scrolling up to load more content.\n        UIManager.addOutlineControls();\n      });\n\n      // Selector that includes chat messages and where new messages are added\n      let targetNode = null;\n      switch (CURRENT_PLATFORM) {\n        case COPILOT:\n          targetNode =\n            document.querySelector('[data-content=\"conversation\"]') ||\n            document.body;\n          break;\n        case GEMINI:\n          targetNode = document.querySelector(\"#__next\") || document.body;\n          break;\n        default:\n          targetNode = document.querySelector(\"main\") || document.body;\n      }\n\n      observer.observe(targetNode, {\n        childList: true,\n        subtree: true,\n        attributes: false,\n      });\n\n      // Additionally, for Gemini, listen for scroll events on the window or a specific scrollable div\n      // if MutationObserver isn't sufficient for detecting all content loads.\n      if (CURRENT_PLATFORM === GEMINI) {\n        let scrollTimeout;\n        window.addEventListener(\n          \"scroll\",\n          () => {\n            clearTimeout(scrollTimeout);\n            scrollTimeout = setTimeout(() => {\n              // Only regenerate if title or tags are different or current data count is less than actual count (implies more loaded)\n              const newChatData = ChatExporter.extractGeminiChatData(document);\n              if (\n                newChatData &&\n                ChatExporter._currentChatData &&\n                (newChatData._raw_title !==\n                  ChatExporter._currentChatData._raw_title ||\n                  newChatData.messages.length >\n                    ChatExporter._currentChatData.messages.length)\n              ) {\n                UIManager.addOutlineControls(); // Regenerate outline\n              }\n            }, 500); // Debounce scroll events\n          },\n          true\n        ); // Use capture phase to ensure it works\n      }\n    },\n\n    /**\n     * Sets up the event listeners for URL changes (popstate and customHistoryChange).\n     * This function will be called *after* the initial page load auto-scroll.\n     */\n    initUrlChangeObserver: function () {\n      // console.log(\"Attaching URL change listeners.\");\n      window.addEventListener(\"popstate\", UIManager.handleUrlChange);\n\n      // Overwrite history.pushState and history.replaceState to dispatch custom event\n      (function (history) {\n        const pushState = history.pushState;\n        history.pushState = function (state) {\n          if (typeof history.onpushstate == \"function\") {\n            history.onpushstate({ state: state });\n          }\n          const customEvent = new Event(\"customHistoryChange\");\n          window.dispatchEvent(customEvent);\n          return pushState.apply(history, arguments);\n        };\n\n        const replaceState = history.replaceState;\n        history.replaceState = function (state) {\n          if (typeof history.onreplacestate == \"function\") {\n            history.onreplacestate({ state: state });\n          }\n          const customEvent = new Event(\"customHistoryChange\");\n          window.dispatchEvent(customEvent);\n          return replaceState.apply(history, arguments);\n        };\n      })(window.history);\n\n      window.addEventListener(\"customHistoryChange\", UIManager.handleUrlChange);\n    },\n\n    /**\n     * Sets up keyboard shortcuts for exporting.\n     * Alt + M for Markdown, Alt + J for JSON.\n     */\n    setupShortcuts() {\n      document.addEventListener(\"keydown\", (e) => {\n        // Check if the user is typing in an input field\n        const isInput =\n          e.target.tagName === \"INPUT\" ||\n          e.target.tagName === \"TEXTAREA\" ||\n          e.target.isContentEditable;\n\n        if (isInput) return;\n\n        // Alt + M -> Export Markdown\n        if (e.altKey && e.code === \"KeyM\") {\n          e.preventDefault();\n          ChatExporter.initiateExport(\"markdown\");\n        }\n\n        // Alt + J -> Export JSON\n        if (e.altKey && e.code === \"KeyJ\") {\n          e.preventDefault();\n          ChatExporter.initiateExport(\"json\");\n        }\n\n        if (CURRENT_PLATFORM === GEMINI && e.altKey && e.code === \"KeyA\") {\n          e.preventDefault();\n          UIManager.autoScrollEnabled = !UIManager.autoScrollEnabled;\n          GM_setValue(\"gm_auto_scroll_enabled\", UIManager.autoScrollEnabled);\n          const label = document.getElementById(\"ai-exporter-scroll-status\");\n          if (label) {\n            const isEnabled = UIManager.autoScrollEnabled;\n            const statusText = isEnabled ? \"ON\" : \"OFF\";\n\n            label.textContent = `⬆️ ${statusText}`;\n            label.title = `Auto-scroll is ${statusText} (Press ALT+A to toggle)`;\n\n            label.style.color = isEnabled ? \"#1e7e34\" : \"#bd2130\";\n            label.style.backgroundColor = isEnabled ? \"#e8f5e9\" : \"#fbe9e7\";\n            label.style.borderColor = isEnabled ? \"#c3e6cb\" : \"#ffcdd2\";\n          }\n        }\n      });\n    },\n\n    /**\n     * Initializes only the non-visual managed automation bridge.\n     */\n    init() {\n      const install = () => UIManager.installAutomationBridge();\n      if (\n        document.readyState === \"complete\" ||\n        document.readyState === \"interactive\"\n      ) {\n        setTimeout(install, DOM_READY_TIMEOUT);\n      } else {\n        window.addEventListener(\n          \"DOMContentLoaded\",\n          () => setTimeout(install, DOM_READY_TIMEOUT),\n          { once: true }\n        );\n      }\n    },\n  };\n\n  // --- Script Initialization ---\n  UIManager.init();\n})();\n"}),
Object.assign({"id":"5b4323dc-326f-4fbb-a1a0-4ac8c00631c0","file":"qq-mail-reader.user.js","version":"0.2.0","sha256":"f42c08b3b43aeb73101bcd93cdaa6e6b26f5d6ca4f73585c4982d543569d6379","matches":[{"origin":"https://wx.mail.qq.com","pathPrefix":"/"}],"origins":["https://wx.mail.qq.com"],"runAt":"document-start","grants":["none"],"noframes":true},{install:function(){// ==UserScript==
// @name         SparkClaw QQ Mail Network Reader
// @namespace    sparkclaw.local
// @version      0.2.0
// @description  Read observed email sources in the signed-in page; SparkClaw owns synchronization and durable receipts.
// @match        https://wx.mail.qq.com/*
// @run-at       document-start
// @grant        none
// @noframes
// ==/UserScript==

// Generated by scripts/email/userscripts/build.mjs. Edit its source modules.
(()=>{
const installOutlookOriginalResolver=function installOutlookOriginalResolver({account,getInbox}) {
  let loading=null,active=true,stage='idle';
  const fail=()=>{throw Object.assign(new Error('email_network_original_unqualified'),{code:'email_network_original_unqualified'});};
  async function nativeModules() {
    if(!loading)loading=(async()=>{
      stage='runtime';
      let nativeRequire;
      if(!Array.isArray(window.webpackChunkOwa))fail();
      window.webpackChunkOwa.push([['sparkclaw_original_'+crypto.randomUUID()],{},require=>{nativeRequire=require;}]);
      if(typeof nativeRequire!=='function'||typeof nativeRequire.e!=='function')fail();
      // The observed native TriageActionImportExport dependency group. Loading
      // code has no mailbox mutation and is needed only for actual downloads.
      stage='modules';await Promise.all([21804,63436,73413,46866,32314,54709].map(id=>nativeRequire.e(id)));
      const build=nativeRequire(643446)?.V,mailbox=nativeRequire(129387)?.A,configuration=nativeRequire(859741)?.C;
      if(typeof build!=='function'||typeof mailbox!=='function'||typeof configuration!=='function')fail();
      return {build,mailbox,configuration};
    })();
    return loading;
  }
  async function prepare({account_address,provider_message_id}) {
    const owner=account(account_address),inbox=getInbox();
    if(!active||location.origin!=='https://outlook.live.com'||!inbox?.qualified||inbox.account!==owner||!/^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/.test(provider_message_id||''))fail();
    let timer;
    try{
      const {build,mailbox,configuration}=await Promise.race([nativeModules(),new Promise((_,reject)=>{timer=setTimeout(()=>reject(Object.assign(new Error('email_network_original_unqualified'),{code:'email_network_original_unqualified'})),20000);})]);
      const info=mailbox();
      stage='mailbox';
      if(!active||info?.type!=='UserMailbox')fail();
      // Resolve this exact native mailbox's configuration, not just the DOM
      // label: global settings and a selected shared mailbox can differ.
      if(String(configuration(info)?.SessionSettings?.UserEmailAddress||'').toLowerCase()!==owner)fail();
      account(owner);
      stage='url';
      const value=build(provider_message_id,'EML',info);
      if(typeof value!=='string')fail();
      const url=new URL(value);
      if(url.origin!=='https://attachment.outlook.live.net'||url.username||url.password||url.hash||
        !/^\/owa\/[^/]+\/service\.svc\/s\/DownloadMessage$/.test(url.pathname)||
        url.searchParams.get('id')!==provider_message_id||url.searchParams.get('outputFormat')!=='0'||!url.searchParams.get('token'))fail();
      account(owner);stage='ready';return url;
    }catch{fail();}finally{clearTimeout(timer);}
  }
  return {prepare,diagnostics:()=>({stage}),dispose(){active=false;loading=null;stage='disposed';}};
};
const installOutlookRangeTransport=function installOutlookRangeTransport({account,getInbox,receiveRows}) {
  const fail=code=>{throw Object.assign(new Error(code),{code});};
  const pattern=/^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/;
  function timestamp(value) {
    const match=typeof value==='string'&&/^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d{1,9}))?(Z|[+-]\d\d:\d\d)$/.exec(value);
    if(!match)return null;
    const wall=Date.parse(match[1]+'Z'),zone=match[3];
    if(!Number.isFinite(wall)||new Date(wall).toISOString().slice(0,19)!==match[1]||zone!=='Z'&&(Number(zone.slice(1,3))>23||Number(zone.slice(4))>59))return null;
    const seconds=Date.parse(match[1]+zone);
    if(!Number.isFinite(seconds))return null;
    return BigInt(seconds)*1000000n+BigInt((match[2]||'').padEnd(9,'0'));
  }
  function utc(ns) {
    let seconds=ns/1000000000n,fraction=ns%1000000000n;
    if(fraction<0){seconds--;fraction+=1000000000n;}
    const suffix=fraction.toString().padStart(9,'0').replace(/0+$/,'');
    return new Date(Number(seconds)*1000).toISOString().slice(0,19)+(suffix?'.'+suffix:'')+'Z';
  }
  let armed=null,active=true;
  function validate(request) {
    const owner=account(request.account_address),inbox=getInbox();
    const start=timestamp(request.interval_start),end=timestamp(request.interval_end);
    if(location.origin!=='https://outlook.live.com'||!inbox?.qualified||inbox.account!==owner||!inbox.folders?.length)fail('email_network_list_unqualified');
    if(start===null||end===null||start>=end||request.page!==0||request.provider_mode!=='time_range'||inbox.folders.length>100)fail('invalid_request');
    // Normalize RFC3339 offsets while preserving nanosecond boundaries.
    if(!inbox.folders.every(f=>pattern.test(f.id)))fail('email_network_list_unqualified');
    return {owner,start,end,folders:structuredClone(inbox.folders),query:`received>=${utc(start)} AND received<${utc(end)}`};
  }
  function arm(request) {
    if(!active||armed)fail('email_network_list_unqualified');
    const state=validate(request);
    let resolve,reject;
    const promise=new Promise((yes,no)=>{resolve=yes;reject=no;});
    promise.catch(()=>{}); // The trusted UI trigger precedes the consumer call.
    const abort=new AbortController();
    const timer=setTimeout(()=>{abort.abort();reject(Object.assign(new Error('email_network_read_failed'),{code:'email_network_read_failed'}));},20000);
    armed={...state,request:structuredClone(request),promise,resolve,reject,abort,timer,network:null};
    return {query:state.query};
  }
  async function decode(response) {
    if(!response.ok||new URL(response.url).origin!==location.origin)fail('email_network_read_failed');
    const reader=response.body.getReader(),chunks=[];let size=0;
    try{while(true){const {done,value}=await reader.read();if(done)break;size+=value.length;if(size>10<<20)fail('email_network_list_unqualified');chunks.push(value);}}
    finally{await reader.cancel().catch(()=>{});}
    const bytes=new Uint8Array(size);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length;}
    try{return JSON.parse(new TextDecoder().decode(bytes));}catch{fail('email_network_list_unqualified');}
  }
  function fetchSearch(args,next) {
    const state=armed;
    let body,url;
    try{url=new URL(typeof args[0]==='string'?args[0]:args[0]?.url,location.href);body=JSON.parse(args[1]?.body);}catch{return next(...args);}
    if(!active||!state||url.origin!==location.origin||url.pathname!=='/searchservice/api/v2/query'||body?.EntityRequests?.[0]?.Query?.QueryString!==state.query)return next(...args);
    if(state.network)return state.network.then(r=>r.clone());
    state.network=(async()=>{
      account(state.owner);
      if(args[1]?.method?.toUpperCase()!=='POST'||body.EntityRequests.length!==1||body.EntityRequests[0].ContentSources?.join(',')!=='Exchange')fail('email_network_list_unqualified');
      const entity=body.EntityRequests[0];
      entity.EntityType='Message';entity.From=0;entity.Size=50;
      entity.EnableTopResults=false;entity.TopResultsCount=0;entity.RefiningQueries=null;
      entity.Sort=[{Field:'Time',SortDirection:'Desc'}];
      entity.Filter={Or:state.folders.map(f=>({Term:{FolderId:f.id}}))};
      body.QueryAlterationOptions={...(body.QueryAlterationOptions||{}),EnableSuggestion:false,EnableAlteration:false};
      const response=await next(args[0],{...args[1],body:JSON.stringify(body),signal:state.abort.signal,redirect:'error'});
      const value=await decode(response.clone());account(state.owner);
      if(!active||armed!==state||state.abort.signal.aborted)fail('email_network_read_failed');
      state.resolve(value);clearTimeout(state.timer);return response;
    })().catch(cause=>{clearTimeout(state.timer);state.reject(cause);throw cause;});
    return state.network.then(r=>r.clone());
  }
  async function listPage(request) {
    const expected=validate(request),state=armed;
    if(!state||state.query!==expected.query||state.owner!==expected.owner||JSON.stringify(state.folders)!==JSON.stringify(expected.folders))fail('email_network_list_unqualified');
    const value=await state.promise;account(state.owner);
    if(!active||armed!==state||state.abort.signal.aborted)fail('email_network_read_failed');
    if(!value||typeof value!=='object'||Array.isArray(value))fail('email_network_list_unqualified');
    const entity=value?.EntitySets?.[0],result=entity?.ResultSets?.[0];
    if(value.EntitySets?.length!==1||entity.EntityType!=='Message'||entity.IsPartial!==false||entity.Properties?.HasParseException!==false||entity.ResultSets?.length!==1||
      !Number.isSafeInteger(result?.Total)||result.Total<0||typeof result.MoreResultsAvailable!=='boolean')fail('email_network_list_unqualified');
    const sources=result.Results??(result.Total===0?[]:null);
    if(!Array.isArray(sources)||sources.length>50||result.Total<sources.length||(!result.MoreResultsAvailable&&result.Total!==sources.length))fail('email_network_list_unqualified');
    const rows=[],ids=new Set(),folders=new Map(state.folders.map(f=>[f.id,f]));let unsupported=0;
    for(const item of sources){
      const node=item?.Source,id=typeof node?.ImmutableId==='string'?node.ImmutableId.replace(/_/g,'+').replace(/-/g,'/'):null;
      const thread=node?.ConversationId?.Id,folder=folders.get(node?.ParentFolderId?.Id),received=timestamp(node?.DateTimeReceived);
      // The native Message adapter uses this immutable EWS form. Ordinary
      // ItemId can change on a folder move and must not own a second source.
      if(item.Type!=='Message'||!pattern.test(id||'')||!pattern.test(node?.ItemId?.Id||'')||!pattern.test(thread||'')||ids.has(id)||!folder||typeof node.IsDraft!=='boolean'||typeof node.IsRead!=='boolean'||received===null||received<state.start||received>=state.end){unsupported++;continue;}
      ids.add(id);if(node.IsDraft)continue;
      rows.push({provider_message_id:id,provider_selection_id:thread,provider_thread_id:thread,received_at:node.DateTimeReceived,draft:false,unread:!node.IsRead,sent:false,inbox:folder.inbox,folder:folder.inbox?'inbox':'outlook:'+folder.id});
    }
    receiveRows(rows);
    const digest=await crypto.subtle.digest('SHA-256',new TextEncoder().encode(JSON.stringify(state.folders.map(f=>f.id))));
    if(!active||armed!==state||state.abort.signal.aborted)fail('email_network_read_failed');
    return {provider:'outlook',account_address:state.owner,rows,unsupported_rows:unsupported,has_next:result.MoreResultsAvailable,page:0,scope:'inbound_received',folder_scope_id:Array.from(new Uint8Array(digest),v=>v.toString(16).padStart(2,'0')).join('')};
  }
  function resetRound(){if(armed){clearTimeout(armed.timer);armed.abort.abort();armed.reject(Object.assign(new Error('email_network_read_failed'),{code:'email_network_read_failed'}));}armed=null;}
  function dispose(){active=false;resetRound();}
  return {arm,listPage,fetchSearch,resetRound,dispose};
};
const installOutlookEarlyBridge=function installOutlookEarlyBridge() {
  if(window.top!==window || !['https://outlook.live.com','https://outlook.office.com','https://outlook.office365.com'].includes(location.origin))return null;
  if(window.SparkClawOutlookEarlyBridge)return window.SparkClawOutlookEarlyBridge;
  const NativeWorker=window.Worker,NativeChannel=window.MessageChannel,originalFetch=window.fetch;
  if(!NativeWorker||!NativeChannel)return null;
  const queue=[],listeners=[];let consumer=null,startup=null,active=true;
  const observe=(worker,message)=>{
    const operation=message?.argumentList?.[0]?.value?.operationName;
    if(!['ItemRows','ConversationRows','ItemExport'].includes(operation))return;
    const item={worker,message:structuredClone(message)};
    if(consumer)consumer.request(item.worker,item.message);
    else {queue.push(item);if(queue.length>40)queue.shift();}
  };
  const Worker=class extends NativeWorker {postMessage(message,...rest){try{observe(this,message);}catch{}return super.postMessage(message,...rest);}};
  const Channel=class extends NativeChannel {constructor(){super();for(const port of [this.port1,this.port2]){
    const listener=event=>consumer?.result(event,port);port.addEventListener('message',listener);listeners.push([port,listener]);
  }}};
  window.Worker=Worker;window.MessageChannel=Channel;
  const fetch=async function(...args){
    const next=(...values)=>originalFetch.apply(this,values);
    const response=await (consumer?.fetchSearch?consumer.fetchSearch(args,next):next(...args));
    try{
      const url=new URL(response.url);
      if(active&&response.ok&&url.origin===location.origin&&/^\/owa\/\d+\/startupdata\.ashx$/.test(url.pathname))void(async()=>{
        const reader=response.clone().body.getReader(),chunks=[];let size=0;
        try{for(;;){const {done,value}=await reader.read();if(done)break;size+=value.length;if(size>10<<20){void reader.cancel();return;}chunks.push(value);}}
        finally{reader.releaseLock();}
        const bytes=new Uint8Array(size);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length;}
        const value=JSON.parse(new TextDecoder().decode(bytes));if(!active)return;
        // Retain hierarchy evidence only, never session secrets or mail bodies.
        startup={owaUserConfig:{SessionSettings:{UserEmailAddress:value?.owaUserConfig?.SessionSettings?.UserEmailAddress}},findFolders:value.findFolders,findConversation:{Body:{FolderId:value?.findConversation?.Body?.FolderId}}};
        consumer?.startup?.(startup);
      })().catch(()=>{});
    }catch{}
    return response;
  };
  window.fetch=fetch;
  const bridge=Object.freeze({
    attach(next){if(!active)return;consumer=next;for(const item of queue.splice(0))consumer.request(item.worker,item.message);if(startup)consumer.startup?.(startup);},
    detach(next){if(consumer===next)consumer=null;},
    dispose(){
      active=false;consumer=null;startup=null;queue.length=0;
      for(const [port,listener]of listeners)port.removeEventListener('message',listener);listeners.length=0;
      if(window.Worker===Worker)window.Worker=NativeWorker;
      if(window.MessageChannel===Channel)window.MessageChannel=NativeChannel;
      if(window.fetch===fetch)window.fetch=originalFetch;
      delete window.SparkClawOutlookEarlyBridge;
    },
  });
  Object.defineProperty(window,'SparkClawOutlookEarlyBridge',{configurable:true,value:bridge});
  return bridge;
};
const parseGmailList=function parseGmailList(value, includeThreads = false) {
  const batches = value?.[19];
  if (!Array.isArray(batches)) return [];
  const result = [];
  for (const batch of batches) {
    if (!Array.isArray(batch?.[1])) continue;
    for (const container of batch[1]) {
      const thread = container?.[0];
      if (!Array.isArray(thread) || !/^(?:thread-f:\d+|thread-a:r-?\d+)$/u.test(thread[3]) || !Array.isArray(thread[4])) continue;
      const messages = thread[4];
      if ((!includeThreads && messages.length !== 1) || messages.length < 1 || messages.length > 1000) continue;
      const members=[];
      for (const message of messages) {
      const id = message?.[55], labels = message?.[10];
      if (!/^(?:msg-f:\d+|msg-a:r-?\d+)$/u.test(message?.[0]) || !/^[a-f0-9]{1,32}$/u.test(id) ||
          !Array.isArray(labels) || !labels.every(label => typeof label === 'string')) continue;
      if (thread[3].startsWith('thread-f:')) {
        if (!message[0].startsWith('msg-f:') || BigInt(`0x${id}`).toString() !== message[0].slice(6) || !includeThreads && thread[3].slice(9) !== message[0].slice(6)) continue;
      } else if (!message[0].startsWith('msg-a:')) continue;
      members.push({ provider_message_id: id, provider_thread_id: thread[3],
        unread: labels.includes('^u'), inbox: labels.includes('^i'), draft: labels.includes('^r'),
        ...(includeThreads ? {sent:labels.includes('^f'),observed_message_count:messages.length,
          // Observed internal receipt timestamp: the pinned original's Received
          // trace agrees at the second, and RFC Date is independently earlier.
          ...(Number.isSafeInteger(message[6]) && message[6]>=946684800000 && message[6]<=Date.now()+300000 ? {received_at:new Date(message[6]).toISOString()} : {})} : {}) });
      }
      if(members.length===messages.length && new Set(members.map(member=>member.provider_message_id)).size===members.length) result.push(...members);
    }
  }
  return result;
};
const parseOutlookList=function parseOutlookList(value, includeInventory = false) {
  const rows=[];
  let visited=0;
  const visit=(node,depth)=>{
    if(!node || typeof node!=='object' || depth>20 || ++visited>20000) return;
    if(Array.isArray(node.Conversations)) for(const item of node.Conversations.slice(0,100)) {
      const selection=item?.ConversationId?.Id, id=item?.ItemIds?.[0]?.Id;
      if(typeof selection!=='string' || !selection)continue;
      let inventory = includeInventory ? {last_delivery_time:typeof item.LastDeliveryTime==='string' && Number.isFinite(Date.parse(item.LastDeliveryTime)) ? item.LastDeliveryTime : null} : {};
      if (includeInventory && Number.isInteger(item.GlobalMessageCount) && item.GlobalMessageCount >= 1 && item.GlobalMessageCount <= 1000 &&
          Array.isArray(item.GlobalItemIds) && item.GlobalItemIds.length === item.GlobalMessageCount && Array.isArray(item.ItemIds) &&
          item.ItemIds.length === item.MessageCount && Array.isArray(item.DraftItemIds)) {
        const globalIDs = item.GlobalItemIds.map(value=>value?.Id), localIDs = item.ItemIds.map(value=>value?.Id), drafts = item.DraftItemIds.map(value=>value?.Id);
        if (globalIDs.every(value=>typeof value==='string' && /^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/u.test(value)) && new Set(globalIDs).size === globalIDs.length &&
            localIDs.every(value=>globalIDs.includes(value)) && drafts.every(value=>globalIDs.includes(value)) && new Set(drafts).size === drafts.length) {
          inventory = {members:globalIDs.map(value=>({provider_message_id:value,local:localIDs.includes(value),draft:drafts.includes(value)})),
            inventory_complete:true,global_unread_count:item.GlobalUnreadCount,
            last_delivery_time: typeof item.LastDeliveryTime==='string' && Number.isFinite(Date.parse(item.LastDeliveryTime)) ? item.LastDeliveryTime : null};
        }
      }
      if(typeof id!=='string' || !id || selection===id ||
          item.MessageCount!==1 || item.GlobalMessageCount!==1 || item.ItemIds?.length!==1 ||
          item.GlobalItemIds?.length!==1 || item.GlobalItemIds[0]?.Id!==id ||
          ![0,1].includes(item.UnreadCount) || item.GlobalUnreadCount!==item.UnreadCount) {
        rows.push({provider_selection_id:selection,provider_message_id:null,unread:null,...inventory});continue;
      }
      rows.push({provider_selection_id:selection,provider_message_id:id,unread:item.UnreadCount===1,...inventory});
    }
    for(const child of Object.values(node))if(child&&typeof child==='object')visit(child,depth+1);
  };
  visit(value,0);return rows;
};
const parseQQMailList=function parseQQMailList(value) {
  if(value?.head?.ret!==0 || !Number.isInteger(value.head.time) || !Array.isArray(value.body?.list) || !Number.isInteger(value.body.total_num) || value.body.total_num<0)return null;
  const rows=[];
  for(const item of value.body.list.slice(0,2000)){
    if(typeof item?.emailid!=='string' || !/^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/u.test(item.emailid) || !Number.isInteger(item.dirid) || item.dirid<1 ||
      !Number.isInteger(item.totime) || item.totime<946684800 || item.totime>value.head.time+300 || item.unread!==undefined && item.unread!==0 && item.unread!==1)continue;
    rows.push({provider_message_id:item.emailid,provider_selection_id:item.emailid,provider_thread_id:item.emailid,
      folder:item.dirid===1?'inbox':item.dirid===3?'sent':`qq:${item.dirid}`,unread:item.unread===1,
      received_at:new Date(item.totime*1000).toISOString()});
  }
  return {rows,total_count:value.body.total_num,unsupported_rows:value.body.list.length-rows.length};
};
const markQQMailRead=async function markQQMailRead({provider_message_id,row,binding,fetch:originalFetch}) {
  if (row?.provider_message_id !== provider_message_id || row.unread !== true ||
      !(binding instanceof URL) || binding.origin !== location.origin || binding.pathname !== '/list/maillist' ||
      binding.searchParams.get('func') !== '1' || !binding.searchParams.get('sid') || typeof originalFetch !== 'function') return null;
  const matches=[...document.querySelectorAll('.mail-list-page-item[data-mailid]')].filter(node=>
    node?.getAttribute?.('data-mailid')===provider_message_id && node.isConnected!==false && node.getClientRects?.().length>0);
  if(matches.length!==1||typeof matches[0].click!=='function')return null;
  matches[0].click();
  for(let attempt=0;attempt<20;attempt++){
    await new Promise(resolve=>setTimeout(resolve,250));
    let response,value;
    try{
      response=await originalFetch.call(window,binding.href,{method:'GET',credentials:'same-origin',redirect:'error',cache:'no-store',signal:AbortSignal.timeout(20000)});
      if(!response.ok||response.url&&new URL(response.url).origin!==location.origin)return {provider_message_id,read_state:'unknown'};
      const text=await response.text();
      if(text.length>10<<20)return {provider_message_id,read_state:'unknown'};
      value=JSON.parse(text);
    }catch{return {provider_message_id,read_state:'unknown'};}
    const parsed=parseQQMailList(value);
    const confirmed=parsed?.rows?.find(candidate=>candidate.provider_message_id===provider_message_id);
    if(confirmed?.unread===false)return {provider_message_id,read_state:'read'};
  }
  return {provider_message_id,read_state:'unknown'};
};
(function installReader(config) {
  'use strict';
  if (window.top !== window || !config.origins.includes(location.origin)) return;
  window.SparkClawMailReader?.dispose();
  const originalFetch = window.fetch, originalOpen = XMLHttpRequest.prototype.open;
  const originalSend = XMLHttpRequest.prototype.send, originalSetHeader = XMLHttpRequest.prototype.setRequestHeader;
  const records = new Map(), pageTokens = new Map();
  let listRequest = null, networkQuery = null, inbox = null, transport = null, objectURL = null, originalAbort = null;
  let originalBytes = null, originalMessageID = '';
  let originalState = 'unlearned', responseOrigin = '';
  let listStage = 'idle';
  let nativeQueryReply = null;
  let active = true, account = '', binding = null, anchor = null;
  let pendingOperations = 0;
  const idPattern = /^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/;
  const failure = code => { throw Object.assign(new Error(code), {code}); };
  async function inRound(operation, request) {
    if (!active) failure('email_network_capability_unavailable');
    pendingOperations++;
    try { return await operation(request); } finally { pendingOperations--; }
  }
  // Only the Controller's idle mailbox lease may reset a round. Keep learned
  // transport/session templates, but never retain source bytes or query results
  // across polls. This performs no network request and does not retry failures.
  function resetRound({account_address} = {}) {
    if (!active || pendingOperations || originalAbort) failure('email_network_list_unqualified');
    checkedAccount(account_address);
    if(config.provider==='outlook' && typeof transport?.resetRound!=='function') failure('email_network_capability_unavailable');
    transport?.resetRound?.();
    records.clear(); pageTokens.clear(); networkQuery=null; nativeQueryReply=null;
    anchor?.remove(); anchor=null;
    if(objectURL)URL.revokeObjectURL(objectURL);
    objectURL=null; originalBytes=null; originalMessageID='';
    originalState='unlearned'; responseOrigin=''; listStage='idle';
    return {provider:config.provider,account_address:account};
  }
  // Gateway checkpoints carry micro/nanoseconds; Date.parse alone truncates
  // those bounds and can falsely certify a missed endpoint millisecond.
  function instantNanos(value) {
    const match=typeof value==='string'&&/^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d{1,9}))?(Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$/.exec(value);
    if(!match)return null;
    const seconds=Date.parse(match[1]+match[3]);
    const offset=match[3]==='Z'?0:(match[3][0]==='-'?-1:1)*(Number(match[3].slice(1,3))*60+Number(match[3].slice(4,6)));
    if(!Number.isFinite(seconds)||new Date(seconds+offset*60000).toISOString().slice(0,19)!==match[1])return null;
    return BigInt(seconds)*1000000n+BigInt((match[2]||'').padEnd(9,'0'));
  }
  const inlineOriginalLimit = 1 << 20;
  const base64 = bytes => {
    let binary = '';
    for (let offset = 0; offset < bytes.length; offset += 0x8000) {
      binary += String.fromCharCode(...bytes.subarray(offset, offset + 0x8000));
    }
    return btoa(binary);
  };
  function checkedAccount(expected) {
    const current = config.account();
    if (!current) failure('email_account_identity_unavailable');
    if (account && account !== current.toLowerCase()) {
      records.clear(); pageTokens.clear(); binding = null; listRequest = null; networkQuery = null; nativeQueryReply = null; inbox = null; transport?.dispose();
      failure('email_account_identity_mismatch');
    }
    account = current.toLowerCase();
    if (expected && expected.toLowerCase() !== account) failure('email_account_identity_mismatch');
    return account;
  }
  function observe(url, text, requestQuery) {
    if (!active || typeof text !== 'string' || text.length > 10 << 20) return;
    try {
      const u = new URL(url, location.href);
      if (u.origin !== location.origin || !config.listURL(u)) return;
      const value = JSON.parse(text);
      if(config.provider==='gmail' && typeof requestQuery==='string') nativeQueryReply={query:requestQuery,value};
      if(config.provider==='outlook' && u.pathname.endsWith('/startupdata.ashx')) {
        const owner=value.owaUserConfig?.SessionSettings?.UserEmailAddress, id=value.findConversation?.Body?.FolderId?.Id;
        if(typeof owner==='string' && typeof id==='string')inbox=config.parseFolders?.(value)||{account:owner.toLowerCase(),id,qualified:false};
      }
      const rows = config.parse(value);
      if (!rows) return;
      checkedAccount();
      for (const row of rows) {
        if (row && row.provider_selection_id === undefined) row.provider_selection_id = row.provider_thread_id || row.provider_message_id;
        if (!idPattern.test(row.provider_message_id || '')) continue;
        records.set(row.provider_message_id, row);
      }
      while (records.size > 2000) records.delete(records.keys().next().value);
      if (config.provider === 'qq_mail' && u.searchParams.get('func') === '1' && u.searchParams.get('sid')) binding = u;
    } catch { /* Unqualified responses never become admitted source evidence. */ }
  }
  function rememberListRequest(request, body) {
    try {
      const url = new URL(request.url, location.href);
      if (url.origin !== location.origin || !config.listURL(url)) return;
      listRequest = {method: request.method, url, headers: {...(request.headers || {})}, ...(body === undefined ? {} : {body})};
    } catch {}
  }
  function qqListSource() {
    const source = listRequest || binding;
    if (source) return new URL(source.url || source, location.href);
    // The current QQ client can restore /home/index from its own cache without
    // issuing /list/maillist again. Its signed-in route still carries the same
    // first-party session id required by /list/search, so construct only the
    // fixed list binding instead of depending on an incidental resource entry.
    try {
      const current = new URL(location.href);
      const sid = current.origin===location.origin && current.pathname==='/home/index'
        ? current.searchParams.get('sid') : '';
      if(sid && sid.length<=4096) {
        const url=new URL('/list/maillist',location.origin);
        url.searchParams.set('func','1');url.searchParams.set('sid',sid);
        return url;
      }
    } catch {}
    // QQ can load its first list before the userscript's fetch/XHR hooks run.
    const resources = performance.getEntriesByType('resource');
    for (let index = resources.length - 1; index >= 0; index--) {
      try {
        const url = new URL(resources[index].name);
        if (url.origin === location.origin && url.pathname === '/list/maillist' &&
            url.searchParams.get('func') === '1' && url.searchParams.get('sid')) return url;
      } catch {}
    }
    return null;
  }
  const open = function(method, url, ...args) {
    this.__sparkclawMailURL = url;
    this.__sparkclawMailRequest = {method, url, headers:{}};
    return originalOpen.call(this, method, url, ...args);
  };
  const setHeader = function(key, value) {
    if (this.__sparkclawMailRequest) this.__sparkclawMailRequest.headers[key.toLowerCase()] = value;
    return originalSetHeader.call(this,key,value);
  };
  const send = function(...args) {
    try {
      const request = this.__sparkclawMailRequest;
      const body = args[0] === undefined ? undefined : JSON.parse(args[0]);
      if (request) {
        const previous = listRequest?.body?.[0]?.[3];
        rememberListRequest(request, body);
        if (previous && body?.[0]?.[3] !== previous) pageTokens.clear();
      }
    } catch {}
    const url = this.__sparkclawMailURL;
    let requestQuery;
    try { requestQuery=JSON.parse(args[0])?.[0]?.[3]; } catch {}
    this.addEventListener('load', () => {
      if (this.status === 200 && (!this.responseType || this.responseType === 'text')) observe(url, this.responseText, requestQuery);
    }, {once:true});
    return originalSend.apply(this, args);
  };
  const fetch = async function(...args) {
    const response = await originalFetch.apply(this, args);
    try {
      const u = new URL(response.url);
      if (u.origin === location.origin && config.listURL(u)) {
        if (!listRequest) rememberListRequest({method:String(args[1]?.method || 'GET').toUpperCase(), url:u, headers:args[1]?.headers || {}}, args[1]?.body ? JSON.parse(args[1].body) : undefined);
      }
      if (active && response.ok && u.origin === location.origin && config.listURL(u) && Number(response.headers.get('content-length')) <= 10 << 20) {
        void (async () => {
          const copy = response.clone(), reader = copy.body.getReader(), chunks = []; let size = 0;
          try {
            for (;;) { const {done,value} = await reader.read(); if (done) break;
              size += value.length; if (size > 10 << 20) { void reader.cancel(); return; } chunks.push(value); }
            const bytes = new Uint8Array(size); let offset = 0;
            for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.length; }
            let requestQuery;try{requestQuery=JSON.parse(args[1]?.body)?.[0]?.[3];}catch{}
            observe(response.url, new TextDecoder().decode(bytes),requestQuery);
          } finally { reader.releaseLock(); }
        })().catch(()=>{});
      }
    } catch {}
    return response;
  };
  XMLHttpRequest.prototype.open = open; XMLHttpRequest.prototype.send = send; XMLHttpRequest.prototype.setRequestHeader = setHeader;
  window.fetch = fetch;
  function snapshot({account_address, interval_start, interval_end} = {}) {
    checkedAccount(account_address);
    const start = instantNanos(interval_start), end = instantNanos(interval_end);
    if (start===null || end===null || start >= end) failure('invalid_request');
    let unsupported = 0;
    const rows = [];
    for (const row of records.values()) {
      if (row.draft || row.sent) continue;
      if (row.grouped) { unsupported++; continue; }
      const received = instantNanos(row.received_at);
      if (received===null) { unsupported++; continue; }
      if (received >= start && received < end) rows.push({...row});
    }
    return {provider:config.provider, account_address:account, rows, unsupported_rows:unsupported,
      scan_complete:false, reason:'folder_scope_and_pagination_unqualified'};
  }
  async function prepareRetainedOriginal(target = {}) {
    const {account_address,provider_message_id,provider_selection_id,provider_native_id,folder}=target;
    checkedAccount(account_address);
    if(!idPattern.test(provider_message_id||'')||!idPattern.test(provider_selection_id||'')||folder==='sent')failure('invalid_request');
    // These IDs came from a previous qualified list and were retained by the
    // Gateway. Reuse the exact original endpoint without searching the inbox.
    const row={provider_message_id,provider_selection_id,native_message_id:provider_native_id,folder,draft:false,sent:false};
    if(config.provider==='gmail') {
      if(!/^(?:msg-f:\d+|msg-a:r-?\d+)$/u.test(provider_native_id||''))failure('email_network_target_unobserved');
      if(provider_native_id.startsWith('msg-f:')&&(!/^[a-f0-9]{1,32}$/u.test(provider_message_id)||BigInt(`0x${provider_message_id}`).toString()!==provider_native_id.slice(6)))failure('email_network_target_unobserved');
    }
    if(config.provider==='qq_mail') {
      if(/^(?:C|@)/u.test(provider_message_id))failure('email_network_target_unobserved');
      binding=qqListSource();
      if(!binding)failure('email_network_original_unqualified');
    }
    return prepareOriginal(target,row);
  }
  async function prepareOriginal({account_address, provider_message_id} = {}, retainedRow = null) {
    checkedAccount(account_address);
    if (!idPattern.test(provider_message_id || '')) failure('invalid_request');
    const row = retainedRow || records.get(provider_message_id);
    if (!row || row.draft || row.grouped) failure('email_network_target_unobserved');
    let url = await config.download?.({id:provider_message_id, row, binding, request:listRequest});
    if (!url && config.provider==='outlook' && transport?.prepareOriginal) url = await transport.prepareOriginal({account_address:account,provider_message_id});
    if (!url) failure('email_network_original_unqualified');
    try {
      url = new URL(url, location.href);
      if (url.protocol !== 'https:' || url.username || url.password ||
          (url.origin !== location.origin && !(config.provider === 'gmail' && url.origin === 'https://mail-attachment.googleusercontent.com') &&
            !(config.provider === 'outlook' && url.origin === 'https://attachment.outlook.live.net'))) failure('email_network_original_unqualified');
    } catch (error) { failure(error.code || 'email_network_original_unqualified'); }
    if(originalAbort)failure('email_network_original_unqualified');
    originalState='fetching';originalAbort=new AbortController();
    return (async()=>{
      try {
        const response=await originalFetch.call(window,url.href,{credentials:url.origin===location.origin?'same-origin':'omit',redirect:config.provider==='gmail'?'follow':'error',cache:'no-store',signal:AbortSignal.any([originalAbort.signal,AbortSignal.timeout(20000)])});
        originalState='http_'+response.status;responseOrigin=response.url?new URL(response.url).origin:'';if(response.url&&responseOrigin!==url.origin&&!(config.provider==='gmail'&&responseOrigin==='https://mail-attachment.googleusercontent.com'))failure('email_network_original_unqualified');if(!response.ok)failure('email_network_original_unqualified');
        const reader=response.body.getReader(),chunks=[];let length=0;
        try {
          for(;;){const {done,value}=await reader.read();if(done)break;length+=value.length;if(length>110<<20){void reader.cancel();failure('email_capture_limit');}chunks.push(value);}
        }finally{reader.releaseLock();}
        checkedAccount(account_address);
        if(!active)failure('email_network_original_unqualified');
        const bytes=new Uint8Array(length);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length;}
        const header=new TextDecoder().decode(bytes.subarray(0,Math.min(bytes.length,65536)));
        const headerEnd=header.search(/\r?\n\r?\n/);
        const headerBlock=headerEnd<0?'':header.slice(0,headerEnd);
        let precedingField=false;
        const validHeaders=headerBlock.split(/\r?\n/).every(line=>{
          if(/^[ \t]/.test(line))return precedingField;
          precedingField=/^[!-9;-~]+:/.test(line);
          return precedingField;
        });
        // A provider's HTML "show original" page may embed From: and a blank
        // line much later. It is an adapter response error, not a bad email and
        // must never consume a mail-specific retry allowance.
        if(headerEnd<0||!validHeaders||!/^From:/im.test(headerBlock))failure('email_network_original_unqualified');
        anchor?.remove();if(objectURL)URL.revokeObjectURL(objectURL);
        originalBytes = bytes;
        originalMessageID = provider_message_id;
        objectURL=URL.createObjectURL(new Blob([bytes],{type:'message/rfc822'}));
        anchor=document.createElement('a');anchor.id='sparkclaw-mail-original';anchor.href=objectURL;
        anchor.download='message.eml';anchor.textContent='SparkClaw EML';anchor.hidden=true;document.body.append(anchor);
        originalState='ready';
        const result={selector:'#sparkclaw-mail-original',account_address:account,provider_message_id,bytes:bytes.length};
        if(bytes.length<=inlineOriginalLimit) { result.inline_bytes=bytes.length; result.inline_base64=base64(bytes); }
        return result;
      }catch(error){
        if(['email_account_identity_mismatch','email_account_identity_unavailable','email_capture_limit'].includes(error.code))throw error;
        if(originalState==='fetching')originalState='fetch_failed';failure('email_network_original_unqualified');
      }finally{originalAbort=null;}
    })();
  }

  async function listPage({account_address,interval_start,interval_end,page=0,folder='inbox',provider_mode}) {
    checkedAccount(account_address);
    if(config.provider==='outlook' && transport?.listPage)return transport.listPage({account_address,interval_start,interval_end,page,folder,provider_mode});
    if (config.provider === 'qq_mail') {
      listStage='qq_source';
      const start=instantNanos(interval_start), end=instantNanos(interval_end);
      if (start===null||end===null||start>=end||!Number.isInteger(page)||page<0||page>128) failure('invalid_request');
      const deadline=Date.now()+5000;
      let url=qqListSource();
      while(!url && Date.now()<deadline) {
        await new Promise(resolve=>setTimeout(resolve,100));
        url=qqListSource();
      }
      if (!url || url.origin!==location.origin || url.pathname!=='/list/maillist' || url.searchParams.get('func') !== '1' || !url.searchParams.get('sid')) failure('email_network_list_unqualified');
      if(provider_mode==='time_range') {
        if(page!==0)failure('email_incremental_unqualified');
        // QQ's current public web client uses /list/search with epoch-second
        // after/before parameters. No mailbox-head pagination participates.
        const search=new URL('/list/search',location.origin);
        search.searchParams.set('sid',url.searchParams.get('sid'));
        const body=new URLSearchParams({page_now:'0',page_size:'50',after:String(start/1000000000n-1n),before:String((end+999999999n)/1000000000n),sort_type:'1',sort_direction:'1'});
        listStage='qq_request';
        const response=await originalFetch.call(window,search.href,{method:'POST',credentials:'same-origin',redirect:'error',headers:{'content-type':'application/x-www-form-urlencoded'},body,signal:AbortSignal.timeout(20000)});
        if([401,403].includes(response.status))failure('email_login_required');if(!response.ok)failure('email_network_read_failed');
        const reader=response.body.getReader(),chunks=[];let length=0;
        try {for(;;){const {done,value}=await reader.read();if(done)break;length+=value.length;if(length>10<<20){void reader.cancel();failure('email_capture_limit');}chunks.push(value);}}finally{reader.releaseLock();}
        const bytes=new Uint8Array(length);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length;}
        listStage='qq_json';
        let value;try{value=JSON.parse(new TextDecoder().decode(bytes));}catch{failure('email_network_list_unqualified');}
        checkedAccount(account_address);
        listStage='qq_head';if(value?.head?.ret!==0)failure('email_network_list_unqualified');
        listStage='qq_total';if(!Number.isInteger(value.body?.total_num)||value.body.total_num<0)failure('email_network_list_unqualified');
        listStage='qq_lock';if(!Number.isInteger(value.body.lock_num)||value.body.lock_num<0)failure('email_network_list_unqualified');
        if(value.body.total_num===0&&value.body.list===undefined)value.body.list=[];
        listStage='qq_list';if(!Array.isArray(value.body.list)||value.body.list.length>50)failure('email_network_list_unqualified');
        const parsed=config.parse(value),rows=[];
        let unsupported=value.body.list.length-parsed.length+value.body.lock_num;
        for(const row of parsed){
          if(row.grouped){unsupported++;continue;}
          if(row.folder!=='inbox'&&!/^qq:[1-9][0-9]{3,9}$/.test(row.folder))continue;
          const at=instantNanos(row.received_at);if(at===null){unsupported++;continue;}
          if(at>=start&&at<end){records.set(row.provider_message_id,row);rows.push(row);}
        }
        binding=url;
        listStage='qq_complete';
        return {provider:config.provider,account_address:account,rows,unsupported_rows:unsupported,has_next:value.body.total_num>value.body.list.length,page:0,scope:'inbound_received',folder_scope_id:'search_inbound_v1'};
      }
      const dir=folder==='inbox'?1:/^qq:[1-9][0-9]{3,9}$/u.test(folder)?Number(folder.slice(3)):folder==='sent'?3:null;
      if (!Number.isInteger(dir)||dir<1) failure('email_network_list_unqualified');
      url.searchParams.set('func','1');url.searchParams.set('dir',String(dir));url.searchParams.set('dirid',String(dir));url.searchParams.set('page_now',String(page));url.searchParams.set('page_size','50');
      const response=await originalFetch.call(window,url.href,{method:'GET',credentials:'same-origin',redirect:'error',cache:'no-store',signal:AbortSignal.timeout(20000)});
      if([401,403].includes(response.status))failure('email_login_required');if(!response.ok)failure('email_network_read_failed');
      let value;try{value=await response.json();}catch{failure('email_network_list_unqualified');}
      checkedAccount(account_address);
      if(value?.head?.ret!==0||!Array.isArray(value.body?.list)||!Number.isInteger(value.body.total_num)||value.body.total_num<0)failure('email_network_list_unqualified');
      binding=url;
      const rows=config.parse(value).filter(row=>row.folder===(folder==='inbox'?'inbox':folder));
      const received=rows.filter(row=>{const at=instantNanos(row.received_at);return at!==null&&at>=start&&at<end;});
      const unsupportedRows=Math.max(0,value.body.list.length-rows.length);
      const receiptTimes=rows.map(row=>instantNanos(row.received_at));
      const receiptOrdered=receiptTimes.length>0 && receiptTimes.every((at,index)=>at!==null&&(index===0||receiptTimes[index-1]>=at));
      // QQ's native inbox page is ordered by `totime` descending. Once a fully
      // qualified page crosses the requested lower bound, every later page is
      // older and scanning the rest of mailbox history would add no evidence.
      // Any invalid row or ordering violation keeps pagination fail-closed.
      const boundaryReached=unsupportedRows===0&&receiptOrdered&&receiptTimes.at(-1)<start;
      const hasNext=!boundaryReached&&(page+1)*50<value.body.total_num;
      for(const row of rows)records.set(row.provider_message_id,row);
      return {provider:config.provider,account_address:account,rows:received,unsupported_rows:unsupportedRows,has_next:hasNext,next_page:hasNext?page+1:undefined,page,scope:'inbound_received',folder_scope_id:String(dir)};
    }
    if (config.provider !== 'gmail') failure('email_network_list_unqualified');
    listStage = 'template';
    const start=instantNanos(interval_start),end=instantNanos(interval_end);
    if (start===null||end===null||start>=end||!Number.isInteger(page)||page<0||page>10000) failure('invalid_request');
    const query=`-in:trash -in:spam -in:drafts after:${start/1000000000n-1n} before:${(end+999999999n)/1000000000n}`;
    if (networkQuery && networkQuery !== query) failure('email_network_list_unqualified');
    networkQuery = query;
    let value;
    const usableTemplate=String(listRequest?.method).toUpperCase()==='POST' && Array.isArray(listRequest?.body?.[0]) && typeof listRequest.body[0][3]==='string' && Array.isArray(listRequest.body[2]) && Array.isArray(listRequest.body[0][15]);
    if(!usableTemplate && page===0 && typeof config.search==='function') {
      listStage='native_query';
      config.search(query);
      const deadline=Date.now()+20000;
      while(active && nativeQueryReply?.query!==query && Date.now()<deadline) await new Promise(resolve=>setTimeout(resolve,100));
      if(!active||nativeQueryReply?.query!==query)failure('email_network_list_unqualified');
      value=nativeQueryReply.value;
    } else {
    if (!listRequest) failure('email_network_list_unqualified');
    if (String(listRequest.method).toUpperCase() !== 'POST' || listRequest.url.origin !== location.origin ||
        !Array.isArray(listRequest.body?.[0]) || typeof listRequest.body[0][3] !== 'string' || !Array.isArray(listRequest.body?.[2])) failure('email_network_list_unqualified');
    const body=structuredClone(listRequest.body);
    listStage = 'request_shape';
    // The observed XHR is only a transport template. Bind the replay to the
    // requested receipt interval instead of trusting whatever search happened
    // to be open when the userscript was installed.
    body[0][3]=query;
    if(page>0 && !pageTokens.has(page))failure('email_network_list_unqualified');
    if(!Array.isArray(body[0][15]))failure('email_network_list_unqualified');
    body[0][15][13]=page===0?null:pageTokens.get(page);
    body[0][9]=page; body[0][7]=2000; body[2][0]=0; // Observed native pagination and full-response request.
    const response=await originalFetch.call(window,listRequest.url.href,{method:'POST',credentials:'same-origin',redirect:'error',headers:listRequest.headers,body:JSON.stringify(body),signal:AbortSignal.timeout(20000)});
    listStage = 'response';
    if ([401,403].includes(response.status)) failure('email_login_required');
    if (!response.ok) failure('email_network_read_failed');
    const reader=response.body.getReader(),chunks=[];let length=0;
    try {
      for (;;) {const {done,value}=await reader.read();if(done)break;length+=value.length;if(length>10<<20){void reader.cancel();failure('email_capture_limit');}chunks.push(value);}
    } finally {reader.releaseLock();}
    const bytes=new Uint8Array(length);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length;}
    try{value=JSON.parse(new TextDecoder().decode(bytes));}catch{failure('email_network_list_unqualified');}
    }
    checkedAccount(account_address);
    listStage = 'response_shape';
    if (!Array.isArray(value) || value?.[0] !== 0 || !Array.isArray(value?.[19]) || ![0,1].includes(value[3])) failure('email_network_list_unqualified');
    if(value[3]===1) {
      const token=value?.[13]?.[8];
      if(typeof token!=='string'||!token||token.length>8192||token!==value?.[13]?.[9])failure('email_network_list_unqualified');
      pageTokens.set(page+1,token);
    }
    const rows=config.parse(value), received=[];let unsupported=0;
    listStage = 'members';
    let rawCount=0;
    for(const batch of value[19]) {
      // Gmail's terminal empty batch is a four-field network envelope with a
      // null thread list. The network contract is the complete evidence source.
      if(value[3]===0&&value[19].length===1&&Array.isArray(batch)&&batch.length===4&&batch[1]===null) continue;
      if(!Array.isArray(batch?.[1]))failure('email_network_list_unqualified');
      for(const container of batch[1]) {
        if(!Array.isArray(container?.[0]?.[4]) || !container[0][4].length)failure('email_network_list_unqualified');
        rawCount+=container[0][4].filter(message=>{const labels=message?.[10];return !(Array.isArray(labels)&&labels.every(label=>typeof label==='string')&&(labels.includes('^r')||labels.includes('^f')));}).length;
      }
    }
    unsupported+=Math.max(0,rawCount-rows.length);
    for(const row of rows) {
      records.set(row.provider_message_id,row);
      if(row.draft||row.sent)continue;
      const at=instantNanos(row.received_at);
      if(at===null){unsupported++;continue;}
      if(at>=start&&at<end)received.push({...row});
    }
    while(records.size>2000)records.delete(records.keys().next().value);
    listStage = 'complete';
    return {provider:config.provider,account_address:account,rows:received,unsupported_rows:unsupported,has_next:value[3]===1,page};
  }
  async function markRead({account_address,provider_message_id} = {}) {
    checkedAccount(account_address);
    if (!idPattern.test(provider_message_id || '') || !records.has(provider_message_id)) failure('email_network_target_unobserved');
    const row=records.get(provider_message_id);
    if (row.unread === false) return {provider:config.provider,account_address:account,provider_message_id,read_state:'read'};
    const result=await config.markRead?.({account_address:account,provider_message_id,row,binding,request:listRequest,fetch:originalFetch});
    if (!result || result.provider_message_id !== provider_message_id || !['read','unknown'].includes(result.read_state)) failure('email_network_mark_read_unqualified');
    checkedAccount(account_address);
    if(result.read_state==='read')row.unread=false;
    return {provider:config.provider,account_address:account,provider_message_id,read_state:result.read_state};
  }
  function dispose() {
    nativeQueryReply=null;
    active = false;originalAbort?.abort();if(objectURL)URL.revokeObjectURL(objectURL);objectURL=null;originalBytes=null;originalMessageID=''; records.clear(); pageTokens.clear(); networkQuery = null; binding = null; listRequest = null; inbox = null; transport?.dispose(); anchor?.remove();
    if (XMLHttpRequest.prototype.open === open) XMLHttpRequest.prototype.open = originalOpen;
    if (XMLHttpRequest.prototype.send === send) XMLHttpRequest.prototype.send = originalSend;
    if (XMLHttpRequest.prototype.setRequestHeader === setHeader) XMLHttpRequest.prototype.setRequestHeader = originalSetHeader;
    if (window.fetch === fetch) window.fetch = originalFetch;
    delete window.SparkClawMailReader;
  }
  transport=config.installTransport?.({account:checkedAccount,getInbox:()=>inbox,receiveStartup(value){inbox=config.parseFolders?.(value)||null;},receiveRows(rows){
    checkedAccount();for(const row of rows){if(row && row.provider_selection_id===undefined)row.provider_selection_id=row.provider_thread_id||row.provider_message_id;if(idPattern.test(row.provider_message_id||''))records.set(row.provider_message_id,row);}
    while(records.size>2000)records.delete(records.keys().next().value);
  }});
  Object.defineProperty(window, 'SparkClawMailReader', {configurable:true, value:Object.freeze({
    armRangeSearch(request){checkedAccount(request.account_address);if(!transport?.armRangeSearch)failure('email_network_list_unqualified');return transport.armRangeSearch(request);},
    version:'0.2.0', provider:config.provider, diagnostics:()=>({original:{state:originalState,responseOrigin},list:{stage:listStage,template:Boolean(listRequest),body:Array.isArray(listRequest?.body),paging:Array.isArray(listRequest?.body?.[0]?.[15])},inbox:Boolean(inbox),records:records.size,transport:transport?.diagnostics?.()}), snapshot, resetRound,
    listPage:request=>inRound(listPage,request), prepareOriginal:request=>inRound(prepareOriginal,request), prepareRetainedOriginal:request=>inRound(prepareRetainedOriginal,request), markRead, dispose,
    verifyTarget({account_address,provider_message_id,provider_selection_id,folder}) {
      checkedAccount(account_address);
      const row=records.get(provider_message_id);
      return Boolean(row && !row.draft && !row.sent && row.provider_selection_id===provider_selection_id && (!folder||folder==='all'||row.folder===folder) && Number.isFinite(Date.parse(row.received_at)));
    },
  })});
})({"provider":"qq_mail","origins":["https://wx.mail.qq.com"],"account":()=>document.querySelector('.frame-header .profile-user-info .user-email')?.textContent?.trim()||'',"listURL":u=>u.pathname==='/list/maillist',"parse":value=>(parseQQMailList(value)?.rows??[]).map(row=>({...row,draft:false,sent:row.folder==='sent',grouped:/^(?:C|@)/.test(row.provider_message_id)})),"download":({id,row,binding})=>{if(!binding||row.grouped)return null;const u=new URL('/read/readmail',location.origin);u.searchParams.set('func','5');u.searchParams.set('mailid',id);u.searchParams.set('sid',binding.searchParams.get('sid'));return u;},"markRead":async function markQQMailRead({provider_message_id,row,binding,fetch:originalFetch}) {
  if (row?.provider_message_id !== provider_message_id || row.unread !== true ||
      !(binding instanceof URL) || binding.origin !== location.origin || binding.pathname !== '/list/maillist' ||
      binding.searchParams.get('func') !== '1' || !binding.searchParams.get('sid') || typeof originalFetch !== 'function') return null;
  const matches=[...document.querySelectorAll('.mail-list-page-item[data-mailid]')].filter(node=>
    node?.getAttribute?.('data-mailid')===provider_message_id && node.isConnected!==false && node.getClientRects?.().length>0);
  if(matches.length!==1||typeof matches[0].click!=='function')return null;
  matches[0].click();
  for(let attempt=0;attempt<20;attempt++){
    await new Promise(resolve=>setTimeout(resolve,250));
    let response,value;
    try{
      response=await originalFetch.call(window,binding.href,{method:'GET',credentials:'same-origin',redirect:'error',cache:'no-store',signal:AbortSignal.timeout(20000)});
      if(!response.ok||response.url&&new URL(response.url).origin!==location.origin)return {provider_message_id,read_state:'unknown'};
      const text=await response.text();
      if(text.length>10<<20)return {provider_message_id,read_state:'unknown'};
      value=JSON.parse(text);
    }catch{return {provider_message_id,read_state:'unknown'};}
    const parsed=parseQQMailList(value);
    const confirmed=parsed?.rows?.find(candidate=>candidate.provider_message_id===provider_message_id);
    if(confirmed?.unread===false)return {provider_message_id,read_state:'read'};
  }
  return {provider_message_id,read_state:'unknown'};
}});
})();

return {state:globalThis.SparkClawMailReader?.version==="0.2.0"?"ready":"unavailable"};}}),
Object.assign({"id":"1b57e83c-a914-48c1-a490-77d4bca191d5","file":"gmail-mail-reader.user.js","version":"0.2.0","sha256":"cbaac6e8fabb8679651753e6852986b70792bb0d34b5204046016df326f7068f","matches":[{"origin":"https://mail.google.com","pathPrefix":"/"}],"origins":["https://mail.google.com"],"runAt":"document-start","grants":["none"],"noframes":true},{install:function(){// ==UserScript==
// @name         SparkClaw Gmail Network Reader
// @namespace    sparkclaw.local
// @version      0.2.0
// @description  Read observed email sources in the signed-in page; SparkClaw owns synchronization and durable receipts.
// @match        https://mail.google.com/*
// @run-at       document-start
// @grant        none
// @noframes
// ==/UserScript==

// Generated by scripts/email/userscripts/build.mjs. Edit its source modules.
(()=>{
const installOutlookOriginalResolver=function installOutlookOriginalResolver({account,getInbox}) {
  let loading=null,active=true,stage='idle';
  const fail=()=>{throw Object.assign(new Error('email_network_original_unqualified'),{code:'email_network_original_unqualified'});};
  async function nativeModules() {
    if(!loading)loading=(async()=>{
      stage='runtime';
      let nativeRequire;
      if(!Array.isArray(window.webpackChunkOwa))fail();
      window.webpackChunkOwa.push([['sparkclaw_original_'+crypto.randomUUID()],{},require=>{nativeRequire=require;}]);
      if(typeof nativeRequire!=='function'||typeof nativeRequire.e!=='function')fail();
      // The observed native TriageActionImportExport dependency group. Loading
      // code has no mailbox mutation and is needed only for actual downloads.
      stage='modules';await Promise.all([21804,63436,73413,46866,32314,54709].map(id=>nativeRequire.e(id)));
      const build=nativeRequire(643446)?.V,mailbox=nativeRequire(129387)?.A,configuration=nativeRequire(859741)?.C;
      if(typeof build!=='function'||typeof mailbox!=='function'||typeof configuration!=='function')fail();
      return {build,mailbox,configuration};
    })();
    return loading;
  }
  async function prepare({account_address,provider_message_id}) {
    const owner=account(account_address),inbox=getInbox();
    if(!active||location.origin!=='https://outlook.live.com'||!inbox?.qualified||inbox.account!==owner||!/^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/.test(provider_message_id||''))fail();
    let timer;
    try{
      const {build,mailbox,configuration}=await Promise.race([nativeModules(),new Promise((_,reject)=>{timer=setTimeout(()=>reject(Object.assign(new Error('email_network_original_unqualified'),{code:'email_network_original_unqualified'})),20000);})]);
      const info=mailbox();
      stage='mailbox';
      if(!active||info?.type!=='UserMailbox')fail();
      // Resolve this exact native mailbox's configuration, not just the DOM
      // label: global settings and a selected shared mailbox can differ.
      if(String(configuration(info)?.SessionSettings?.UserEmailAddress||'').toLowerCase()!==owner)fail();
      account(owner);
      stage='url';
      const value=build(provider_message_id,'EML',info);
      if(typeof value!=='string')fail();
      const url=new URL(value);
      if(url.origin!=='https://attachment.outlook.live.net'||url.username||url.password||url.hash||
        !/^\/owa\/[^/]+\/service\.svc\/s\/DownloadMessage$/.test(url.pathname)||
        url.searchParams.get('id')!==provider_message_id||url.searchParams.get('outputFormat')!=='0'||!url.searchParams.get('token'))fail();
      account(owner);stage='ready';return url;
    }catch{fail();}finally{clearTimeout(timer);}
  }
  return {prepare,diagnostics:()=>({stage}),dispose(){active=false;loading=null;stage='disposed';}};
};
const installOutlookRangeTransport=function installOutlookRangeTransport({account,getInbox,receiveRows}) {
  const fail=code=>{throw Object.assign(new Error(code),{code});};
  const pattern=/^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/;
  function timestamp(value) {
    const match=typeof value==='string'&&/^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d{1,9}))?(Z|[+-]\d\d:\d\d)$/.exec(value);
    if(!match)return null;
    const wall=Date.parse(match[1]+'Z'),zone=match[3];
    if(!Number.isFinite(wall)||new Date(wall).toISOString().slice(0,19)!==match[1]||zone!=='Z'&&(Number(zone.slice(1,3))>23||Number(zone.slice(4))>59))return null;
    const seconds=Date.parse(match[1]+zone);
    if(!Number.isFinite(seconds))return null;
    return BigInt(seconds)*1000000n+BigInt((match[2]||'').padEnd(9,'0'));
  }
  function utc(ns) {
    let seconds=ns/1000000000n,fraction=ns%1000000000n;
    if(fraction<0){seconds--;fraction+=1000000000n;}
    const suffix=fraction.toString().padStart(9,'0').replace(/0+$/,'');
    return new Date(Number(seconds)*1000).toISOString().slice(0,19)+(suffix?'.'+suffix:'')+'Z';
  }
  let armed=null,active=true;
  function validate(request) {
    const owner=account(request.account_address),inbox=getInbox();
    const start=timestamp(request.interval_start),end=timestamp(request.interval_end);
    if(location.origin!=='https://outlook.live.com'||!inbox?.qualified||inbox.account!==owner||!inbox.folders?.length)fail('email_network_list_unqualified');
    if(start===null||end===null||start>=end||request.page!==0||request.provider_mode!=='time_range'||inbox.folders.length>100)fail('invalid_request');
    // Normalize RFC3339 offsets while preserving nanosecond boundaries.
    if(!inbox.folders.every(f=>pattern.test(f.id)))fail('email_network_list_unqualified');
    return {owner,start,end,folders:structuredClone(inbox.folders),query:`received>=${utc(start)} AND received<${utc(end)}`};
  }
  function arm(request) {
    if(!active||armed)fail('email_network_list_unqualified');
    const state=validate(request);
    let resolve,reject;
    const promise=new Promise((yes,no)=>{resolve=yes;reject=no;});
    promise.catch(()=>{}); // The trusted UI trigger precedes the consumer call.
    const abort=new AbortController();
    const timer=setTimeout(()=>{abort.abort();reject(Object.assign(new Error('email_network_read_failed'),{code:'email_network_read_failed'}));},20000);
    armed={...state,request:structuredClone(request),promise,resolve,reject,abort,timer,network:null};
    return {query:state.query};
  }
  async function decode(response) {
    if(!response.ok||new URL(response.url).origin!==location.origin)fail('email_network_read_failed');
    const reader=response.body.getReader(),chunks=[];let size=0;
    try{while(true){const {done,value}=await reader.read();if(done)break;size+=value.length;if(size>10<<20)fail('email_network_list_unqualified');chunks.push(value);}}
    finally{await reader.cancel().catch(()=>{});}
    const bytes=new Uint8Array(size);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length;}
    try{return JSON.parse(new TextDecoder().decode(bytes));}catch{fail('email_network_list_unqualified');}
  }
  function fetchSearch(args,next) {
    const state=armed;
    let body,url;
    try{url=new URL(typeof args[0]==='string'?args[0]:args[0]?.url,location.href);body=JSON.parse(args[1]?.body);}catch{return next(...args);}
    if(!active||!state||url.origin!==location.origin||url.pathname!=='/searchservice/api/v2/query'||body?.EntityRequests?.[0]?.Query?.QueryString!==state.query)return next(...args);
    if(state.network)return state.network.then(r=>r.clone());
    state.network=(async()=>{
      account(state.owner);
      if(args[1]?.method?.toUpperCase()!=='POST'||body.EntityRequests.length!==1||body.EntityRequests[0].ContentSources?.join(',')!=='Exchange')fail('email_network_list_unqualified');
      const entity=body.EntityRequests[0];
      entity.EntityType='Message';entity.From=0;entity.Size=50;
      entity.EnableTopResults=false;entity.TopResultsCount=0;entity.RefiningQueries=null;
      entity.Sort=[{Field:'Time',SortDirection:'Desc'}];
      entity.Filter={Or:state.folders.map(f=>({Term:{FolderId:f.id}}))};
      body.QueryAlterationOptions={...(body.QueryAlterationOptions||{}),EnableSuggestion:false,EnableAlteration:false};
      const response=await next(args[0],{...args[1],body:JSON.stringify(body),signal:state.abort.signal,redirect:'error'});
      const value=await decode(response.clone());account(state.owner);
      if(!active||armed!==state||state.abort.signal.aborted)fail('email_network_read_failed');
      state.resolve(value);clearTimeout(state.timer);return response;
    })().catch(cause=>{clearTimeout(state.timer);state.reject(cause);throw cause;});
    return state.network.then(r=>r.clone());
  }
  async function listPage(request) {
    const expected=validate(request),state=armed;
    if(!state||state.query!==expected.query||state.owner!==expected.owner||JSON.stringify(state.folders)!==JSON.stringify(expected.folders))fail('email_network_list_unqualified');
    const value=await state.promise;account(state.owner);
    if(!active||armed!==state||state.abort.signal.aborted)fail('email_network_read_failed');
    if(!value||typeof value!=='object'||Array.isArray(value))fail('email_network_list_unqualified');
    const entity=value?.EntitySets?.[0],result=entity?.ResultSets?.[0];
    if(value.EntitySets?.length!==1||entity.EntityType!=='Message'||entity.IsPartial!==false||entity.Properties?.HasParseException!==false||entity.ResultSets?.length!==1||
      !Number.isSafeInteger(result?.Total)||result.Total<0||typeof result.MoreResultsAvailable!=='boolean')fail('email_network_list_unqualified');
    const sources=result.Results??(result.Total===0?[]:null);
    if(!Array.isArray(sources)||sources.length>50||result.Total<sources.length||(!result.MoreResultsAvailable&&result.Total!==sources.length))fail('email_network_list_unqualified');
    const rows=[],ids=new Set(),folders=new Map(state.folders.map(f=>[f.id,f]));let unsupported=0;
    for(const item of sources){
      const node=item?.Source,id=typeof node?.ImmutableId==='string'?node.ImmutableId.replace(/_/g,'+').replace(/-/g,'/'):null;
      const thread=node?.ConversationId?.Id,folder=folders.get(node?.ParentFolderId?.Id),received=timestamp(node?.DateTimeReceived);
      // The native Message adapter uses this immutable EWS form. Ordinary
      // ItemId can change on a folder move and must not own a second source.
      if(item.Type!=='Message'||!pattern.test(id||'')||!pattern.test(node?.ItemId?.Id||'')||!pattern.test(thread||'')||ids.has(id)||!folder||typeof node.IsDraft!=='boolean'||typeof node.IsRead!=='boolean'||received===null||received<state.start||received>=state.end){unsupported++;continue;}
      ids.add(id);if(node.IsDraft)continue;
      rows.push({provider_message_id:id,provider_selection_id:thread,provider_thread_id:thread,received_at:node.DateTimeReceived,draft:false,unread:!node.IsRead,sent:false,inbox:folder.inbox,folder:folder.inbox?'inbox':'outlook:'+folder.id});
    }
    receiveRows(rows);
    const digest=await crypto.subtle.digest('SHA-256',new TextEncoder().encode(JSON.stringify(state.folders.map(f=>f.id))));
    if(!active||armed!==state||state.abort.signal.aborted)fail('email_network_read_failed');
    return {provider:'outlook',account_address:state.owner,rows,unsupported_rows:unsupported,has_next:result.MoreResultsAvailable,page:0,scope:'inbound_received',folder_scope_id:Array.from(new Uint8Array(digest),v=>v.toString(16).padStart(2,'0')).join('')};
  }
  function resetRound(){if(armed){clearTimeout(armed.timer);armed.abort.abort();armed.reject(Object.assign(new Error('email_network_read_failed'),{code:'email_network_read_failed'}));}armed=null;}
  function dispose(){active=false;resetRound();}
  return {arm,listPage,fetchSearch,resetRound,dispose};
};
const installOutlookEarlyBridge=function installOutlookEarlyBridge() {
  if(window.top!==window || !['https://outlook.live.com','https://outlook.office.com','https://outlook.office365.com'].includes(location.origin))return null;
  if(window.SparkClawOutlookEarlyBridge)return window.SparkClawOutlookEarlyBridge;
  const NativeWorker=window.Worker,NativeChannel=window.MessageChannel,originalFetch=window.fetch;
  if(!NativeWorker||!NativeChannel)return null;
  const queue=[],listeners=[];let consumer=null,startup=null,active=true;
  const observe=(worker,message)=>{
    const operation=message?.argumentList?.[0]?.value?.operationName;
    if(!['ItemRows','ConversationRows','ItemExport'].includes(operation))return;
    const item={worker,message:structuredClone(message)};
    if(consumer)consumer.request(item.worker,item.message);
    else {queue.push(item);if(queue.length>40)queue.shift();}
  };
  const Worker=class extends NativeWorker {postMessage(message,...rest){try{observe(this,message);}catch{}return super.postMessage(message,...rest);}};
  const Channel=class extends NativeChannel {constructor(){super();for(const port of [this.port1,this.port2]){
    const listener=event=>consumer?.result(event,port);port.addEventListener('message',listener);listeners.push([port,listener]);
  }}};
  window.Worker=Worker;window.MessageChannel=Channel;
  const fetch=async function(...args){
    const next=(...values)=>originalFetch.apply(this,values);
    const response=await (consumer?.fetchSearch?consumer.fetchSearch(args,next):next(...args));
    try{
      const url=new URL(response.url);
      if(active&&response.ok&&url.origin===location.origin&&/^\/owa\/\d+\/startupdata\.ashx$/.test(url.pathname))void(async()=>{
        const reader=response.clone().body.getReader(),chunks=[];let size=0;
        try{for(;;){const {done,value}=await reader.read();if(done)break;size+=value.length;if(size>10<<20){void reader.cancel();return;}chunks.push(value);}}
        finally{reader.releaseLock();}
        const bytes=new Uint8Array(size);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length;}
        const value=JSON.parse(new TextDecoder().decode(bytes));if(!active)return;
        // Retain hierarchy evidence only, never session secrets or mail bodies.
        startup={owaUserConfig:{SessionSettings:{UserEmailAddress:value?.owaUserConfig?.SessionSettings?.UserEmailAddress}},findFolders:value.findFolders,findConversation:{Body:{FolderId:value?.findConversation?.Body?.FolderId}}};
        consumer?.startup?.(startup);
      })().catch(()=>{});
    }catch{}
    return response;
  };
  window.fetch=fetch;
  const bridge=Object.freeze({
    attach(next){if(!active)return;consumer=next;for(const item of queue.splice(0))consumer.request(item.worker,item.message);if(startup)consumer.startup?.(startup);},
    detach(next){if(consumer===next)consumer=null;},
    dispose(){
      active=false;consumer=null;startup=null;queue.length=0;
      for(const [port,listener]of listeners)port.removeEventListener('message',listener);listeners.length=0;
      if(window.Worker===Worker)window.Worker=NativeWorker;
      if(window.MessageChannel===Channel)window.MessageChannel=NativeChannel;
      if(window.fetch===fetch)window.fetch=originalFetch;
      delete window.SparkClawOutlookEarlyBridge;
    },
  });
  Object.defineProperty(window,'SparkClawOutlookEarlyBridge',{configurable:true,value:bridge});
  return bridge;
};
const parseGmailList=function parseGmailList(value, includeThreads = false) {
  const batches = value?.[19];
  if (!Array.isArray(batches)) return [];
  const result = [];
  for (const batch of batches) {
    if (!Array.isArray(batch?.[1])) continue;
    for (const container of batch[1]) {
      const thread = container?.[0];
      if (!Array.isArray(thread) || !/^(?:thread-f:\d+|thread-a:r-?\d+)$/u.test(thread[3]) || !Array.isArray(thread[4])) continue;
      const messages = thread[4];
      if ((!includeThreads && messages.length !== 1) || messages.length < 1 || messages.length > 1000) continue;
      const members=[];
      for (const message of messages) {
      const id = message?.[55], labels = message?.[10];
      if (!/^(?:msg-f:\d+|msg-a:r-?\d+)$/u.test(message?.[0]) || !/^[a-f0-9]{1,32}$/u.test(id) ||
          !Array.isArray(labels) || !labels.every(label => typeof label === 'string')) continue;
      if (thread[3].startsWith('thread-f:')) {
        if (!message[0].startsWith('msg-f:') || BigInt(`0x${id}`).toString() !== message[0].slice(6) || !includeThreads && thread[3].slice(9) !== message[0].slice(6)) continue;
      } else if (!message[0].startsWith('msg-a:')) continue;
      members.push({ provider_message_id: id, provider_thread_id: thread[3],
        unread: labels.includes('^u'), inbox: labels.includes('^i'), draft: labels.includes('^r'),
        ...(includeThreads ? {sent:labels.includes('^f'),observed_message_count:messages.length,
          // Observed internal receipt timestamp: the pinned original's Received
          // trace agrees at the second, and RFC Date is independently earlier.
          ...(Number.isSafeInteger(message[6]) && message[6]>=946684800000 && message[6]<=Date.now()+300000 ? {received_at:new Date(message[6]).toISOString()} : {})} : {}) });
      }
      if(members.length===messages.length && new Set(members.map(member=>member.provider_message_id)).size===members.length) result.push(...members);
    }
  }
  return result;
};
const parseOutlookList=function parseOutlookList(value, includeInventory = false) {
  const rows=[];
  let visited=0;
  const visit=(node,depth)=>{
    if(!node || typeof node!=='object' || depth>20 || ++visited>20000) return;
    if(Array.isArray(node.Conversations)) for(const item of node.Conversations.slice(0,100)) {
      const selection=item?.ConversationId?.Id, id=item?.ItemIds?.[0]?.Id;
      if(typeof selection!=='string' || !selection)continue;
      let inventory = includeInventory ? {last_delivery_time:typeof item.LastDeliveryTime==='string' && Number.isFinite(Date.parse(item.LastDeliveryTime)) ? item.LastDeliveryTime : null} : {};
      if (includeInventory && Number.isInteger(item.GlobalMessageCount) && item.GlobalMessageCount >= 1 && item.GlobalMessageCount <= 1000 &&
          Array.isArray(item.GlobalItemIds) && item.GlobalItemIds.length === item.GlobalMessageCount && Array.isArray(item.ItemIds) &&
          item.ItemIds.length === item.MessageCount && Array.isArray(item.DraftItemIds)) {
        const globalIDs = item.GlobalItemIds.map(value=>value?.Id), localIDs = item.ItemIds.map(value=>value?.Id), drafts = item.DraftItemIds.map(value=>value?.Id);
        if (globalIDs.every(value=>typeof value==='string' && /^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/u.test(value)) && new Set(globalIDs).size === globalIDs.length &&
            localIDs.every(value=>globalIDs.includes(value)) && drafts.every(value=>globalIDs.includes(value)) && new Set(drafts).size === drafts.length) {
          inventory = {members:globalIDs.map(value=>({provider_message_id:value,local:localIDs.includes(value),draft:drafts.includes(value)})),
            inventory_complete:true,global_unread_count:item.GlobalUnreadCount,
            last_delivery_time: typeof item.LastDeliveryTime==='string' && Number.isFinite(Date.parse(item.LastDeliveryTime)) ? item.LastDeliveryTime : null};
        }
      }
      if(typeof id!=='string' || !id || selection===id ||
          item.MessageCount!==1 || item.GlobalMessageCount!==1 || item.ItemIds?.length!==1 ||
          item.GlobalItemIds?.length!==1 || item.GlobalItemIds[0]?.Id!==id ||
          ![0,1].includes(item.UnreadCount) || item.GlobalUnreadCount!==item.UnreadCount) {
        rows.push({provider_selection_id:selection,provider_message_id:null,unread:null,...inventory});continue;
      }
      rows.push({provider_selection_id:selection,provider_message_id:id,unread:item.UnreadCount===1,...inventory});
    }
    for(const child of Object.values(node))if(child&&typeof child==='object')visit(child,depth+1);
  };
  visit(value,0);return rows;
};
const parseQQMailList=function parseQQMailList(value) {
  if(value?.head?.ret!==0 || !Number.isInteger(value.head.time) || !Array.isArray(value.body?.list) || !Number.isInteger(value.body.total_num) || value.body.total_num<0)return null;
  const rows=[];
  for(const item of value.body.list.slice(0,2000)){
    if(typeof item?.emailid!=='string' || !/^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/u.test(item.emailid) || !Number.isInteger(item.dirid) || item.dirid<1 ||
      !Number.isInteger(item.totime) || item.totime<946684800 || item.totime>value.head.time+300 || item.unread!==undefined && item.unread!==0 && item.unread!==1)continue;
    rows.push({provider_message_id:item.emailid,provider_selection_id:item.emailid,provider_thread_id:item.emailid,
      folder:item.dirid===1?'inbox':item.dirid===3?'sent':`qq:${item.dirid}`,unread:item.unread===1,
      received_at:new Date(item.totime*1000).toISOString()});
  }
  return {rows,total_count:value.body.total_num,unsupported_rows:value.body.list.length-rows.length};
};
(function installReader(config) {
  'use strict';
  if (window.top !== window || !config.origins.includes(location.origin)) return;
  window.SparkClawMailReader?.dispose();
  const originalFetch = window.fetch, originalOpen = XMLHttpRequest.prototype.open;
  const originalSend = XMLHttpRequest.prototype.send, originalSetHeader = XMLHttpRequest.prototype.setRequestHeader;
  const records = new Map(), pageTokens = new Map();
  let listRequest = null, networkQuery = null, inbox = null, transport = null, objectURL = null, originalAbort = null;
  let originalBytes = null, originalMessageID = '';
  let originalState = 'unlearned', responseOrigin = '';
  let listStage = 'idle';
  let nativeQueryReply = null;
  let active = true, account = '', binding = null, anchor = null;
  let pendingOperations = 0;
  const idPattern = /^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/;
  const failure = code => { throw Object.assign(new Error(code), {code}); };
  async function inRound(operation, request) {
    if (!active) failure('email_network_capability_unavailable');
    pendingOperations++;
    try { return await operation(request); } finally { pendingOperations--; }
  }
  // Only the Controller's idle mailbox lease may reset a round. Keep learned
  // transport/session templates, but never retain source bytes or query results
  // across polls. This performs no network request and does not retry failures.
  function resetRound({account_address} = {}) {
    if (!active || pendingOperations || originalAbort) failure('email_network_list_unqualified');
    checkedAccount(account_address);
    if(config.provider==='outlook' && typeof transport?.resetRound!=='function') failure('email_network_capability_unavailable');
    transport?.resetRound?.();
    records.clear(); pageTokens.clear(); networkQuery=null; nativeQueryReply=null;
    anchor?.remove(); anchor=null;
    if(objectURL)URL.revokeObjectURL(objectURL);
    objectURL=null; originalBytes=null; originalMessageID='';
    originalState='unlearned'; responseOrigin=''; listStage='idle';
    return {provider:config.provider,account_address:account};
  }
  // Gateway checkpoints carry micro/nanoseconds; Date.parse alone truncates
  // those bounds and can falsely certify a missed endpoint millisecond.
  function instantNanos(value) {
    const match=typeof value==='string'&&/^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d{1,9}))?(Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$/.exec(value);
    if(!match)return null;
    const seconds=Date.parse(match[1]+match[3]);
    const offset=match[3]==='Z'?0:(match[3][0]==='-'?-1:1)*(Number(match[3].slice(1,3))*60+Number(match[3].slice(4,6)));
    if(!Number.isFinite(seconds)||new Date(seconds+offset*60000).toISOString().slice(0,19)!==match[1])return null;
    return BigInt(seconds)*1000000n+BigInt((match[2]||'').padEnd(9,'0'));
  }
  const inlineOriginalLimit = 1 << 20;
  const base64 = bytes => {
    let binary = '';
    for (let offset = 0; offset < bytes.length; offset += 0x8000) {
      binary += String.fromCharCode(...bytes.subarray(offset, offset + 0x8000));
    }
    return btoa(binary);
  };
  function checkedAccount(expected) {
    const current = config.account();
    if (!current) failure('email_account_identity_unavailable');
    if (account && account !== current.toLowerCase()) {
      records.clear(); pageTokens.clear(); binding = null; listRequest = null; networkQuery = null; nativeQueryReply = null; inbox = null; transport?.dispose();
      failure('email_account_identity_mismatch');
    }
    account = current.toLowerCase();
    if (expected && expected.toLowerCase() !== account) failure('email_account_identity_mismatch');
    return account;
  }
  function observe(url, text, requestQuery) {
    if (!active || typeof text !== 'string' || text.length > 10 << 20) return;
    try {
      const u = new URL(url, location.href);
      if (u.origin !== location.origin || !config.listURL(u)) return;
      const value = JSON.parse(text);
      if(config.provider==='gmail' && typeof requestQuery==='string') nativeQueryReply={query:requestQuery,value};
      if(config.provider==='outlook' && u.pathname.endsWith('/startupdata.ashx')) {
        const owner=value.owaUserConfig?.SessionSettings?.UserEmailAddress, id=value.findConversation?.Body?.FolderId?.Id;
        if(typeof owner==='string' && typeof id==='string')inbox=config.parseFolders?.(value)||{account:owner.toLowerCase(),id,qualified:false};
      }
      const rows = config.parse(value);
      if (!rows) return;
      checkedAccount();
      for (const row of rows) {
        if (row && row.provider_selection_id === undefined) row.provider_selection_id = row.provider_thread_id || row.provider_message_id;
        if (!idPattern.test(row.provider_message_id || '')) continue;
        records.set(row.provider_message_id, row);
      }
      while (records.size > 2000) records.delete(records.keys().next().value);
      if (config.provider === 'qq_mail' && u.searchParams.get('func') === '1' && u.searchParams.get('sid')) binding = u;
    } catch { /* Unqualified responses never become admitted source evidence. */ }
  }
  function rememberListRequest(request, body) {
    try {
      const url = new URL(request.url, location.href);
      if (url.origin !== location.origin || !config.listURL(url)) return;
      listRequest = {method: request.method, url, headers: {...(request.headers || {})}, ...(body === undefined ? {} : {body})};
    } catch {}
  }
  function qqListSource() {
    const source = listRequest || binding;
    if (source) return new URL(source.url || source, location.href);
    // The current QQ client can restore /home/index from its own cache without
    // issuing /list/maillist again. Its signed-in route still carries the same
    // first-party session id required by /list/search, so construct only the
    // fixed list binding instead of depending on an incidental resource entry.
    try {
      const current = new URL(location.href);
      const sid = current.origin===location.origin && current.pathname==='/home/index'
        ? current.searchParams.get('sid') : '';
      if(sid && sid.length<=4096) {
        const url=new URL('/list/maillist',location.origin);
        url.searchParams.set('func','1');url.searchParams.set('sid',sid);
        return url;
      }
    } catch {}
    // QQ can load its first list before the userscript's fetch/XHR hooks run.
    const resources = performance.getEntriesByType('resource');
    for (let index = resources.length - 1; index >= 0; index--) {
      try {
        const url = new URL(resources[index].name);
        if (url.origin === location.origin && url.pathname === '/list/maillist' &&
            url.searchParams.get('func') === '1' && url.searchParams.get('sid')) return url;
      } catch {}
    }
    return null;
  }
  const open = function(method, url, ...args) {
    this.__sparkclawMailURL = url;
    this.__sparkclawMailRequest = {method, url, headers:{}};
    return originalOpen.call(this, method, url, ...args);
  };
  const setHeader = function(key, value) {
    if (this.__sparkclawMailRequest) this.__sparkclawMailRequest.headers[key.toLowerCase()] = value;
    return originalSetHeader.call(this,key,value);
  };
  const send = function(...args) {
    try {
      const request = this.__sparkclawMailRequest;
      const body = args[0] === undefined ? undefined : JSON.parse(args[0]);
      if (request) {
        const previous = listRequest?.body?.[0]?.[3];
        rememberListRequest(request, body);
        if (previous && body?.[0]?.[3] !== previous) pageTokens.clear();
      }
    } catch {}
    const url = this.__sparkclawMailURL;
    let requestQuery;
    try { requestQuery=JSON.parse(args[0])?.[0]?.[3]; } catch {}
    this.addEventListener('load', () => {
      if (this.status === 200 && (!this.responseType || this.responseType === 'text')) observe(url, this.responseText, requestQuery);
    }, {once:true});
    return originalSend.apply(this, args);
  };
  const fetch = async function(...args) {
    const response = await originalFetch.apply(this, args);
    try {
      const u = new URL(response.url);
      if (u.origin === location.origin && config.listURL(u)) {
        if (!listRequest) rememberListRequest({method:String(args[1]?.method || 'GET').toUpperCase(), url:u, headers:args[1]?.headers || {}}, args[1]?.body ? JSON.parse(args[1].body) : undefined);
      }
      if (active && response.ok && u.origin === location.origin && config.listURL(u) && Number(response.headers.get('content-length')) <= 10 << 20) {
        void (async () => {
          const copy = response.clone(), reader = copy.body.getReader(), chunks = []; let size = 0;
          try {
            for (;;) { const {done,value} = await reader.read(); if (done) break;
              size += value.length; if (size > 10 << 20) { void reader.cancel(); return; } chunks.push(value); }
            const bytes = new Uint8Array(size); let offset = 0;
            for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.length; }
            let requestQuery;try{requestQuery=JSON.parse(args[1]?.body)?.[0]?.[3];}catch{}
            observe(response.url, new TextDecoder().decode(bytes),requestQuery);
          } finally { reader.releaseLock(); }
        })().catch(()=>{});
      }
    } catch {}
    return response;
  };
  XMLHttpRequest.prototype.open = open; XMLHttpRequest.prototype.send = send; XMLHttpRequest.prototype.setRequestHeader = setHeader;
  window.fetch = fetch;
  function snapshot({account_address, interval_start, interval_end} = {}) {
    checkedAccount(account_address);
    const start = instantNanos(interval_start), end = instantNanos(interval_end);
    if (start===null || end===null || start >= end) failure('invalid_request');
    let unsupported = 0;
    const rows = [];
    for (const row of records.values()) {
      if (row.draft || row.sent) continue;
      if (row.grouped) { unsupported++; continue; }
      const received = instantNanos(row.received_at);
      if (received===null) { unsupported++; continue; }
      if (received >= start && received < end) rows.push({...row});
    }
    return {provider:config.provider, account_address:account, rows, unsupported_rows:unsupported,
      scan_complete:false, reason:'folder_scope_and_pagination_unqualified'};
  }
  async function prepareRetainedOriginal(target = {}) {
    const {account_address,provider_message_id,provider_selection_id,provider_native_id,folder}=target;
    checkedAccount(account_address);
    if(!idPattern.test(provider_message_id||'')||!idPattern.test(provider_selection_id||'')||folder==='sent')failure('invalid_request');
    // These IDs came from a previous qualified list and were retained by the
    // Gateway. Reuse the exact original endpoint without searching the inbox.
    const row={provider_message_id,provider_selection_id,native_message_id:provider_native_id,folder,draft:false,sent:false};
    if(config.provider==='gmail') {
      if(!/^(?:msg-f:\d+|msg-a:r-?\d+)$/u.test(provider_native_id||''))failure('email_network_target_unobserved');
      if(provider_native_id.startsWith('msg-f:')&&(!/^[a-f0-9]{1,32}$/u.test(provider_message_id)||BigInt(`0x${provider_message_id}`).toString()!==provider_native_id.slice(6)))failure('email_network_target_unobserved');
    }
    if(config.provider==='qq_mail') {
      if(/^(?:C|@)/u.test(provider_message_id))failure('email_network_target_unobserved');
      binding=qqListSource();
      if(!binding)failure('email_network_original_unqualified');
    }
    return prepareOriginal(target,row);
  }
  async function prepareOriginal({account_address, provider_message_id} = {}, retainedRow = null) {
    checkedAccount(account_address);
    if (!idPattern.test(provider_message_id || '')) failure('invalid_request');
    const row = retainedRow || records.get(provider_message_id);
    if (!row || row.draft || row.grouped) failure('email_network_target_unobserved');
    let url = await config.download?.({id:provider_message_id, row, binding, request:listRequest});
    if (!url && config.provider==='outlook' && transport?.prepareOriginal) url = await transport.prepareOriginal({account_address:account,provider_message_id});
    if (!url) failure('email_network_original_unqualified');
    try {
      url = new URL(url, location.href);
      if (url.protocol !== 'https:' || url.username || url.password ||
          (url.origin !== location.origin && !(config.provider === 'gmail' && url.origin === 'https://mail-attachment.googleusercontent.com') &&
            !(config.provider === 'outlook' && url.origin === 'https://attachment.outlook.live.net'))) failure('email_network_original_unqualified');
    } catch (error) { failure(error.code || 'email_network_original_unqualified'); }
    if(originalAbort)failure('email_network_original_unqualified');
    originalState='fetching';originalAbort=new AbortController();
    return (async()=>{
      try {
        const response=await originalFetch.call(window,url.href,{credentials:url.origin===location.origin?'same-origin':'omit',redirect:config.provider==='gmail'?'follow':'error',cache:'no-store',signal:AbortSignal.any([originalAbort.signal,AbortSignal.timeout(20000)])});
        originalState='http_'+response.status;responseOrigin=response.url?new URL(response.url).origin:'';if(response.url&&responseOrigin!==url.origin&&!(config.provider==='gmail'&&responseOrigin==='https://mail-attachment.googleusercontent.com'))failure('email_network_original_unqualified');if(!response.ok)failure('email_network_original_unqualified');
        const reader=response.body.getReader(),chunks=[];let length=0;
        try {
          for(;;){const {done,value}=await reader.read();if(done)break;length+=value.length;if(length>110<<20){void reader.cancel();failure('email_capture_limit');}chunks.push(value);}
        }finally{reader.releaseLock();}
        checkedAccount(account_address);
        if(!active)failure('email_network_original_unqualified');
        const bytes=new Uint8Array(length);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length;}
        const header=new TextDecoder().decode(bytes.subarray(0,Math.min(bytes.length,65536)));
        const headerEnd=header.search(/\r?\n\r?\n/);
        const headerBlock=headerEnd<0?'':header.slice(0,headerEnd);
        let precedingField=false;
        const validHeaders=headerBlock.split(/\r?\n/).every(line=>{
          if(/^[ \t]/.test(line))return precedingField;
          precedingField=/^[!-9;-~]+:/.test(line);
          return precedingField;
        });
        // A provider's HTML "show original" page may embed From: and a blank
        // line much later. It is an adapter response error, not a bad email and
        // must never consume a mail-specific retry allowance.
        if(headerEnd<0||!validHeaders||!/^From:/im.test(headerBlock))failure('email_network_original_unqualified');
        anchor?.remove();if(objectURL)URL.revokeObjectURL(objectURL);
        originalBytes = bytes;
        originalMessageID = provider_message_id;
        objectURL=URL.createObjectURL(new Blob([bytes],{type:'message/rfc822'}));
        anchor=document.createElement('a');anchor.id='sparkclaw-mail-original';anchor.href=objectURL;
        anchor.download='message.eml';anchor.textContent='SparkClaw EML';anchor.hidden=true;document.body.append(anchor);
        originalState='ready';
        const result={selector:'#sparkclaw-mail-original',account_address:account,provider_message_id,bytes:bytes.length};
        if(bytes.length<=inlineOriginalLimit) { result.inline_bytes=bytes.length; result.inline_base64=base64(bytes); }
        return result;
      }catch(error){
        if(['email_account_identity_mismatch','email_account_identity_unavailable','email_capture_limit'].includes(error.code))throw error;
        if(originalState==='fetching')originalState='fetch_failed';failure('email_network_original_unqualified');
      }finally{originalAbort=null;}
    })();
  }

  async function listPage({account_address,interval_start,interval_end,page=0,folder='inbox',provider_mode}) {
    checkedAccount(account_address);
    if(config.provider==='outlook' && transport?.listPage)return transport.listPage({account_address,interval_start,interval_end,page,folder,provider_mode});
    if (config.provider === 'qq_mail') {
      listStage='qq_source';
      const start=instantNanos(interval_start), end=instantNanos(interval_end);
      if (start===null||end===null||start>=end||!Number.isInteger(page)||page<0||page>128) failure('invalid_request');
      const deadline=Date.now()+5000;
      let url=qqListSource();
      while(!url && Date.now()<deadline) {
        await new Promise(resolve=>setTimeout(resolve,100));
        url=qqListSource();
      }
      if (!url || url.origin!==location.origin || url.pathname!=='/list/maillist' || url.searchParams.get('func') !== '1' || !url.searchParams.get('sid')) failure('email_network_list_unqualified');
      if(provider_mode==='time_range') {
        if(page!==0)failure('email_incremental_unqualified');
        // QQ's current public web client uses /list/search with epoch-second
        // after/before parameters. No mailbox-head pagination participates.
        const search=new URL('/list/search',location.origin);
        search.searchParams.set('sid',url.searchParams.get('sid'));
        const body=new URLSearchParams({page_now:'0',page_size:'50',after:String(start/1000000000n-1n),before:String((end+999999999n)/1000000000n),sort_type:'1',sort_direction:'1'});
        listStage='qq_request';
        const response=await originalFetch.call(window,search.href,{method:'POST',credentials:'same-origin',redirect:'error',headers:{'content-type':'application/x-www-form-urlencoded'},body,signal:AbortSignal.timeout(20000)});
        if([401,403].includes(response.status))failure('email_login_required');if(!response.ok)failure('email_network_read_failed');
        const reader=response.body.getReader(),chunks=[];let length=0;
        try {for(;;){const {done,value}=await reader.read();if(done)break;length+=value.length;if(length>10<<20){void reader.cancel();failure('email_capture_limit');}chunks.push(value);}}finally{reader.releaseLock();}
        const bytes=new Uint8Array(length);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length;}
        listStage='qq_json';
        let value;try{value=JSON.parse(new TextDecoder().decode(bytes));}catch{failure('email_network_list_unqualified');}
        checkedAccount(account_address);
        listStage='qq_head';if(value?.head?.ret!==0)failure('email_network_list_unqualified');
        listStage='qq_total';if(!Number.isInteger(value.body?.total_num)||value.body.total_num<0)failure('email_network_list_unqualified');
        listStage='qq_lock';if(!Number.isInteger(value.body.lock_num)||value.body.lock_num<0)failure('email_network_list_unqualified');
        if(value.body.total_num===0&&value.body.list===undefined)value.body.list=[];
        listStage='qq_list';if(!Array.isArray(value.body.list)||value.body.list.length>50)failure('email_network_list_unqualified');
        const parsed=config.parse(value),rows=[];
        let unsupported=value.body.list.length-parsed.length+value.body.lock_num;
        for(const row of parsed){
          if(row.grouped){unsupported++;continue;}
          if(row.folder!=='inbox'&&!/^qq:[1-9][0-9]{3,9}$/.test(row.folder))continue;
          const at=instantNanos(row.received_at);if(at===null){unsupported++;continue;}
          if(at>=start&&at<end){records.set(row.provider_message_id,row);rows.push(row);}
        }
        binding=url;
        listStage='qq_complete';
        return {provider:config.provider,account_address:account,rows,unsupported_rows:unsupported,has_next:value.body.total_num>value.body.list.length,page:0,scope:'inbound_received',folder_scope_id:'search_inbound_v1'};
      }
      const dir=folder==='inbox'?1:/^qq:[1-9][0-9]{3,9}$/u.test(folder)?Number(folder.slice(3)):folder==='sent'?3:null;
      if (!Number.isInteger(dir)||dir<1) failure('email_network_list_unqualified');
      url.searchParams.set('func','1');url.searchParams.set('dir',String(dir));url.searchParams.set('dirid',String(dir));url.searchParams.set('page_now',String(page));url.searchParams.set('page_size','50');
      const response=await originalFetch.call(window,url.href,{method:'GET',credentials:'same-origin',redirect:'error',cache:'no-store',signal:AbortSignal.timeout(20000)});
      if([401,403].includes(response.status))failure('email_login_required');if(!response.ok)failure('email_network_read_failed');
      let value;try{value=await response.json();}catch{failure('email_network_list_unqualified');}
      checkedAccount(account_address);
      if(value?.head?.ret!==0||!Array.isArray(value.body?.list)||!Number.isInteger(value.body.total_num)||value.body.total_num<0)failure('email_network_list_unqualified');
      binding=url;
      const rows=config.parse(value).filter(row=>row.folder===(folder==='inbox'?'inbox':folder));
      const received=rows.filter(row=>{const at=instantNanos(row.received_at);return at!==null&&at>=start&&at<end;});
      const unsupportedRows=Math.max(0,value.body.list.length-rows.length);
      const receiptTimes=rows.map(row=>instantNanos(row.received_at));
      const receiptOrdered=receiptTimes.length>0 && receiptTimes.every((at,index)=>at!==null&&(index===0||receiptTimes[index-1]>=at));
      // QQ's native inbox page is ordered by `totime` descending. Once a fully
      // qualified page crosses the requested lower bound, every later page is
      // older and scanning the rest of mailbox history would add no evidence.
      // Any invalid row or ordering violation keeps pagination fail-closed.
      const boundaryReached=unsupportedRows===0&&receiptOrdered&&receiptTimes.at(-1)<start;
      const hasNext=!boundaryReached&&(page+1)*50<value.body.total_num;
      for(const row of rows)records.set(row.provider_message_id,row);
      return {provider:config.provider,account_address:account,rows:received,unsupported_rows:unsupportedRows,has_next:hasNext,next_page:hasNext?page+1:undefined,page,scope:'inbound_received',folder_scope_id:String(dir)};
    }
    if (config.provider !== 'gmail') failure('email_network_list_unqualified');
    listStage = 'template';
    const start=instantNanos(interval_start),end=instantNanos(interval_end);
    if (start===null||end===null||start>=end||!Number.isInteger(page)||page<0||page>10000) failure('invalid_request');
    const query=`-in:trash -in:spam -in:drafts after:${start/1000000000n-1n} before:${(end+999999999n)/1000000000n}`;
    if (networkQuery && networkQuery !== query) failure('email_network_list_unqualified');
    networkQuery = query;
    let value;
    const usableTemplate=String(listRequest?.method).toUpperCase()==='POST' && Array.isArray(listRequest?.body?.[0]) && typeof listRequest.body[0][3]==='string' && Array.isArray(listRequest.body[2]) && Array.isArray(listRequest.body[0][15]);
    if(!usableTemplate && page===0 && typeof config.search==='function') {
      listStage='native_query';
      config.search(query);
      const deadline=Date.now()+20000;
      while(active && nativeQueryReply?.query!==query && Date.now()<deadline) await new Promise(resolve=>setTimeout(resolve,100));
      if(!active||nativeQueryReply?.query!==query)failure('email_network_list_unqualified');
      value=nativeQueryReply.value;
    } else {
    if (!listRequest) failure('email_network_list_unqualified');
    if (String(listRequest.method).toUpperCase() !== 'POST' || listRequest.url.origin !== location.origin ||
        !Array.isArray(listRequest.body?.[0]) || typeof listRequest.body[0][3] !== 'string' || !Array.isArray(listRequest.body?.[2])) failure('email_network_list_unqualified');
    const body=structuredClone(listRequest.body);
    listStage = 'request_shape';
    // The observed XHR is only a transport template. Bind the replay to the
    // requested receipt interval instead of trusting whatever search happened
    // to be open when the userscript was installed.
    body[0][3]=query;
    if(page>0 && !pageTokens.has(page))failure('email_network_list_unqualified');
    if(!Array.isArray(body[0][15]))failure('email_network_list_unqualified');
    body[0][15][13]=page===0?null:pageTokens.get(page);
    body[0][9]=page; body[0][7]=2000; body[2][0]=0; // Observed native pagination and full-response request.
    const response=await originalFetch.call(window,listRequest.url.href,{method:'POST',credentials:'same-origin',redirect:'error',headers:listRequest.headers,body:JSON.stringify(body),signal:AbortSignal.timeout(20000)});
    listStage = 'response';
    if ([401,403].includes(response.status)) failure('email_login_required');
    if (!response.ok) failure('email_network_read_failed');
    const reader=response.body.getReader(),chunks=[];let length=0;
    try {
      for (;;) {const {done,value}=await reader.read();if(done)break;length+=value.length;if(length>10<<20){void reader.cancel();failure('email_capture_limit');}chunks.push(value);}
    } finally {reader.releaseLock();}
    const bytes=new Uint8Array(length);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length;}
    try{value=JSON.parse(new TextDecoder().decode(bytes));}catch{failure('email_network_list_unqualified');}
    }
    checkedAccount(account_address);
    listStage = 'response_shape';
    if (!Array.isArray(value) || value?.[0] !== 0 || !Array.isArray(value?.[19]) || ![0,1].includes(value[3])) failure('email_network_list_unqualified');
    if(value[3]===1) {
      const token=value?.[13]?.[8];
      if(typeof token!=='string'||!token||token.length>8192||token!==value?.[13]?.[9])failure('email_network_list_unqualified');
      pageTokens.set(page+1,token);
    }
    const rows=config.parse(value), received=[];let unsupported=0;
    listStage = 'members';
    let rawCount=0;
    for(const batch of value[19]) {
      // Gmail's terminal empty batch is a four-field network envelope with a
      // null thread list. The network contract is the complete evidence source.
      if(value[3]===0&&value[19].length===1&&Array.isArray(batch)&&batch.length===4&&batch[1]===null) continue;
      if(!Array.isArray(batch?.[1]))failure('email_network_list_unqualified');
      for(const container of batch[1]) {
        if(!Array.isArray(container?.[0]?.[4]) || !container[0][4].length)failure('email_network_list_unqualified');
        rawCount+=container[0][4].filter(message=>{const labels=message?.[10];return !(Array.isArray(labels)&&labels.every(label=>typeof label==='string')&&(labels.includes('^r')||labels.includes('^f')));}).length;
      }
    }
    unsupported+=Math.max(0,rawCount-rows.length);
    for(const row of rows) {
      records.set(row.provider_message_id,row);
      if(row.draft||row.sent)continue;
      const at=instantNanos(row.received_at);
      if(at===null){unsupported++;continue;}
      if(at>=start&&at<end)received.push({...row});
    }
    while(records.size>2000)records.delete(records.keys().next().value);
    listStage = 'complete';
    return {provider:config.provider,account_address:account,rows:received,unsupported_rows:unsupported,has_next:value[3]===1,page};
  }
  async function markRead({account_address,provider_message_id} = {}) {
    checkedAccount(account_address);
    if (!idPattern.test(provider_message_id || '') || !records.has(provider_message_id)) failure('email_network_target_unobserved');
    const row=records.get(provider_message_id);
    if (row.unread === false) return {provider:config.provider,account_address:account,provider_message_id,read_state:'read'};
    const result=await config.markRead?.({account_address:account,provider_message_id,row,binding,request:listRequest,fetch:originalFetch});
    if (!result || result.provider_message_id !== provider_message_id || !['read','unknown'].includes(result.read_state)) failure('email_network_mark_read_unqualified');
    checkedAccount(account_address);
    if(result.read_state==='read')row.unread=false;
    return {provider:config.provider,account_address:account,provider_message_id,read_state:result.read_state};
  }
  function dispose() {
    nativeQueryReply=null;
    active = false;originalAbort?.abort();if(objectURL)URL.revokeObjectURL(objectURL);objectURL=null;originalBytes=null;originalMessageID=''; records.clear(); pageTokens.clear(); networkQuery = null; binding = null; listRequest = null; inbox = null; transport?.dispose(); anchor?.remove();
    if (XMLHttpRequest.prototype.open === open) XMLHttpRequest.prototype.open = originalOpen;
    if (XMLHttpRequest.prototype.send === send) XMLHttpRequest.prototype.send = originalSend;
    if (XMLHttpRequest.prototype.setRequestHeader === setHeader) XMLHttpRequest.prototype.setRequestHeader = originalSetHeader;
    if (window.fetch === fetch) window.fetch = originalFetch;
    delete window.SparkClawMailReader;
  }
  transport=config.installTransport?.({account:checkedAccount,getInbox:()=>inbox,receiveStartup(value){inbox=config.parseFolders?.(value)||null;},receiveRows(rows){
    checkedAccount();for(const row of rows){if(row && row.provider_selection_id===undefined)row.provider_selection_id=row.provider_thread_id||row.provider_message_id;if(idPattern.test(row.provider_message_id||''))records.set(row.provider_message_id,row);}
    while(records.size>2000)records.delete(records.keys().next().value);
  }});
  Object.defineProperty(window, 'SparkClawMailReader', {configurable:true, value:Object.freeze({
    armRangeSearch(request){checkedAccount(request.account_address);if(!transport?.armRangeSearch)failure('email_network_list_unqualified');return transport.armRangeSearch(request);},
    version:'0.2.0', provider:config.provider, diagnostics:()=>({original:{state:originalState,responseOrigin},list:{stage:listStage,template:Boolean(listRequest),body:Array.isArray(listRequest?.body),paging:Array.isArray(listRequest?.body?.[0]?.[15])},inbox:Boolean(inbox),records:records.size,transport:transport?.diagnostics?.()}), snapshot, resetRound,
    listPage:request=>inRound(listPage,request), prepareOriginal:request=>inRound(prepareOriginal,request), prepareRetainedOriginal:request=>inRound(prepareRetainedOriginal,request), markRead, dispose,
    verifyTarget({account_address,provider_message_id,provider_selection_id,folder}) {
      checkedAccount(account_address);
      const row=records.get(provider_message_id);
      return Boolean(row && !row.draft && !row.sent && row.provider_selection_id===provider_selection_id && (!folder||folder==='all'||row.folder===folder) && Number.isFinite(Date.parse(row.received_at)));
    },
  })});
})({"provider":"gmail","origins":["https://mail.google.com"],"account":()=>document.querySelector('[aria-label^="Google Account:"]')?.getAttribute('aria-label')?.match(/[A-Za-z0-9.!#$%&'*+/=?^_`{|}~-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}/)?.[0]||'',"listURL":u=>/^\/sync\/u\/\d+\/i\/bv$/.test(u.pathname),"search":query=>{location.hash='#search/'+encodeURIComponent(query);},"download":function gmailOriginalURL({id,row,request}) {
  const source=request?.url,origin=globalThis.location?.origin;
  if(!row?.native_message_id||!source||source.origin!==origin||!/^[a-f0-9]{1,32}$/u.test(id))return null;
  const account=/^\/sync\/u\/(\d+)\/i\/bv$/u.exec(source.pathname)?.[1];
  if(account===undefined)return null;
  const url=new URL(`/mail/u/${account}/`,origin);
  url.searchParams.set('view','att');url.searchParams.set('th',id);url.searchParams.set('attid','0');
  url.searchParams.set('disp','comp');url.searchParams.set('safe','1');url.searchParams.set('zw','');
  return url;
},"parse":function parseGmailReceivedList(value) {
  const rows=[];
  for(const batch of value?.[19]??[])for(const container of batch?.[1]??[]) {
    const thread=container?.[0];
    if(!Array.isArray(thread?.[4]))continue;
    for(const message of thread[4]) {
      const labels=message?.[10];
      if(Array.isArray(labels)&&labels.every(label=>typeof label==='string')&&(labels.includes('^r')||labels.includes('^f')))continue;
      const individual=[...thread];individual[4]=[message];
      const source=[];source[19]=[[null,[[individual]]]];
      for(const row of parseGmailList(source,true))rows.push({...row,native_message_id:message[0]});
    }
  }
  return rows;
},"markRead":()=>null});
})();

return {state:globalThis.SparkClawMailReader?.version==="0.2.0"?"ready":"unavailable"};}}),
Object.assign({"id":"c2b90f5c-629d-41bd-8fc8-179729ad70b4","file":"outlook-mail-reader.user.js","version":"0.2.0","sha256":"ff5afbb8af361fbfba4262bfa023d06410f4f2b2f8030a2378144b11ff9be3fb","matches":[{"origin":"https://outlook.live.com","pathPrefix":"/"},{"origin":"https://outlook.office.com","pathPrefix":"/"},{"origin":"https://outlook.office365.com","pathPrefix":"/"}],"origins":["https://outlook.live.com","https://outlook.office.com","https://outlook.office365.com"],"runAt":"document-start","grants":["none"],"noframes":true},{install:function(){// ==UserScript==
// @name         SparkClaw Outlook Network Reader
// @namespace    sparkclaw.local
// @version      0.2.0
// @description  Read observed email sources in the signed-in page; SparkClaw owns synchronization and durable receipts.
// @match        https://outlook.live.com/*
// @match        https://outlook.office.com/*
// @match        https://outlook.office365.com/*
// @run-at       document-start
// @grant        none
// @noframes
// ==/UserScript==

// Generated by scripts/email/userscripts/build.mjs. Edit its source modules.
(()=>{
const installOutlookOriginalResolver=function installOutlookOriginalResolver({account,getInbox}) {
  let loading=null,active=true,stage='idle';
  const fail=()=>{throw Object.assign(new Error('email_network_original_unqualified'),{code:'email_network_original_unqualified'});};
  async function nativeModules() {
    if(!loading)loading=(async()=>{
      stage='runtime';
      let nativeRequire;
      if(!Array.isArray(window.webpackChunkOwa))fail();
      window.webpackChunkOwa.push([['sparkclaw_original_'+crypto.randomUUID()],{},require=>{nativeRequire=require;}]);
      if(typeof nativeRequire!=='function'||typeof nativeRequire.e!=='function')fail();
      // The observed native TriageActionImportExport dependency group. Loading
      // code has no mailbox mutation and is needed only for actual downloads.
      stage='modules';await Promise.all([21804,63436,73413,46866,32314,54709].map(id=>nativeRequire.e(id)));
      const build=nativeRequire(643446)?.V,mailbox=nativeRequire(129387)?.A,configuration=nativeRequire(859741)?.C;
      if(typeof build!=='function'||typeof mailbox!=='function'||typeof configuration!=='function')fail();
      return {build,mailbox,configuration};
    })();
    return loading;
  }
  async function prepare({account_address,provider_message_id}) {
    const owner=account(account_address),inbox=getInbox();
    if(!active||location.origin!=='https://outlook.live.com'||!inbox?.qualified||inbox.account!==owner||!/^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/.test(provider_message_id||''))fail();
    let timer;
    try{
      const {build,mailbox,configuration}=await Promise.race([nativeModules(),new Promise((_,reject)=>{timer=setTimeout(()=>reject(Object.assign(new Error('email_network_original_unqualified'),{code:'email_network_original_unqualified'})),20000);})]);
      const info=mailbox();
      stage='mailbox';
      if(!active||info?.type!=='UserMailbox')fail();
      // Resolve this exact native mailbox's configuration, not just the DOM
      // label: global settings and a selected shared mailbox can differ.
      if(String(configuration(info)?.SessionSettings?.UserEmailAddress||'').toLowerCase()!==owner)fail();
      account(owner);
      stage='url';
      const value=build(provider_message_id,'EML',info);
      if(typeof value!=='string')fail();
      const url=new URL(value);
      if(url.origin!=='https://attachment.outlook.live.net'||url.username||url.password||url.hash||
        !/^\/owa\/[^/]+\/service\.svc\/s\/DownloadMessage$/.test(url.pathname)||
        url.searchParams.get('id')!==provider_message_id||url.searchParams.get('outputFormat')!=='0'||!url.searchParams.get('token'))fail();
      account(owner);stage='ready';return url;
    }catch{fail();}finally{clearTimeout(timer);}
  }
  return {prepare,diagnostics:()=>({stage}),dispose(){active=false;loading=null;stage='disposed';}};
};
const installOutlookRangeTransport=function installOutlookRangeTransport({account,getInbox,receiveRows}) {
  const fail=code=>{throw Object.assign(new Error(code),{code});};
  const pattern=/^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/;
  function timestamp(value) {
    const match=typeof value==='string'&&/^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d{1,9}))?(Z|[+-]\d\d:\d\d)$/.exec(value);
    if(!match)return null;
    const wall=Date.parse(match[1]+'Z'),zone=match[3];
    if(!Number.isFinite(wall)||new Date(wall).toISOString().slice(0,19)!==match[1]||zone!=='Z'&&(Number(zone.slice(1,3))>23||Number(zone.slice(4))>59))return null;
    const seconds=Date.parse(match[1]+zone);
    if(!Number.isFinite(seconds))return null;
    return BigInt(seconds)*1000000n+BigInt((match[2]||'').padEnd(9,'0'));
  }
  function utc(ns) {
    let seconds=ns/1000000000n,fraction=ns%1000000000n;
    if(fraction<0){seconds--;fraction+=1000000000n;}
    const suffix=fraction.toString().padStart(9,'0').replace(/0+$/,'');
    return new Date(Number(seconds)*1000).toISOString().slice(0,19)+(suffix?'.'+suffix:'')+'Z';
  }
  let armed=null,active=true;
  function validate(request) {
    const owner=account(request.account_address),inbox=getInbox();
    const start=timestamp(request.interval_start),end=timestamp(request.interval_end);
    if(location.origin!=='https://outlook.live.com'||!inbox?.qualified||inbox.account!==owner||!inbox.folders?.length)fail('email_network_list_unqualified');
    if(start===null||end===null||start>=end||request.page!==0||request.provider_mode!=='time_range'||inbox.folders.length>100)fail('invalid_request');
    // Normalize RFC3339 offsets while preserving nanosecond boundaries.
    if(!inbox.folders.every(f=>pattern.test(f.id)))fail('email_network_list_unqualified');
    return {owner,start,end,folders:structuredClone(inbox.folders),query:`received>=${utc(start)} AND received<${utc(end)}`};
  }
  function arm(request) {
    if(!active||armed)fail('email_network_list_unqualified');
    const state=validate(request);
    let resolve,reject;
    const promise=new Promise((yes,no)=>{resolve=yes;reject=no;});
    promise.catch(()=>{}); // The trusted UI trigger precedes the consumer call.
    const abort=new AbortController();
    const timer=setTimeout(()=>{abort.abort();reject(Object.assign(new Error('email_network_read_failed'),{code:'email_network_read_failed'}));},20000);
    armed={...state,request:structuredClone(request),promise,resolve,reject,abort,timer,network:null};
    return {query:state.query};
  }
  async function decode(response) {
    if(!response.ok||new URL(response.url).origin!==location.origin)fail('email_network_read_failed');
    const reader=response.body.getReader(),chunks=[];let size=0;
    try{while(true){const {done,value}=await reader.read();if(done)break;size+=value.length;if(size>10<<20)fail('email_network_list_unqualified');chunks.push(value);}}
    finally{await reader.cancel().catch(()=>{});}
    const bytes=new Uint8Array(size);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length;}
    try{return JSON.parse(new TextDecoder().decode(bytes));}catch{fail('email_network_list_unqualified');}
  }
  function fetchSearch(args,next) {
    const state=armed;
    let body,url;
    try{url=new URL(typeof args[0]==='string'?args[0]:args[0]?.url,location.href);body=JSON.parse(args[1]?.body);}catch{return next(...args);}
    if(!active||!state||url.origin!==location.origin||url.pathname!=='/searchservice/api/v2/query'||body?.EntityRequests?.[0]?.Query?.QueryString!==state.query)return next(...args);
    if(state.network)return state.network.then(r=>r.clone());
    state.network=(async()=>{
      account(state.owner);
      if(args[1]?.method?.toUpperCase()!=='POST'||body.EntityRequests.length!==1||body.EntityRequests[0].ContentSources?.join(',')!=='Exchange')fail('email_network_list_unqualified');
      const entity=body.EntityRequests[0];
      entity.EntityType='Message';entity.From=0;entity.Size=50;
      entity.EnableTopResults=false;entity.TopResultsCount=0;entity.RefiningQueries=null;
      entity.Sort=[{Field:'Time',SortDirection:'Desc'}];
      entity.Filter={Or:state.folders.map(f=>({Term:{FolderId:f.id}}))};
      body.QueryAlterationOptions={...(body.QueryAlterationOptions||{}),EnableSuggestion:false,EnableAlteration:false};
      const response=await next(args[0],{...args[1],body:JSON.stringify(body),signal:state.abort.signal,redirect:'error'});
      const value=await decode(response.clone());account(state.owner);
      if(!active||armed!==state||state.abort.signal.aborted)fail('email_network_read_failed');
      state.resolve(value);clearTimeout(state.timer);return response;
    })().catch(cause=>{clearTimeout(state.timer);state.reject(cause);throw cause;});
    return state.network.then(r=>r.clone());
  }
  async function listPage(request) {
    const expected=validate(request),state=armed;
    if(!state||state.query!==expected.query||state.owner!==expected.owner||JSON.stringify(state.folders)!==JSON.stringify(expected.folders))fail('email_network_list_unqualified');
    const value=await state.promise;account(state.owner);
    if(!active||armed!==state||state.abort.signal.aborted)fail('email_network_read_failed');
    if(!value||typeof value!=='object'||Array.isArray(value))fail('email_network_list_unqualified');
    const entity=value?.EntitySets?.[0],result=entity?.ResultSets?.[0];
    if(value.EntitySets?.length!==1||entity.EntityType!=='Message'||entity.IsPartial!==false||entity.Properties?.HasParseException!==false||entity.ResultSets?.length!==1||
      !Number.isSafeInteger(result?.Total)||result.Total<0||typeof result.MoreResultsAvailable!=='boolean')fail('email_network_list_unqualified');
    const sources=result.Results??(result.Total===0?[]:null);
    if(!Array.isArray(sources)||sources.length>50||result.Total<sources.length||(!result.MoreResultsAvailable&&result.Total!==sources.length))fail('email_network_list_unqualified');
    const rows=[],ids=new Set(),folders=new Map(state.folders.map(f=>[f.id,f]));let unsupported=0;
    for(const item of sources){
      const node=item?.Source,id=typeof node?.ImmutableId==='string'?node.ImmutableId.replace(/_/g,'+').replace(/-/g,'/'):null;
      const thread=node?.ConversationId?.Id,folder=folders.get(node?.ParentFolderId?.Id),received=timestamp(node?.DateTimeReceived);
      // The native Message adapter uses this immutable EWS form. Ordinary
      // ItemId can change on a folder move and must not own a second source.
      if(item.Type!=='Message'||!pattern.test(id||'')||!pattern.test(node?.ItemId?.Id||'')||!pattern.test(thread||'')||ids.has(id)||!folder||typeof node.IsDraft!=='boolean'||typeof node.IsRead!=='boolean'||received===null||received<state.start||received>=state.end){unsupported++;continue;}
      ids.add(id);if(node.IsDraft)continue;
      rows.push({provider_message_id:id,provider_selection_id:thread,provider_thread_id:thread,received_at:node.DateTimeReceived,draft:false,unread:!node.IsRead,sent:false,inbox:folder.inbox,folder:folder.inbox?'inbox':'outlook:'+folder.id});
    }
    receiveRows(rows);
    const digest=await crypto.subtle.digest('SHA-256',new TextEncoder().encode(JSON.stringify(state.folders.map(f=>f.id))));
    if(!active||armed!==state||state.abort.signal.aborted)fail('email_network_read_failed');
    return {provider:'outlook',account_address:state.owner,rows,unsupported_rows:unsupported,has_next:result.MoreResultsAvailable,page:0,scope:'inbound_received',folder_scope_id:Array.from(new Uint8Array(digest),v=>v.toString(16).padStart(2,'0')).join('')};
  }
  function resetRound(){if(armed){clearTimeout(armed.timer);armed.abort.abort();armed.reject(Object.assign(new Error('email_network_read_failed'),{code:'email_network_read_failed'}));}armed=null;}
  function dispose(){active=false;resetRound();}
  return {arm,listPage,fetchSearch,resetRound,dispose};
};
const installOutlookEarlyBridge=function installOutlookEarlyBridge() {
  if(window.top!==window || !['https://outlook.live.com','https://outlook.office.com','https://outlook.office365.com'].includes(location.origin))return null;
  if(window.SparkClawOutlookEarlyBridge)return window.SparkClawOutlookEarlyBridge;
  const NativeWorker=window.Worker,NativeChannel=window.MessageChannel,originalFetch=window.fetch;
  if(!NativeWorker||!NativeChannel)return null;
  const queue=[],listeners=[];let consumer=null,startup=null,active=true;
  const observe=(worker,message)=>{
    const operation=message?.argumentList?.[0]?.value?.operationName;
    if(!['ItemRows','ConversationRows','ItemExport'].includes(operation))return;
    const item={worker,message:structuredClone(message)};
    if(consumer)consumer.request(item.worker,item.message);
    else {queue.push(item);if(queue.length>40)queue.shift();}
  };
  const Worker=class extends NativeWorker {postMessage(message,...rest){try{observe(this,message);}catch{}return super.postMessage(message,...rest);}};
  const Channel=class extends NativeChannel {constructor(){super();for(const port of [this.port1,this.port2]){
    const listener=event=>consumer?.result(event,port);port.addEventListener('message',listener);listeners.push([port,listener]);
  }}};
  window.Worker=Worker;window.MessageChannel=Channel;
  const fetch=async function(...args){
    const next=(...values)=>originalFetch.apply(this,values);
    const response=await (consumer?.fetchSearch?consumer.fetchSearch(args,next):next(...args));
    try{
      const url=new URL(response.url);
      if(active&&response.ok&&url.origin===location.origin&&/^\/owa\/\d+\/startupdata\.ashx$/.test(url.pathname))void(async()=>{
        const reader=response.clone().body.getReader(),chunks=[];let size=0;
        try{for(;;){const {done,value}=await reader.read();if(done)break;size+=value.length;if(size>10<<20){void reader.cancel();return;}chunks.push(value);}}
        finally{reader.releaseLock();}
        const bytes=new Uint8Array(size);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length;}
        const value=JSON.parse(new TextDecoder().decode(bytes));if(!active)return;
        // Retain hierarchy evidence only, never session secrets or mail bodies.
        startup={owaUserConfig:{SessionSettings:{UserEmailAddress:value?.owaUserConfig?.SessionSettings?.UserEmailAddress}},findFolders:value.findFolders,findConversation:{Body:{FolderId:value?.findConversation?.Body?.FolderId}}};
        consumer?.startup?.(startup);
      })().catch(()=>{});
    }catch{}
    return response;
  };
  window.fetch=fetch;
  const bridge=Object.freeze({
    attach(next){if(!active)return;consumer=next;for(const item of queue.splice(0))consumer.request(item.worker,item.message);if(startup)consumer.startup?.(startup);},
    detach(next){if(consumer===next)consumer=null;},
    dispose(){
      active=false;consumer=null;startup=null;queue.length=0;
      for(const [port,listener]of listeners)port.removeEventListener('message',listener);listeners.length=0;
      if(window.Worker===Worker)window.Worker=NativeWorker;
      if(window.MessageChannel===Channel)window.MessageChannel=NativeChannel;
      if(window.fetch===fetch)window.fetch=originalFetch;
      delete window.SparkClawOutlookEarlyBridge;
    },
  });
  Object.defineProperty(window,'SparkClawOutlookEarlyBridge',{configurable:true,value:bridge});
  return bridge;
};
const parseGmailList=function parseGmailList(value, includeThreads = false) {
  const batches = value?.[19];
  if (!Array.isArray(batches)) return [];
  const result = [];
  for (const batch of batches) {
    if (!Array.isArray(batch?.[1])) continue;
    for (const container of batch[1]) {
      const thread = container?.[0];
      if (!Array.isArray(thread) || !/^(?:thread-f:\d+|thread-a:r-?\d+)$/u.test(thread[3]) || !Array.isArray(thread[4])) continue;
      const messages = thread[4];
      if ((!includeThreads && messages.length !== 1) || messages.length < 1 || messages.length > 1000) continue;
      const members=[];
      for (const message of messages) {
      const id = message?.[55], labels = message?.[10];
      if (!/^(?:msg-f:\d+|msg-a:r-?\d+)$/u.test(message?.[0]) || !/^[a-f0-9]{1,32}$/u.test(id) ||
          !Array.isArray(labels) || !labels.every(label => typeof label === 'string')) continue;
      if (thread[3].startsWith('thread-f:')) {
        if (!message[0].startsWith('msg-f:') || BigInt(`0x${id}`).toString() !== message[0].slice(6) || !includeThreads && thread[3].slice(9) !== message[0].slice(6)) continue;
      } else if (!message[0].startsWith('msg-a:')) continue;
      members.push({ provider_message_id: id, provider_thread_id: thread[3],
        unread: labels.includes('^u'), inbox: labels.includes('^i'), draft: labels.includes('^r'),
        ...(includeThreads ? {sent:labels.includes('^f'),observed_message_count:messages.length,
          // Observed internal receipt timestamp: the pinned original's Received
          // trace agrees at the second, and RFC Date is independently earlier.
          ...(Number.isSafeInteger(message[6]) && message[6]>=946684800000 && message[6]<=Date.now()+300000 ? {received_at:new Date(message[6]).toISOString()} : {})} : {}) });
      }
      if(members.length===messages.length && new Set(members.map(member=>member.provider_message_id)).size===members.length) result.push(...members);
    }
  }
  return result;
};
const parseOutlookList=function parseOutlookList(value, includeInventory = false) {
  const rows=[];
  let visited=0;
  const visit=(node,depth)=>{
    if(!node || typeof node!=='object' || depth>20 || ++visited>20000) return;
    if(Array.isArray(node.Conversations)) for(const item of node.Conversations.slice(0,100)) {
      const selection=item?.ConversationId?.Id, id=item?.ItemIds?.[0]?.Id;
      if(typeof selection!=='string' || !selection)continue;
      let inventory = includeInventory ? {last_delivery_time:typeof item.LastDeliveryTime==='string' && Number.isFinite(Date.parse(item.LastDeliveryTime)) ? item.LastDeliveryTime : null} : {};
      if (includeInventory && Number.isInteger(item.GlobalMessageCount) && item.GlobalMessageCount >= 1 && item.GlobalMessageCount <= 1000 &&
          Array.isArray(item.GlobalItemIds) && item.GlobalItemIds.length === item.GlobalMessageCount && Array.isArray(item.ItemIds) &&
          item.ItemIds.length === item.MessageCount && Array.isArray(item.DraftItemIds)) {
        const globalIDs = item.GlobalItemIds.map(value=>value?.Id), localIDs = item.ItemIds.map(value=>value?.Id), drafts = item.DraftItemIds.map(value=>value?.Id);
        if (globalIDs.every(value=>typeof value==='string' && /^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/u.test(value)) && new Set(globalIDs).size === globalIDs.length &&
            localIDs.every(value=>globalIDs.includes(value)) && drafts.every(value=>globalIDs.includes(value)) && new Set(drafts).size === drafts.length) {
          inventory = {members:globalIDs.map(value=>({provider_message_id:value,local:localIDs.includes(value),draft:drafts.includes(value)})),
            inventory_complete:true,global_unread_count:item.GlobalUnreadCount,
            last_delivery_time: typeof item.LastDeliveryTime==='string' && Number.isFinite(Date.parse(item.LastDeliveryTime)) ? item.LastDeliveryTime : null};
        }
      }
      if(typeof id!=='string' || !id || selection===id ||
          item.MessageCount!==1 || item.GlobalMessageCount!==1 || item.ItemIds?.length!==1 ||
          item.GlobalItemIds?.length!==1 || item.GlobalItemIds[0]?.Id!==id ||
          ![0,1].includes(item.UnreadCount) || item.GlobalUnreadCount!==item.UnreadCount) {
        rows.push({provider_selection_id:selection,provider_message_id:null,unread:null,...inventory});continue;
      }
      rows.push({provider_selection_id:selection,provider_message_id:id,unread:item.UnreadCount===1,...inventory});
    }
    for(const child of Object.values(node))if(child&&typeof child==='object')visit(child,depth+1);
  };
  visit(value,0);return rows;
};
const parseQQMailList=function parseQQMailList(value) {
  if(value?.head?.ret!==0 || !Number.isInteger(value.head.time) || !Array.isArray(value.body?.list) || !Number.isInteger(value.body.total_num) || value.body.total_num<0)return null;
  const rows=[];
  for(const item of value.body.list.slice(0,2000)){
    if(typeof item?.emailid!=='string' || !/^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/u.test(item.emailid) || !Number.isInteger(item.dirid) || item.dirid<1 ||
      !Number.isInteger(item.totime) || item.totime<946684800 || item.totime>value.head.time+300 || item.unread!==undefined && item.unread!==0 && item.unread!==1)continue;
    rows.push({provider_message_id:item.emailid,provider_selection_id:item.emailid,provider_thread_id:item.emailid,
      folder:item.dirid===1?'inbox':item.dirid===3?'sent':`qq:${item.dirid}`,unread:item.unread===1,
      received_at:new Date(item.totime*1000).toISOString()});
  }
  return {rows,total_count:value.body.total_num,unsupported_rows:value.body.list.length-rows.length};
};
(function installReader(config) {
  'use strict';
  if (window.top !== window || !config.origins.includes(location.origin)) return;
  window.SparkClawMailReader?.dispose();
  const originalFetch = window.fetch, originalOpen = XMLHttpRequest.prototype.open;
  const originalSend = XMLHttpRequest.prototype.send, originalSetHeader = XMLHttpRequest.prototype.setRequestHeader;
  const records = new Map(), pageTokens = new Map();
  let listRequest = null, networkQuery = null, inbox = null, transport = null, objectURL = null, originalAbort = null;
  let originalBytes = null, originalMessageID = '';
  let originalState = 'unlearned', responseOrigin = '';
  let listStage = 'idle';
  let nativeQueryReply = null;
  let active = true, account = '', binding = null, anchor = null;
  let pendingOperations = 0;
  const idPattern = /^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/;
  const failure = code => { throw Object.assign(new Error(code), {code}); };
  async function inRound(operation, request) {
    if (!active) failure('email_network_capability_unavailable');
    pendingOperations++;
    try { return await operation(request); } finally { pendingOperations--; }
  }
  // Only the Controller's idle mailbox lease may reset a round. Keep learned
  // transport/session templates, but never retain source bytes or query results
  // across polls. This performs no network request and does not retry failures.
  function resetRound({account_address} = {}) {
    if (!active || pendingOperations || originalAbort) failure('email_network_list_unqualified');
    checkedAccount(account_address);
    if(config.provider==='outlook' && typeof transport?.resetRound!=='function') failure('email_network_capability_unavailable');
    transport?.resetRound?.();
    records.clear(); pageTokens.clear(); networkQuery=null; nativeQueryReply=null;
    anchor?.remove(); anchor=null;
    if(objectURL)URL.revokeObjectURL(objectURL);
    objectURL=null; originalBytes=null; originalMessageID='';
    originalState='unlearned'; responseOrigin=''; listStage='idle';
    return {provider:config.provider,account_address:account};
  }
  // Gateway checkpoints carry micro/nanoseconds; Date.parse alone truncates
  // those bounds and can falsely certify a missed endpoint millisecond.
  function instantNanos(value) {
    const match=typeof value==='string'&&/^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d{1,9}))?(Z|[+-](?:[01]\d|2[0-3]):[0-5]\d)$/.exec(value);
    if(!match)return null;
    const seconds=Date.parse(match[1]+match[3]);
    const offset=match[3]==='Z'?0:(match[3][0]==='-'?-1:1)*(Number(match[3].slice(1,3))*60+Number(match[3].slice(4,6)));
    if(!Number.isFinite(seconds)||new Date(seconds+offset*60000).toISOString().slice(0,19)!==match[1])return null;
    return BigInt(seconds)*1000000n+BigInt((match[2]||'').padEnd(9,'0'));
  }
  const inlineOriginalLimit = 1 << 20;
  const base64 = bytes => {
    let binary = '';
    for (let offset = 0; offset < bytes.length; offset += 0x8000) {
      binary += String.fromCharCode(...bytes.subarray(offset, offset + 0x8000));
    }
    return btoa(binary);
  };
  function checkedAccount(expected) {
    const current = config.account();
    if (!current) failure('email_account_identity_unavailable');
    if (account && account !== current.toLowerCase()) {
      records.clear(); pageTokens.clear(); binding = null; listRequest = null; networkQuery = null; nativeQueryReply = null; inbox = null; transport?.dispose();
      failure('email_account_identity_mismatch');
    }
    account = current.toLowerCase();
    if (expected && expected.toLowerCase() !== account) failure('email_account_identity_mismatch');
    return account;
  }
  function observe(url, text, requestQuery) {
    if (!active || typeof text !== 'string' || text.length > 10 << 20) return;
    try {
      const u = new URL(url, location.href);
      if (u.origin !== location.origin || !config.listURL(u)) return;
      const value = JSON.parse(text);
      if(config.provider==='gmail' && typeof requestQuery==='string') nativeQueryReply={query:requestQuery,value};
      if(config.provider==='outlook' && u.pathname.endsWith('/startupdata.ashx')) {
        const owner=value.owaUserConfig?.SessionSettings?.UserEmailAddress, id=value.findConversation?.Body?.FolderId?.Id;
        if(typeof owner==='string' && typeof id==='string')inbox=config.parseFolders?.(value)||{account:owner.toLowerCase(),id,qualified:false};
      }
      const rows = config.parse(value);
      if (!rows) return;
      checkedAccount();
      for (const row of rows) {
        if (row && row.provider_selection_id === undefined) row.provider_selection_id = row.provider_thread_id || row.provider_message_id;
        if (!idPattern.test(row.provider_message_id || '')) continue;
        records.set(row.provider_message_id, row);
      }
      while (records.size > 2000) records.delete(records.keys().next().value);
      if (config.provider === 'qq_mail' && u.searchParams.get('func') === '1' && u.searchParams.get('sid')) binding = u;
    } catch { /* Unqualified responses never become admitted source evidence. */ }
  }
  function rememberListRequest(request, body) {
    try {
      const url = new URL(request.url, location.href);
      if (url.origin !== location.origin || !config.listURL(url)) return;
      listRequest = {method: request.method, url, headers: {...(request.headers || {})}, ...(body === undefined ? {} : {body})};
    } catch {}
  }
  function qqListSource() {
    const source = listRequest || binding;
    if (source) return new URL(source.url || source, location.href);
    // The current QQ client can restore /home/index from its own cache without
    // issuing /list/maillist again. Its signed-in route still carries the same
    // first-party session id required by /list/search, so construct only the
    // fixed list binding instead of depending on an incidental resource entry.
    try {
      const current = new URL(location.href);
      const sid = current.origin===location.origin && current.pathname==='/home/index'
        ? current.searchParams.get('sid') : '';
      if(sid && sid.length<=4096) {
        const url=new URL('/list/maillist',location.origin);
        url.searchParams.set('func','1');url.searchParams.set('sid',sid);
        return url;
      }
    } catch {}
    // QQ can load its first list before the userscript's fetch/XHR hooks run.
    const resources = performance.getEntriesByType('resource');
    for (let index = resources.length - 1; index >= 0; index--) {
      try {
        const url = new URL(resources[index].name);
        if (url.origin === location.origin && url.pathname === '/list/maillist' &&
            url.searchParams.get('func') === '1' && url.searchParams.get('sid')) return url;
      } catch {}
    }
    return null;
  }
  const open = function(method, url, ...args) {
    this.__sparkclawMailURL = url;
    this.__sparkclawMailRequest = {method, url, headers:{}};
    return originalOpen.call(this, method, url, ...args);
  };
  const setHeader = function(key, value) {
    if (this.__sparkclawMailRequest) this.__sparkclawMailRequest.headers[key.toLowerCase()] = value;
    return originalSetHeader.call(this,key,value);
  };
  const send = function(...args) {
    try {
      const request = this.__sparkclawMailRequest;
      const body = args[0] === undefined ? undefined : JSON.parse(args[0]);
      if (request) {
        const previous = listRequest?.body?.[0]?.[3];
        rememberListRequest(request, body);
        if (previous && body?.[0]?.[3] !== previous) pageTokens.clear();
      }
    } catch {}
    const url = this.__sparkclawMailURL;
    let requestQuery;
    try { requestQuery=JSON.parse(args[0])?.[0]?.[3]; } catch {}
    this.addEventListener('load', () => {
      if (this.status === 200 && (!this.responseType || this.responseType === 'text')) observe(url, this.responseText, requestQuery);
    }, {once:true});
    return originalSend.apply(this, args);
  };
  const fetch = async function(...args) {
    const response = await originalFetch.apply(this, args);
    try {
      const u = new URL(response.url);
      if (u.origin === location.origin && config.listURL(u)) {
        if (!listRequest) rememberListRequest({method:String(args[1]?.method || 'GET').toUpperCase(), url:u, headers:args[1]?.headers || {}}, args[1]?.body ? JSON.parse(args[1].body) : undefined);
      }
      if (active && response.ok && u.origin === location.origin && config.listURL(u) && Number(response.headers.get('content-length')) <= 10 << 20) {
        void (async () => {
          const copy = response.clone(), reader = copy.body.getReader(), chunks = []; let size = 0;
          try {
            for (;;) { const {done,value} = await reader.read(); if (done) break;
              size += value.length; if (size > 10 << 20) { void reader.cancel(); return; } chunks.push(value); }
            const bytes = new Uint8Array(size); let offset = 0;
            for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.length; }
            let requestQuery;try{requestQuery=JSON.parse(args[1]?.body)?.[0]?.[3];}catch{}
            observe(response.url, new TextDecoder().decode(bytes),requestQuery);
          } finally { reader.releaseLock(); }
        })().catch(()=>{});
      }
    } catch {}
    return response;
  };
  XMLHttpRequest.prototype.open = open; XMLHttpRequest.prototype.send = send; XMLHttpRequest.prototype.setRequestHeader = setHeader;
  window.fetch = fetch;
  function snapshot({account_address, interval_start, interval_end} = {}) {
    checkedAccount(account_address);
    const start = instantNanos(interval_start), end = instantNanos(interval_end);
    if (start===null || end===null || start >= end) failure('invalid_request');
    let unsupported = 0;
    const rows = [];
    for (const row of records.values()) {
      if (row.draft || row.sent) continue;
      if (row.grouped) { unsupported++; continue; }
      const received = instantNanos(row.received_at);
      if (received===null) { unsupported++; continue; }
      if (received >= start && received < end) rows.push({...row});
    }
    return {provider:config.provider, account_address:account, rows, unsupported_rows:unsupported,
      scan_complete:false, reason:'folder_scope_and_pagination_unqualified'};
  }
  async function prepareRetainedOriginal(target = {}) {
    const {account_address,provider_message_id,provider_selection_id,provider_native_id,folder}=target;
    checkedAccount(account_address);
    if(!idPattern.test(provider_message_id||'')||!idPattern.test(provider_selection_id||'')||folder==='sent')failure('invalid_request');
    // These IDs came from a previous qualified list and were retained by the
    // Gateway. Reuse the exact original endpoint without searching the inbox.
    const row={provider_message_id,provider_selection_id,native_message_id:provider_native_id,folder,draft:false,sent:false};
    if(config.provider==='gmail') {
      if(!/^(?:msg-f:\d+|msg-a:r-?\d+)$/u.test(provider_native_id||''))failure('email_network_target_unobserved');
      if(provider_native_id.startsWith('msg-f:')&&(!/^[a-f0-9]{1,32}$/u.test(provider_message_id)||BigInt(`0x${provider_message_id}`).toString()!==provider_native_id.slice(6)))failure('email_network_target_unobserved');
    }
    if(config.provider==='qq_mail') {
      if(/^(?:C|@)/u.test(provider_message_id))failure('email_network_target_unobserved');
      binding=qqListSource();
      if(!binding)failure('email_network_original_unqualified');
    }
    return prepareOriginal(target,row);
  }
  async function prepareOriginal({account_address, provider_message_id} = {}, retainedRow = null) {
    checkedAccount(account_address);
    if (!idPattern.test(provider_message_id || '')) failure('invalid_request');
    const row = retainedRow || records.get(provider_message_id);
    if (!row || row.draft || row.grouped) failure('email_network_target_unobserved');
    let url = await config.download?.({id:provider_message_id, row, binding, request:listRequest});
    if (!url && config.provider==='outlook' && transport?.prepareOriginal) url = await transport.prepareOriginal({account_address:account,provider_message_id});
    if (!url) failure('email_network_original_unqualified');
    try {
      url = new URL(url, location.href);
      if (url.protocol !== 'https:' || url.username || url.password ||
          (url.origin !== location.origin && !(config.provider === 'gmail' && url.origin === 'https://mail-attachment.googleusercontent.com') &&
            !(config.provider === 'outlook' && url.origin === 'https://attachment.outlook.live.net'))) failure('email_network_original_unqualified');
    } catch (error) { failure(error.code || 'email_network_original_unqualified'); }
    if(originalAbort)failure('email_network_original_unqualified');
    originalState='fetching';originalAbort=new AbortController();
    return (async()=>{
      try {
        const response=await originalFetch.call(window,url.href,{credentials:url.origin===location.origin?'same-origin':'omit',redirect:config.provider==='gmail'?'follow':'error',cache:'no-store',signal:AbortSignal.any([originalAbort.signal,AbortSignal.timeout(20000)])});
        originalState='http_'+response.status;responseOrigin=response.url?new URL(response.url).origin:'';if(response.url&&responseOrigin!==url.origin&&!(config.provider==='gmail'&&responseOrigin==='https://mail-attachment.googleusercontent.com'))failure('email_network_original_unqualified');if(!response.ok)failure('email_network_original_unqualified');
        const reader=response.body.getReader(),chunks=[];let length=0;
        try {
          for(;;){const {done,value}=await reader.read();if(done)break;length+=value.length;if(length>110<<20){void reader.cancel();failure('email_capture_limit');}chunks.push(value);}
        }finally{reader.releaseLock();}
        checkedAccount(account_address);
        if(!active)failure('email_network_original_unqualified');
        const bytes=new Uint8Array(length);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length;}
        const header=new TextDecoder().decode(bytes.subarray(0,Math.min(bytes.length,65536)));
        const headerEnd=header.search(/\r?\n\r?\n/);
        const headerBlock=headerEnd<0?'':header.slice(0,headerEnd);
        let precedingField=false;
        const validHeaders=headerBlock.split(/\r?\n/).every(line=>{
          if(/^[ \t]/.test(line))return precedingField;
          precedingField=/^[!-9;-~]+:/.test(line);
          return precedingField;
        });
        // A provider's HTML "show original" page may embed From: and a blank
        // line much later. It is an adapter response error, not a bad email and
        // must never consume a mail-specific retry allowance.
        if(headerEnd<0||!validHeaders||!/^From:/im.test(headerBlock))failure('email_network_original_unqualified');
        anchor?.remove();if(objectURL)URL.revokeObjectURL(objectURL);
        originalBytes = bytes;
        originalMessageID = provider_message_id;
        objectURL=URL.createObjectURL(new Blob([bytes],{type:'message/rfc822'}));
        anchor=document.createElement('a');anchor.id='sparkclaw-mail-original';anchor.href=objectURL;
        anchor.download='message.eml';anchor.textContent='SparkClaw EML';anchor.hidden=true;document.body.append(anchor);
        originalState='ready';
        const result={selector:'#sparkclaw-mail-original',account_address:account,provider_message_id,bytes:bytes.length};
        if(bytes.length<=inlineOriginalLimit) { result.inline_bytes=bytes.length; result.inline_base64=base64(bytes); }
        return result;
      }catch(error){
        if(['email_account_identity_mismatch','email_account_identity_unavailable','email_capture_limit'].includes(error.code))throw error;
        if(originalState==='fetching')originalState='fetch_failed';failure('email_network_original_unqualified');
      }finally{originalAbort=null;}
    })();
  }

  async function listPage({account_address,interval_start,interval_end,page=0,folder='inbox',provider_mode}) {
    checkedAccount(account_address);
    if(config.provider==='outlook' && transport?.listPage)return transport.listPage({account_address,interval_start,interval_end,page,folder,provider_mode});
    if (config.provider === 'qq_mail') {
      listStage='qq_source';
      const start=instantNanos(interval_start), end=instantNanos(interval_end);
      if (start===null||end===null||start>=end||!Number.isInteger(page)||page<0||page>128) failure('invalid_request');
      const deadline=Date.now()+5000;
      let url=qqListSource();
      while(!url && Date.now()<deadline) {
        await new Promise(resolve=>setTimeout(resolve,100));
        url=qqListSource();
      }
      if (!url || url.origin!==location.origin || url.pathname!=='/list/maillist' || url.searchParams.get('func') !== '1' || !url.searchParams.get('sid')) failure('email_network_list_unqualified');
      if(provider_mode==='time_range') {
        if(page!==0)failure('email_incremental_unqualified');
        // QQ's current public web client uses /list/search with epoch-second
        // after/before parameters. No mailbox-head pagination participates.
        const search=new URL('/list/search',location.origin);
        search.searchParams.set('sid',url.searchParams.get('sid'));
        const body=new URLSearchParams({page_now:'0',page_size:'50',after:String(start/1000000000n-1n),before:String((end+999999999n)/1000000000n),sort_type:'1',sort_direction:'1'});
        listStage='qq_request';
        const response=await originalFetch.call(window,search.href,{method:'POST',credentials:'same-origin',redirect:'error',headers:{'content-type':'application/x-www-form-urlencoded'},body,signal:AbortSignal.timeout(20000)});
        if([401,403].includes(response.status))failure('email_login_required');if(!response.ok)failure('email_network_read_failed');
        const reader=response.body.getReader(),chunks=[];let length=0;
        try {for(;;){const {done,value}=await reader.read();if(done)break;length+=value.length;if(length>10<<20){void reader.cancel();failure('email_capture_limit');}chunks.push(value);}}finally{reader.releaseLock();}
        const bytes=new Uint8Array(length);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length;}
        listStage='qq_json';
        let value;try{value=JSON.parse(new TextDecoder().decode(bytes));}catch{failure('email_network_list_unqualified');}
        checkedAccount(account_address);
        listStage='qq_head';if(value?.head?.ret!==0)failure('email_network_list_unqualified');
        listStage='qq_total';if(!Number.isInteger(value.body?.total_num)||value.body.total_num<0)failure('email_network_list_unqualified');
        listStage='qq_lock';if(!Number.isInteger(value.body.lock_num)||value.body.lock_num<0)failure('email_network_list_unqualified');
        if(value.body.total_num===0&&value.body.list===undefined)value.body.list=[];
        listStage='qq_list';if(!Array.isArray(value.body.list)||value.body.list.length>50)failure('email_network_list_unqualified');
        const parsed=config.parse(value),rows=[];
        let unsupported=value.body.list.length-parsed.length+value.body.lock_num;
        for(const row of parsed){
          if(row.grouped){unsupported++;continue;}
          if(row.folder!=='inbox'&&!/^qq:[1-9][0-9]{3,9}$/.test(row.folder))continue;
          const at=instantNanos(row.received_at);if(at===null){unsupported++;continue;}
          if(at>=start&&at<end){records.set(row.provider_message_id,row);rows.push(row);}
        }
        binding=url;
        listStage='qq_complete';
        return {provider:config.provider,account_address:account,rows,unsupported_rows:unsupported,has_next:value.body.total_num>value.body.list.length,page:0,scope:'inbound_received',folder_scope_id:'search_inbound_v1'};
      }
      const dir=folder==='inbox'?1:/^qq:[1-9][0-9]{3,9}$/u.test(folder)?Number(folder.slice(3)):folder==='sent'?3:null;
      if (!Number.isInteger(dir)||dir<1) failure('email_network_list_unqualified');
      url.searchParams.set('func','1');url.searchParams.set('dir',String(dir));url.searchParams.set('dirid',String(dir));url.searchParams.set('page_now',String(page));url.searchParams.set('page_size','50');
      const response=await originalFetch.call(window,url.href,{method:'GET',credentials:'same-origin',redirect:'error',cache:'no-store',signal:AbortSignal.timeout(20000)});
      if([401,403].includes(response.status))failure('email_login_required');if(!response.ok)failure('email_network_read_failed');
      let value;try{value=await response.json();}catch{failure('email_network_list_unqualified');}
      checkedAccount(account_address);
      if(value?.head?.ret!==0||!Array.isArray(value.body?.list)||!Number.isInteger(value.body.total_num)||value.body.total_num<0)failure('email_network_list_unqualified');
      binding=url;
      const rows=config.parse(value).filter(row=>row.folder===(folder==='inbox'?'inbox':folder));
      const received=rows.filter(row=>{const at=instantNanos(row.received_at);return at!==null&&at>=start&&at<end;});
      const unsupportedRows=Math.max(0,value.body.list.length-rows.length);
      const receiptTimes=rows.map(row=>instantNanos(row.received_at));
      const receiptOrdered=receiptTimes.length>0 && receiptTimes.every((at,index)=>at!==null&&(index===0||receiptTimes[index-1]>=at));
      // QQ's native inbox page is ordered by `totime` descending. Once a fully
      // qualified page crosses the requested lower bound, every later page is
      // older and scanning the rest of mailbox history would add no evidence.
      // Any invalid row or ordering violation keeps pagination fail-closed.
      const boundaryReached=unsupportedRows===0&&receiptOrdered&&receiptTimes.at(-1)<start;
      const hasNext=!boundaryReached&&(page+1)*50<value.body.total_num;
      for(const row of rows)records.set(row.provider_message_id,row);
      return {provider:config.provider,account_address:account,rows:received,unsupported_rows:unsupportedRows,has_next:hasNext,next_page:hasNext?page+1:undefined,page,scope:'inbound_received',folder_scope_id:String(dir)};
    }
    if (config.provider !== 'gmail') failure('email_network_list_unqualified');
    listStage = 'template';
    const start=instantNanos(interval_start),end=instantNanos(interval_end);
    if (start===null||end===null||start>=end||!Number.isInteger(page)||page<0||page>10000) failure('invalid_request');
    const query=`-in:trash -in:spam -in:drafts after:${start/1000000000n-1n} before:${(end+999999999n)/1000000000n}`;
    if (networkQuery && networkQuery !== query) failure('email_network_list_unqualified');
    networkQuery = query;
    let value;
    const usableTemplate=String(listRequest?.method).toUpperCase()==='POST' && Array.isArray(listRequest?.body?.[0]) && typeof listRequest.body[0][3]==='string' && Array.isArray(listRequest.body[2]) && Array.isArray(listRequest.body[0][15]);
    if(!usableTemplate && page===0 && typeof config.search==='function') {
      listStage='native_query';
      config.search(query);
      const deadline=Date.now()+20000;
      while(active && nativeQueryReply?.query!==query && Date.now()<deadline) await new Promise(resolve=>setTimeout(resolve,100));
      if(!active||nativeQueryReply?.query!==query)failure('email_network_list_unqualified');
      value=nativeQueryReply.value;
    } else {
    if (!listRequest) failure('email_network_list_unqualified');
    if (String(listRequest.method).toUpperCase() !== 'POST' || listRequest.url.origin !== location.origin ||
        !Array.isArray(listRequest.body?.[0]) || typeof listRequest.body[0][3] !== 'string' || !Array.isArray(listRequest.body?.[2])) failure('email_network_list_unqualified');
    const body=structuredClone(listRequest.body);
    listStage = 'request_shape';
    // The observed XHR is only a transport template. Bind the replay to the
    // requested receipt interval instead of trusting whatever search happened
    // to be open when the userscript was installed.
    body[0][3]=query;
    if(page>0 && !pageTokens.has(page))failure('email_network_list_unqualified');
    if(!Array.isArray(body[0][15]))failure('email_network_list_unqualified');
    body[0][15][13]=page===0?null:pageTokens.get(page);
    body[0][9]=page; body[0][7]=2000; body[2][0]=0; // Observed native pagination and full-response request.
    const response=await originalFetch.call(window,listRequest.url.href,{method:'POST',credentials:'same-origin',redirect:'error',headers:listRequest.headers,body:JSON.stringify(body),signal:AbortSignal.timeout(20000)});
    listStage = 'response';
    if ([401,403].includes(response.status)) failure('email_login_required');
    if (!response.ok) failure('email_network_read_failed');
    const reader=response.body.getReader(),chunks=[];let length=0;
    try {
      for (;;) {const {done,value}=await reader.read();if(done)break;length+=value.length;if(length>10<<20){void reader.cancel();failure('email_capture_limit');}chunks.push(value);}
    } finally {reader.releaseLock();}
    const bytes=new Uint8Array(length);let offset=0;for(const chunk of chunks){bytes.set(chunk,offset);offset+=chunk.length;}
    try{value=JSON.parse(new TextDecoder().decode(bytes));}catch{failure('email_network_list_unqualified');}
    }
    checkedAccount(account_address);
    listStage = 'response_shape';
    if (!Array.isArray(value) || value?.[0] !== 0 || !Array.isArray(value?.[19]) || ![0,1].includes(value[3])) failure('email_network_list_unqualified');
    if(value[3]===1) {
      const token=value?.[13]?.[8];
      if(typeof token!=='string'||!token||token.length>8192||token!==value?.[13]?.[9])failure('email_network_list_unqualified');
      pageTokens.set(page+1,token);
    }
    const rows=config.parse(value), received=[];let unsupported=0;
    listStage = 'members';
    let rawCount=0;
    for(const batch of value[19]) {
      // Gmail's terminal empty batch is a four-field network envelope with a
      // null thread list. The network contract is the complete evidence source.
      if(value[3]===0&&value[19].length===1&&Array.isArray(batch)&&batch.length===4&&batch[1]===null) continue;
      if(!Array.isArray(batch?.[1]))failure('email_network_list_unqualified');
      for(const container of batch[1]) {
        if(!Array.isArray(container?.[0]?.[4]) || !container[0][4].length)failure('email_network_list_unqualified');
        rawCount+=container[0][4].filter(message=>{const labels=message?.[10];return !(Array.isArray(labels)&&labels.every(label=>typeof label==='string')&&(labels.includes('^r')||labels.includes('^f')));}).length;
      }
    }
    unsupported+=Math.max(0,rawCount-rows.length);
    for(const row of rows) {
      records.set(row.provider_message_id,row);
      if(row.draft||row.sent)continue;
      const at=instantNanos(row.received_at);
      if(at===null){unsupported++;continue;}
      if(at>=start&&at<end)received.push({...row});
    }
    while(records.size>2000)records.delete(records.keys().next().value);
    listStage = 'complete';
    return {provider:config.provider,account_address:account,rows:received,unsupported_rows:unsupported,has_next:value[3]===1,page};
  }
  async function markRead({account_address,provider_message_id} = {}) {
    checkedAccount(account_address);
    if (!idPattern.test(provider_message_id || '') || !records.has(provider_message_id)) failure('email_network_target_unobserved');
    const row=records.get(provider_message_id);
    if (row.unread === false) return {provider:config.provider,account_address:account,provider_message_id,read_state:'read'};
    const result=await config.markRead?.({account_address:account,provider_message_id,row,binding,request:listRequest,fetch:originalFetch});
    if (!result || result.provider_message_id !== provider_message_id || !['read','unknown'].includes(result.read_state)) failure('email_network_mark_read_unqualified');
    checkedAccount(account_address);
    if(result.read_state==='read')row.unread=false;
    return {provider:config.provider,account_address:account,provider_message_id,read_state:result.read_state};
  }
  function dispose() {
    nativeQueryReply=null;
    active = false;originalAbort?.abort();if(objectURL)URL.revokeObjectURL(objectURL);objectURL=null;originalBytes=null;originalMessageID=''; records.clear(); pageTokens.clear(); networkQuery = null; binding = null; listRequest = null; inbox = null; transport?.dispose(); anchor?.remove();
    if (XMLHttpRequest.prototype.open === open) XMLHttpRequest.prototype.open = originalOpen;
    if (XMLHttpRequest.prototype.send === send) XMLHttpRequest.prototype.send = originalSend;
    if (XMLHttpRequest.prototype.setRequestHeader === setHeader) XMLHttpRequest.prototype.setRequestHeader = originalSetHeader;
    if (window.fetch === fetch) window.fetch = originalFetch;
    delete window.SparkClawMailReader;
  }
  transport=config.installTransport?.({account:checkedAccount,getInbox:()=>inbox,receiveStartup(value){inbox=config.parseFolders?.(value)||null;},receiveRows(rows){
    checkedAccount();for(const row of rows){if(row && row.provider_selection_id===undefined)row.provider_selection_id=row.provider_thread_id||row.provider_message_id;if(idPattern.test(row.provider_message_id||''))records.set(row.provider_message_id,row);}
    while(records.size>2000)records.delete(records.keys().next().value);
  }});
  Object.defineProperty(window, 'SparkClawMailReader', {configurable:true, value:Object.freeze({
    armRangeSearch(request){checkedAccount(request.account_address);if(!transport?.armRangeSearch)failure('email_network_list_unqualified');return transport.armRangeSearch(request);},
    version:'0.2.0', provider:config.provider, diagnostics:()=>({original:{state:originalState,responseOrigin},list:{stage:listStage,template:Boolean(listRequest),body:Array.isArray(listRequest?.body),paging:Array.isArray(listRequest?.body?.[0]?.[15])},inbox:Boolean(inbox),records:records.size,transport:transport?.diagnostics?.()}), snapshot, resetRound,
    listPage:request=>inRound(listPage,request), prepareOriginal:request=>inRound(prepareOriginal,request), prepareRetainedOriginal:request=>inRound(prepareRetainedOriginal,request), markRead, dispose,
    verifyTarget({account_address,provider_message_id,provider_selection_id,folder}) {
      checkedAccount(account_address);
      const row=records.get(provider_message_id);
      return Boolean(row && !row.draft && !row.sent && row.provider_selection_id===provider_selection_id && (!folder||folder==='all'||row.folder===folder) && Number.isFinite(Date.parse(row.received_at)));
    },
  })});
})({"provider":"outlook","parseFolders":function parseOutlookFolders(value) {
  const owner=value?.owaUserConfig?.SessionSettings?.UserEmailAddress;
  const root=value?.findFolders?.Body?.ResponseMessages?.Items?.[0]?.RootFolder;
  const inboxID=value?.findConversation?.Body?.FolderId?.Id;
  if(typeof owner!=='string'||typeof inboxID!=='string'||!Array.isArray(root?.Folders))return null;
  const idPattern=/^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/;
  const items=root.Folders.slice(0,2000),byID=new Map(),folders=[];
  let qualified=root.IncludesLastItemInRange===true&&root.TotalItemsInView===items.length&&root.Folders.length<=2000;
  const excluded=new Set(['junkemail','drafts','sentitems','deleteditems','outbox','conversationhistory']);
  const hidden=node=>node?.ExtendedProperty?.find(p=>typeof p?.ExtendedFieldURI?.PropertyTag==='string'&&p.ExtendedFieldURI.PropertyTag.toLowerCase()==='0x10f4'&&p.ExtendedFieldURI.PropertyType==='Boolean')?.Value;
  for(const item of items){const id=item?.FolderId?.Id;if(!idPattern.test(id||'')||byID.has(id)){qualified=false;continue;}byID.set(id,item);}
  for(const item of items) {
    if(!byID.has(item?.FolderId?.Id)||item.FolderClass!=='IPF.Note')continue;
    if(!['true','false'].includes(hidden(item))){qualified=false;continue;}
    if(hidden(item)==='true'||excluded.has(item.DistinguishedFolderId))continue;
    if(item.DistinguishedFolderId&&!['inbox','archive'].includes(item.DistinguishedFolderId)){qualified=false;continue;}
    let parent=item.ParentFolderId?.Id,skip=false;const seen=new Set([item.FolderId.Id]);
    while(parent!==root.ParentFolder?.FolderId?.Id) {
      const ancestor=byID.get(parent);
      if(!ancestor||seen.has(parent)){qualified=false;skip=true;break;}
      seen.add(parent);
      if(excluded.has(ancestor.DistinguishedFolderId)||hidden(ancestor)==='true'){skip=true;break;}
      parent=ancestor.ParentFolderId?.Id;
    }
    if(skip)continue;
    if(typeof item.DisplayName!=='string'||!item.DisplayName||item.DisplayName.length>256){qualified=false;continue;}
    folders.push({id:item.FolderId.Id,name:item.DisplayName,inbox:item.DistinguishedFolderId==='inbox'});
  }
  folders.sort((a,b)=>Number(b.inbox)-Number(a.inbox)||a.id.localeCompare(b.id));
  if(folders.length>100||!folders.some(folder=>folder.inbox&&folder.id===inboxID))qualified=false;
  return {account:owner.toLowerCase(),id:inboxID,folders:folders.slice(0,100),qualified};
},"installTransport":function installOutlookTransport({account, getInbox, receiveRows, receiveStartup}) {
  const existing=window.SparkClawOutlookEarlyBridge;
  const bridge=existing||installOutlookEarlyBridge();
  if(!bridge)return {dispose(){}};
  const templates=new Map(),pending=new Map();
  let active=true;
  const range=installOutlookRangeTransport({account,getInbox,receiveRows});
  const originalResolver=installOutlookOriginalResolver({account,getInbox});
  const fail=code=>{throw Object.assign(new Error(code),{code});};
  const ownerOf=info=>String(info?.mailboxSmtpAddress||info?.userIdentity||'').toLowerCase();
  function request(worker,message) {
    const value=message?.argumentList?.[0]?.value;
    if(!active||message?.type!=='APPLY'||message?.path?.length!==1||message.path[0]!=='execute')return;
    if(['ItemRows','ConversationRows'].includes(value?.operationName)) {
      const info=value.variables?.mailboxInfo;
      if(!info||!value.variables?.pagingInfo)return;
      templates.set(value.operationName+':'+value.variables.folderId,{worker,message:structuredClone(message)});
      if(value.operationName==='ItemRows')templates.set('ItemRows',{worker,message:structuredClone(message)});
    }
  }
  function result(event,port) {
    const message=event.data;
    const id=message?.argumentList?.[0]?.value,waiter=pending.get(id);
    if(!active||!waiter||message.type!=='APPLY'||!['next','complete','error'].includes(message.path?.[0]))return;
    event.stopImmediatePropagation();
    port.postMessage({type:'RAW',id:message.id,value:undefined});
    const value=message.argumentList?.[1]?.value;
    if(value?.data?.itemRows||value?.errors)waiter.resolve(value);
    if(message.path[0]==='error')waiter.reject(Object.assign(new Error('email_network_read_failed'),{code:'email_network_read_failed'}));
  }
  const consumer={request,result,fetchSearch:range.fetchSearch,startup:receiveStartup};bridge.attach(consumer);
  async function listPage({account_address,interval_start,interval_end,page=0,provider_mode}) {
    if(provider_mode==='time_range')return range.listPage({account_address,interval_start,interval_end,page,provider_mode});
    const owner=account(account_address),inbox=getInbox();
    if(location.origin!=='https://outlook.live.com'||!inbox||inbox.account!==owner)fail('email_network_list_unqualified');
    const deadline=Date.now()+5000;
    while(active&&Date.now()<deadline&&(!templates.get('ItemRows')||!templates.get('ConversationRows:'+inbox.id)))await new Promise(resolve=>setTimeout(resolve,100));
    const item=templates.get('ItemRows'),normal=templates.get('ConversationRows:'+inbox.id);
    if(!item||!normal)fail('email_network_list_unqualified');
    const start=Date.parse(interval_start),end=Date.parse(interval_end);
    if(!Number.isFinite(start)||!Number.isFinite(end)||start>=end||!Number.isInteger(page)||page<0||page>409599)fail('invalid_request');
    const folders=inbox.folders?.length?inbox.folders:[{id:inbox.id,inbox:true}],folderIndex=Math.floor(page/4096),offset=page%4096,folder=folders[folderIndex];
    if(!folder)fail('email_network_list_unqualified');
    const message=structuredClone(item.message),body=message.argumentList[0].value,source=normal.message.argumentList[0].value;
    if(ownerOf(source.variables.mailboxInfo)!==owner)fail('email_account_identity_mismatch');
    let id;do{id=700000000+crypto.getRandomValues(new Uint32Array(1))[0]%100000000;}while(pending.has(id));
    message.id=crypto.randomUUID();body.requestId=id;body.context.workerRequestId=id;body.context.forceFetch=true;body.context.queryDeduplication=false;
    body.variables.folderId=folder.id;body.variables.mailboxInfo=source.variables.mailboxInfo;
    body.variables.viewFilter=source.variables.viewFilter;if(body.variables.focusedViewFilter!=='None'||source.variables.viewFilter!=='All')fail('email_network_list_unqualified');
    body.variables.focusedViewFilter='None';
    body.variables.sortBy={...source.variables.sortBy,isDraftsFolder:false};body.variables.pagingInfo={numberOfRows:25,pageOrigin:'Beginning',offset:offset*25};
    let timer;
    const value=await new Promise((resolve,reject)=>{
      pending.set(id,{resolve,reject,operation:'list'});timer=setTimeout(()=>reject(Object.assign(new Error('email_network_read_failed'),{code:'email_network_read_failed'})),20000);
      try{item.worker.postMessage(message);}catch{reject(Object.assign(new Error('email_network_read_failed'),{code:'email_network_read_failed'}));}
    }).finally(()=>clearTimeout(timer));
    account(account_address);
    // Keep this request's acknowledger until page disposal. A cached and a fresh
    // callback can both arrive; neither is forwarded into the site's request map.
    const data=value?.data?.itemRows;
    if(value?.errors?.length||!data||!Array.isArray(data.edges)||typeof data.pageInfo?.hasNextPage!=='boolean'||data.edges.length>25||!Number.isInteger(data.indexedOffset)||data.indexedOffset<offset*25)fail('email_network_list_unqualified');
    const rows=[];let unsupported=0;
    for(const edge of data.edges) {
      const node=edge?.node,id=node?.ItemId?.Id,thread=node?.ConversationId?.Id,received=Date.parse(node?.DateTimeReceived);
      if(!/^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/.test(id||'')||!/^[A-Za-z0-9_+=:.\/~\-]{1,1024}$/.test(thread||'')||typeof node.DateTimeReceived!=='string'||!/^\d{4}-\d{2}-\d{2}T/.test(node.DateTimeReceived)||typeof node.IsDraft!=='boolean'||typeof node.IsRead!=='boolean'||!Number.isFinite(received)||node.ParentFolderId?.Id!==folder.id){unsupported++;continue;}
      const row={provider_message_id:id,provider_selection_id:thread,provider_thread_id:thread,received_at:node.DateTimeReceived,draft:node.IsDraft,unread:!node.IsRead,sent:false,inbox:folder.inbox,folder:folder.inbox?'inbox':'outlook:'+folder.id};
      receiveRows([row]);
      if(!node.IsDraft&&received>=start&&received<end)rows.push(row);
    }
    const scopeBytes=new TextEncoder().encode(JSON.stringify(folders.map(folder=>folder.id)));
    const folder_scope_id=Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',scopeBytes)),n=>n.toString(16).padStart(2,'0')).join('');
    const more=data.pageInfo.hasNextPage||folderIndex+1<folders.length;
    if(data.pageInfo.hasNextPage&&offset===4095)fail('email_network_list_unqualified');
    return {provider:'outlook',account_address:owner,rows,unsupported_rows:unsupported,has_next:more,
      ...(more?{next_page:data.pageInfo.hasNextPage?page+1:(folderIndex+1)*4096}:{}),page,folder_scope_id,scope:inbox.qualified?'inbound_received':'inbox_loaded'};
  }
  async function prepareOriginal({account_address,provider_message_id}) {
    return originalResolver.prepare({account_address,provider_message_id});
  }
  function dispose(){active=false;range.dispose();originalResolver.dispose();bridge.detach(consumer);if(!existing)bridge.dispose();for(const waiter of pending.values())waiter.reject(Object.assign(new Error('email_network_read_failed'),{code:'email_network_read_failed'}));pending.clear();templates.clear();}

  return {listPage,armRangeSearch:range.arm,resetRound:range.resetRound,prepareOriginal,dispose,diagnostics(){return {itemTemplate:templates.has('ItemRows'),inboxTemplate:Boolean(getInbox()&&templates.has('ConversationRows:'+getInbox().id)),templateCount:templates.size,original:originalResolver.diagnostics()};}};
},"origins":["https://outlook.live.com","https://outlook.office.com","https://outlook.office365.com"],"account":()=>{const roots=[...document.querySelectorAll('[role="tree"] [role="treeitem"][aria-level="1"][data-folder-name]')];const addresses=roots.map(n=>{const title=n.getAttribute('title')||'';return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(title)&&[...n.querySelectorAll(':scope > span')].some(child=>child.textContent.trim().toLowerCase()===title.toLowerCase())?title:'';}).filter(Boolean);return addresses.length===1?addresses[0]:document.querySelector('#mectrl_currentAccount_secondary')?.textContent?.trim()||'';},"listURL":u=>/^\/owa\/\d+\/(?:startupdata\.ashx|service\.svc)$/.test(u.pathname),"parse":value=>parseOutlookList(value,true).flatMap(row=>row.members?.map(member=>({...member,provider_thread_id:row.provider_selection_id,provider_selection_id:row.provider_selection_id,...(row.members.length===1?{received_at:row.last_delivery_time}:{}),sent:!member.local}))??(row.provider_message_id?[{...row,received_at:row.last_delivery_time,draft:false,sent:false}]:[])),"markRead":()=>null});
})();

return {state:globalThis.SparkClawMailReader?.version==="0.2.0"?"ready":"unavailable"};}})];
const IPC = Object.freeze({
  config: "sparkclaw:managed-script-config",
  get: "sparkclaw:managed-script-get",
  set: "sparkclaw:managed-script-set",
  menuRegister: "sparkclaw:managed-script-menu-register",
  menuRun: "sparkclaw:managed-script-menu-run",
  status: "sparkclaw:managed-script-status",
});
const EXPORTER_WORLD_ID = 1004;
const callbacks = new Map();

if (window.top === window) installForTopFrame();

function installForTopFrame() {
  const config = ipcRenderer.sendSync(IPC.config);
  const qualificationOrigin = validOrigin(config?.qualification_origin) ? config.qualification_origin : "";
  for (const script of MANAGED_SCRIPTS) {
    if (!matches(script, location)) continue;
    if (script.grants.length === 1 && script.grants[0] === "none") installMainWorld(script);
    else installIsolatedWorld(script);
  }
  if (qualificationOrigin && location.origin === qualificationOrigin) installQualificationProof();
}

function installMainWorld(script) {
  let result;
  try {
    result = contextBridge.executeInMainWorld({
      func: script.install,
      args: [],
    });
  } catch {
    result = { state: "failed" };
  }
  report(script, result?.state === "ready" ? "ready" : "failed", "main");
}

function installIsolatedWorld(script) {
  const apiKey = `sparkclawGM_${script.id.replaceAll("-", "_")}`;
  try {
    contextBridge.exposeInIsolatedWorld(EXPORTER_WORLD_ID, apiKey, Object.freeze({
      get: (key, fallback) => syncValue(IPC.get, script.id, key, fallback),
      set: (key, value) => syncValue(IPC.set, script.id, key, value),
      registerMenu: (label, callback) => {
        if (typeof callback !== "function") throw new TypeError("Menu callback is invalid");
        const response = ipcRenderer.sendSync(IPC.menuRegister, { script_id: script.id, label });
        if (!response?.ok || typeof response.command_id !== "string") throw new Error("Menu registration failed");
        callbacks.set(response.command_id, callback);
        return response.command_id;
      },
    }));
    const code = `"use strict";{const api=globalThis[${JSON.stringify(apiKey)}];` +
      `Object.defineProperties(globalThis,{GM_getValue:{value:(key,fallback)=>api.get(key,fallback)},` +
      `GM_setValue:{value:(key,value)=>api.set(key,value)},` +
      `GM_registerMenuCommand:{value:(label,callback)=>api.registerMenu(label,callback)}});` +
      `${script.source}\n}`;
    void webFrame.executeJavaScriptInIsolatedWorld(EXPORTER_WORLD_ID, [{ code }]).then(
      () => report(script, "ready", "isolated"),
      () => report(script, "failed", "isolated"),
    );
  } catch {
    report(script, "failed", "isolated");
  }
}

function installQualificationProof() {
  const id = "sparkclaw.qualification.managed-script";
  const documentStart = contextBridge.executeInMainWorld({
    func: () => {
      globalThis.__sparkclawDocumentStartProof = document.readyState === "loading";
      return globalThis.__sparkclawDocumentStartProof;
    },
    args: [],
  });
  const script = { id, version: "1", sha256: "0".repeat(64) };
  try {
    const apiKey = "sparkclawQualificationGM";
    contextBridge.exposeInIsolatedWorld(EXPORTER_WORLD_ID, apiKey, Object.freeze({
      get: (key, fallback) => syncValue(IPC.get, id, key, fallback),
      set: (key, value) => syncValue(IPC.set, id, key, value),
      registerMenu: (label, callback) => {
        const response = ipcRenderer.sendSync(IPC.menuRegister, { script_id: id, label });
        if (!response?.ok || typeof callback !== "function") throw new Error("Qualification menu registration failed");
        callbacks.set(response.command_id, callback);
        return response.command_id;
      },
    }));
    const code = `"use strict";{const api=globalThis.${apiKey};` +
      `const previous=api.get("persistent-proof","missing");` +
      `api.set("persistent-proof","stored");` +
      `api.set("previous-proof",previous);` +
      `globalThis.__sparkclawGMProof=api.get("persistent-proof","");` +
      `api.registerMenu("Qualification command",()=>api.set("menu-proof","invoked"));}`;
    void webFrame.executeJavaScriptInIsolatedWorld(EXPORTER_WORLD_ID, [{ code }]).then(
      () => report(script, documentStart ? "ready" : "failed", "qualification"),
      () => report(script, "failed", "qualification"),
    );
  } catch {
    report(script, "failed", "qualification");
  }
}

ipcRenderer.on(IPC.menuRun, (_event, commandID) => {
  const callback = callbacks.get(commandID);
  if (callback) Promise.resolve().then(() => callback()).catch(() => {});
});

function syncValue(channel, scriptID, key, value) {
  const response = ipcRenderer.sendSync(channel, { script_id: scriptID, key, value });
  if (!response?.ok) throw new Error("Managed script storage failed");
  return response.value;
}

function matches(script, currentLocation) {
  return script.noframes && script.matches.some((match) =>
    currentLocation.origin === match.origin && currentLocation.pathname.startsWith(match.pathPrefix));
}

function report(script, state, world) {
  ipcRenderer.send(IPC.status, {
    script_id: script.id,
    version: script.version,
    sha256: script.sha256,
    state,
    world,
  });
}

function validOrigin(value) {
  if (typeof value !== "string" || value.length > 256) return false;
  try {
    const url = new URL(value);
    return url.origin === value && !url.username && !url.password && !url.hash && !url.search;
  } catch {
    return false;
  }
}
