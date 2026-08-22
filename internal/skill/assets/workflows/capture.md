# Capture

Resolve the selected local file, send a bounded `source.ingest` envelope to the stdio-only `moss call` entrypoint, and report the structured stdout result. Never interpolate source content into a shell command or materialize a protocol envelope.

For a later privacy correction, call `source.mark_sensitive`; require confirmation before lowering sensitivity and report any stricter classifications propagated to derived records.
