# SparkClaw Workbench

## Product

SparkClaw is a local agent workbench for starting, monitoring, and reviewing bounded agent tasks. It connects to a SparkClaw gateway and runtime, then keeps task history, runtime state, approvals, memory, scheduled work, connections, artifacts, and traces in one operator surface.

## Primary user and job

The primary user is an operator working with local or connected agents. They need to understand whether the runtime is ready, begin a task quickly, and inspect ongoing work without losing context.

## Frontend direction

- Use a compact, light workbench shell with a persistent desktop sidebar and a drawer on narrower screens.
- Keep the home state calm and task-focused: gateway status, a clear starting prompt, and concise task suggestions.
- Preserve SparkClaw naming and the existing information architecture while aligning the visual hierarchy and proportions with the supplied standalone HTML reference.
- Make offline and authentication states explicit. Controls that require an active session must not imply that they will run while disconnected.

## Constraints

- Preserve existing routes, API behavior, and truthful runtime state.
- Keep token pairing available when gateway authentication is required.
- Maintain keyboard, responsive, and accessible interaction behavior.
- Treat the current reference direction as light-mode-first; do not invent a dark theme without a separate product decision.
