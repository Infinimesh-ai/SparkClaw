import { SparkClawBrowserBridge } from "./background.mjs";
import { BRIDGE_VERSION } from "./protocol.mjs";

const BOOTSTRAP_VERSION = "1.0.18";
if (BRIDGE_VERSION !== BOOTSTRAP_VERSION) throw new Error("Browser Bridge version mismatch");
new SparkClawBrowserBridge();
