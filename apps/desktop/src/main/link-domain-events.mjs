// Business-domain invalidations over the link layer: the backend pushes a
// "domain" event naming the domains that changed, and every (re)connection
// reads all of them again, because events only accelerate (link contract
// section 5). Each domain client's own read stays the truth.
const CATEGORIES = Object.freeze({ mail: "email", notifications: "notifications", medicine: "medicine", settings: "settings" });
const ALL = Object.freeze(["tasks", "approvals", "notifications", "settings", "email", "medicine"]);

export class LinkDomainEvents {
  constructor({ auth, onEvents = () => {} }) {
    Object.assign(this, { auth, onEvents });
    this.onFrame = (frame) => {
      if (this.closed || frame?.topic !== "domain" || !Array.isArray(frame.data?.domains)) return;
      const categories = [...new Set(frame.data.domains.map((domain) => CATEGORIES[domain]).filter(Boolean))];
      if (categories.length) this.onEvents({ categories });
    };
    this.closed = true;
  }

  // Called on every connected state.
  start() {
    if (this.closed) { this.closed = false; this.auth.link.on("event", this.onFrame); }
    this.onEvents({ categories: [...ALL] });
  }

  close() {
    if (this.closed) return;
    this.closed = true;
    this.auth.link.off("event", this.onFrame);
  }
}
