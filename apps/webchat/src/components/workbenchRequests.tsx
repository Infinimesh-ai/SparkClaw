import type { Language } from "../i18n";
import type { WorkbenchRequestState, WorkbenchRequestStatus } from "../lib/workbenchRequest";

const labels: Record<Language, Record<WorkbenchRequestState, string>> = {
  en: { accepted: "Submitted", running: "Running", approval_pending: "Waiting for approval", browser_login_blocked: "Browser sign-in required", completed: "Completed", blocked: "Blocked", delivered: "Delivered", delivery_failed: "Delivery failed; the execution has finished", failed: "Failed", canceled: "Canceled", unknown: "Outcome unconfirmed; this request will not run again", delivery_expired: "Result delivery expired" },
  zh: { accepted: "已提交", running: "执行中", approval_pending: "等待审批", browser_login_blocked: "需要登录浏览器", completed: "已完成", blocked: "已阻止", delivered: "已送达", delivery_failed: "执行已结束，投递失败", failed: "执行失败", canceled: "已取消", unknown: "结果待确认；此请求不会再次执行", delivery_expired: "结果投递已过期" }
};

export function WorkbenchRequests({ requests, language, onRefresh, onCancel }: {
  requests: WorkbenchRequestStatus[];
  language: Language;
  onRefresh: (requestID: string) => void;
  onCancel: (requestID: string) => void;
}) {
  const visible = requests.filter((request) => !["completed", "delivered"].includes(request.state));
  if (!visible.length) return null;
  return <div className="workbenchRequests" aria-label={language === "zh" ? "执行状态" : "Execution status"}>
    {visible.map((request) => <div className="noticeBanner" key={request.request_id}>
      <span>{labels[language][request.state]} <small>· {request.request_id.slice(0, 8)}</small></span>
      <button type="button" onClick={() => onRefresh(request.request_id)}>{language === "zh" ? "检查状态" : "Check status"}</button>
      {["accepted", "running"].includes(request.state) && <button type="button" onClick={() => onCancel(request.request_id)}>{language === "zh" ? "取消" : "Cancel"}</button>}
    </div>)}
  </div>;
}
