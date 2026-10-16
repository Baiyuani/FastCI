# The image sha tag names the Workspace's commit

The `sha-<short>` image tag is derived from the Workspace's HEAD commit, not
from the Sync commit FastCI creates in the Target repo. The Workspace's commits
never reach the Target repo (ADR-0001), so the workflow cannot get this tag from
`github.sha`; FastCI renders the tag into the workflow at Sync time, from the
same Workspace commit it writes into the Marker file.

Consequences: the build depends on the Marker file being present and current,
and tagging the Sync commit instead would make every tag unlookupable from the
Workspace. A Sync from a dirty Workspace publishes an image whose tag names only
the base commit.
