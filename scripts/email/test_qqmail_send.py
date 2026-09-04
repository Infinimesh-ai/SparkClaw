#!/usr/bin/env python3

from __future__ import annotations

import copy
import json
import hashlib
import os
from pathlib import Path
import subprocess
import tempfile
import textwrap
import unittest


ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "scripts" / "email" / "qqmail-send.mjs"

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
        "session_closes": 0,
        "owner_tabs": {"owner-t1": "https://owner.example/private"},
        "task_tabs": {},
        "owner_touched": False,
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
    "compose_button": '.frame-sidebar-compose-btn[data-a11y="button"]',
    "login_page": ".login-page",
    "compose_page": ".mail-compose-page",
    "recipient": 'input[aria-label="To"], input[aria-label="收件人"]',
    "recipient_chip": ".mail-compose-page .receiver-editor .xmail-cmp-account:not(.cmp-account-invalid)",
    "subject": 'input[aria-label="Subject"], input[aria-label="主题"]',
    "body": '.mail-compose-page [contenteditable="true"][aria-label="Enter content"], .mail-compose-page [contenteditable="true"][aria-label="输入正文"], .mail-compose-page .mail-content-editor-inner[contenteditable="true"]',
    "send": '.mail-compose-header .xmail-ui-btn[data-a11y="button"]',
    "sent_page": ".mail-list-page",
}

def new_task():
    if mode == "login":
        url = "https://mail.qq.com/"
    elif mode == "wrong_origin":
        url = "https://example.com/account"
    else:
        url = "https://wx.mail.qq.com/home/index?sid=must-not-leak#/list/1"
    return {
        "body": "",
        "composing": False,
        "recipient": "",
        "send_clicks": 0,
        "sent": False,
        "subject": "",
        "url": url,
    }

def active_task():
    return state["task_tabs"].get(state["active"])

results = []
after_send_click = False
for command in commands:
    result = {}
    success = True
    if command[:2] == ["tab", "new"]:
        label = command[3]
        if command[2] != "--label" or label in state["task_tabs"]:
            success = False
        else:
            if len(command) != 5 or command[4] != "about:blank":
                success = False
            else:
                task = new_task()
                task["url"] = "about:blank"
                state["task_tabs"][label] = task
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
            task["url"] = new_task()["url"]
            result["url"] = task["url"]
        elif command[:2] == ["get", "url"]:
            if mode == "send_batch_failure" and after_send_click:
                success = False
            else:
                result["url"] = task["url"]
        elif command[:2] == ["get", "count"]:
            selector = command[2]
            if selector == selectors["compose_button"]:
                result["count"] = 0 if mode == "login" else 1
            elif selector == selectors["login_page"]:
                result["count"] = 1 if mode == "login" else 0
            elif selector == selectors["send"]:
                result["count"] = 1 if task["composing"] else 0
            else:
                result["count"] = 0
        elif command[:2] == ["is", "visible"]:
            selector = command[2]
            if selector == selectors["compose_button"]:
                result["visible"] = mode != "login"
            elif selector == selectors["login_page"]:
                result["visible"] = mode == "login"
            elif selector == selectors["compose_page"]:
                result["visible"] = task["composing"] and not task["sent"]
            elif selector == selectors["send"]:
                result["visible"] = task["composing"] and not task["sent"]
            elif selector == selectors["sent_page"]:
                result["visible"] = task["sent"]
            else:
                result["visible"] = False
        elif command[:2] == ["is", "enabled"]:
            result["enabled"] = command[2] == selectors["send"] and task["composing"]
        elif command[:2] == ["click", selectors["compose_button"]]:
            task["composing"] = True
            task["url"] = "https://wx.mail.qq.com/home/index?sid=must-not-leak#/compose"
        elif command[:2] == ["fill", selectors["recipient"]]:
            task["recipient"] = command[2]
        elif command[:2] == ["fill", selectors["subject"]]:
            task["subject"] = command[2]
        elif command[:2] == ["fill", selectors["body"]]:
            task["body"] = command[2]
        elif command[:3] == ["get", "text", selectors["recipient_chip"]]:
            result["text"] = "other@example.com" if mode == "draft_mismatch" else task["recipient"]
        elif command[:3] == ["get", "value", selectors["subject"]]:
            result["value"] = task["subject"]
        elif command[:3] == ["get", "text", selectors["body"]]:
            result["text"] = "Hello from\nSparkClaw" if mode == "body_mismatch" else task["body"]
        elif command[:3] == ["get", "text", selectors["send"]]:
            result["text"] = "Not Send" if mode == "send_label_mismatch" else "Send"
        elif command[:2] == ["click", selectors["send"]]:
            task["send_clicks"] += 1
            after_send_click = True
            if task["send_clicks"] > 1:
                success = False
            elif mode in {"success", "send_batch_failure"}:
                task["sent"] = True
                task["composing"] = False
                task["url"] = "https://wx.mail.qq.com/home/index?sid=rotated#/list/5"
            elif mode == "unknown_route":
                task["sent"] = True
                task["composing"] = False
                task["url"] = "https://wx.mail.qq.com/home/index?sid=rotated#/list/1"
        elif command[0] in {"wait", "press"}:
            pass
    results.append({"command": command, "error": None if success else "failed", "result": result, "success": success})
    if not success:
        break

