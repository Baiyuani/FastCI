# FastCI owns the Target repo's contents

Every Sync makes the Target repo match the Workspace exactly: files deleted in
the Workspace are deleted in the Target repo, and Managed files are rewritten
from the current tool version. Hand-edits made in the Target repo are not
merged; they are detected as divergence and abort the Sync unless forced.

Considered and rejected: treating the Target repo as a second writable copy
that FastCI merges into. There is no sensible merge semantics between a
Workspace and an injected workflow, and Sync would stop being idempotent.
