const TASK_BOUNDS = Object.freeze({ x: 780, y: 80, width: 640, height: 720 });
const TASK_VIEWPORT = Object.freeze({ width: 640, height: 720 });

export class BrowserPresentation {
  constructor({ window, shieldView, explicitSelection = false }) {
    this.window = window;
    // The workbench owns opening/selecting tabs. Background host activity may
    // update a retained view, but cannot reveal it over a collapsed panel/modal.
    // Task-only qualification windows retain their existing automatic mode.
    this.panelVisible = !explicitSelection;
    this.explicitSelection = explicitSelection;
    this.shieldView = shieldView;
    this.records = new Map();
    this.presented = null;
    this.panelBounds = { ...TASK_BOUNDS };
    this.insufficientSpace = false;
    this.window.contentView.addChildView(shieldView);
    shieldView.setBounds(TASK_BOUNDS);
    shieldView.setVisible(false);
    shieldView.webContents.on("before-input-event", (event) => event.preventDefault());
    shieldView.webContents.on("context-menu", (event) => event.preventDefault());
  }

  add(record) {
    this.records.set(record.tabID, record);
    this.window.contentView.addChildView(record.view, 0);
    record.view.setBounds(TASK_BOUNDS);
    record.view.setVisible(false);
  }

  remove(record) {
    if (this.presented?.tabID === record.tabID) {
      this.presented = null;
      this.shieldView.setVisible(false);
    }
    this.records.delete(record.tabID);
    try {
      this.window.contentView.removeChildView(record.view);
    } catch {
      // A destroyed renderer may already have detached its view.
    }
  }

  setPanelVisible(visible) {
    this.panelVisible = visible;
    if (!visible) this.#hidePresented();
  }

  showTask(record, explicit = false) {
    if (explicit) this.panelVisible = true;
    if (!this.panelVisible || (this.explicitSelection && !explicit && this.presented?.tabID !== record.tabID)) return;
    this.#hidePresented();
    this.presented = record;
    const bounds = this.#taskBounds();
    this.insufficientSpace = !bounds;
    if (!bounds) return;
    record.view.setBounds(bounds);
    record.view.setVisible(true);
    this.shieldView.setBounds(bounds);
    this.window.contentView.addChildView(this.shieldView);
    this.shieldView.setVisible(true);
    if (!this.explicitSelection) this.shieldView.webContents.focus();
  }

  showPersonal(record, explicit = false) {
    if (explicit) this.panelVisible = true;
    if (!this.panelVisible || (this.explicitSelection && !explicit && this.presented?.tabID !== record.tabID)) return;
    this.#hidePresented();
    this.presented = record;
    this.insufficientSpace = false;
    this.shieldView.setVisible(false);
    record.view.setBounds(this.panelBounds);
    record.view.setVisible(true);
    // Workbench DOM focus (for example a dialog trigger or omnibox) survives
    // presentation recovery. Native content acquires focus when the user clicks it.
    if (!this.explicitSelection) record.webContents.focus();
  }

  hideTask() {
    if (this.presented?.role !== "task") return;
    this.presented.view.setVisible(false);
    this.presented = null;
    this.shieldView.setVisible(false);
  }

  hidePresented() {
    this.#hidePresented();
  }

  redirectTaskFocus() {
    if (this.presented?.role === "task" && !this.shieldView.webContents.isDestroyed()) {
      this.shieldView.webContents.focus();
    }
  }

  isPresented(record) {
    return this.presented?.tabID === record.tabID;
  }

  bounds() {
    return { ...this.panelBounds };
  }

  setPanelBounds(bounds) {
    this.panelBounds = { ...bounds };
    if (!this.presented) return;
    if (this.presented.role === "personal") {
      this.insufficientSpace = false;
      this.presented.view.setBounds(this.panelBounds);
      return;
    }
    const taskBounds = this.#taskBounds();
    this.insufficientSpace = !taskBounds;
    this.presented.view.setVisible(Boolean(taskBounds));
    this.shieldView.setVisible(Boolean(taskBounds));
    if (taskBounds) {
      this.presented.view.setBounds(taskBounds);
      this.shieldView.setBounds(taskBounds);
    }
  }

  status() {
    return {
      panel_bounds: { ...this.panelBounds },
      insufficient_space: this.insufficientSpace,
      presented_page_ref: this.presented?.pageRef ?? "",
    };
  }

  #hidePresented() {
    if (this.presented) this.presented.view.setVisible(false);
    this.presented = null;
    this.shieldView.setVisible(false);
  }

  #taskBounds() {
    if (this.panelBounds.width < TASK_VIEWPORT.width || this.panelBounds.height < TASK_VIEWPORT.height) return null;
    return {
      x: this.panelBounds.x + Math.floor((this.panelBounds.width - TASK_VIEWPORT.width) / 2),
      y: this.panelBounds.y + Math.floor((this.panelBounds.height - TASK_VIEWPORT.height) / 2),
      ...TASK_VIEWPORT,
    };
  }
}
