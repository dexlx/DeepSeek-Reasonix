// Run: tsx src/__tests__/pending-prompt-stale-status.test.tsx
//
// Regression for #6429 (also #5561/#5481): switching to a session whose plan
// approval / ask is pending flashed the prompt and then lost it. The backend
// replays the prompt event when the detached runtime re-attaches, but a
// runtime snapshot fetched BEFORE that event (pre-attach ListTabs, activation
// metas) could be dispatched AFTER it — reporting the tab idle, clearing the
// prompt, and skipping the compensating replay because its pendingPrompt was
// false. Snapshots that predate the live prompt event must be ignored.

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import {
  initialState,
  promptEventClock,
  reducer,
  runtimeSnapshotPredatesPrompt,
  useController,
} from "../lib/useController";
import type { AppBindings } from "../lib/bridge";
import type { ContextInfo, EffortInfo, Meta, TabMeta, WireEvent } from "../lib/types";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  ok(actual === expected, `${label}${actual === expected ? "" : `: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`}`);
}

console.log("\npending prompt vs stale runtime snapshots");

// ---- reducer invariants ----

const planApprovalEvent = { kind: "approval_request", approval: { id: "plan-1", tool: "exit_plan_mode", subject: "Approve plan" } } as WireEvent;
const askEvent = { kind: "ask_request", ask: { id: "ask-1", question: "Which option?" } } as WireEvent;
const idleStatus = { type: "backend_status", running: false, pendingPrompt: false, backgroundJobs: 0, cancelRequested: false, cancellable: false } as const;

const beforePrompt = promptEventClock();
const withApproval = reducer({ ...initialState }, { type: "event", e: planApprovalEvent });
const afterPrompt = promptEventClock();

eq(withApproval.approval?.id, "plan-1", "approval event arms the prompt");
ok(typeof withApproval.promptArrivedAt === "number", "approval event records its arrival time");

const staleIdle = reducer(withApproval, { ...idleStatus, snapshotAt: beforePrompt });
eq(staleIdle, withApproval, "idle snapshot fetched before the prompt event is ignored");
eq(staleIdle.approval?.id, "plan-1", "stale idle snapshot keeps the approval visible");
eq(staleIdle.pendingPrompt, true, "stale idle snapshot keeps the prompt gate");
eq(staleIdle.running, true, "stale idle snapshot keeps the tab blocked on the user");

const tieIdle = reducer(withApproval, { ...idleStatus, snapshotAt: withApproval.promptArrivedAt });
eq(tieIdle, withApproval, "snapshot tied with the prompt arrival counts as stale");

const staleRunning = reducer(withApproval, { type: "backend_status", running: true, pendingPrompt: false, backgroundJobs: 0, cancelRequested: false, cancellable: true, snapshotAt: beforePrompt });
eq(staleRunning, withApproval, "stale running snapshot cannot drop the prompt gate either");

const freshIdle = reducer(withApproval, { ...idleStatus, snapshotAt: afterPrompt });
eq(freshIdle.approval, undefined, "idle snapshot fetched after the prompt event still reconciles a dead prompt");
eq(freshIdle.running, false, "fresh idle snapshot ends the turn");

const legacyIdle = reducer(withApproval, { ...idleStatus });
eq(legacyIdle.approval, undefined, "snapshot without freshness metadata keeps the legacy clearing behavior");

const withAsk = reducer({ ...initialState }, { type: "event", e: askEvent });
const staleAskIdle = reducer(withAsk, { ...idleStatus, snapshotAt: beforePrompt });
eq(staleAskIdle.ask?.id, "ask-1", "stale idle snapshot keeps the ask card visible");
const freshAskIdle = reducer(withAsk, { ...idleStatus, snapshotAt: promptEventClock() });
eq(freshAskIdle.ask, undefined, "fresh idle snapshot still reconciles a dead ask");

// Production shape: prompt cleared by activation, replayed by the backend,
// then a snapshot fetched between the original and the replay lands late.
const betweenPrompts = promptEventClock();
const replayed = reducer(withApproval, { type: "event", e: planApprovalEvent });
ok((replayed.promptArrivedAt ?? 0) >= (withApproval.promptArrivedAt ?? 0), "replayed prompt refreshes its arrival time");
const staleAfterReplay = reducer(replayed, { ...idleStatus, snapshotAt: betweenPrompts });
eq(staleAfterReplay.approval?.id, "plan-1", "snapshot fetched before the replayed prompt stays stale");

const answered = reducer(withApproval, { type: "clearApproval" });
eq(answered.approval, undefined, "explicit answer clears the prompt");
const idleAfterAnswer = reducer(answered, { ...idleStatus, snapshotAt: beforePrompt });
eq(idleAfterAnswer.running, false, "without a live prompt, even old snapshots reconcile normally");

eq(runtimeSnapshotPredatesPrompt(withApproval, beforePrompt), true, "predates: snapshot older than the prompt");
eq(runtimeSnapshotPredatesPrompt(withApproval, afterPrompt), false, "predates: snapshot newer than the prompt");
eq(runtimeSnapshotPredatesPrompt(withApproval, undefined), false, "predates: unknown snapshot freshness is not stale");
eq(runtimeSnapshotPredatesPrompt({ ...initialState }, beforePrompt), false, "predates: no live prompt means nothing to protect");
eq(runtimeSnapshotPredatesPrompt(undefined, beforePrompt), false, "predates: missing state is not stale");

