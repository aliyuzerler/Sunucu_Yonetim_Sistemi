# Secure Development Checklist

- [ ] Threat model updated for the module.
- [ ] Authorization checked on server side for every read/write action.
- [ ] Scope checked against node/group/resource ID; never trust UI filtering.
- [ ] Input schemas and size limits applied.
- [ ] No arbitrary command execution or unsafe string-built command.
- [ ] Secret values not returned after create/update; redaction tested.
- [ ] Audit event emitted for sensitive changes.
- [ ] Idempotency, timeout, retry and cancellation behavior documented.
- [ ] Resource limits and concurrency limits applied.
- [ ] CSRF/CORS/session controls reviewed for browser endpoints.
- [ ] SSRF/path traversal/symlink/ZIP-slip risks reviewed where applicable.
- [ ] Unit, integration, contract and negative security tests added.
- [ ] Dependency licenses and vulnerabilities scanned.
- [ ] Rollback/restore behavior documented.
