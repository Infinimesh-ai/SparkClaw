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
SCRIPT = ROOT / "scripts" / "email" / "qqmail-login-probe.mjs"

FAKE_AGENT_BROWSER = r"""#!/usr/bin/env python3
import json
import os
from pathlib import Path
import sys
import time

log_path = Path(os.environ["FAKE_QQMAIL_LOG"])
state_path = Path(os.environ["FAKE_QQMAIL_STATE"])
mode = os.environ.get("FAKE_QQMAIL_MODE", "ready")
commands = json.load(sys.stdin)

if state_path.exists():
    state = json.loads(state_path.read_text(encoding="utf-8"))
else:
    state = {
        "active": "owner-t1",
        "call_index": 0,
        "closed": [],
        "session_closes": 0,
        "owner_tabs": {"owner-t1": "https://owner.example/private"},
        "task_tabs": {},
        "owner_touched": False,
    }
state["call_index"] += 1
call_index = state["call_index"]

agent_environment = sorted(
    name for name in os.environ if name.startswith("AGENT_BROWSER_")
)
with log_path.open("a", encoding="utf-8") as handle:
    handle.write(json.dumps({
        "argv": sys.argv[1:],
        "commands": commands,
        "session": os.environ.get("AGENT_BROWSER_SESSION", ""),
        "namespace": os.environ.get("AGENT_BROWSER_NAMESPACE", ""),
        "agent_environment": agent_environment,
    }) + "\n")

if mode == "timeout" and call_index == 1:
    state_path.write_text(json.dumps(state), encoding="utf-8")
    time.sleep(30)

selectors = {
    "compose": '.frame-sidebar .frame-sidebar-compose-btn[data-a11y="button"]',
    "profile": ".frame-header .xmail-cmp-profile-btn",
    "account": ".frame-header .xmail-cmp-profile-btn .profile-user-info .user-email",
    "login_page": ".login-page",
    "login_tabs": ".login-page .xmail-cmp-head-tabs",
    "app_login": ".login-page .xmail-cmp-app-login",
    "wx_frame": ".login-page .xmail-cmp-wx-login-wrap iframe",
    "qq_frame": ".login-page .xmail-cmp-qq-pt-login-wrap iframe",
    "connect_frame": ".login-page .xmail-cmp-qq-connect-login-wrap iframe",
}
positive = {selectors["compose"], selectors["profile"], selectors["account"]}

def task_url():
    if mode == "wrong_origin":
        return "https://example.com/account"
    if mode == "login":
        return "https://mail.qq.com/"
    return "https://wx.mail.qq.com/home/index?sid=must-not-leak#/list/1"

def selector_visible(selector):
    if mode == "ready":
        return selector in positive
    if mode == "login":
        return selector in {selectors["login_page"], selectors["login_tabs"], selectors["app_login"]}
    if mode == "conflict":
        return selector in positive or selector in {selectors["login_page"], selectors["app_login"]}
    return False

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
        else:
            if len(command) != 5 or command[4] != "about:blank":
                success = False
            else:
                state["task_tabs"][label] = {"url": "about:blank"}
                state["active"] = label
                result = {"label": label, "tabId": "task-tab", "url": "about:blank"}
    elif command[:2] == ["tab", "close"]:
        label = command[2]
        if label not in state["task_tabs"]:
            success = False
        else:
            del state["task_tabs"][label]
            state["closed"].append(label)
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
            task["url"] = task_url()
            result["url"] = task["url"]
        elif command[:2] == ["get", "url"]:
            result["url"] = task["url"]
        elif command[:2] == ["get", "count"]:
            result["count"] = 1 if selector_visible(command[2]) else 0
        elif command[:2] == ["is", "visible"]:
            result["visible"] = selector_visible(command[2])
        elif command[:3] == ["get", "text", selectors["account"]]:
            result["text"] = "123456789@qq.com"
        elif command[0] == "wait" and command[1] == "3000":
            pass
    results.append({"command": command, "error": None if success else "failed", "result": result, "success": success})
    if not success:
        break

state_path.write_text(json.dumps(state), encoding="utf-8")
if mode == "invalid_output" and call_index == 1:
    print("not-json")
else:
    print(json.dumps(results))
if not all(entry["success"] for entry in results):
    raise SystemExit(1)
"""


