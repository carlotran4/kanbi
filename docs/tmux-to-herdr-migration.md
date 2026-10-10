# Legacy tmux session migration

Herdr is Kanbi's only supported runtime. Change explicit `multiplexer.default: tmux` or `KANBI_MULTIPLEXER=tmux` to `herdr`; omitted runtime settings already default to Herdr. Removed tmux configuration keys do not affect new launches.

Existing ticket/session history and exported tmux fields remain readable. Kanbi never rewrites old container IDs as Herdr IDs, probes or controls tmux, or assumes an old live process has exited because support was removed.

- Active legacy attempts produce repair guidance. Close the old terminal externally before choosing a new attempt. Reconciliation reports retired-runtime observations as degraded rather than silently closing the old row.
- Inactive legacy attempts with a verified harness resume ref can resume into a new Herdr attempt. The new row owns actual Herdr metadata; the old row remains history.
- With no usable ref, choose start fresh explicitly in the repair flow. This deactivates the previous attempt and creates a new row without deleting history.

Back up the database before upgrading. Schema compatibility fields remain intentionally; removing them would change durable/export contracts and is separate from removing the runtime dependency.
