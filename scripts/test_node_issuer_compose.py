#!/usr/bin/env python3
"""Exercise persistent, confined node issuer mounts through the normal entrypoints."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
FILES = ("authority/responder/device.identity.json", "authority/responder/device.identity.key",
         "authority/issuer-admin.token", "public-pins/responder.json", "public-pins/grant-signer.json",
         "public-pins/invitation-signer.json", "registry/peers.json", "responder-credentials/enrollment.json")


class NodeIssuerComposeTest(unittest.TestCase):
    def prepare(self, root):
        root.mkdir(mode=0o700)
        for name in FILES:
            target = root / name
            target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            target.write_text("{}")
            target.chmod(0o600)
        (root / "authority").chmod(0o700)
        (root / "issuer-socket").mkdir(mode=0o700)

    def configure(self, root):
        environment = dict(os.environ, SPARKCLAW_WORKBENCH_ISCP_NODE_ROOT=str(root))
        return subprocess.run(["bash", "-c", 'source scripts/lib/deployment-profile.sh; '
                               'compose_args=(); sparkclaw_configure_node_issuer "$PWD" || exit; '
                               'printf "%s\\n" "${compose_args[@]}"'],
                              cwd=ROOT, env=environment, capture_output=True, text=True)

    def test_disabled_node_does_not_add_overlay(self):
        result = self.configure("")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), "")

    def test_provisioned_node_is_reapplied_by_normal_start(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve() / "node"
            self.prepare(root)
            result = self.configure(root)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout.splitlines(), ["-f", str(ROOT / "docker/compose.node-issuer.yaml")])
            for name in ("start_remote_compose.sh", "start_local_compose.sh"):
                self.assertIn('sparkclaw_configure_node_issuer "$ROOT"', (ROOT / "scripts" / name).read_text())

    def test_missing_or_nonprivate_or_symlinked_node_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve() / "node"
            self.assertNotEqual(self.configure(root).returncode, 0)
            self.prepare(root)
            key = root / "authority/responder/device.identity.key"
            key.chmod(0o644)
            self.assertNotEqual(self.configure(root).returncode, 0)
            key.chmod(0o600)
            alias = root.parent / "alias"
            alias.symlink_to(root, target_is_directory=True)
            self.assertNotEqual(self.configure(alias).returncode, 0)
            key.unlink()
            key.symlink_to(root / "authority/issuer-admin.token")
            self.assertNotEqual(self.configure(root).returncode, 0)

    def test_expanded_compose_has_only_confined_node_mounts(self):
        environment = dict(os.environ, SPARKCLAW_WORKBENCH_ISCP_NODE_ROOT="/private/owned-node",
                           SPARKCLAW_WEBCHAT_PROXY_TOKEN="node_compose_test_token_0123456789abcdef")
        command = ["docker", "compose", "--env-file", "docker/env/sparkclaw.product.env",
                   "--env-file", "docker/env/sparkclaw.remote.env", "-f", "docker/compose.yaml",
                   "-f", "docker/compose.node-issuer.yaml", "--profile", "product", "config", "--format", "json"]
        result = subprocess.run(command, cwd=ROOT, env=environment, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        gateway = json.loads(result.stdout)["services"]["gateway"]
        volumes = [v for v in gateway["volumes"] if v["target"].startswith("/run/sparkclaw/public-node/")]
        self.assertEqual(len(volumes), 6)
        self.assertEqual({v["source"].removeprefix("/private/owned-node/") for v in volumes},
                         {"authority/responder", "public-pins", "authority/issuer-admin.token",
                          "issuer-socket", "registry", "responder-credentials"})
        for volume in volumes:
            self.assertEqual(volume.get("read_only", False), not volume["source"].endswith("/responder-credentials"))
        self.assertEqual(gateway["environment"]["SPARKCLAW_WORKBENCH_ISCP_NODE_MANAGED"], "1")


if __name__ == "__main__":
    unittest.main()
