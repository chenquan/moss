# Action ledger

Use `action.create.plan` or `action.update.plan` through stdin/stdout, explain the proposed fields, and call `action.apply` only after confirmation. Use `action.query` for today, overdue, and waiting items without inventing reminders.

To record what happened after an action, call `action.result.plan` with the action ID, bounded result status and summary, then show the diff and call `action.result.apply` only after explicit confirmation. A result is independent of the action lifecycle: Moss creates `action → produces → action_result`, but does not mark the action done or update knowledge. Later feedback uses an explicit compile `resulted_in` relation.
