const INITIAL_BOUNDS = Object.freeze({ x: 780, y: 80, width: 640, height: 720 });

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
    this.panelBounds = { ...INITIAL_BOUNDS };
    this.insufficientSpace = false;
    this.window.contentView.addChildView(shieldView);
    shieldView.setBounds(INITIAL_BOUNDS);
    shieldView.setVisible(false);
    shieldView.webContents.on("before-input-event", (event) => event.preventDefault());
    shieldView.webContents.on("context-menu", (event) => event.preventDefault());
    if (explicitSelection) {
      // Native pages survive a workbench reload/crash, but the replacement
      // document starts with its browser closed. React cleanup cannot run when
      // that document is lost; revoke its presentation in the owning process.
      window.webContents.on("did-start-navigation", (details) => {
        if (details.isMainFrame && !details.isSameDocument) this.setPanelVisible(false);
      });
      window.webContents.on("render-process-gone", () => this.setPanelVisible(false));
    }
  }

  add(record) {
    this.records.set(record.tabID, record);
    this.window.contentView.addChildView(record.view, 0);
    record.layoutRevision = 0;
    this.#setBounds(record, this.panelBounds);
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
    const bounds = this.panelBounds;
    this.insufficientSpace = false;
    this.#setBounds(record, bounds);
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
    this.#setBounds(record, this.panelBounds);
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
    this.insufficientSpace = false;
    this.#setBounds(this.presented, this.panelBounds);
    if (this.presented.role === "task") this.shieldView.setBounds(this.panelBounds);
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

  #setBounds(record, bounds) {
    const previous = record.layoutBounds;
    // Position changes do not reflow a page. Size changes invalidate the
    // element snapshot, even if a later resize restores the earlier size.
    if (previous && (previous.width !== bounds.width || previous.height !== bounds.height)) record.layoutRevision++;
    record.layoutBounds = { ...bounds };
    record.view.setBounds(bounds);
  }
}
