import assert from "node:assert/strict";
import path from "node:path";

// Node qualification reopens the profile selected by actual native enrollment.
// It must never reinterpret a local-test profile as production authorization.
export function qualificationLaunchEnvironment(directory, env = process.env) {
  const nodeManaged = env.SPARKCLAW_CONTROLLER_NODE_MANAGED === "1";
  assert.ok(env.SPARKCLAW_CONTROLLER_NODE_MANAGED === undefined || nodeManaged);
  const selected = { ...env, SPARKCLAW_DESKTOP_USER_DATA_DIR: path.join(directory, "controller-native-profile") };
  if (nodeManaged) {
    assert.equal(env.SPARKCLAW_DESKTOP_ISCP_CONFIG, undefined);
    assert.equal(env.SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST, undefined);
    return selected;
  }
  return { ...selected, SPARKCLAW_DESKTOP_ISCP_LOCAL_TEST: "1", SPARKCLAW_DESKTOP_ISCP_CONFIG: path.join(directory, "desktop-profile.json") };
}

export function verifyQualificationAuthorization(connection, env = process.env) {
  assert.equal(connection.backend.transport, "iscp");
  if (env.SPARKCLAW_CONTROLLER_NODE_MANAGED === "1") {
    assert.equal(connection.backend.mode, "node-managed");
    assert.equal(connection.backend.relay_profile, "production");
    assert.equal(connection.backend.test_mode, false);
  } else {
    assert.equal(connection.backend.relay_profile, "local-lab");
    assert.equal(connection.backend.test_mode, true);
  }
}
