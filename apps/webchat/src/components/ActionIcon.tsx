import type { ReactNode } from "react";

export type ActionIconName =
  | "bell"
  | "mail"
  | "calendar"
  | "search"
  | "compose"
  | "upload"
  | "document"
  | "microphone"
  | "microphone-off"
  | "panel-left"
  | "panel-right"
  | "settings"
  | "account"
  | "arrow-up"
  | "globe"
  | "appearance"
  | "devices"
  | "models"
  | "permissions"
  | "connections"
  | "memory"
  | "approvals"
  | "timeline"
  | "circle-alert"
  | "message-check"
  | "info"
  | "inbox"
  | "archive";

type IconPair = { outline: ReactNode; solid: ReactNode };

const alertPair: IconPair = {
  outline: <><circle cx="12" cy="12" r="9" /><path d="M12 7v6m0 4h.01" /></>,
  solid: <path fillRule="evenodd" d="M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20Zm-.9 5.2a.9.9 0 0 1 1.8 0v5.6a.9.9 0 0 1-1.8 0V7.2Zm.9 8.1a1.2 1.2 0 1 0 0 2.4 1.2 1.2 0 0 0 0-2.4Z" />
};

// Share the key's contour and hole between both states so selecting it only
// changes its weight, without moving the shaft or introducing another tooth.
const deviceKeyContour = "M12.5826 6.75H16.75V4.9Q16.75 4.5 17.15 4.5H18.85Q19.25 4.5 19.25 4.9V6.75H24.5Q25.25 6.75 25.25 7.5V8.5Q25.25 9.25 24.5 9.25H12.5826A4.75 4.75 0 1 1 12.5826 6.75Z";
const deviceKeyPath = `${deviceKeyContour}M8 6.65a1.35 1.35 0 1 0 0 2.7a1.35 1.35 0 1 0 0-2.7Z`;

