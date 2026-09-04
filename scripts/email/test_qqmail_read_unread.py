#!/usr/bin/env python3

from __future__ import annotations

import json
import os
from pathlib import Path
import subprocess
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "email" / "qqmail-read-unread.mjs"

FAKE_AGENT_BROWSER = r"""#!/usr/bin/env python3
import copy
import json
import os
from pathlib import Path
import sys

log_path = Path(os.environ["FAKE_QQMAIL_LOG"])
state_path = Path(os.environ["FAKE_QQMAIL_STATE"])
mode = os.environ.get("FAKE_QQMAIL_MODE", "success")
commands = json.load(sys.stdin)

if state_path.exists():
    state = json.loads(state_path.read_text(encoding="utf-8"))
else:
    state = {
        "active": "owner-t1",
        "closed_tasks": {},
        "owner_tabs": {"owner-t1": "https://owner.example/private"},
        "owner_touched": False,
        "session_closes": 0,
        "task_tabs": {},
    }

with log_path.open("a", encoding="utf-8") as handle:
    handle.write(json.dumps({
        "argv": sys.argv[1:],
        "commands": commands,
        "session": os.environ.get("AGENT_BROWSER_SESSION", ""),
        "namespace": os.environ.get("AGENT_BROWSER_NAMESPACE", ""),
        "agent_environment": sorted(
            name for name in os.environ if name.startswith("AGENT_BROWSER_")
        ),
    }) + "\n")

selectors = {
    "inbox": ".mail-list-page",
    "login": ".login-page",
    "unread": ":nth-match(.mail-list-page-item:has(.mail-subject.mail-unread), 1)",
    "list_sender": ":nth-match(.mail-list-page-item:has(.mail-subject.mail-unread), 1) .mail-sender",
    "list_subject": ":nth-match(.mail-list-page-item:has(.mail-subject.mail-unread), 1) .mail-subject",
    "list_digest": ":nth-match(.mail-list-page-item:has(.mail-subject.mail-unread), 1) .mail-digest",
    "list_time": ":nth-match(.mail-list-page-item:has(.mail-subject.mail-unread), 1) .mail-time",
    "reader": ".mail-list-page-reader",
    "subject": ".mail-detail-subject .mail-subject-text",
    "sender_name": ".mail-detail-basic .basic-body-item:first-child .cmp-account-nick",
    "sender_address": ".mail-detail-basic .basic-body-item:first-child .cmp-account-email",
    "received_at": ".mail-detail-basic .time-text",
    "body": ".mail-reader-body .reader-body-children",
    "attachment_card": ".mail-detail-attaches > .mail-detail-attach-card",
}

def new_task():
    if mode == "login":
        return {"opened": False, "url": "https://mail.qq.com/"}
    if mode == "wrong_origin":
        return {"opened": False, "url": "https://example.com/account"}
    return {
        "opened": False,
        "url": "https://wx.mail.qq.com/home/index?sid=must-not-leak#/list/1",
    }

def active_task():
    return state["task_tabs"].get(state["active"])

results = []
for command in commands:
    result = {}
    success = True
    if command[:2] == ["tab", "new"]:
        label = command[3]
        if command[2] != "--label" or label in state["task_tabs"]:
            success = False
        elif len(command) != 5 or command[4] != "about:blank":
            success = False
        else:
            state["task_tabs"][label] = {"opened": False, "url": "about:blank"}
            state["active"] = label
            result = {"label": label, "tabId": "task-tab", "url": "about:blank"}
    elif command[:2] == ["tab", "close"]:
        label = command[2]
        if label not in state["task_tabs"]:
            success = False
        else:
            state["closed_tasks"][label] = copy.deepcopy(state["task_tabs"][label])
            del state["task_tabs"][label]
            state["active"] = "owner-t1"
    elif command == ["close"]:
        state["session_closes"] += 1
        result = {"closed": True}
    elif command[0] == "tab":
        label = command[1]
        if label not in state["task_tabs"]:
            success = False
        else:
            state["active"] = label
    else:
        task = active_task()
        if task is None:
            state["owner_touched"] = True
            success = False
        elif command[0] == "open" and command[1] == "https://wx.mail.qq.com/":
            task.update(new_task())
            result["url"] = task["url"]
        elif command[:2] == ["get", "url"]:
            result["url"] = task["url"]
        elif command[:2] == ["get", "count"]:
            selector = command[2]
            if selector == selectors["inbox"]:
                result["count"] = 0 if mode == "login" else 1
            elif selector == selectors["login"]:
                result["count"] = 1 if mode == "login" else 0
            elif selector == selectors["unread"]:
                result["count"] = 0 if mode == "no_unread" else 1
            elif selector == selectors["attachment_card"]:
                result["count"] = 0 if mode == "no_attachments" else 2
            else:
                result["count"] = 0
        elif command[:2] == ["is", "visible"]:
            selector = command[2]
            if selector == selectors["inbox"]:
                result["visible"] = mode != "login"
            elif selector == selectors["login"]:
                result["visible"] = mode == "login"
            elif selector == selectors["unread"]:
                result["visible"] = mode != "no_unread"
            elif selector == selectors["reader"]:
                result["visible"] = task["opened"]
            else:
                result["visible"] = False
        elif command[:3] == ["get", "text", selectors["list_sender"]]:
            result["text"] = "Example Sender"
        elif command[:3] == ["get", "text", selectors["list_subject"]]:
            result["text"] = "Unread subject"
        elif command[:3] == ["get", "text", selectors["list_digest"]]:
            result["text"] = "Message preview"
        elif command[:3] == ["get", "text", selectors["list_time"]]:
            result["text"] = "09:30"
        elif command[:2] == ["click", selectors["unread"]]:
            task["opened"] = True
            task["url"] = "https://wx.mail.qq.com/home/index?sid=rotated#/read/123"
        elif command[:3] == ["get", "text", selectors["subject"]]:
            if mode == "detail_failure":
                success = False
            else:
                result["text"] = "Different subject" if mode == "mismatch" else "Unread subject"
        elif command[:3] == ["get", "text", selectors["sender_name"]]:
            result["text"] = "Example Sender"
        elif command[:3] == ["get", "text", selectors["sender_address"]]:
            result["text"] = "<sender@example.com>"
        elif command[:3] == ["get", "text", selectors["received_at"]]:
            result["text"] = "2026-09-02 09:30"
        elif command[:3] == ["get", "text", selectors["body"]]:
            result["text"] = "First line\nSecond line"
        elif command[:2] == ["get", "text"] and command[2].startswith(
            selectors["attachment_card"] + ":nth-child("
        ):
            remainder = command[2][len(selectors["attachment_card"] + ":nth-child("):]
            index_text, field = remainder.split(" of .mail-detail-attach-card) .", 1)
            index = int(index_text)
            if mode == "attachment_failure" and index == 2 and field == "attach-name":
                success = False
            elif field == "attach-name":
                result["text"] = ["report", "image"][index - 1]
            elif field == "attach-suffix":
                result["text"] = [".pdf", ".jpg"][index - 1]
            elif field == "attach-size":
                result["text"] = ["(12K)", "(649K)"][index - 1]
        elif command[0] == "wait":
            pass
    results.append({
        "command": command,
        "error": None if success else "failed",
        "result": result,
        "success": success,
    })
    if not success:
        break

state_path.write_text(json.dumps(state), encoding="utf-8")
print(json.dumps(results))
if not all(entry["success"] for entry in results):
    raise SystemExit(1)
"""


