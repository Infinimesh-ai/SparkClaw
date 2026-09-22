const { app, BrowserWindow } = require("electron");
const { execFileSync } = require("node:child_process");
const fs = require("node:fs");
const path = require("node:path");
const readline = require("node:readline");

function fail(message) {
  process.stderr.write(`electron-anchor: ${message}\n`);
  process.exit(2);
}

const display = process.env.DISPLAY || "";
const isolatedDisplay = process.env.SPARKCLAW_SURFACE_TEST_XSERVER_DISPLAY || "";
const originalDisplay = process.env.SPARKCLAW_SURFACE_TEST_ORIGINAL_DISPLAY || "";
const xserverPID = process.env.SPARKCLAW_SURFACE_TEST_XSERVER_PID || "";
if (process.env.SPARKCLAW_SURFACE_TEST_ISOLATED !== "1" || !display ||
    display !== isolatedDisplay || display === originalDisplay || !/^\d+$/u.test(xserverPID)) {
  fail("a process-owned disposable Xvfb display is required");
}
let xserverCommandLine;
try {
  xserverCommandLine = fs.readFileSync(`/proc/${xserverPID}/cmdline`).toString().replaceAll("\0", " ");
} catch {
  fail("the registered X server process is unavailable");
}
if (!xserverCommandLine.includes("Xvfb") || !xserverCommandLine.includes("-nolisten tcp")) {
  fail("the registered X server is not a TCP-disabled Xvfb");
}

const marker = process.env.SPARKCLAW_ELECTRON_ANCHOR_MARKER || "";
const tempRootValue = process.env.SPARKCLAW_ELECTRON_ANCHOR_TEMP_ROOT || "";
const userDataValue = process.env.SPARKCLAW_ELECTRON_ANCHOR_USER_DATA_DIR || "";
if (!/^[0-9a-f]{32,128}$/u.test(marker) || !path.isAbsolute(tempRootValue) ||
    !path.isAbsolute(userDataValue)) {
  fail("marker and disposable data paths are invalid");
}

fs.mkdirSync(tempRootValue, { recursive: true, mode: 0o700 });
fs.mkdirSync(userDataValue, { recursive: true, mode: 0o700 });
const tempRoot = fs.realpathSync(tempRootValue);
const userData = fs.realpathSync(userDataValue);
const relativeUserData = path.relative(tempRoot, userData);
if (!relativeUserData || relativeUserData.startsWith("..") || path.isAbsolute(relativeUserData)) {
  fail("user data must be a child of the disposable test root");
}

app.disableHardwareAcceleration();
app.commandLine.appendSwitch("ozone-platform", "x11");
app.commandLine.appendSwitch("disable-background-networking");
app.commandLine.appendSwitch("disable-component-update");
app.commandLine.appendSwitch("disable-default-apps");
app.commandLine.appendSwitch("disable-sync");
app.commandLine.appendSwitch("metrics-recording-only");
app.setPath("userData", userData);

let anchorWindow;

function nativeWindowID(window) {
  const handle = window.getNativeWindowHandle();
  if (handle.length < 4) fail("native X11 handle is unavailable");
  const value = handle.readUInt32LE(0);
  if (!value) fail("native X11 handle is zero");
  return `0x${value.toString(16)}`;
}

function setIdentity(windowID) {
  const assignments = [
    ["_NET_WM_PID", "32c", String(process.pid)],
    ["_SPARKCLAW_SURFACE_MARKER", "8s", marker],
    ["_SPARKCLAW_SURFACE_ROLE", "8s", "anchor"],
  ];
  for (const [name, format, value] of assignments) {
    execFileSync("xprop", ["-id", windowID, "-f", name, format, "-set", name, value], {
      stdio: ["ignore", "ignore", "pipe"],
    });
  }
}

function emit(value) {
  process.stdout.write(`${JSON.stringify(value)}\n`);
}

app.whenReady().then(async () => {
  anchorWindow = new BrowserWindow({
    x: 80,
    y: 60,
    width: 640,
    height: 480,
    useContentSize: true,
    frame: false,
    show: false,
    backgroundColor: "#243447",
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });
  anchorWindow.setMenuBarVisibility(false);
  await anchorWindow.loadURL(
    "data:text/html;charset=utf-8," + encodeURIComponent(
      "<!doctype html><meta charset=utf-8><title>Electron anchor fixture</title>" +
      "<style>html,body{height:100%;margin:0;background:#243447;color:white}" +
      "body{display:grid;place-items:center;font:20px sans-serif}</style>" +
      "<body><button autofocus>Disposable Electron anchor</button></body>"
    )
  );
  anchorWindow.show();
  const windowID = nativeWindowID(anchorWindow);
  setIdentity(windowID);
  emit({
    event: "ready",
    window: windowID,
    pid: process.pid,
    exe: process.execPath,
    user_data_dir: userData,
    electron: process.versions.electron,
    chromium: process.versions.chrome,
  });

  const input = readline.createInterface({ input: process.stdin, terminal: false });
  input.on("line", (line) => {
    let request;
    try {
      request = JSON.parse(line);
    } catch {
      emit({ event: "error", code: "invalid_json" });
      return;
    }
    if (request?.command === "focus") {
      anchorWindow.focus();
      emit({ event: "focused" });
    } else if (request?.command === "quit") {
      emit({ event: "quitting" });
      app.quit();
    } else {
      emit({ event: "error", code: "invalid_command" });
    }
  });
}).catch((error) => fail(error?.stack || String(error)));

app.on("window-all-closed", () => app.quit());
