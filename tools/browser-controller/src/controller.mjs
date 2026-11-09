import { BrowserController as CoreController } from "./controller-core.mjs";
import { MailObserverFeed } from "./mail-observer-feed.mjs";

// Backend assembly retains mail observers; the client imports the generic core.
export class BrowserController extends CoreController {
  constructor(options) {
    super({ ...options, mailObserverFactory: controller => new MailObserverFeed(controller) });
  }
}
