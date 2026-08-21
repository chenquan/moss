# Cairn maintenance workflow

The Skill is the only user interface. Keep installation, upgrade, backup, and restore inside Claude Code and invoke the local runtime only through the file-based `call` protocol.

## Bootstrap

Use a trusted packaged `cairn-cli` binary or a checked-out Cairn source tree. Install the matching binary and this Skill resource into the private application locations, then call `system.handshake` and `system.health`. Report compatibility or repair errors without claiming readiness.

## Upgrade

Call `system.handshake`, then `system.export` before replacing the binary or Skill. Install the trusted matching resources, run handshake and health as the migration preflight, and stop normal writes if either fails. Ask for confirmation before calling `system.restore` on the recorded pre-upgrade backup.

Never ask the user to run Cairn commands, expose shell output, or bypass the backup and confirmation boundary.