class QQMailReadUnreadScriptTest(unittest.TestCase):
    def setUp(self) -> None:
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.directory = Path(self.temp.name)
        self.browser = self.directory / "agent-browser"
        self.browser.write_text(textwrap.dedent(FAKE_AGENT_BROWSER), encoding="utf-8")
        self.browser.chmod(0o755)
        self.log = self.directory / "calls.jsonl"
        self.state = self.directory / "state.json"

    def request(self) -> dict[str, object]:
        return {
            "schema_version": 1,
            "operation": "read_first_unread",
            "invocation_id": "read-123",
            "provider": "qq_mail",
            "account": "default",
        }

    def run_script(
        self,
        mode: str = "success",
        *,
        payload: dict[str, object] | None = None,
        raw_input: str | None = None,
        with_cdp: bool = True,
        browser_environment: dict[str, str] | None = None,
    ) -> subprocess.CompletedProcess[str]:
        environment = os.environ.copy()
        for name in list(environment):
            if name.startswith("AGENT_BROWSER_") or name.startswith("SPARKCLAW_QQMAIL_"):
                environment.pop(name, None)
        environment.update(
            {
                "SPARKCLAW_AGENT_BROWSER": str(self.browser),
                "FAKE_QQMAIL_LOG": str(self.log),
                "FAKE_QQMAIL_STATE": str(self.state),
                "FAKE_QQMAIL_MODE": mode,
            }
        )
        if with_cdp:
            environment["AGENT_BROWSER_CDP"] = "ws://host-cdp.example/capability-secret"
        if browser_environment:
            environment.update(browser_environment)
        return subprocess.run(
            ["node", str(SCRIPT)],
            cwd=ROOT,
            env=environment,
            input=raw_input if raw_input is not None else json.dumps(payload or self.request()),
            capture_output=True,
            text=True,
            check=False,
        )

    def calls(self) -> list[dict[str, object]]:
        if not self.log.exists():
            return []
        return [json.loads(line) for line in self.log.read_text(encoding="utf-8").splitlines()]

    def final_state(self) -> dict[str, object]:
        return json.loads(self.state.read_text(encoding="utf-8"))

    def error_payload(self, result: subprocess.CompletedProcess[str]) -> dict[str, object]:
        self.assertEqual(result.stdout, "")
        self.assertEqual(len(result.stderr.splitlines()), 1)
        return json.loads(result.stderr)

    def click_count(self) -> int:
        return sum(
            1
            for call in self.calls()
            for command in call["commands"]
            if command[:2] == ["click", selectors_unread()]
        )

    def assert_owned_tab_lifecycle(self) -> tuple[str, dict[str, object]]:
        calls = self.calls()
        creation = calls[0]["commands"][0]
        self.assertEqual(creation[:3], ["tab", "new", "--label"])
        self.assertEqual(creation[4], "about:blank")
        label = creation[3]
        self.assertTrue(label.startswith("qqmail-read-"))
        self.assertEqual(calls[-2]["commands"], [["tab", "close", label]])
        self.assertEqual(calls[-1]["commands"], [["close"]])

        for call in calls[1:-2]:
            commands = call["commands"]
            self.assertEqual(len(commands) % 2, 0)
            for index in range(0, len(commands), 2):
                self.assertEqual(commands[index], ["tab", label])
        navigation = calls[1]["commands"]
        self.assertEqual(navigation[1], ["open", "https://wx.mail.qq.com/"])
        self.assertEqual(navigation[3], ["wait", "3000"])
        self.assertEqual(navigation[5], ["get", "url"])

        sessions = {call["session"] for call in calls}
        namespaces = {call["namespace"] for call in calls}
        self.assertEqual(len(sessions), 1)
        self.assertEqual(len(namespaces), 1)
        self.assertRegex(next(iter(sessions)), r"^scq-r-[a-f0-9]{16}$")
        self.assertRegex(next(iter(namespaces)), r"^scq-[a-f0-9]{16}$")
        for call in calls:
            self.assertEqual(
                call["agent_environment"],
                [
                    "AGENT_BROWSER_CDP",
                    "AGENT_BROWSER_IDLE_TIMEOUT_MS",
                    "AGENT_BROWSER_NAMESPACE",
                    "AGENT_BROWSER_SESSION",
                ],
            )
            self.assertEqual(call["argv"][0:1], ["--config"])
            self.assertEqual(call["argv"][-3:], ["batch", "--json", "--bail"])

        state = self.final_state()
        self.assertFalse(state["owner_touched"])
        self.assertEqual(state["owner_tabs"], {"owner-t1": "https://owner.example/private"})
        self.assertEqual(state["task_tabs"], {})
        self.assertIn(label, state["closed_tasks"])
        self.assertEqual(state["session_closes"], 1)
        return label, state["closed_tasks"][label]

    def test_reads_first_unread_message_once(self) -> None:
        result = self.run_script()

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stderr, "")
        payload = json.loads(result.stdout)
        self.assertEqual(payload["schema_version"], 1)
        self.assertEqual(payload["status"], "read")
        self.assertTrue(payload["was_unread"])
        self.assertTrue(payload["marked_read"])
        self.assertEqual(payload["message"]["sender"]["name"], "Example Sender")
        self.assertEqual(payload["message"]["sender"]["address"], "sender@example.com")
        self.assertEqual(payload["message"]["subject"], "Unread subject")
        self.assertEqual(payload["message"]["body"], "First line\nSecond line")
        self.assertEqual(payload["message"]["attachment_count"], 2)
        self.assertTrue(payload["message"]["attachments_complete"])
        self.assertEqual(
            payload["message"]["attachments"],
            [
                {"name": "report.pdf", "extension": ".pdf", "size": "12K"},
                {"name": "image.jpg", "extension": ".jpg", "size": "649K"},
            ],
        )
        label, task = self.assert_owned_tab_lifecycle()
        self.assertTrue(task["opened"])
        self.assertEqual(self.click_count(), 1)
        for secret in ("capability-secret", "must-not-leak", "rotated", "task-tab", label):
            self.assertNotIn(secret, result.stdout)

    def test_no_unread_message_does_not_click(self) -> None:
        result = self.run_script("no_unread")

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(
            json.loads(result.stdout),
            {"schema_version": 1, "status": "no_unread", "provider": "qq_mail"},
        )
        self.assertEqual(self.click_count(), 0)
        _, task = self.assert_owned_tab_lifecycle()
        self.assertFalse(task["opened"])

    def test_message_without_attachments_returns_complete_empty_list(self) -> None:
        result = self.run_script("no_attachments")

        self.assertEqual(result.returncode, 0, result.stderr)
        payload = json.loads(result.stdout)
        self.assertEqual(payload["status"], "read")
        self.assertEqual(payload["message"]["attachment_count"], 0)
        self.assertTrue(payload["message"]["attachments_complete"])
        self.assertEqual(payload["message"]["attachments"], [])
        self.assertEqual(self.click_count(), 1)
        self.assert_owned_tab_lifecycle()

    def test_mismatched_detail_is_explicit(self) -> None:
        result = self.run_script("mismatch")

        self.assertEqual(result.returncode, 0, result.stderr)
        payload = json.loads(result.stdout)
        self.assertEqual(payload["message"]["subject"], "Different subject")
        self.assertFalse(payload["message"]["list_matches_detail"])
        self.assertEqual(self.click_count(), 1)
        self.assert_owned_tab_lifecycle()

    def test_post_click_detail_failure_has_unknown_outcome_and_is_not_retried(self) -> None:
        result = self.run_script("detail_failure")

        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.error_payload(result)["code"], "read_outcome_unknown")
        self.assertEqual(self.click_count(), 1)
        self.assert_owned_tab_lifecycle()

    def test_post_click_attachment_failure_has_unknown_outcome(self) -> None:
        result = self.run_script("attachment_failure")

        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.error_payload(result)["code"], "read_outcome_unknown")
        self.assertEqual(self.click_count(), 1)
        self.assert_owned_tab_lifecycle()

    def test_login_page_fails_before_click(self) -> None:
        result = self.run_script("login")

        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.error_payload(result)["code"], "email_login_required")
        self.assertEqual(self.click_count(), 0)
        self.assert_owned_tab_lifecycle()

    def test_wrong_origin_fails_before_click(self) -> None:
        result = self.run_script("wrong_origin")

        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.error_payload(result)["code"], "provider_origin_mismatch")
        self.assertEqual(self.click_count(), 0)
        self.assert_owned_tab_lifecycle()

    def test_missing_cdp_and_profile_environment_are_rejected(self) -> None:
        missing = self.run_script(with_cdp=False)
        self.assertNotEqual(missing.returncode, 0)
        self.assertEqual(self.error_payload(missing)["code"], "host_cdp_required")
        self.assertEqual(self.calls(), [])

        forbidden = self.run_script(browser_environment={"AGENT_BROWSER_PROFILE": "/tmp/profile"})
        self.assertNotEqual(forbidden.returncode, 0)
        self.assertEqual(self.error_payload(forbidden)["code"], "forbidden_browser_environment")
        self.assertEqual(self.calls(), [])

    def test_input_contract_rejects_unknown_fields_and_invalid_json(self) -> None:
        request = self.request()
        request["unexpected"] = True
        unknown = self.run_script(payload=request)
        self.assertNotEqual(unknown.returncode, 0)
        self.assertEqual(self.error_payload(unknown)["code"], "invalid_input")
        self.assertEqual(self.calls(), [])

        invalid = self.run_script(raw_input="{")
        self.assertNotEqual(invalid.returncode, 0)
        self.assertEqual(self.error_payload(invalid)["code"], "invalid_json")
        self.assertEqual(self.calls(), [])


def selectors_unread() -> str:
    return ":nth-match(.mail-list-page-item:has(.mail-subject.mail-unread), 1)"


if __name__ == "__main__":
    unittest.main()