// Every runtime-status dispatch must carry the fetch time of its snapshot; a
// two-argument call reintroduces the unguarded clearing path.
const here = dirname(fileURLToPath(import.meta.url));
const controllerSource = readFileSync(resolve(here, "../lib/useController.ts"), "utf8");
const twoArgStatusCalls = controllerSource.match(/dispatchRuntimeStatusForTab\(\s*[^(),]+,\s*[^(),]+\s*\)/g) ?? [];
eq(twoArgStatusCalls.length, 0, "every dispatchRuntimeStatusForTab call passes its snapshot fetch time");

// ---- hook-level race: replayed approval vs in-flight stale ListTabs ----

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.localStorage = dom.window.localStorage;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

function flushPromises(): Promise<void> {
  return new Promise((resolvePromise) => setTimeout(resolvePromise, 0));
}

function deferred<T>() {
  let resolvePromise!: (value: T) => void;
  const promise = new Promise<T>((res) => {
    resolvePromise = res;
  });
  return { promise, resolve: resolvePromise };
}

async function waitFor(label: string, predicate: () => boolean) {
  for (let attempt = 0; attempt < 50; attempt += 1) {
    await act(async () => {
      await flushPromises();
    });
    if (predicate()) return;
  }
  throw new Error(`timed out waiting for ${label}`);
}

function tabMeta(): TabMeta {
  return {
    id: "tab-a",
    scope: "project",
    workspaceRoot: "/repo",
    workspaceName: "repo",
    workspacePath: "/repo",
    topicId: "topic-a",
    topicTitle: "General",
    sessionPath: "/repo/sessions/tab-a.jsonl",
    label: "model",
    ready: true,
    running: false,
    cancellable: false,
    mode: "normal",
    toolApprovalMode: "ask",
    tokenMode: "full",
    active: true,
    cwd: "/repo",
  };
}

function metaForTab(): Meta {
  return {
    label: "model",
    ready: true,
    eventChannel: "agent:event",
    cwd: "/repo",
    workspaceRoot: "/repo",
    workspaceName: "repo",
    workspacePath: "/repo",
    autoApproveTools: false,
    bypass: false,
    collaborationMode: "normal",
    toolApprovalMode: "ask",
    tokenMode: "full",
    goal: "",
    goalStatus: "stopped",
  };
}

const context: ContextInfo = { used: 0, window: 100, sessionTokens: 0 };
const effortInfo: EffortInfo = { supported: true, current: "auto", default: "auto", levels: ["auto"] };
const eventHandlers: Array<(e: WireEvent) => void> = [];
let holdNextListTabs: Promise<void> | undefined;

window.runtime = {
  EventsOn: (name: string, cb: (payload: unknown) => void) => {
    if (name === "agent:event") eventHandlers.push(cb as (e: WireEvent) => void);
    return () => {};
  },
  BrowserOpenURL: () => {},
};
window.go = {
  main: {
    App: {
      ListTabs: async () => {
        if (holdNextListTabs) {
          const gatePromise = holdNextListTabs;
          holdNextListTabs = undefined;
          await gatePromise;
        }
        return [tabMeta()];
      },
      MetaForTab: async () => metaForTab(),
      ContextUsageForTab: async () => context,
      EffortForTab: async () => effortInfo,
      BalanceForTab: async () => ({ available: false, display: "" }),
      JobsForTab: async () => [],
      CheckpointsForTab: async () => [],
      HistoryForTab: async () => [],
      HistoryPageForTab: async () => ({ messages: [], startTurn: 0, endTurn: 0, totalTurns: 0, hasOlder: false }),
      HistoryCheckpointTurnsForTab: async () => [],
      ReplayPendingPrompts: async () => {},
      SetActiveTab: async () => {},
    } as Partial<AppBindings> as AppBindings,
  },
};

type Controller = ReturnType<typeof useController>;
let controller: Controller | undefined;

function Probe() {
  controller = useController();
  return null;
}

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");
const root = createRoot(rootEl);

await act(async () => {
  root.render(<Probe />);
  await flushPromises();
});
await waitFor("active tab", () => controller?.activeTabId === "tab-a");
await act(async () => {
  await flushPromises();
  await flushPromises();
});

// A reconciliation fetch starts (its snapshot time is captured now), then the
// backend attach replays the pending plan approval before the fetch resolves.
const gate = deferred<void>();
holdNextListTabs = gate.promise;
let syncPromise: Promise<string | undefined> | undefined;
await act(async () => {
  syncPromise = controller?.syncActiveTab(false);
  await flushPromises();
});
await act(async () => {
  for (const handler of eventHandlers) {
    handler({ kind: "approval_request", tabId: "tab-a", approval: { id: "plan-live", tool: "exit_plan_mode", subject: "Approve plan" } } as WireEvent);
  }
  await flushPromises();
});
eq(controller?.state.approval?.id, "plan-live", "replayed plan approval renders while a snapshot fetch is in flight");

await act(async () => {
  gate.resolve();
  await syncPromise;
  await flushPromises();
});
eq(controller?.state.approval?.id, "plan-live", "a snapshot fetched before the prompt event cannot clear the approval");
eq(controller?.state.pendingPrompt, true, "the prompt gate survives the stale reconciliation");
eq(controller?.state.running, true, "the tab stays blocked on the user after the stale reconciliation");

// A snapshot fetched after the event still reconciles: if the backend truly
// has no pending prompt anymore, the zombie prompt is cleared.
await act(async () => {
  await controller?.syncActiveTab(false);
  await flushPromises();
});
eq(controller?.state.approval?.id, undefined, "a snapshot fetched after the prompt event still reconciles a dead prompt");
eq(controller?.state.running, false, "fresh idle snapshot releases the blocked state");

await act(async () => {
  root.unmount();
});
dom.window.close();

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
