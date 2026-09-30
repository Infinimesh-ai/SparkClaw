import {BrowserController as Controller} from '../../src/controller.mjs';

// Scheduler unit tests replace the external application host, not Registry.
// The fake backend deliberately enters through the same public reservation
// API that ApplicationHostDriver uses. Production runScript owns no lane.
export class BrowserController extends Controller {
  constructor(options) {
    super(options);
    const factory = options.scriptFactory;
    if (!factory) return;
    const run = factory.runScript.bind(factory), close = factory.close.bind(factory);
    const work = new Set();
    factory.runScript = args => {
      const pending = (async () => {
        const exclusive = args.operation === 'send';
        const reservation = await this.reserveApplication({taskID: args.taskID, resource: args.provider,
          exclusive, signal: args.signal, waitMS: args.waitMS ?? 0});
        try {
          if (exclusive) await factory.drainIdleMailReads();
          return await run({...args, sessionID: reservation.sessionID,
            sessionGeneration: reservation.sessionGeneration, controllerGeneration: this.controllerGeneration,
            signal: args.signal ? AbortSignal.any([args.signal, reservation.abortController.signal]) : reservation.abortController.signal});
        } finally {this.finishApplication(reservation);}
      })();
      work.add(pending); void pending.finally(() => work.delete(pending)).catch(() => {});
      return pending;
    };
    factory.close = async () => {await Promise.allSettled([...work]); await close();};
  }
}
