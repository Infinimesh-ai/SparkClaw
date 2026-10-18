import test from "node:test";
import assert from "node:assert/strict";
import { BrowserPresentation } from "../src/main/presentation.mjs";

function fixture(explicitSelection = true) {
  const view = () => ({
    visible: false, focused: false,
    setBounds() {}, setVisible(value) { this.visible = value; },
    webContents: { on() {}, focus() { this.focused = true; }, isDestroyed: () => false },
  });
  const shield = view();
  const presentation = new BrowserPresentation({
    window: { contentView: { addChildView() {}, removeChildView() {} } }, shieldView: shield, explicitSelection,
  });
  const record = id => {
    const nativeView = view();
    const page = { tabID: id, pageRef: id, role: "personal", view: nativeView, webContents: nativeView.webContents };
    presentation.add(page); return page;
  };
  return { presentation, shield, first: record("first"), second: record("second") };
}

test("workbench presentation requires explicit selection and preserves DOM focus after overlays", () => {
  const { presentation, first, second } = fixture();
  presentation.showPersonal(first);
  assert.equal(first.view.visible, false);
  presentation.showPersonal(first, true);
  assert.equal(first.view.visible, true);
  presentation.showPersonal(second);
  assert.equal(presentation.presented, first);
  assert.equal(second.view.visible, false);
  presentation.setPanelVisible(false);
  presentation.showPersonal(first);
  assert.equal(first.view.visible, false);
  presentation.showPersonal(first, true);
  assert.equal(first.view.visible, true);
  assert.equal(first.webContents.focused, undefined, "automatic native focus must not steal the restored dialog trigger");
});

test("task-only qualification retains automatic presentation and shield focus", () => {
  const { presentation, shield, first } = fixture(false);
  first.role = "task";
  presentation.showTask(first);
  assert.equal(first.view.visible, true);
  assert.equal(shield.visible, true);
  assert.equal(shield.webContents.focused, true);
});
