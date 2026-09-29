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
      const value = await context.browser.call('inspect', '()=>({title:document.title, heading:document.querySelector("#native-target").textContent})');
      return {data: value.result};
    }},
  }};
}
