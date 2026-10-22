import type { CSSProperties } from "react";
import qqMail from "../assets/connections/providers/qq_mail-v2.webp";
import outlook from "../assets/connections/providers/outlook.webp";
import gmail from "../assets/connections/providers/gmail.webp";
import chatgpt from "../assets/connections/providers/chatgpt.webp";
import claude from "../assets/connections/providers/claude.webp";
import gemini from "../assets/connections/providers/gemini.webp";
import grok from "../assets/connections/providers/grok.webp";
import weixin from "../assets/connections/providers/weixin.webp";
import telegram from "../assets/connections/providers/telegram.webp";

const artwork = { qq_mail: qqMail, outlook, gmail, chatgpt, claude, gemini, grok, weixin, telegram };

export type ProviderBrandIconName = keyof typeof artwork;

// The silhouette comes entirely from the generated raster's alpha channel.
// Applying the theme color keeps the same generated mark readable in both themes.
export function ProviderBrandIcon({
  name,
  className
}: {
  name: ProviderBrandIconName;
  className?: string;
}) {
  return <span
    className={["providerBrandIcon", className].filter(Boolean).join(" ")}
    style={{ "--provider-icon-image": `url("${artwork[name]}")` } as CSSProperties}
    aria-hidden="true"
  />;
}
