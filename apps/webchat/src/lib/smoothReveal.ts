// Progress previews arrive as cumulative snapshots, one per transport round
// trip. Reveal each newly received suffix at a steady rate spread over the
// observed arrival interval, so text flows instead of jumping by paragraphs.
// Display only: the snapshot itself is never altered or delayed beyond one gap.
import { useEffect, useRef, useState } from "react";
import { APPEARANCE_CHANGE_EVENT, currentMotion } from "./appearance";

const MIN_INTERVAL_MS = 120;
const MAX_INTERVAL_MS = 1500;
const FIRST_INTERVAL_MS = 300;

export type RevealState = { shown: string; position: number; lastArrival: number; interval: number };

export function revealState(target: string): RevealState {
  return { shown: target, position: target.length, lastArrival: 0, interval: FIRST_INTERVAL_MS };
}

// Record a new snapshot. Returns the reveal speed in UTF-16 units per
// millisecond, or 0 when the text must be shown immediately.
export function revealArrival(state: RevealState, target: string, now: number): number {
  if (!target.startsWith(state.shown)) {
    Object.assign(state, revealState(target));
    return 0;
  }
  if (state.lastArrival) state.interval = Math.min(MAX_INTERVAL_MS, Math.max(MIN_INTERVAL_MS, (state.interval + now - state.lastArrival) / 2));
  state.lastArrival = now;
  return (target.length - state.position) / state.interval;
}

// Advance by elapsed milliseconds; returns true while more text remains.
export function revealStep(state: RevealState, target: string, speed: number, elapsed: number): boolean {
  state.position = Math.min(target.length, state.position + Math.max(speed * elapsed, 1));
  let end = Math.floor(state.position);
  // Never split a surrogate pair.
  if (end < target.length && end > 0 && target.charCodeAt(end - 1) >= 0xd800 && target.charCodeAt(end - 1) <= 0xdbff) end++;
  state.shown = target.slice(0, end);
  return end < target.length;
}

function reducedMotion() {
  return currentMotion() === "reduced" || (typeof window !== "undefined" && typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches);
}

export function useSmoothReveal(target: string): string {
  const state = useRef<RevealState>(revealState(target));
  const [shown, setShown] = useState(target);
  const [reduce, setReduce] = useState(reducedMotion);
  useEffect(() => {
    const updateMotion = () => setReduce(reducedMotion());
    const media = window.matchMedia?.("(prefers-reduced-motion: reduce)");
    window.addEventListener(APPEARANCE_CHANGE_EVENT, updateMotion);
    media?.addEventListener("change", updateMotion);
    return () => {
      window.removeEventListener(APPEARANCE_CHANGE_EVENT, updateMotion);
      media?.removeEventListener("change", updateMotion);
    };
  }, []);
  useEffect(() => {
    const current = state.current;
    if (target === current.shown) return;
    const speed = revealArrival(current, target, performance.now());
    if (!speed || reduce || typeof window.requestAnimationFrame !== "function") {
      Object.assign(current, revealState(target), { lastArrival: current.lastArrival, interval: current.interval });
      setShown(target);
      return;
    }
    let frame = 0, last = performance.now();
    const tick = (time: number) => {
      const more = revealStep(current, target, speed, Math.max(0, time - last));
      last = time;
      setShown(current.shown);
      if (more) frame = window.requestAnimationFrame(tick);
    };
    frame = window.requestAnimationFrame(tick);
    return () => window.cancelAnimationFrame(frame);
  }, [target, reduce]);
  return shown;
}
