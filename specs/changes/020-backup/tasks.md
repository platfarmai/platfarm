# 020 tasks

- [x] `tools/pctl/backup.go`: backup (bundled pg_dumpall + plugin-pg + .keys/.env.plugins, 0600) / restore (--force gate)
- [x] `uninstall --purge`: plugin DB snapshot before drop
- [x] `.gitignore`: backups/
- [x] verified: external-DB mode prints owner instruction and still archives keys/credentials
