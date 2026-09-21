# Architecture decision records

hgit-native inherits its format and behaviour from the original hgit, so its
decision records live there and are **not copied here**; a copy would drift.
They are available in this checkout through the `contract/` submodule:

| ADR | Topic |
|---|---|
| [0001](../../contract/docs/adr/0001-repository-model.md) | Repository model |
| [0002](../../contract/docs/adr/0002-canonical-encoding.md) | Canonical encoding |
| [0003](../../contract/docs/adr/0003-path-length-ceiling.md) | Path-length ceiling and the combined metadata file |
| [0004](../../contract/docs/adr/0004-stable-entity-identity.md) | Stable entity identity |
| [0005](../../contract/docs/adr/0005-typed-relation-vocabulary.md) | Typed relation vocabulary |
| [0006](../../contract/docs/adr/0006-entity-scoped-relations.md) | Entity-scoped relations |
| [0007](../../contract/docs/adr/0007-dynamic-archive-buffer.md) | Dynamic archive buffer |
| [0008](../../contract/docs/adr/0008-fossil-delta-format-prototype.md) | Fossil delta format |
| [0009](../../contract/docs/adr/0009-rename-detection.md) | Rename detection |
| [0010](../../contract/docs/adr/0010-subdirectory-support.md) | Subdirectory support |
| [0011](../../contract/docs/adr/0011-merge.md) | Merge |
| [0012](../../contract/docs/adr/0012-named-paths.md) | Named paths |
| [0013](../../contract/docs/adr/0013-repository-integrity-check.md) | Repository integrity check |
| [0014](../../contract/docs/adr/0014-ignore-rules.md) | Ignore rules |
| [0015](../../contract/docs/adr/0015-file-attributes-and-modes.md) | File attributes and modes |
| [0016](../../contract/docs/adr/0016-conflict-as-repository-data.md) | Conflicts as repository data |
| [0017](../../contract/docs/adr/0017-rename-aware-merge.md) | Rename-aware merge |

Decisions that are specific to the native port (for example the timestamp
epoch) are recorded in this folder, numbered `N-0001`, `N-0002`, ..., so they
never collide with the inherited numbering above.

| ADR | Topic |
|---|---|
| [N-0001](N-0001-timestamp-epoch.md) | Timestamp epoch |
