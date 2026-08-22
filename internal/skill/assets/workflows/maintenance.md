# Moss maintenance workflow

The Skill is the only user interface. Keep installation, upgrade, backup, and restore inside Claude Code and invoke the local runtime through the stdio-only `moss call` entrypoint.

## Bootstrap

Use a trusted packaged `moss` binary or a checked-out Moss source tree. The user may install the matching Skill resources with `moss skill install` (default: global Claude; use `--target codex` or `--scope project` when explicitly requested). To install both editor integrations, repeat the target flag, for example `moss skill install --target codex --target claude`; do not use a combined `both` value. Install the matching binary and Skill as a pair through the trusted environment, then call `system.handshake` and `system.health` through stdin/stdout. Report compatibility or repair errors without claiming readiness.

## Upgrade

Call `system.handshake`, then `system.export` through stdin/stdout before replacing the binary or Skill. Install the trusted matching resources as a pair, run handshake and health as the migration preflight, and stop normal writes if either fails. Restore both previous runtime resources on a failed preflight; never attempt a transport-flag fallback. Ask for confirmation before calling `system.restore` on the recorded pre-upgrade backup.

Never ask the user to run the runtime `call` protocol, expose shell output, or bypass the backup and confirmation boundary. The explicit Skill installer is the only supported setup command.
