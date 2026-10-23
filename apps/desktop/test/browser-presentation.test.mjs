import test from "node:test";
import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { BrowserPresentation } from "../src/main/presentation.mjs";

function fixture(explicitSelection = true) {
  const view = () => ({
    visible: false, focused: false,
    setBounds(bounds) { this.bounds = { ...bounds }; }, setVisible(value) { this.visible = value; },
    webContents: { on() {}, focus() { this.focused = true; }, isDestroyed: () => false },
  });
  const shield = view();
  const workbench = new EventEmitter();
  const presentation = new BrowserPresentation({
    window: { webContents: workbench, contentView: { addChildView() {}, removeChildView() {} } }, shieldView: shield, explicitSelection,
  });
  const record = id => {
    const nativeView = view();
    const page = { tabID: id, pageRef: id, role: "personal", view: nativeView, webContents: nativeView.webContents };
    presentation.add(page); return page;
  };
  return { presentation, shield, workbench, first: record("first"), second: record("second") };
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

test("task pages fill small and large panels without recreating pages or losing the shield", () => {
  const { presentation, shield, first, second } = fixture();
  first.role = "task";
  presentation.showTask(first, true);
  for (const bounds of [{ x: 700, y: 120, width: 480, height: 420 }, { x: 300, y: 95, width: 1100, height: 800 }]) {
    presentation.setPanelBounds(bounds);
    assert.deepEqual(first.view.bounds, bounds);
    assert.deepEqual(shield.bounds, bounds);
    assert.equal(first.view.visible, true);
    assert.equal(shield.visible, true);
    assert.equal(presentation.status().insufficient_space, false);
    assert.equal(presentation.records.get(first.tabID), first);
  }
  const revision = first.layoutRevision;
  presentation.setPanelBounds({ ...first.view.bounds, x: 310 });
  assert.equal(first.layoutRevision, revision, "moving a viewport does not reflow its content");
  presentation.showPersonal(second, true);
  presentation.setPanelBounds({ x: 300, y: 95, width: 420, height: 360 });
  assert.equal(first.layoutRevision, revision, "hidden retained pages keep their last layout");
  presentation.showTask(first, true);
  assert.deepEqual(first.view.bounds, presentation.bounds());
  assert.equal(first.layoutRevision, revision + 1);
  presentation.setPanelVisible(false);
  presentation.setPanelBounds({ x: 300, y: 95, width: 500, height: 400 });
  assert.equal(first.view.visible, false, "resizing cannot reopen a collapsed task page");
  assert.equal(shield.visible, false);
});

for (const role of ["personal", "task"]) {
  for (const event of ["did-start-navigation", "render-process-gone"]) {
    test(`${role} presentation is released on workbench ${event} until explicitly selected again`, () => {
      const { presentation, shield, workbench, first } = fixture();
      first.role = role;
      const show = explicit => role === "task" ? presentation.showTask(first, explicit) : presentation.showPersonal(first, explicit);
      show(true);
      workbench.emit("did-start-navigation", { isMainFrame: false, isSameDocument: false });
      workbench.emit("did-start-navigation", { isMainFrame: true, isSameDocument: true });
      assert.equal(first.view.visible, true, "subframes and same-document routes keep the selected page");
      workbench.emit(event, { isMainFrame: true, isSameDocument: false });
      assert.equal(first.view.visible, false);
      assert.equal(shield.visible, false);
      assert.equal(presentation.status().presented_page_ref, "");
      show(false);
      assert.equal(first.view.visible, false, "background activity cannot restore a lost selection");
      assert.equal(presentation.records.get(first.tabID), first, "the retained page is not destroyed");
      show(true);
      assert.equal(first.view.visible, true);
      assert.equal(shield.visible, role === "task");
    });
  }
}
