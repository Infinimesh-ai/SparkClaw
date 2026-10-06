import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import test from "node:test";
import { bindWorkbenchActivation } from "../src/main/workbench-activation.mjs";

for (const event of ["activate", "second-instance"]) {
  for (const initial of ["hidden", "minimized", "visible"]) {
    test(`${event} restores and focuses a ${initial} workbench`, () => {
      const app = new EventEmitter();
      const actions = [];
      let minimized = initial === "minimized";
      let visible = initial === "visible";
      const window = {
        isDestroyed: () => false,
        isMinimized: () => minimized,
        isVisible: () => visible,
        restore: () => { actions.push("restore"); minimized = false; visible = true; },
        show: () => { actions.push("show"); visible = true; },
        focus: () => actions.push("focus"),
      };
      bindWorkbenchActivation(app, () => window);
      app.emit(event);
      assert.equal(visible, true);
      assert.equal(minimized, false);
      assert.deepEqual(actions, initial === "hidden" ? ["show", "focus"]
        : initial === "minimized" ? ["restore", "focus"] : ["focus"]);
      actions.length = 0;
      app.emit(event);
      assert.deepEqual(actions, ["focus"]);
    });
  }

  test(`${event} tolerates an unavailable workbench`, () => {
    const app = new EventEmitter();
    let window;
    bindWorkbenchActivation(app, () => window);
    assert.doesNotThrow(() => app.emit(event));
    window = { isDestroyed: () => true };
    assert.doesNotThrow(() => app.emit(event));
  });
}
