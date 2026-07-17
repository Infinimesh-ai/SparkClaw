import { APIError } from "../api/client";
import type { Copy as CopyText } from "../i18n";

export function notificationBindingErrorMessage(error: unknown, text: CopyText) {
  if (error instanceof APIError) {
    switch (error.code) {
      case "invalid_bot_token":
        return text.settings.telegramTokenRejected;
      case "telegram_rate_limited":
        return text.settings.telegramRateLimited;
      case "telegram_unavailable":
        return text.settings.telegramUnavailable;
      case "telegram_unreachable":
        return text.settings.telegramUnreachable;
      case "telegram_verification_failed":
        return text.settings.telegramVerificationFailed;
    }
  }
  return error instanceof Error ? error.message : text.errors.binding;
}
