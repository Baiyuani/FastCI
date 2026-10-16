# FastCI

FastCI mirrors a local Workspace into a GitHub repository, keeps that
repository building the Workspace's Dockerfile, and publishes the resulting
image to a container registry.

## Language

**Workspace**:
A local git repository that the user points FastCI at, and the only source of
truth for its files.
_Avoid_: project, source, local dir

**Target repo**:
The GitHub repository FastCI creates and owns for a Workspace.
_Avoid_: remote, mirror, destination

**Sync**:
Making the Target repo's contents match the Workspace and pushing that as a
single new commit.
_Avoid_: upload, publish, deploy

**Managed file**:
A file inside the Target repo whose content FastCI generates and owns; the
Workspace is not its source of truth.
_Avoid_: generated file, template artifact

**Marker file**:
The Managed file `.fastci.json` that records which Workspace produced a Target
repo, with which settings and tool version.
_Avoid_: manifest, metadata, state file

**State file**:
The local record of every Target repo this machine has created, used by the
listing and status commands.
_Avoid_: cache, index, marker

**Image tag**:
A tag on the published image, such as `latest` or `sha-1a2b3c4`. Every tag in
this project is an image tag; FastCI never creates git tags, and `tags` in
configuration always means image tags.
_Avoid_: tag, version, release
