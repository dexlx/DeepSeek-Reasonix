// Run: tsx src/__tests__/transcript-process-fold.test.ts

import { JSDOM } from "jsdom";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { createServer, type ViteDevServer } from "vite";
import type { Item } from "../lib/useController";

let passed = 0;
let failed = 0;

function ok(value: unknown, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

console.log("\ntranscript process fold");

let displayMode = "standard";
let processFoldPref = "auto";
Object.defineProperty(globalThis, "localStorage", {
  configurable: true,
  value: {
    getItem(key: string) {
      if (key === "reasonix-display-mode") return displayMode;
      if (key === "reasonix-process-fold") return processFoldPref;
      return null;
    },
    setItem() {},
    removeItem() {},
    clear() {},
    key() { return null; },
    length: 0,
  },
});

let server: ViteDevServer | undefined;
try {
  server = await createServer({
    appType: "custom",
    logLevel: "silent",
    server: { middlewareMode: true },
  });
  const { Transcript } = await server.ssrLoadModule("/src/components/Transcript.tsx");
  const { LocaleProvider } = await server.ssrLoadModule("/src/lib/i18n.tsx");

  function render(items: Item[], options: { mode?: "standard" | "compact"; running?: boolean; turnStartAt?: number; foldPref?: "auto" | "expanded" } = {}) {
    displayMode = options.mode ?? "standard";
    processFoldPref = options.foldPref ?? "auto";
    const markup = renderToStaticMarkup(
      React.createElement(
        LocaleProvider,
        null,
        React.createElement(Transcript, {
          items,
          onPrompt: () => {},
          questionNavigator: false,
          running: options.running ?? false,
          turnStartAt: options.turnStartAt,
        }),
      ),
    );
    return new JSDOM(markup).window.document;
  }

  const warningTurn: Item[] = [
    { kind: "user", id: "u1", text: "inspect" },
    { kind: "assistant", id: "a1", text: "", reasoning: "first thought", streaming: false },
    { kind: "tool", id: "t1", name: "read_file", args: "{}", readOnly: true, status: "done", durationMs: 400 },
    { kind: "notice", id: "n1", level: "warn", text: "gateway warning" },
    { kind: "assistant", id: "a2", text: "", reasoning: "second thought", streaming: false },
    { kind: "tool", id: "t2", name: "bash", args: "{}", readOnly: false, status: "done", durationMs: 600 },
    { kind: "assistant", id: "a3", text: "final answer", reasoning: "final thought", streaming: false, workDurationMs: 24_000 },
  ];

  for (const mode of ["standard", "compact"] as const) {
    const doc = render(warningTurn, { mode });
    const warning = doc.querySelector(".notice-line--warn");
    const finalAnswer = Array.from(doc.querySelectorAll(".msg--assistant")).find((node) => node.textContent?.includes("final answer"));
    ok(doc.querySelectorAll(".turn-collapse").length === 1, `${mode} mode renders one work fold for the turn`);
    ok(warning && !warning.closest(".turn-collapse"), `${mode} warning remains visible without splitting the fold`);
    ok(finalAnswer && !finalAnswer.closest(".turn-collapse"), `${mode} final answer renders outside the work fold`);
  }

  // Assistant content is model output addressed to the user — every message
  // with answer text stays outside the fold, not just the last one (#4092).
  const intermediateDoc = render([
    { kind: "user", id: "u2", text: "continue" },
    { kind: "assistant", id: "a4", text: "I will inspect the files", reasoning: "plan", streaming: false },
    { kind: "tool", id: "t3", name: "read_file", args: "{}", readOnly: true, status: "done" },
    { kind: "assistant", id: "a5", text: "all done", reasoning: "verify", streaming: false },
  ]);
  const intermediate = Array.from(intermediateDoc.querySelectorAll(".msg--assistant")).find((node) => node.textContent?.includes("I will inspect the files"));
  const final = Array.from(intermediateDoc.querySelectorAll(".msg--assistant")).find((node) => node.textContent?.includes("all done"));
  ok(intermediateDoc.querySelectorAll(".turn-collapse").length === 1, "intermediate assistant text does not create another fold");
  ok(intermediate && !intermediate.closest(".turn-collapse"), "intermediate assistant text renders outside the work fold");
  ok(final && !final.closest(".turn-collapse"), "final assistant answer renders outside the work fold");
  const fold = intermediateDoc.querySelector(".turn-collapse");
  const foldBeforeIntermediate = Boolean(
    fold &&
    intermediate &&
    (fold.compareDocumentPosition(intermediate) & intermediateDoc.defaultView!.Node.DOCUMENT_POSITION_FOLLOWING),
  );
  const intermediateBeforeFinal = Boolean(
    intermediate &&
    final &&
    (intermediate.compareDocumentPosition(final) & intermediateDoc.defaultView!.Node.DOCUMENT_POSITION_FOLLOWING),
  );
  ok(foldBeforeIntermediate && intermediateBeforeFinal, "answers keep their order after the fold");
  ok(fold?.textContent?.includes("plan") && fold?.textContent?.includes("verify"), "reasoning segments stay inside the fold");

  // A mid-turn steer is the user's own message (#6238): it renders on the
  // user side, outside the fold; ordinary info notices keep folding.
  const steerDoc = render([
    { kind: "user", id: "u-steer", text: "start" },
    { kind: "assistant", id: "a-steer-1", text: "", reasoning: "thinking", streaming: false },
    { kind: "notice", id: "s1", level: "info", text: "↪ use plan B instead" },
    { kind: "notice", id: "i1", level: "info", text: "plain info notice" },
    { kind: "assistant", id: "a-steer-2", text: "done via plan B", reasoning: "", streaming: false },
  ]);
  const steer = steerDoc.querySelector(".steer-line");
  ok(steer && !steer.closest(".turn-collapse"), "steer notice renders outside the work fold");
  ok(steer?.textContent?.includes("use plan B instead"), "steer bubble carries the user's guidance text");
  const plainInfo = Array.from(steerDoc.querySelectorAll(".notice-line")).find((node) => node.textContent?.includes("plain info notice"));
  ok(plainInfo && plainInfo.closest(".turn-collapse"), "plain info notices keep folding");

  const errorDoc = render([
    { kind: "user", id: "u-error", text: "finish" },
    { kind: "assistant", id: "a-error", text: "partial result", reasoning: "worked", streaming: false },
    { kind: "notice", id: "n-error", level: "warn", text: "turn stopped" },
  ]);
  const errorAnswer = Array.from(errorDoc.querySelectorAll(".msg--assistant")).find((node) => node.textContent?.includes("partial result"));
  const trailingWarning = errorDoc.querySelector(".notice-line--warn");
  const followsAnswer = Boolean(
    errorAnswer &&
    trailingWarning &&
    (errorAnswer.compareDocumentPosition(trailingWarning) & errorDoc.defaultView!.Node.DOCUMENT_POSITION_FOLLOWING),
  );
  ok(followsAnswer, "warnings outside the fold preserve their order relative to the final answer");

  const originalNow = Date.now;
  Date.now = () => 25_000;
  try {
    const runningDoc = render([
      { kind: "user", id: "u3", text: "run" },
      { kind: "assistant", id: "a6", text: "", reasoning: "working", streaming: false, workDurationMs: 5_000 },
    ], { running: true, turnStartAt: 1_000 });
    ok(runningDoc.querySelector(".turn-collapse__label")?.textContent === "Working 24s · 1 thoughts", "active turn stays Working and counts its process items");
  } finally {
    Date.now = originalNow;
  }

  const completedDoc = render([
    { kind: "user", id: "u4", text: "finish" },
    { kind: "assistant", id: "a7", text: "done", reasoning: "worked", streaming: false, workDurationMs: 24_000 },
  ]);
  ok(completedDoc.querySelector(".turn-collapse__label")?.textContent === "Worked 24s · 1 thoughts", "completed turn keeps the persisted wall-clock duration and counts");

  const countsDoc = render(warningTurn);
  const countsLabel = countsDoc.querySelector(".turn-collapse__label")?.textContent ?? "";
  ok(countsLabel.includes("2 tools") && countsLabel.includes("3 thoughts"), "fold label surfaces tool and thought counts");

  // A turn whose fold is the only content (e.g. cancelled before any answer)
  // must not collapse into a bare label — nothing would remain visible.
  const aloneDoc = render([
    { kind: "user", id: "u5", text: "cancelled" },
    { kind: "assistant", id: "a8", text: "", reasoning: "got cut off", streaming: false, workDurationMs: 3_000 },
  ]);
  ok(aloneDoc.querySelector(".turn-collapse--open"), "fold with nothing outside stays expanded");
  const answeredDoc = render([
    { kind: "user", id: "u6", text: "ask" },
    { kind: "assistant", id: "a9", text: "answered", reasoning: "quick", streaming: false, workDurationMs: 3_000 },
  ]);
  ok(!answeredDoc.querySelector(".turn-collapse--open"), "fold with an answer outside starts collapsed");

  // settings.processFold = expanded keeps completed folds open (#4233, #2278).
  const expandedDoc = render([
    { kind: "user", id: "u7", text: "ask" },
    { kind: "assistant", id: "a10", text: "answered", reasoning: "quick", streaming: false, workDurationMs: 3_000 },
  ], { foldPref: "expanded" });
  ok(expandedDoc.querySelector(".turn-collapse--open"), "keep-expanded preference leaves the fold open");

  // Each reasoning segment inside the fold is independently collapsible (#6340).
  const segmentDoc = render(warningTurn);
  const segmentHeads = segmentDoc.querySelectorAll("button.turn-collapse__reasoning-head");
  ok(segmentHeads.length === 3, "every reasoning segment gets its own toggle");
  ok(Array.from(segmentHeads).every((head) => head.getAttribute("aria-expanded") === "true"), "reasoning segments default to expanded");
} finally {
  await server?.close();
  delete (globalThis as { localStorage?: Storage }).localStorage;
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
