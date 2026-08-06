import { Bell, CheckCheck, ExternalLink, X } from "lucide-react";
import type { PassiveNotification } from "../api/types";
import type { Copy, Language } from "../i18n";

type NotificationCenterProps = {
  notifications: PassiveNotification[];
  unreadCount: number;
  open: boolean;
  toast: PassiveNotification | null;
  language: Language;
  text: Copy;
  onToggle: () => void;
  onDismissToast: () => void;
  onRead: (id: string) => Promise<void>;
  onReadAll: () => Promise<void>;
};

function notificationKind(notification: PassiveNotification, text: Copy) {
  return notification.kind === "comment_mention"
    ? text.notifications.commentMention
    : text.notifications.documentMention;
}

function notificationTime(notification: PassiveNotification, language: Language) {
  return new Intl.DateTimeFormat(language === "zh" ? "zh-CN" : "en", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit"
  }).format(new Date(notification.occurred_at));
}

export function NotificationCenter({
  notifications,
  unreadCount,
  open,
  toast,
  language,
  text,
  onToggle,
  onDismissToast,
  onRead,
  onReadAll
}: NotificationCenterProps) {
  return (
    <>
      <div className="notificationCenter">
        <button
          className="iconButton notificationBell"
          onClick={onToggle}
          title={text.notifications.title}
          aria-label={text.notifications.title}
          aria-expanded={open}
        >
          <Bell size={18} />
          {unreadCount > 0 ? <span className="notificationBadge">{unreadCount > 99 ? "99+" : unreadCount}</span> : null}
        </button>
        {open ? (
          <section className="notificationInbox" aria-label={text.notifications.title}>
            <header>
              <div>
                <strong>{text.notifications.title}</strong>
                <span>{unreadCount} {text.notifications.unread}</span>
              </div>
              <button
                className="miniIconButton"
                onClick={() => void onReadAll()}
                disabled={unreadCount === 0}
                title={text.notifications.markAllRead}
                aria-label={text.notifications.markAllRead}
              >
                <CheckCheck size={14} />
              </button>
            </header>
            <div className="notificationList">
              {notifications.length === 0 ? (
                <p className="notificationEmpty">{text.notifications.empty}</p>
              ) : notifications.map((notification) => (
                <article className={notification.read_at ? "notificationItem" : "notificationItem unread"} key={notification.id}>
                  <span className="notificationUnreadDot" aria-hidden="true" />
                  <div>
                    <strong>LocalMind</strong>
                    <span>{notificationKind(notification, text)}</span>
                    <time dateTime={notification.occurred_at}>{notificationTime(notification, language)}</time>
                  </div>
                  <a
                    className="notificationOpen"
                    href={notification.deep_link}
                    target="_blank"
                    rel="noopener noreferrer"
                    onClick={() => void onRead(notification.id)}
                    title={text.notifications.open}
                    aria-label={text.notifications.open}
                  >
                    <ExternalLink size={15} />
                  </a>
                </article>
              ))}
            </div>
          </section>
        ) : null}
      </div>
      {toast ? (
        <aside className="notificationToast" role="status">
          <Bell size={17} />
          <div>
            <strong>LocalMind</strong>
            <span>{notificationKind(toast, text)}</span>
          </div>
          <a
            href={toast.deep_link}
            target="_blank"
            rel="noopener noreferrer"
            onClick={() => void onRead(toast.id)}
            title={text.notifications.open}
            aria-label={text.notifications.open}
          >
            <ExternalLink size={15} />
          </a>
          <button onClick={onDismissToast} title={text.notifications.dismiss} aria-label={text.notifications.dismiss}>
            <X size={15} />
          </button>
        </aside>
      ) : null}
    </>
  );
}
