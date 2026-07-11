import type { CommandInfo } from "./types";

export type InvocationDisplay = {
  name: string;
  label: string;
  source?: string;
};

export function commandUsesStructuredInvocation(command: CommandInfo): boolean {
  return command.kind === "skill" || command.kind === "subagent";
}

export function invocationLabel(name: string): string {
  const unqualified = name.split(":").pop() || name;
  return unqualified
    .split(/[-_.]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

export function invocationDisplayForCommand(command: CommandInfo): InvocationDisplay {
  return {
    name: command.name,
    label: invocationLabel(command.name),
    source: command.plugin || command.name.split(":").slice(0, -1).join(":") || undefined,
  };
}

export function invocationDisplayFromMessage(displayText: string, submitText?: string): InvocationDisplay | null {
  const display = displayText.trim();
  const submit = submitText?.trim() ?? "";
  if (!submit || submit === display || /^\/[A-Za-z0-9_.:-]+(?:\s|$)/.test(display)) return null;

  const sessionQuestionMarker = "当前用户问题：\n";
  const markerIndex = submit.lastIndexOf(sessionQuestionMarker);
  const invocationText = (markerIndex >= 0 ? submit.slice(markerIndex + sessionQuestionMarker.length) : submit).trimStart();
  const match = markerIndex >= 0
    ? /^\/([A-Za-z0-9_.:-]+)(?=\s|$)/.exec(invocationText)
    : /(?:^|\n)\/([A-Za-z0-9_.:-]+)(?=\s|$)/.exec(invocationText);
  if (!match) return null;
  if (markerIndex < 0) {
    const slashOffset = match.index + match[0].lastIndexOf("/");
    const prefix = invocationText.slice(0, slashOffset).trim();
    if (prefix && !prefix.startsWith("<") && !prefix.startsWith("[Plan mode")) return null;
  }
  const name = match[1];
  return {
    name,
    label: invocationLabel(name),
    source: name.includes(":") ? name.split(":").slice(0, -1).join(":") : undefined,
  };
}
