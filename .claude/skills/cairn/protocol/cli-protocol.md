# Cairn machine protocol

The only runtime entrypoint is:

`cairn-cli call --request <request-file> --response <response-file>`

Requests and responses are JSON files. `stdout` carries no business data, `stderr` is diagnostic only, and every mutation has a request ID plus an idempotency key. Keep the protocol version and Skill version compatible through `system.handshake`.

The compile workflow operations are `compile.start`, `compile.next`, `compile.submit`, `compile.status`, `compile.preview`, `compile.apply`, and `compile.abort`. `compile.apply` applies only a confirmed compile knowledge plan; generic `plan.apply` remains for forget and rollback plans.
