import assert from 'node:assert/strict';
import test from 'node:test';
import {chromium} from 'playwright';
import {PAGE_READ_FUNCTION} from '../src/dom-actions.mjs';
import {normalizePageRead} from '../src/mcp-arguments.mjs';

const enabled = process.env.SPARKCLAW_MAIL_BROWSER_TEST === '1';
async function pageFor(t) {
  const browser = await chromium.launch({headless:true});
  t.after(() => browser.close());
  return browser.newPage();
}

test('bounded page read exposes form ownership after large non-interface markup without changing the page', {skip:!enabled}, async t => {
  const page = await pageFor(t);
  await page.setContent(`<!doctype html><html lang="zh-CN" data-site="fixture"><head>
    <title>Attachment ownership fixture</title><style>/*${'s'.repeat(150000)}*/</style>
    <script>window.constructed=0;customElements.define('fixture-control',class extends HTMLElement{constructor(){super();window.constructed++}})</script>
    <script type="application/json">${'h'.repeat(150000)}</script>
    </head><body data-owner="fixture">
    <script>/*${'b'.repeat(150000)}*/</script><style>/*${'c'.repeat(150000)}*/</style>
    <noscript><input type="file" id="noscript-decoy"></noscript>
    <template><input type="file" id="template-decoy"></template>
    <input type="file" id="outside-decoy" accept="image/*">
    <section data-app-section="MailCompose" data-owner="expected">
      <form id="owned-form"><input type="file" id="attachment" accept=".txt" multiple
        aria-label="Attach files" aria-controls="attachment-list" data-attachment-purpose="file">
        <div contenteditable="true" aria-label="邮件正文">Synthetic body</div>
        <div id="attachment-list" role="list"></div><fixture-control></fixture-control>
        <button type="button" id="listener-proof">Fixture action</button>
      </form>
    </section></body></html>`);
  await page.evaluate(() => {
    window.fixtureClicks=0;
    document.querySelector('#listener-proof').addEventListener('click', () => window.fixtureClicks++);
    window.fixtureMutations=[];
    new MutationObserver(records => window.fixtureMutations.push(...records)).observe(document.documentElement,
      {subtree:true, childList:true, attributes:true, characterData:true});
  });
  const before = await page.evaluate(() => ({html:document.documentElement.outerHTML, constructed:window.constructed}));
  assert.equal(before.html.slice(0,120000).includes('owned-form'),false,'the previous projection loses the entire form');
  const result = await page.evaluate(`(${PAGE_READ_FUNCTION})()`);
  assert.deepEqual(Object.keys(result).sort(), ['html','lang','ready_state','scroll_height','text','title','url']);
  assert.ok(result.html.length <= 120000);
  assert.equal(result.lang,'zh-CN');
  assert.equal(result.title,'Attachment ownership fixture');
  assert.ok(result.text.includes('Synthetic body'));
  const structure = await page.evaluate(raw => {
    const parsed = new DOMParser().parseFromString(raw,'text/html');
    const input=parsed.querySelector('#attachment');
    return {
      hiddenSources:parsed.querySelectorAll('script,style,noscript,template,link,meta').length,
      files:parsed.querySelectorAll('input[type="file"]').length,
      ownedFiles:parsed.querySelector('[data-app-section="MailCompose"]').querySelectorAll('input[type="file"]').length,
      form:input.form.id, owner:input.closest('[data-owner]').getAttribute('data-owner'),
      accept:input.accept, multiple:input.multiple, label:input.getAttribute('aria-label'),
      controls:input.getAttribute('aria-controls'), purpose:input.getAttribute('data-attachment-purpose'),
      htmlSite:parsed.documentElement.getAttribute('data-site'),bodyOwner:parsed.body.getAttribute('data-owner'),
    };
  },result.html);
  assert.deepEqual(structure, {hiddenSources:0,files:2,ownedFiles:1,form:'owned-form',owner:'expected',
    accept:'.txt',multiple:true,label:'Attach files',controls:'attachment-list',purpose:'file',htmlSite:'fixture',bodyOwner:'fixture'});
  assert.equal(/<head(?:\s|>)/u.test(result.html),false);
  const after = await page.evaluate(() => ({html:document.documentElement.outerHTML, constructed:window.constructed,
    mutations:window.fixtureMutations.length,clicks:window.fixtureClicks}));
  assert.deepEqual(after,{...before,mutations:0,clicks:0},'projection must not mutate DOM or run custom-element constructors');
  await page.click('#listener-proof');
  assert.equal(await page.evaluate(() => window.fixtureClicks),1,'the original listener is preserved');
});

test('page read retains both the hard 120k bounds and the caller truncation', {skip:!enabled}, async t => {
  const page = await pageFor(t);
  await page.setContent(`<html><head><script type="application/json">${'h'.repeat(150000)}</script></head><body><main>${'x'.repeat(150000)}</main></body></html>`);
  assert.deepEqual(await page.evaluate(`(${PAGE_READ_FUNCTION})(document.createElement('div'))`),{error:'browser_page_stale'});
  assert.deepEqual(await page.evaluate(`(${PAGE_READ_FUNCTION})(document.implementation.createHTMLDocument('').body)`),{error:'browser_page_stale'});
  const result = await page.evaluate(`(${PAGE_READ_FUNCTION})()`);
  assert.equal(result.html.length,120000);
  assert.equal(result.text.length,120000);
  assert.ok(result.html.startsWith('<html><body><main>'));
  const bounded = normalizePageRead(result,32);
  assert.equal(bounded.html.length,32);
  assert.equal(bounded.text.length,32);
  assert.equal(bounded.text_truncated,true);
  assert.equal(bounded.text_length,120000);
});
