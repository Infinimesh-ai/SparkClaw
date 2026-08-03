import { useEffect, useId, useMemo, useRef, useState } from "react";
import { Bot, Check, ChevronDown, MessageCircle } from "lucide-react";
import type { DeliveryEndpoint } from "../api/types";
import type { Copy as CopyText } from "../i18n";

type DeliveryTargetPickerProps = {
  endpoints: DeliveryEndpoint[];
  activeEndpoint?: DeliveryEndpoint;
  hasExternalIntent: boolean;
  disabled: boolean;
  text: CopyText;
  onSelect: (endpointId: string) => void;
};

export function DeliveryTargetPicker({
  endpoints,
  activeEndpoint,
  hasExternalIntent,
  disabled,
  text,
  onSelect
}: DeliveryTargetPickerProps) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement | null>(null);
  const menuId = useId();
  const groups = useMemo(() => {
    const grouped = new Map<string, { label: string; endpoints: DeliveryEndpoint[] }>();
    for (const endpoint of endpoints) {
      const key = endpoint.channel || endpoint.software_display_name;
      const group = grouped.get(key) ?? {
        label: endpoint.software_display_name || endpoint.channel,
        endpoints: []
      };
      group.endpoints.push(endpoint);
      grouped.set(key, group);
    }
    return [...grouped.entries()]
      .map(([key, group]) => ({
        key,
        label: group.label,
        endpoints: group.endpoints.sort((a, b) => a.recipient.display_name.localeCompare(b.recipient.display_name))
      }))
      .sort((a, b) => a.label.localeCompare(b.label));
  }, [endpoints]);

  useEffect(() => {
    if (!open) return;
    function closeOnOutsideClick(event: MouseEvent) {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    }
    function closeOnEscape(event: globalThis.KeyboardEvent) {
      if (event.key === "Escape") setOpen(false);
    }
    document.addEventListener("mousedown", closeOnOutsideClick);
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("mousedown", closeOnOutsideClick);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  const targetName = activeEndpoint?.recipient.display_name
    ?? (hasExternalIntent ? text.chat.destinationUnavailable : "SparkClaw");
  const targetContext = activeEndpoint
    ? activeEndpoint.software_display_name || activeEndpoint.channel
    : text.chat.currentConversation;

  function choose(endpointId: string) {
    onSelect(endpointId);
    setOpen(false);
  }

  return (
    <div className="deliveryTargetPicker" ref={rootRef}>
      <button
        className={`deliveryTargetButton ${activeEndpoint ? "external" : ""}`}
        type="button"
        aria-label={`${text.chat.sendTo}: ${targetName}`}
        aria-expanded={open}
        aria-controls={menuId}
        aria-haspopup="dialog"
        disabled={disabled}
        title={text.chat.chooseDestination}
        onClick={() => setOpen((current) => !current)}
      >
        {activeEndpoint ? <MessageCircle size={15} aria-hidden="true" /> : <Bot size={15} aria-hidden="true" />}
        <span className="deliveryTargetButtonText">
          <small>{text.chat.sendTo}</small>
          <strong>{targetName}</strong>
        </span>
        <span className="deliveryTargetContext">{targetContext}</span>
        <ChevronDown className={open ? "open" : ""} size={14} aria-hidden="true" />
      </button>
      {open && (
        <div className="deliveryTargetMenu" id={menuId} role="dialog" aria-label={text.chat.chooseDestination}>
          <div className="deliveryTargetMenuHeader">{text.chat.chooseDestination}</div>
          <button
            className={`deliveryTargetOption ${!hasExternalIntent ? "selected" : ""}`}
            type="button"
            aria-pressed={!hasExternalIntent}
            onClick={() => choose("")}
          >
            <Bot size={16} aria-hidden="true" />
            <span>
              <strong>SparkClaw</strong>
              <small>{text.chat.currentConversation}</small>
            </span>
            {!hasExternalIntent && <Check size={15} aria-hidden="true" />}
          </button>
          {groups.length === 0 ? (
            <span className="deliveryTargetEmpty">{text.chat.noDeliveryEndpoints}</span>
          ) : groups.map((group) => (
            <div className="deliveryTargetGroup" key={group.key} role="group" aria-label={group.label}>
              <span className="deliveryTargetGroupLabel">{group.label}</span>
              {group.endpoints.map((endpoint) => {
                const selected = endpoint.id === activeEndpoint?.id;
                const context = [endpoint.account_display_name, endpoint.conversation_label].filter(Boolean).join(" · ");
                return (
                  <button
                    className={`deliveryTargetOption ${selected ? "selected" : ""}`}
                    key={endpoint.id}
                    type="button"
                    aria-pressed={selected}
                    onClick={() => choose(endpoint.id)}
                  >
                    <MessageCircle size={16} aria-hidden="true" />
                    <span>
                      <strong>{endpoint.recipient.display_name}</strong>
                      <small>{context}</small>
                    </span>
                    {selected && <Check size={15} aria-hidden="true" />}
                  </button>
                );
              })}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
