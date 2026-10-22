import { useId } from "react";
import { Sparkles } from "lucide-react";
import type { Language } from "../i18n";
import type { Motion, ReadingWidth } from "../lib/appearance";

const previewCopy = {
  zh: {
    title: "预览",
    description: "对话示例",
    user: "你的消息",
    assistant: "SparkX 的回复",
    question: "帮我安排明天的工作。",
    introduction: "先专注，再处理日常事务。",
    items: ["先完成明天最重要的一件事。", "集中处理邮件，留出沟通时间。"]
  },
  en: {
    title: "Preview",
    description: "Sample conversation",
    user: "Your message",
    assistant: "SparkX reply",
    question: "Plan tomorrow’s work.",
    introduction: "Keep the day focused.",
    items: ["Focus on the most important task.", "Keep time for email and follow-ups."]
  }
} as const;

export function AppearancePreview({
  language,
  readingWidth,
  motion
}: {
  language: Language;
  readingWidth: ReadingWidth;
  motion: Motion;
}) {
  const titleId = useId();
  const copy = previewCopy[language];

  return <section className="appearancePreview" aria-labelledby={titleId}>
    <header className="appearancePreviewHeader">
      <h2 id={titleId}>{copy.title}</h2>
      <span>{copy.description}</span>
    </header>
    <div className="appearancePreviewCanvas" data-reading-width={readingWidth}>
      <div className="appearancePreviewConversation" key={`${language}:${motion}`}>
        <article className="appearancePreviewUser" aria-label={copy.user}>
          <p>{copy.question}</p>
        </article>
        <article className="appearancePreviewAssistant" aria-label={copy.assistant}>
          <div className="appearancePreviewMeta"><Sparkles size={14} strokeWidth={1.75} aria-hidden="true" /><span>SparkX</span></div>
          <p>{copy.introduction}</p>
          <ul>{copy.items.map(item => <li key={item}>{item}</li>)}</ul>
        </article>
      </div>
    </div>
  </section>;
}