// Each filled drawing has its own silhouette. Transparent details keep the
// icons legible on both opaque surfaces and translucent controls.
const icons: Record<ActionIconName, IconPair> = {
  appearance: {
    outline: <><path d="M21 11.5C21 6.3 17 3 12 3a9 9 0 0 0 0 18h1.2a2 2 0 0 0 1.5-3.3 1.8 1.8 0 0 1 1.4-3H18a3 3 0 0 0 3-3.2Z" /><circle cx="7.5" cy="11" r=".8" /><circle cx="10" cy="7" r=".8" /><circle cx="15" cy="7.5" r=".8" /></>,
    solid: <path fillRule="evenodd" d="M12 2.2a9.8 9.8 0 0 0 0 19.6h1.2a2.8 2.8 0 0 0 2.1-4.6 1 1 0 0 1 .8-1.7H18a3.8 3.8 0 0 0 3.8-4c0-5.7-4.5-9.3-9.8-9.3ZM7.5 9.7a1.3 1.3 0 1 0 0 2.6 1.3 1.3 0 0 0 0-2.6ZM10 5.7a1.3 1.3 0 1 0 0 2.6 1.3 1.3 0 0 0 0-2.6Zm5 .5a1.3 1.3 0 1 0 0 2.6 1.3 1.3 0 0 0 0-2.6Z" />
  },
  devices: {
    outline: <path d={deviceKeyPath} transform="rotate(45 8 8)" />,
    solid: <g transform="rotate(45 8 8)"><path d={deviceKeyPath} fillRule="evenodd" /><path d={deviceKeyContour} fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" /></g>
  },
  models: {
    outline: <><rect x="4" y="7" width="16" height="13" rx="4" /><path d="M12 3v4M2 12v4m20-4v4M9 16h6" /><circle cx="8.5" cy="12" r=".5" /><circle cx="15.5" cy="12" r=".5" /></>,
    solid: <><path fillRule="evenodd" d="M8 6.2A4.8 4.8 0 0 0 3.2 11v5A4.8 4.8 0 0 0 8 20.8h8a4.8 4.8 0 0 0 4.8-4.8v-5A4.8 4.8 0 0 0 16 6.2H8Zm.5 4.3a1.2 1.2 0 1 0 0 2.4 1.2 1.2 0 0 0 0-2.4Zm7 0a1.2 1.2 0 1 0 0 2.4 1.2 1.2 0 0 0 0-2.4ZM9 15.2a.8.8 0 0 0 0 1.6h6a.8.8 0 0 0 0-1.6H9Z" /><rect x="11.1" y="2.2" width="1.8" height="4.5" rx=".9" /><rect x="1" y="11" width="1.6" height="6" rx=".8" /><rect x="21.4" y="11" width="1.6" height="6" rx=".8" /></>
  },
  permissions: {
    outline: <><path d="m12 3 8 3v6c0 4.5-3.1 7.2-8 9-4.9-1.8-8-4.5-8-9V6l8-3Z" /><path d="m8.5 12 2.4 2.4 4.6-4.8" /></>,
    solid: <path fillRule="evenodd" d="m12 2.1 8.8 3.3V12c0 5-3.5 8-8.8 9.9C6.7 20 3.2 17 3.2 12V5.4L12 2.1Zm4.1 6.9a.9.9 0 0 0-1.3 0l-3.9 4.1-1.8-1.8a.9.9 0 0 0-1.2 1.3l2.4 2.4a.9.9 0 0 0 1.3 0l4.6-4.8a.9.9 0 0 0-.1-1.2Z" />
  },
  connections: {
    outline: <><path d="M8 3v5m8-5v5M5 8h14v3a7 7 0 0 1-7 7v3M8 8v3m8-3v3" /></>,
    solid: <><rect x="7" y="2" width="2" height="6" rx="1" /><rect x="15" y="2" width="2" height="6" rx="1" /><path d="M5 7.2a.8.8 0 0 0-.8.8v3a7.8 7.8 0 0 0 6.8 7.7V21a1 1 0 0 0 2 0v-2.3a7.8 7.8 0 0 0 6.8-7.7V8a.8.8 0 0 0-.8-.8H5Z" /></>
  },
  memory: {
    outline: <><path d="M12 5a3.5 3.5 0 0 0-6.8-1 4 4 0 0 0-2 6.5 4 4 0 0 0 .8 6.9A4.5 4.5 0 0 0 12 20V5Zm0 0a3.5 3.5 0 0 1 6.8-1 4 4 0 0 1 2 6.5 4 4 0 0 1-.8 6.9A4.5 4.5 0 0 1 12 20" /><path d="M6 7c2 0 3 1 3 3m-5 4c2 0 3 1 3 3m11-10c-2 0-3 1-3 3m5 4c-2 0-3 1-3 3" /></>,
    solid: <><path d="M11.2 4.7a2.7 2.7 0 0 0-5.3-.5A3.8 3.8 0 0 0 3 10.3a3.8 3.8 0 0 0 1.7 6.6 3.7 3.7 0 0 0 6.5 3V4.7Z" /><path d="M12.8 4.7a2.7 2.7 0 0 1 5.3-.5 3.8 3.8 0 0 1 2.9 6.1 3.8 3.8 0 0 1-1.7 6.6 3.7 3.7 0 0 1-6.5 3V4.7Z" /></>
  },
  approvals: alertPair,
  "circle-alert": alertPair,
  "message-check": {
    outline: <><path d="M7 3.5h10A3.5 3.5 0 0 1 20.5 7v8a3.5 3.5 0 0 1-3.5 3.5H8L3.5 22V7A3.5 3.5 0 0 1 7 3.5Z" /><path d="m8.25 11 2.5 2.5 5-5" /></>,
    solid: <><path fillRule="evenodd" d="M7 3.5h10A3.5 3.5 0 0 1 20.5 7v8a3.5 3.5 0 0 1-3.5 3.5H8L3.5 22V7A3.5 3.5 0 0 1 7 3.5ZM7.6 10.4a.9.9 0 0 1 1.3 0l1.9 1.9 4.3-4.3a.9.9 0 0 1 1.3 1.3l-5 5a.9.9 0 0 1-1.3 0l-2.5-2.5a.9.9 0 0 1 0-1.4Z" /><path d="M7 3.5h10A3.5 3.5 0 0 1 20.5 7v8a3.5 3.5 0 0 1-3.5 3.5H8L3.5 22V7A3.5 3.5 0 0 1 7 3.5Z" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinejoin="round" /></>
  },
  timeline: {
    outline: <><path d="M3.5 9a9 9 0 1 1 0 6M3 3v6h6" /><path d="M12 7v5l3.5 2" /></>,
    solid: <><path fillRule="evenodd" d="M12 2a10 10 0 1 1-9.8 12H5a1.2 1.2 0 0 0 0-2.4H2A10 10 0 0 1 12 2Zm-.9 5a.9.9 0 0 1 1.8 0v4.5L16 13.2a.9.9 0 0 1-.9 1.6l-3.5-2a.9.9 0 0 1-.5-.8V7Z" /><path d="M2 2a1 1 0 0 1 1 1v5h5a1 1 0 0 1 0 2H1V3a1 1 0 0 1 1-1Z" /></>
  },
  info: {
    outline: <><circle cx="12" cy="12" r="9" /><path d="M12 11v6m0-10h.01" /></>,
    solid: <path fillRule="evenodd" d="M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20Zm0 4.2a1.2 1.2 0 1 0 0 2.4 1.2 1.2 0 0 0 0-2.4Zm-.9 4.6a.9.9 0 0 1 1.8 0v6a.9.9 0 0 1-1.8 0v-6Z" />
  },
  inbox: {
    outline: <><path d="m3 13 3-9h12l3 9v5a3 3 0 0 1-3 3H6a3 3 0 0 1-3-3v-5Zm0 0h5l1.5 3h5l1.5-3h5" /></>,
    solid: <path fillRule="evenodd" d="M6.2 3.2a1 1 0 0 0-.9.7l-3 9-.1.3V18A3.8 3.8 0 0 0 6 21.8h12a3.8 3.8 0 0 0 3.8-3.8v-4.8l-.1-.3-3-9a1 1 0 0 0-.9-.7H6.2ZM7 5l-2.4 7.2H8a1 1 0 0 1 .9.5l1.1 2.5h4l1.1-2.5a1 1 0 0 1 .9-.5h3.4L17 5H7Z" />
  },
  archive: {
    outline: <><rect x="3" y="4" width="18" height="5" rx="1.5" /><path d="M5 9v9a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V9m-9 4h4" /></>,
    solid: <><rect x="2.2" y="3.2" width="19.6" height="6.6" rx="2" /><path fillRule="evenodd" d="M4.2 11h15.6v7a3.8 3.8 0 0 1-3.8 3.8H8A3.8 3.8 0 0 1 4.2 18v-7Zm5.8 2.1a.9.9 0 0 0 0 1.8h4a.9.9 0 0 0 0-1.8h-4Z" /></>
  },
  bell: {
    outline: <><path d="M6 9a6 6 0 0 1 12 0c0 6 2 6 2 8H4c0-2 2-2 2-8Z" /><path d="M9.5 20a2.9 2.9 0 0 0 5 0" /></>,
    solid: <><path d="M12 2.5a6.7 6.7 0 0 0-6.7 6.7c0 3.8-.7 4.9-1.6 6.1-.5.6-.9 1.2-.9 2a1.2 1.2 0 0 0 1.2 1.2h16a1.2 1.2 0 0 0 1.2-1.2c0-.8-.4-1.4-.9-2-.9-1.2-1.6-2.3-1.6-6.1A6.7 6.7 0 0 0 12 2.5Z" /><path d="M9.3 20a2.8 2.8 0 0 0 5.4 0Z" /></>
  },
  mail: {
    outline: <><rect x="3" y="5" width="18" height="14" rx="3" /><path d="m4 7 8 6 8-6" /></>,
    solid: <path fillRule="evenodd" d="M6 4.5h12A3.5 3.5 0 0 1 21.5 8v8a3.5 3.5 0 0 1-3.5 3.5H6A3.5 3.5 0 0 1 2.5 16V8A3.5 3.5 0 0 1 6 4.5Zm-.6 3a.8.8 0 0 0-.9 1.3l7 5.2a.8.8 0 0 0 1 0l7-5.2a.8.8 0 0 0-.9-1.3L12 12.3 5.4 7.5Z" />
  },
  calendar: {
    outline: <><rect x="3.5" y="5" width="17" height="16" rx="3" /><path d="M7.5 3v4m9-4v4M3.5 10h17M8 14h1m6 0h1m-8 3h1" /></>,
    solid: <><path fillRule="evenodd" d="M7.5 2.2a.8.8 0 0 0-.8.8v1.2H7A4.3 4.3 0 0 0 2.7 8.5v9A4.3 4.3 0 0 0 7 21.8h10a4.3 4.3 0 0 0 4.3-4.3v-9A4.3 4.3 0 0 0 17 4.2h.3V3a.8.8 0 0 0-1.6 0v1.2H8.3V3a.8.8 0 0 0-.8-.8ZM5 9.2a.8.8 0 0 0 0 1.6h14a.8.8 0 0 0 0-1.6H5Zm2.5 4a.8.8 0 0 0 0 1.6h1a.8.8 0 0 0 0-1.6h-1Zm8 0a.8.8 0 0 0 0 1.6h1a.8.8 0 0 0 0-1.6h-1Zm-8 3.5a.8.8 0 0 0 0 1.6h1a.8.8 0 0 0 0-1.6h-1Z" /></>
  },
  search: {
    outline: <><circle cx="10.8" cy="10.8" r="7.3" /><path d="m16.1 16.1 4.4 4.4" /></>,
    solid: <><path fillRule="evenodd" d="M10.8 2.6a8.2 8.2 0 1 0 0 16.4 8.2 8.2 0 0 0 0-16.4Zm0 2.4a5.8 5.8 0 1 0 0 11.6 5.8 5.8 0 0 0 0-11.6Z" /><path d="m15.4 17.1 4.2 4.2a1.2 1.2 0 0 0 1.7-1.7l-4.2-4.2Z" /></>
  },
  compose: {
    outline: <><path d="M12 4H7a3 3 0 0 0-3 3v10a3 3 0 0 0 3 3h10a3 3 0 0 0 3-3v-5" /><path d="m10 14 1-4L18.5 2.5a2.1 2.1 0 0 1 3 3L14 13l-4 1Zm7-10 3 3" /></>,
    solid: <><path d="M11.2 3.2H7A3.8 3.8 0 0 0 3.2 7v10A3.8 3.8 0 0 0 7 20.8h10a3.8 3.8 0 0 0 3.8-3.8v-4.2a.8.8 0 0 0-1.4-.6l-4.3 4.3a1 1 0 0 1-.5.3l-5.1 1.3a1.5 1.5 0 0 1-1.8-1.8L9 11.2a1 1 0 0 1 .3-.5l2.5-2.5 0-4.2a.8.8 0 0 0-.6-.8Z" /><path d="m17.7 3.3 3 3 .9-.9a2.1 2.1 0 0 0-3-3l-.9.9Zm-1.2 1.2-5.3 5.3-.9 3.9 3.9-.9 5.3-5.3-3-3Z" /></>
  },
  upload: {
    outline: <><path d="M4 14v3a3 3 0 0 0 3 3h10a3 3 0 0 0 3-3v-3M12 15V3m-5 5 5-5 5 5" /></>,
    solid: <><path d="M12 2.2a1.2 1.2 0 0 0-.8.4l-5 5a1.2 1.2 0 0 0 1.6 1.7l3-3V15a1.2 1.2 0 0 0 2.4 0V6.3l3 3a1.2 1.2 0 0 0 1.6-1.7l-5-5a1.2 1.2 0 0 0-.8-.4Z" /><path d="M3 14v3a4 4 0 0 0 4 4h10a4 4 0 0 0 4-4v-3a1 1 0 0 0-2 0v3a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2v-3a1 1 0 0 0-2 0Z" /></>
  },
  document: {
    outline: <><path d="M13 3H7a3 3 0 0 0-3 3v12a3 3 0 0 0 3 3h10a3 3 0 0 0 3-3V10l-7-7Z" /><path d="M13 3v5a2 2 0 0 0 2 2h5M8 14h8m-8 3h5" /></>,
    solid: <><path fillRule="evenodd" d="M7 2.2A3.8 3.8 0 0 0 3.2 6v12A3.8 3.8 0 0 0 7 21.8h10a3.8 3.8 0 0 0 3.8-3.8v-7.2H15A2.8 2.8 0 0 1 12.2 8V2.2H7Zm1 11a.8.8 0 0 0 0 1.6h8a.8.8 0 0 0 0-1.6H8Zm0 3a.8.8 0 0 0 0 1.6h5a.8.8 0 0 0 0-1.6H8Z" /><path d="M13.8 2.8V8a1.2 1.2 0 0 0 1.2 1.2h5.2Z" /></>
  },
  microphone: {
    outline: <><rect x="8.5" y="3" width="7" height="12" rx="3.5" /><path d="M5 11v1a7 7 0 0 0 14 0v-1m-7 8v3m-4 0h8" /></>,
    solid: <><rect x="8" y="2.5" width="8" height="13" rx="4" /><path d="M5 10.2a.8.8 0 0 0-.8.8v1a7.8 7.8 0 0 0 7 7.8v1.4H8a.8.8 0 0 0 0 1.6h8a.8.8 0 0 0 0-1.6h-3.2v-1.4a7.8 7.8 0 0 0 7-7.8v-1a.8.8 0 0 0-1.6 0v1a6.2 6.2 0 0 1-12.4 0v-1a.8.8 0 0 0-.8-.8Z" /></>
  },
  "microphone-off": {
    outline: <><path d="M8.5 8V6.5a3.5 3.5 0 0 1 7 0v5.2M8.5 11v.5a3.5 3.5 0 0 0 5.1 3.1M5 11v1a7 7 0 0 0 12 4.9M19 11v1a7 7 0 0 1-.4 2.4M12 19v3m-4 0h8M3 3l18 18" /></>,
    solid: <><path d="M12 2.5a4 4 0 0 0-4 4v.4l8 8V6.5a4 4 0 0 0-4-4Zm-4 8.6v.4a4 4 0 0 0 4 4h.4L8 11.1Z" /><path d="M5 10.2a.8.8 0 0 0-.8.8v1a7.8 7.8 0 0 0 7 7.8v1.4H8a.8.8 0 0 0 0 1.6h8a.8.8 0 0 0 0-1.6h-3.2v-1.4a7.8 7.8 0 0 0 4.3-1.9L16 16.8A6.2 6.2 0 0 1 5.8 12v-1a.8.8 0 0 0-.8-.8Zm14 0a.8.8 0 0 0-.8.8v1a6.2 6.2 0 0 1-.3 1.9l1.2 1.2a7.8 7.8 0 0 0 .7-3.1v-1a.8.8 0 0 0-.8-.8ZM3.6 2.4a.9.9 0 0 0-1.2 1.2l18 18a.9.9 0 0 0 1.2-1.2l-18-18Z" /></>
  },
  "panel-left": {
    outline: <><rect x="3" y="4" width="18" height="16" rx="3" /><path d="M9 4v16" /></>,
    solid: <path fillRule="evenodd" d="M6 3.2h12A3.8 3.8 0 0 1 21.8 7v10a3.8 3.8 0 0 1-3.8 3.8H6A3.8 3.8 0 0 1 2.2 17V7A3.8 3.8 0 0 1 6 3.2Zm4 1.6v14.4h8a2.2 2.2 0 0 0 2.2-2.2V7A2.2 2.2 0 0 0 18 4.8h-8Z" />
  },
  "panel-right": {
    outline: <><rect x="3" y="4" width="18" height="16" rx="3" /><path d="M15 4v16" /></>,
    solid: <path fillRule="evenodd" d="M6 3.2h12A3.8 3.8 0 0 1 21.8 7v10a3.8 3.8 0 0 1-3.8 3.8H6A3.8 3.8 0 0 1 2.2 17V7A3.8 3.8 0 0 1 6 3.2Zm0 1.6A2.2 2.2 0 0 0 3.8 7v10A2.2 2.2 0 0 0 6 19.2h8V4.8H6Z" />
  },
  settings: {
    outline: <><path d="M10.35 2.64h3.3l1.02 2.03 2.34 1.35 2.27-.13 1.65 2.86-1.25 1.9v2.7l1.25 1.9-1.65 2.86-2.27-.13-2.34 1.35-1.02 2.03h-3.3l-1.02-2.03-2.34-1.35-2.27.13-1.65-2.86 1.25-1.9v-2.7l-1.25-1.9 1.65-2.86 2.27.13 2.34-1.35 1.02-2.03Z" /><circle cx="12" cy="12" r="3.3" /></>,
    solid: <path fillRule="evenodd" d="M10.35 2.64h3.3l1.02 2.03 2.34 1.35 2.27-.13 1.65 2.86-1.25 1.9v2.7l1.25 1.9-1.65 2.86-2.27-.13-2.34 1.35-1.02 2.03h-3.3l-1.02-2.03-2.34-1.35-2.27.13-1.65-2.86 1.25-1.9v-2.7l-1.25-1.9 1.65-2.86 2.27.13 2.34-1.35 1.02-2.03ZM12 8.7a3.3 3.3 0 1 0 0 6.6 3.3 3.3 0 0 0 0-6.6Z" />
  },
  account: {
    outline: <><rect x="3" y="3" width="18" height="18" rx="4" /><circle cx="12" cy="9" r="3" /><path d="M6 20v-1a6 6 0 0 1 12 0v1" /></>,
    solid: <path fillRule="evenodd" d="M7 2.2h10A4.8 4.8 0 0 1 21.8 7v10a4.8 4.8 0 0 1-4.8 4.8H7A4.8 4.8 0 0 1 2.2 17V7A4.8 4.8 0 0 1 7 2.2Zm5 3.6a3.2 3.2 0 1 0 0 6.4 3.2 3.2 0 0 0 0-6.4Zm0 8a5.4 5.4 0 0 0-5.4 5.4v.7a3 3 0 0 0 .4 0h10a3 3 0 0 0 .4 0v-.7a5.4 5.4 0 0 0-5.4-5.4Z" />
  },
  "arrow-up": {
    outline: <path d="M12 20V4m-6 6 6-6 6 6" />,
    solid: <path d="M12 2.8a1.3 1.3 0 0 0-.9.4l-6 6A1.3 1.3 0 0 0 7 11l3.7-3.7V20a1.3 1.3 0 0 0 2.6 0V7.3L17 11a1.3 1.3 0 0 0 1.9-1.8l-6-6a1.3 1.3 0 0 0-.9-.4Z" />
  },
  globe: {
    outline: <><circle cx="12" cy="12" r="9" /><ellipse cx="12" cy="12" rx="4" ry="9" /><path d="M3 12h18" /></>,
    solid: <><path d="M3.1 11.2h3.7a19.3 19.3 0 0 1 1.7-7.6 9 9 0 0 0-5.4 7.6Zm0 1.6a9 9 0 0 0 5.4 7.6 19.3 19.3 0 0 1-1.7-7.6H3.1Zm5.3-1.6h7.2C15.4 6.3 13.9 3 12 3s-3.4 3.3-3.6 8.2Zm0 1.6c.2 4.9 1.7 8.2 3.6 8.2s3.4-3.3 3.6-8.2H8.4Zm8.8-1.6h3.7a9 9 0 0 0-5.4-7.6 19.3 19.3 0 0 1 1.7 7.6Zm0 1.6a19.3 19.3 0 0 1-1.7 7.6 9 9 0 0 0 5.4-7.6h-3.7Z" /></>
  }
};

export type ActionIconProps = {
  name: ActionIconName;
  active?: boolean;
  size?: number;
  className?: string;
};

export function ActionIcon({ name, active = false, size = 20, className = "" }: ActionIconProps) {
  const pair = icons[name];
  return (
    <svg
      className={["actionIcon", active ? "isSolid" : "", className].filter(Boolean).join(" ")}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      aria-hidden="true"
      focusable="false"
    >
      <g className="actionIconOutline" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
        {pair.outline}
      </g>
      <g className="actionIconSolid" fill="currentColor" stroke="none">
        {pair.solid}
      </g>
    </svg>
  );
}
