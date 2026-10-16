# Target repos start with fresh history

A Target repo is created with a single generated commit; FastCI never pushes
the Workspace's existing commits. The Target repo is a disposable artifact of
the Workspace, so each Sync adds one commit and the Workspace's history,
including any secrets since removed, never leaves the machine.

Considered and rejected: mirroring the Workspace's history. It would fight the
injected Managed files, force a rewrite (and therefore a force-push) on every
Sync, and leave no clear answer for what "update" means.
