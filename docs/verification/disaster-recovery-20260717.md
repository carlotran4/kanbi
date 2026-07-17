# Disaster-Recovery Drill — 2026-07-17

Status: **PASS for distinct local Linux artifacts; frozen tagged/RC artifact rerun remains required**

## Artifacts

| Role | Version | Commit | Schema | SHA-256 |
| --- | --- | --- | ---: | --- |
| Prior | `0.2.0-test` | `e66ba9e98da43792159d500104ad8dbc4340a6b5` | 3 | `17d0cb10c092f5a98aa24b4d8fc17ad50e8f288256b1482c07205099cfdacb4c` |
| Candidate snapshot | `1.0.0-rc.drill` | `144bd46b779d33c393d64b32b3142c3c1b300b66` | 5 | `41a724e67473cc8029c9ba27b0353c69ba09a6d11434db9f27250d0b7ba531e4` |

Host: Linux x86-64. All state lived under an automatically removed temporary directory.

## Scenario and result

1. The schema-3 artifact initialized a database and created a board, ticket body, and note.
2. The drill required and seeded an attachment with known bytes.
3. It seeded two session attempts for the ticket: one inactive/closed with a ref and one active/running with a distinct ref.
4. The prior artifact created a full backup. Backup SHA-256: `a8c8b19266a94f47ce59e9638bdaab68c019ad07e41a003f47c9a4d0b549fe0b`.
5. The schema-5 artifact opened and migrated the database.
6. Post-migration checks verified ticket/body/note fields, required attachment bytes, both ordered session rows and refs, exactly one active fixture, SQLite `integrity_check`, and foreign-key consistency.
7. The schema-3 artifact attempted to open the migrated database and failed with `database schema version 5 is newer than supported version 3`.
8. The candidate created a post-upgrade ticket and replaced the attachment contents, representing changes expected to be lost on rollback.
9. The prior artifact restored its pre-upgrade backup with `--force`.
10. Post-restore checks verified the original ticket/body/note, absence of the post-backup ticket, restoration of the attachment's original bytes, both session attempts/refs, active flag, SQLite integrity, and foreign-key consistency.

Result: **PASS**.

## Evidence

- Full output: [`upgrade-rollback-drill-20260717T033908Z.txt`](./upgrade-rollback-drill-20260717T033908Z.txt)
- Full-output SHA-256: `2a8399a28ec4e20d6c17ad8cf6e8f0ad5c99dbea9fd41e7fe250a9f5822de8b2`
- Latest pointer: [`upgrade-rollback-drill-latest.txt`](./upgrade-rollback-drill-latest.txt)

## Qualification limitation

The artifacts are distinct, provenance-bearing local snapshots and exercise a real schema 3→5 migration. They are not a published prior tag plus a frozen RC workflow artifact. GH-292 remains `PARTIAL` until this unchanged drill passes those final artifacts and a reviewer records the result.