state_path.write_text(json.dumps(state), encoding="utf-8")
print(json.dumps(results))
if not all(entry["success"] for entry in results):
    raise SystemExit(1)
"""


class QQMailSendScriptTest(unittest.TestCase):
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
            "operation": "send",
            "invocation_id": "send-123",
            "provider": "qq_mail",
            "account": "default",
            "message": {
                "recipient": "recipient@example.com",
                "subject": "Test subject",
                "body": {"format": "text", "content": "Hello from SparkClaw"},
            },
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
                "AGENT_BROWSER_CDP": "ws://host-cdp.example/capability-secret",
            }
        )
        if not with_cdp:
            environment.pop("AGENT_BROWSER_CDP", None)
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

    def assert_owned_tab_lifecycle(self) -> tuple[str, dict[str, object]]:
        calls = self.calls()
        creation = calls[0]["commands"][0]
        self.assertEqual(creation[:3], ["tab", "new", "--label"])
        self.assertEqual(creation[4], "about:blank")
        label = creation[3]
        self.assertTrue(label.startswith("qqmail-send-"))
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
        self.assertRegex(next(iter(sessions)), r"^scq-s-[a-f0-9]{16}$")
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

    def test_success_verifies_fields_clicks_once_and_returns_safe_json(self) -> None:
        request = self.request()
        result = self.run_script(payload=request)

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stderr, "")
        self.assertEqual(len(result.stdout.splitlines()), 1)
        self.assertEqual(
            json.loads(result.stdout),
            {
                "schema_version": 1,
                "status": "sent",
                "provider": "qq_mail",
                "recipient_digest": "sha256:"
                + hashlib.sha256(
                    request["message"]["recipient"].encode("utf-8"),
                ).hexdigest(),
            },
        )
        label, task = self.assert_owned_tab_lifecycle()
        self.assertEqual(task["send_clicks"], 1)
        self.assertEqual(task["recipient"], request["message"]["recipient"])
        self.assertEqual(task["subject"], request["message"]["subject"])
        self.assertEqual(task["body"], request["message"]["body"]["content"])
        for secret in (
            request["message"]["recipient"],
            request["message"]["body"]["content"],
            "capability-secret",
            "task-tab",
            label,
        ):
            self.assertNotIn(secret, result.stdout)

    def test_optional_subject_remains_empty(self) -> None:
        request = self.request()
        del request["message"]["subject"]
        request["message"]["body"]["content"] = "Derived subject\nSecond line"

        result = self.run_script(payload=request)

        self.assertEqual(result.returncode, 0, result.stderr)
        _, task = self.assert_owned_tab_lifecycle()
        self.assertEqual(task["subject"], "")
        self.assertEqual(task["send_clicks"], 1)

    def test_explicit_empty_subject_and_runner_session_are_preserved(self) -> None:
        request = self.request()
        request["message"]["subject"] = ""

        result = self.run_script(
            payload=request,
            browser_environment={"AGENT_BROWSER_SESSION": "sc-email-runtime"},
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        _, task = self.assert_owned_tab_lifecycle()
        self.assertEqual(task["subject"], "")

    def test_login_required_closes_owned_tab_without_send_click(self) -> None:
        result = self.run_script("login")

        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.error_payload(result)["code"], "email_login_required")
        _, task = self.assert_owned_tab_lifecycle()
        self.assertEqual(task["send_clicks"], 0)

    def test_field_mismatch_before_click_fails_and_cleans_up(self) -> None:
        for mode in ("draft_mismatch", "body_mismatch"):
            with self.subTest(mode=mode):
                self.log.unlink(missing_ok=True)
                self.state.unlink(missing_ok=True)
                result = self.run_script(mode)

                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.error_payload(result)["code"], "draft_verification_failed")
                _, task = self.assert_owned_tab_lifecycle()
                self.assertEqual(task["send_clicks"], 0)

    def test_send_control_failure_occurs_before_click(self) -> None:
        result = self.run_script("send_label_mismatch")

        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.error_payload(result)["code"], "send_control_not_ready")
        _, task = self.assert_owned_tab_lifecycle()
        self.assertEqual(task["send_clicks"], 0)

    def test_uncertain_post_click_results_never_retry(self) -> None:
        for mode in ("unknown_route", "send_batch_failure"):
            with self.subTest(mode=mode):
                self.log.unlink(missing_ok=True)
                self.state.unlink(missing_ok=True)
                result = self.run_script(mode)

                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.error_payload(result)["code"], "send_outcome_unknown")
                self.assertNotIn("recipient@example.com", result.stderr)
                self.assertNotIn("Hello from SparkClaw", result.stderr)
                _, task = self.assert_owned_tab_lifecycle()
                self.assertEqual(task["send_clicks"], 1)
                clicks = [
                    command
                    for call in self.calls()
                    for command in call["commands"]
                    if command[:2] == ["click", '.mail-compose-header .xmail-ui-btn[data-a11y="button"]']
                ]
                self.assertEqual(len(clicks), 1)

    def test_strict_field_validation_rejects_before_browser(self) -> None:
        cases: list[tuple[str, dict[str, object], str]] = []

        top_extra = self.request()
        top_extra["extra"] = True
        cases.append(("top_extra", top_extra, "invalid_input"))

        for field, value in {
            "schema_version": 2,
            "operation": "probe",
            "provider": "other_mail",
            "account": "secondary",
            "invocation_id": "contains space",
        }.items():
            invalid_fixed = self.request()
            invalid_fixed[field] = value
            cases.append((f"invalid_{field}", invalid_fixed, "invalid_input"))

        message_extra = self.request()
        message_extra["message"]["extra"] = True
        cases.append(("message_extra", message_extra, "invalid_message"))

        body_extra = self.request()
        body_extra["message"]["body"]["extra"] = True
        cases.append(("body_extra", body_extra, "invalid_body"))

        multiple_recipients = self.request()
        multiple_recipients["message"]["recipient"] = "one@example.com,two@example.com"
        cases.append(("multiple_recipients", multiple_recipients, "invalid_recipient"))

        newline_subject = self.request()
        newline_subject["message"]["subject"] = "Line one\nLine two"
        cases.append(("newline_subject", newline_subject, "invalid_subject"))

        empty_body = self.request()
        empty_body["message"]["body"]["content"] = "   "
        cases.append(("empty_body", empty_body, "invalid_body"))

        html_body = self.request()
        html_body["message"]["body"]["format"] = "html"
        cases.append(("html_body", html_body, "invalid_body"))

        for name, payload, code in cases:
            with self.subTest(name=name):
                result = self.run_script(payload=payload)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.error_payload(result)["code"], code)
                self.assertEqual(self.calls(), [])

        result = self.run_script(raw_input="{not-json")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.error_payload(result)["code"], "invalid_json")
        self.assertEqual(self.calls(), [])

    def test_missing_cdp_and_profile_state_paths_are_rejected(self) -> None:
        result = self.run_script(with_cdp=False)
        self.assertEqual(self.error_payload(result)["code"], "host_cdp_required")
        self.assertEqual(self.calls(), [])

        for name, value in {
            "AGENT_BROWSER_PROFILE": "/tmp/profile",
            "AGENT_BROWSER_STATE": "/tmp/state.json",
            "AGENT_BROWSER_RESTORE": "saved-session",
            "AGENT_BROWSER_AUTO_CONNECT": "1",
        }.items():
            with self.subTest(name=name):
                result = self.run_script(browser_environment={name: value})
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(
                    self.error_payload(result)["code"],
                    "forbidden_browser_environment",
                )
                self.assertEqual(self.calls(), [])

    def test_no_tab_listing_profile_or_state_commands(self) -> None:
        result = self.run_script()
        self.assertEqual(result.returncode, 0, result.stderr)

        forbidden_commands = {"cookies", "storage", "state", "connect"}
        for call in self.calls():
            for command in call["commands"]:
                self.assertNotEqual(command[:2], ["tab", "list"])
                self.assertNotIn(command[0], forbidden_commands)
        self.assertEqual(
            [command for call in self.calls() for command in call["commands"] if command == ["close"]],
            [["close"]],
        )


if __name__ == "__main__":
    unittest.main()
