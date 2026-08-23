#!/usr/bin/env python3

import os
from pathlib import Path
import subprocess
import unittest


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = ROOT / "scripts" / "restart_jingsi_lan_compose.sh"

# The one JingSi LAN route list. Every gateway-side JingSi route lives under
# this prefix, so the wide 18790 proxy blocks the prefix instead of tracking
# individual paths.
JINGSI_PREFIX = "/api/jingsi/"
JINGSI_ROUTES = (
    "/api/jingsi/v0/readyz",
    "/api/jingsi/v0/messages/stream",
    "/api/jingsi/v0/client-events/head",
    "/api/jingsi/v0/client-events",
    "/api/jingsi/v0/client-events/stream",
)


class JingSiLANDeploymentTest(unittest.TestCase):
    def run_check(self, bind, session_id="session-visible", port=None):
        env = os.environ.copy()
        env["SPARKCLAW_JINGSI_LAN_BIND"] = bind
        env["SPARKCLAW_JINGSI_SESSION_ID"] = session_id
        env.pop("SPARKCLAW_JINGSI_LAN_PORT", None)
        if port is not None:
            env["SPARKCLAW_JINGSI_LAN_PORT"] = port
        return subprocess.run(
            ["bash", str(SCRIPT), "--check"],
            cwd=ROOT,
            env=env,
            check=False,
            capture_output=True,
            text=True,
        )

    def test_accepts_rfc1918_bindings(self):
        for address in ("10.0.0.8", "172.16.0.8", "172.31.255.8", "192.168.1.8"):
            with self.subTest(address=address):
                result = self.run_check(address)
                self.assertEqual(result.returncode, 0, result.stderr)

    def test_rejects_public_and_wildcard_bindings(self):
        for address in (
            "0.0.0.0",
            "8.8.8.8",
            "172.32.0.8",
            "192.168.001.8",
            "example.test",
        ):
            with self.subTest(address=address):
                result = self.run_check(address)
                self.assertNotEqual(result.returncode, 0)

    def test_requires_server_bound_session(self):
        result = self.run_check("192.168.1.8", "")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("SPARKCLAW_JINGSI_SESSION_ID", result.stderr)

    def test_port_defaults_and_accepts_override(self):
        result = self.run_check("192.168.1.8")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("port 18793", result.stdout)
        result = self.run_check("192.168.1.8", port="28793")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("port 28793", result.stdout)

    def test_rejects_malformed_port(self):
        for port in ("0", "65536", "eighteen", "-1"):
            with self.subTest(port=port):
                result = self.run_check("192.168.1.8", port=port)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("SPARKCLAW_JINGSI_LAN_PORT", result.stderr)

    def test_nginx_port_has_only_exact_presentation_routes(self):
        nginx = (ROOT / "docker" / "images" / "webchat.nginx.conf.template").read_text(encoding="utf-8")
        listen = "listen ${SPARKCLAW_JINGSI_LAN_PORT};"
        base, presentation = nginx.split(listen, 1)
        self.assertIn(f"location {JINGSI_PREFIX} {{\n    return 404;", base)
        for route in JINGSI_ROUTES:
            self.assertTrue(route.startswith(JINGSI_PREFIX), route)
            self.assertIn(f"location = {route} {{", presentation)
        self.assertNotIn("location /api/ {", presentation)
        self.assertIn("location / {\n    return 404;", presentation)
        self.assertIn("access_log off;", presentation)
        self.assertIn("client_max_body_size 1025k;", presentation)

    def test_base_compose_does_not_publish_port(self):
        base = (ROOT / "docker" / "compose.yaml").read_text(encoding="utf-8")
        overlay = (ROOT / "docker" / "compose.jingsi-lan.yaml").read_text(encoding="utf-8")
        self.assertNotIn(":18793", base)
        self.assertNotIn("SPARKCLAW_JINGSI_LAN_PORT", base)
        self.assertIn("SPARKCLAW_JINGSI_LAN_BIND", overlay)
        self.assertIn(
            ":${SPARKCLAW_JINGSI_LAN_PORT:-18793}:${SPARKCLAW_JINGSI_LAN_PORT:-18793}",
            overlay,
        )


if __name__ == "__main__":
    unittest.main()
