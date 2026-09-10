import {invalidRequest} from './errors.mjs';

export const AI_PLATFORM_URLS = Object.freeze({chatgpt:'https://chatgpt.com/',claude:'https://claude.ai/',gemini:'https://gemini.google.com/',grok:'https://grok.com/'});
export function aiPlatformURL(provider) {
  if (!Object.hasOwn(AI_PLATFORM_URLS,provider)) throw invalidRequest('ai_platform_invalid');
  return AI_PLATFORM_URLS[provider];
}

// Read only visible authentication evidence; never inspect userscripts, cookies or message text.
export function inspectAILoginPage() {
  const visible = node => !!node && node.getClientRects().length > 0 && getComputedStyle(node).visibility !== 'hidden';
  const exists = selector => [...document.querySelectorAll(selector)].some(visible);
  const labels = [...document.querySelectorAll('button,a,[role="button"]')].filter(visible).map(node => (node.getAttribute('aria-label') || node.textContent || '').trim().slice(0,180));
  const login = labels.some(s => /^(log in|login|sign in|continue with google|continue with apple|登录|登入|使用 Google 继续)$/iu.test(s));
  const profile = exists('[data-testid="accounts-profile-button"], [data-testid="user-menu-button"], [aria-label*="profile menu" i], [aria-label*="Google Account" i], [aria-label*="Google 账号"], [aria-label*="user menu" i], [aria-label*="account menu" i]');
  const composer = exists('textarea, [contenteditable="true"][role="textbox"], #prompt-textarea, .ProseMirror[contenteditable="true"], rich-textarea');
  const challenge = exists('iframe[src*="challenges.cloudflare.com"], iframe[src*="recaptcha"][title*="challenge" i]') || /\/challenge(?:\/|$)/u.test(location.pathname);
  return {origin:location.origin, path:location.pathname, login, profile, composer, challenge};
}
export function classifyAILogin(provider,evidence) {
  const origin=new URL(aiPlatformURL(provider)).origin;
  if (!evidence || typeof evidence.origin!=='string') return 'unconfirmed';
  if (evidence.challenge) return 'user_action_required';
  if (evidence.origin!==origin) {
    if (['https://accounts.google.com','https://auth.openai.com','https://auth.grok.com','https://accounts.x.ai'].includes(evidence.origin)) return 'user_action_required';
    return 'unconfirmed';
  }
  if (evidence.login || /\/(login|signin|logout)(?:\/|$)/iu.test(evidence.path)) return 'signed_out';
  if (evidence.profile === true && evidence.composer === true) return 'signed_in';
  return 'unconfirmed';
}
