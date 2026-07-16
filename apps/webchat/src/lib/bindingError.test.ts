import { describe, expect, it } from "vitest";
import { APIError } from "../api/client";
import { dictionaries } from "../i18n";
import { notificationBindingErrorMessage } from "./bindingError";

describe("notificationBindingErrorMessage", () => {
  it("distinguishes rejected tokens from Telegram connectivity failures", () => {
    expect(notificationBindingErrorMessage(new APIError(400, "raw", "invalid_bot_token"), dictionaries.zh))
      .toBe(dictionaries.zh.settings.telegramTokenRejected);
    expect(notificationBindingErrorMessage(new APIError(502, "raw", "telegram_unreachable"), dictionaries.zh))
      .toBe(dictionaries.zh.settings.telegramUnreachable);
  });

  it("localizes retryable Telegram verification failures", () => {
    expect(notificationBindingErrorMessage(new APIError(429, "raw", "telegram_rate_limited"), dictionaries.en))
      .toBe(dictionaries.en.settings.telegramRateLimited);
    expect(notificationBindingErrorMessage(new APIError(503, "raw", "telegram_unavailable"), dictionaries.en))
      .toBe(dictionaries.en.settings.telegramUnavailable);
  });
});
