# Backup and Restore Design

- Back up Control Plane database, configuration, encryption key references, certificates metadata, module state and user-managed file data according to a defined policy.
- Node backups are separate from Control Plane backups.
- Restic is the selected candidate engine; verify repo backend, lock behavior and version before integration.
- Never store encryption master key beside backup repository or encrypted database dump.
- A backup job is not complete until repository integrity/verification is recorded.
- Restore must support preview, target selection, collision handling, pre-restore snapshot and audit event.
- Test restore to an isolated environment on a scheduled basis; a successful backup command is not proof of recoverability.
- For DB consistency use database-native dump/snapshot procedures or coordinated quiesce; do not blindly copy live DB files.
