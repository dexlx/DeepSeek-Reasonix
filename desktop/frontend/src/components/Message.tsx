import { useState } from "react";
import { ChevronRight } from "lucide-react";
import { Markdown } from "./Markdown";
import { CopyButton } from "./CopyButton";
import { useT } from "../lib/i18n";
import type { Item } from "../lib/useController";

type AssistantItem = Extract<Item, { kind: "assistant" }>;

export function UserMessage({ text }: { text: string }) {
  return (
    <div className="msg msg--user">
      <span className="msg__caret">›</span>
      <div className="msg__text">{text}</div>
    </div>
  );
}

export function AssistantMessage({ item }: { item: AssistantItem }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  return (
    <div className="msg msg--assistant">
      {item.reasoning && (
        <div className="reasoning">
          <button className="reasoning__toggle" onClick={() => setOpen((v) => !v)}>
            <ChevronRight
              className={`reasoning__chevron ${open ? "reasoning__chevron--open" : ""}`}
              size={12}
            />
            {t("msg.thinking")}
          </button>
          {open && <div className="reasoning__body">{item.reasoning}</div>}
        </div>
      )}
      <div className="msg__body">
        {item.streaming ? (
          // While streaming, render raw text (stable, monospace-free) instead of
          // re-parsing markdown on every token — partial markdown reflows the
          // layout and makes the view jitter. Markdown renders once, on completion.
          <div className="msg__stream">
            {item.text}
            <span className="cursor" />
          </div>
        ) : (
          <Markdown text={item.text} />
        )}
      </div>
      {!item.streaming && item.text && (
        <div className="msg__actions">
          <CopyButton text={item.text} label={t("msg.copy")} />
        </div>
      )}
    </div>
  );
}
