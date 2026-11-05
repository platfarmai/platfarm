// specs/015 迁移规范参考实现：migrations/NNN_name.sql，启动时按序自应用（append-only）。
// 每个文件一个事务；版本记入 schema_migrations。改过已应用文件 = 违规，用新文件向前修。
package main

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func runMigrations(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INT PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return err
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		version, verr := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if verr != nil {
			return fmt.Errorf("migration %s: filename must start with a numeric version", name)
		}
		var applied bool
		if err := db.QueryRow(
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		sqlBytes, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(sqlBytes)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO schema_migrations (version, name) VALUES ($1,$2)`, version, name); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		logrus.Infof("migration applied: %s", name)
	}
	return nil
}
