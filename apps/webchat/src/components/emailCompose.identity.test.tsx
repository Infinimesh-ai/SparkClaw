// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it } from "vitest";
import type { EmailMessage } from "../api/email";
import { dictionaries } from "../i18n";
import { EmailMessageCard } from "./emailMessage";

it("keeps each message's actual receiving and sending identity visible in a shared conversation", async () => {
  const text = dictionaries.en;
  const incoming: EmailMessage = { id: "in", version: 1, mailbox_id: "first", receiving_address: "first@example.com", direction: "inbound", from: "organizer@example.com", to: ["first@example.com"], cc: [], subject: "Meeting", arrived_at: "2026-10-19T12:00:00Z", attachments: [], viewed: true };
  const outgoing: EmailMessage = { ...incoming, id: "out", mailbox_id: "second", receiving_address: "second@example.com", direction: "outbound", from: "second@example.com", to: ["organizer@example.com"] };
  const host = document.createElement("div"); const root = createRoot(host);
  try {
    await act(async () => root.render(<>{[incoming, outgoing].map(mail => <EmailMessageCard key={mail.id} mail={mail} text={text} language="en" viewed busy={false} conversationMode onReanalyze={() => {}} onError={() => {}} />)}</>));
    const received = host.querySelector('[data-email-mail-id="in"] .emailMessageIdentity')!;
    const sent = host.querySelector('[data-email-mail-id="out"] .emailMessageIdentity')!;
    expect(received.textContent).toBe(`${text.email.receivingAddress}: first@example.com`);
    expect(sent.textContent).toBe(`${text.email.from}: second@example.com → ${text.email.to}: organizer@example.com`);
    expect(sent.textContent).not.toContain("first@example.com");
  } finally { await act(async () => root.unmount()); }
});
