import fs from 'node:fs/promises';
import http from 'node:http';
import {BrowserHostClient} from '@infinimesh/app-cli-runtime/host-port';
import {decode, encode} from '@infinimesh/app-cli-runtime/protocol';

export async function assemble({config, authorization}) {
  const bindings = await Promise.all(config.bindings.map(async value => JSON.parse(await fs.readFile(value.path, 'utf8'))));
  const host = new BrowserHostClient({authorization, bindings, transport: request => new Promise((resolve, reject) => {
    const call = http.request({socketPath: config.browser_host_socket, path: '/v1/application-host', method: 'POST',
      headers: {'Content-Type': 'application/json'}}, response => {
      const chunks = []; response.on('data', chunk => chunks.push(chunk)); response.on('error', reject);
      response.on('end', () => {try {const result = decode(Buffer.concat(chunks)); if (result.error) reject(Object.assign(new Error(result.error.code),result.error)); else resolve(result);} catch (error) {reject(error);}});
    });
    call.setTimeout(15000, () => call.destroy(new Error('fixture host timeout')));
    call.on('error', reject); call.end(encode(request));
  })});
  return {bindings, host, handlers: {
    inspect: {async run(input, context) {
      await context.browser.call('navigate', config.fixture_origin + '/fixture');
      const secrets = {RECIPIENT: 'synthetic@example.test', SUBJECT: 'Quotes " and apostrophe \'', BODY: 'literal \\n\nsecond line\n中文'};
      await context.browser.call('setSecrets', secrets);
      context.beforeEffect();
      await context.browser.call('inspect', '()=>{for(const id of ["RECIPIENT","SUBJECT","BODY"]){const n=document.createElement("textarea");n.id=id;document.body.append(n)}return true}');
      await context.browser.call('inspect', '()=>{const n=document.createElement("button");n.setAttribute("data-sc-fixture-action","prepare");n.textContent="Fixture action";n.onclick=()=>{n.dataset.clicked="true"};document.body.append(n);return true}');
      await context.browser.call('click', '[data-sc-fixture-action="prepare"]');
      for (const [key, value] of Object.entries(secrets)) await context.browser.call('fill', '#' + key, value);
      await context.browser.call('inspect', '()=>{const n=document.createElement("div");n.id="EDITOR";n.contentEditable="true";document.body.append(n);return true}');
      await context.browser.call('fill', '#EDITOR', secrets.BODY);
      const value = await context.browser.call('inspect', `()=>({title:document.title, heading:document.querySelector("#native-target").textContent, private_fields:Object.entries(${JSON.stringify(secrets)}).every(([key,value])=>document.getElementById(key).value===value)&&document.getElementById("EDITOR").innerText===${JSON.stringify(secrets.BODY)}})`);
      return {data: value.result};
    }},
  }};
}
