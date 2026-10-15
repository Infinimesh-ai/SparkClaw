export const inspectFixture = {async run(input, context) {
      await context.browser.call('navigate', input.fixture_origin + '/fixture');
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
    }};