class QQMailLoginProbeScriptTest(unittest.TestCase):
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
            "operation": "probe",
            "invocation_id": "probe-123",
            "provider": "qq_mail",
            "account": "default",
        }

    def run_script(
        self,
        mode: str = "ready",
        *,
        payload: dict[str, object] | None = None,
        raw_input: str | None = None,
        with_cdp: bool = True,
        browser_environment: dict[str, str] | None = None,
        timeout_ms: int | None = None,
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
        if timeout_ms is not None:
            environment["SPARKCLAW_QQMAIL_TIMEOUT_MS"] = str(timeout_ms)

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

    def assert_owned_tab_lifecycle(self) -> str:
        calls = self.calls()
        creation = calls[0]["commands"][0]
        self.assertEqual(creation[:3], ["tab", "new", "--label"])
        self.assertEqual(creation[4], "about:blank")
        label = creation[3]
        self.assertTrue(label.startswith("qqmail-probe-"))

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
        self.assertRegex(next(iter(sessions)), r"^scq-p-[a-f0-9]{16}$")
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
        self.assertIn(label, state["closed"])
        self.assertEqual(state["session_closes"], 1)
        return label

    def test_authenticated_page_returns_safe_result_and_closes_owned_tab(self) -> None:
        result = self.run_script()

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stderr, "")
        self.assertEqual(len(result.stdout.splitlines()), 1)
        self.assertEqual(
            json.loads(result.stdout),
            {
                "schema_version": 1,
                "status": "ready",
                "provider": "qq_mail",
                "account_hint": "12***@qq.com",
            },
        )
        self.assert_owned_tab_lifecycle()
        for secret in ("123456789@qq.com", "capability-secret", "task-tab", "sparkclaw-qqmail"):
            self.assertNotIn(secret, result.stdout)

    def test_logged_out_page_returns_stable_code_and_cleans_up(self) -> None:
        result = self.run_script("login")

        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.error_payload(result)["code"], "email_login_required")
        self.assert_owned_tab_lifecycle()

    def test_conflicting_evidence_fails_closed_and_cleans_up(self) -> None:
        result = self.run_script("conflict")

        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.error_payload(result)["code"], "login_evidence_conflict")
        self.assert_owned_tab_lifecycle()

    def test_wrong_origin_fails_closed_and_cleans_up(self) -> None:
        result = self.run_script("wrong_origin")

        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.error_payload(result)["code"], "provider_origin_mismatch")
        self.assert_owned_tab_lifecycle()

    def test_page_contract_change_fails_closed_and_cleans_up(self) -> None:
        result = self.run_script("contract_change")

        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.error_payload(result)["code"], "page_contract_changed")
        self.assert_owned_tab_lifecycle()

    def test_timeout_and_invalid_output_fail_closed(self) -> None:
        result = self.run_script("timeout", timeout_ms=5000)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.error_payload(result)["code"], "login_probe_timeout")

        self.log.unlink(missing_ok=True)
        self.state.unlink(missing_ok=True)
        result = self.run_script("invalid_output")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.error_payload(result)["code"], "login_probe_invalid_output")
        self.assertEqual(self.final_state()["task_tabs"], {})

    def test_missing_cdp_and_forbidden_startup_paths_are_rejected_before_browser(self) -> None:
        result = self.run_script(with_cdp=False)
        self.assertEqual(self.error_payload(result)["code"], "host_cdp_required")
        self.assertEqual(self.calls(), [])

        forbidden = {
            "AGENT_BROWSER_PROFILE": "/tmp/profile",
            "AGENT_BROWSER_STATE": "/tmp/state.json",
            "AGENT_BROWSER_RESTORE": "saved-session",
            "AGENT_BROWSER_AUTO_CONNECT": "1",
            "AGENT_BROWSER_CONFIG": "/tmp/config.json",
            "AGENT_BROWSER_EXECUTABLE_PATH": "/tmp/chromium",
            "AGENT_BROWSER_ARGS": "--user-data-dir=/tmp/profile",
            "SPARKCLAW_QQMAIL_SESSION": "stable-session",
        }
        for name, value in forbidden.items():
            with self.subTest(name=name):
                result = self.run_script(browser_environment={name: value})
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(
                    self.error_payload(result)["code"],
                    "forbidden_browser_environment",
                )
                self.assertEqual(self.calls(), [])

    def test_accepts_the_runner_owned_session_environment(self) -> None:
        result = self.run_script(
            browser_environment={"AGENT_BROWSER_SESSION": "sc-email-runtime"},
        )

        self.assertEqual(result.returncode, 0, result.stderr)
        self.assert_owned_tab_lifecycle()

    def test_input_schema_is_strict(self) -> None:
        request = self.request()
        request["extra"] = True
        result = self.run_script(payload=request)

        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.error_payload(result)["code"], "invalid_input")
        self.assertEqual(self.calls(), [])

        result = self.run_script(raw_input="{not-json")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.error_payload(result)["code"], "invalid_json")
        self.assertEqual(self.calls(), [])

        for field, value in {
            "schema_version": 2,
            "operation": "send",
            "provider": "other_mail",
            "account": "secondary",
            "invocation_id": "contains space",
        }.items():
            with self.subTest(field=field):
                request = self.request()
                request[field] = value
                result = self.run_script(payload=request)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(self.error_payload(result)["code"], "invalid_input")
                self.assertEqual(self.calls(), [])

    def test_separate_invocations_use_unique_sessions_and_labels(self) -> None:
        first = self.run_script()
        second = self.run_script()
        self.assertEqual(first.returncode, 0, first.stderr)
        self.assertEqual(second.returncode, 0, second.stderr)

        calls = self.calls()
        creations = [
            call for call in calls if call["commands"] and call["commands"][0][:2] == ["tab", "new"]
        ]
        self.assertEqual(len(creations), 2)
        self.assertNotEqual(creations[0]["session"], creations[1]["session"])
        self.assertNotEqual(creations[0]["commands"][0][3], creations[1]["commands"][0][3])

    def test_probe_never_lists_tabs_or_reads_mail_content(self) -> None:
        result = self.run_script()
        self.assertEqual(result.returncode, 0, result.stderr)

        forbidden_fragments = (
            "mail-list",
            "mail-subject",
            "mail-sender",
            "mail-digest",
            "mail-reader",
            "reader-body",
            "mail-detail",
            "attach",
        )
        for call in self.calls():
            for command in call["commands"]:
                self.assertNotEqual(command[:2], ["tab", "list"])
                selector = command[2] if len(command) > 2 else ""
                for fragment in forbidden_fragments:
                    self.assertNotIn(fragment, selector.lower())

        visibility_selectors = {
            command[2]
            for call in self.calls()
            for command in call["commands"]
            if command[:2] == ["is", "visible"]
        }
        self.assertEqual(
            visibility_selectors,
            {
                '.frame-sidebar .frame-sidebar-compose-btn[data-a11y="button"]',
                ".frame-header .xmail-cmp-profile-btn",
                ".frame-header .xmail-cmp-profile-btn .profile-user-info .user-email",
            },
        )


if __name__ == "__main__":
    unittest.main()
