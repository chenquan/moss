# Capture

Resolve the selected local file, call `source.ingest` with a bounded request, and report the structured source result. Never interpolate source content into a shell command.

For a later privacy correction, call `source.mark_sensitive`; require confirmation before lowering sensitivity and report any stricter classifications propagated to derived records.
