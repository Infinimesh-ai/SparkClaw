import email from "../assets/connections/email.webp";
import weixin from "../assets/connections/weixin.webp";
import telegram from "../assets/connections/telegram.webp";
import browser from "../assets/connections/browser.webp";
import aiLogin from "../assets/connections/ai-login.webp";
import info from "../assets/connections/info.webp";
import localmind from "../assets/connections/localmind.webp";
import mcp from "../assets/connections/mcp.webp";

const artwork = {
  email,
  weixin,
  telegram,
  browser,
  "ai-login": aiLogin,
  info,
  localmind,
  mcp
};

type ConnectionIconName = keyof typeof artwork;

export function ConnectionIcon({ name }: { name: ConnectionIconName }) {
  return <img className="connectionArtwork" src={artwork[name]} alt="" aria-hidden="true" draggable={false} />;
}
