import { useT } from "../lib/i18n";
import type { WireApproval } from "../lib/types";

export function ApprovalModal({
  approval,
  onAnswer,
}: {
  approval: WireApproval;
  onAnswer: (allow: boolean, session: boolean) => void;
}) {
  const t = useT();
  // A plan approval is special: the controller proposes it when a plan-mode turn
  // ends with a proposal. The plan itself is already shown above as the assistant's
  // reply, so this is just the gate — start coding vs keep planning.
  if (approval.tool === "exit_plan_mode") {
    return (
      <div className="modal-backdrop">
        <div className="modal modal--plan">
          <div className="modal__title">{t("approval.planTitle")}</div>
          <div className="modal__plannote">{t("approval.planNote")}</div>
          <div className="modal__actions">
            <button className="btn" onClick={() => onAnswer(false, false)}>
              {t("approval.keepPlanning")}
            </button>
            <button className="btn btn--primary" onClick={() => onAnswer(true, false)}>
              {t("approval.proceed")}
            </button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="modal-backdrop">
      <div className="modal">
        <div className="modal__title">{t("approval.toolTitle")}</div>
        <div className="modal__tool">
          <span className="tool__name">{approval.tool}</span>
        </div>
        {approval.subject && <pre className="modal__subject">{approval.subject}</pre>}
        <div className="modal__actions">
          <button className="btn" onClick={() => onAnswer(false, false)}>
            {t("approval.deny")}
          </button>
          <button className="btn" onClick={() => onAnswer(true, false)}>
            {t("approval.allowOnce")}
          </button>
          <button className="btn btn--primary" onClick={() => onAnswer(true, true)}>
            {t("approval.allowSession")}
          </button>
        </div>
      </div>
    </div>
  );
}
