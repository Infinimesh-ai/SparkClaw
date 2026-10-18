import { useCallback, useLayoutEffect, useRef, useState } from "react";

type ReadingPosition = { top: number; following: boolean; anchor: string; offset: number };

// Observe the existing message container; never replace children or own message
// identities. The stream adapter may update the same article in place.
export function useMessageScroll(conversation: string, active = true) {
  const listRef = useRef<HTMLDivElement | null>(null);
  const positions = useRef(new Map<string, ReadingPosition>());
  const current = useRef<ReadingPosition>({ top: 0, following: true, anchor: "", offset: 0 });
  const [away, setAway] = useState(false);
  const resume = useCallback(() => {
    current.current.following = true;
    const list = listRef.current;
    const selection = document.getSelection();
    if (list && selection?.anchorNode && list.contains(selection.anchorNode)) selection.removeAllRanges();
    if (list) { list.scrollTop = list.scrollHeight; current.current.top = list.scrollTop; }
    setAway(false);
  }, []);

  useLayoutEffect(() => {
    const list = listRef.current;
    if (!list || !active) return;
    const position = positions.current.get(conversation) ?? { top: 0, following: true, anchor: "", offset: 0 };
    current.current = { ...position };
    let frame = 0;
    let restoring = true;
    const selected = () => {
      const selection = document.getSelection();
      return Boolean(selection && !selection.isCollapsed && selection.anchorNode && list.contains(selection.anchorNode));
    };
    const remember = () => {
      if (current.current.following) { current.current.top = list.scrollTop; current.current.anchor = ""; return; }
      const first = [...list.children].find(child => child.getBoundingClientRect().bottom > list.getBoundingClientRect().top);
      current.current.top = list.scrollTop;
      current.current.anchor = first?.getAttribute("data-scroll-anchor") || first?.getAttribute("data-message-id") || first?.id || "";
      current.current.offset = first ? first.getBoundingClientRect().top - list.getBoundingClientRect().top : 0;
    };
    const update = () => {
      frame = 0;
      if (!list.clientHeight || (restoring && !list.querySelector(".message"))) return; // A focus layout may temporarily hide chat.
      if (selected()) current.current.following = false;
      if (current.current.following) list.scrollTop = list.scrollHeight;
      else if (current.current.anchor) {
        const anchor = [...list.children].find(child => (child.getAttribute("data-scroll-anchor") || child.getAttribute("data-message-id") || child.id) === current.current.anchor);
        if (anchor) list.scrollTop += anchor.getBoundingClientRect().top - list.getBoundingClientRect().top - current.current.offset;
        else list.scrollTop = current.current.top;
      } else list.scrollTop = current.current.top;
      restoring = false; remember(); setAway(!current.current.following);
    };
    const schedule = () => { if (!frame) frame = requestAnimationFrame(update); };
    const scroll = () => {
      // Empty async loads clamp scrollTop to zero and emit a native scroll event.
      // Keep the saved conversation position until its messages arrive.
      if (restoring) return;
      const distance = list.scrollHeight - list.clientHeight - list.scrollTop;
      if ((distance > 80 && list.scrollTop < current.current.top) || selected()) current.current.following = false;
      else if (distance <= 2) current.current.following = true;
      remember(); setAway(!current.current.following);
    };
    const selection = () => { if (selected()) { current.current.following = false; remember(); setAway(true); } };
    setAway(!position.following);
    update();
    const observer = new ResizeObserver(schedule);
    const observeChildren = () => { observer.disconnect(); observer.observe(list); for (const child of list.children) observer.observe(child); };
    observeChildren();
    const changes = new MutationObserver(records => { if (records.some(record => record.target === list && record.type === "childList")) observeChildren(); schedule(); });
    changes.observe(list, { childList: true, subtree: true, characterData: true });
    list.addEventListener("scroll", scroll, { passive: true });
    document.addEventListener("selectionchange", selection);
    return () => {
      positions.current.set(conversation, { ...current.current });
      cancelAnimationFrame(frame); observer.disconnect(); changes.disconnect();
      list.removeEventListener("scroll", scroll); document.removeEventListener("selectionchange", selection);
    };
  }, [conversation, active]);
  return { listRef, away, resume };
}
