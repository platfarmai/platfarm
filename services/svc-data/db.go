// svc-data 存储层：pf_od_data 库。
// dataset（数据集配置，后台可编辑）+ record（原文 JSONB）+ record_index（按规则抽取的筛选索引）。
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var db *pgxpool.Pool

const ddl = `
CREATE TABLE IF NOT EXISTS dataset (
	id             text NOT NULL PRIMARY KEY,
	name           text NOT NULL DEFAULT '',
	source         text NOT NULL DEFAULT '',
	webhook_secret text NOT NULL,
	pk_field       text NOT NULL DEFAULT 'id',
	index_rules    jsonb NOT NULL DEFAULT '[]',
	enabled        boolean NOT NULL DEFAULT true,
	created_at     timestamptz NOT NULL DEFAULT now(),
	updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS record (
	dataset    text NOT NULL,
	pk         text NOT NULL,
	payload    jsonb NOT NULL,
	updated_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (dataset, pk)
);

CREATE TABLE IF NOT EXISTS record_index (
	dataset text NOT NULL,
	pk      text NOT NULL,
	field   text NOT NULL,
	k       text NOT NULL DEFAULT '',
	num     numeric NULL,
	txt     text NULL,
	PRIMARY KEY (dataset, pk, field, k)
);
CREATE INDEX IF NOT EXISTS idx_ri_num ON record_index (dataset, field, k, num);
CREATE INDEX IF NOT EXISTS idx_ri_txt ON record_index (dataset, field, txt);

-- 投递去重（X-MMOM-Delivery）：重复投递返回首次结果，不重复写入
CREATE TABLE IF NOT EXISTS delivery (
	dataset    text NOT NULL,
	delivery   text NOT NULL,
	result     jsonb NOT NULL,
	created_at timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (dataset, delivery)
);

-- 管理操作审计
CREATE TABLE IF NOT EXISTS dataset_audit (
	id         bigserial PRIMARY KEY,
	dataset    text NOT NULL,
	actor      text NOT NULL,
	action     text NOT NULL,
	detail     jsonb NOT NULL DEFAULT '{}',
	created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_audit_ds ON dataset_audit (dataset, created_at DESC);

-- 留存策略 + 出站变更通知（P1）
ALTER TABLE dataset ADD COLUMN IF NOT EXISTS retention_days int NOT NULL DEFAULT 0;
ALTER TABLE dataset ADD COLUMN IF NOT EXISTS notify_url text NOT NULL DEFAULT '';
ALTER TABLE dataset ADD COLUMN IF NOT EXISTS notify_secret text NOT NULL DEFAULT '';
`

func mustInitDB() {
	url := os.Getenv("DATA_DATABASE_URL")
	if url == "" {
		log.Fatal("DATA_DATABASE_URL required")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		log.Fatalf("db connect: %v", err)
	}
	if _, err := pool.Exec(context.Background(), ddl); err != nil {
		log.Fatalf("db migrate: %v", err)
	}
	db = pool
}

// Dataset 数据集配置——结构不进代码，全部可在后台编辑。
// Secret 为 webhook 明文密钥：库内以 AES-GCM 密文存储（见 secret.go），
// 仅创建/轮换时对外回显，管理接口与列表一律不返回。
type Dataset struct {
	ID            string      `json:"id"`
	Name          string      `json:"name"`
	Source        string      `json:"source"`
	Secret        string      `json:"-"`
	PKField       string      `json:"pkField"`
	Rules         []IndexRule `json:"indexRules"`
	Enabled       bool        `json:"enabled"`
	RetentionDays int         `json:"retentionDays"`
	NotifyURL     string      `json:"notifyUrl"`
	NotifySecret  string      `json:"-"`
	UpdatedAt     string      `json:"updatedAt"`
}

const datasetCols = `id, name, source, webhook_secret, pk_field, index_rules, enabled,
	retention_days, notify_url, notify_secret, updated_at::text`

func (d *Dataset) scan(row interface{ Scan(...any) error }, rules *[]byte) error {
	return row.Scan(&d.ID, &d.Name, &d.Source, &d.Secret, &d.PKField, rules,
		&d.Enabled, &d.RetentionDays, &d.NotifyURL, &d.NotifySecret, &d.UpdatedAt)
}

var errDatasetNotFound = errors.New("dataset not found")

func loadDataset(ctx context.Context, id string) (*Dataset, error) {
	var d Dataset
	var rules []byte
	err := d.scan(db.QueryRow(ctx, `SELECT `+datasetCols+` FROM dataset WHERE id=$1`, id), &rules)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errDatasetNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(rules, &d.Rules); err != nil {
		return nil, err
	}
	if d.Secret, err = open(d.Secret); err != nil {
		return nil, err
	}
	if d.NotifySecret, err = open(d.NotifySecret); err != nil {
		return nil, err
	}
	return &d, nil
}

func listDatasets(ctx context.Context) ([]Dataset, error) {
	rows, err := db.Query(ctx, `SELECT `+datasetCols+` FROM dataset ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Dataset{}
	for rows.Next() {
		var d Dataset
		var rules []byte
		if err := d.scan(rows, &rules); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(rules, &d.Rules); err != nil {
			return nil, err
		}
		d.Secret, d.NotifySecret = "", "" // 列表不携带任何密钥材料
		out = append(out, d)
	}
	return out, rows.Err()
}

func newSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("rand: %v", err)
	}
	return "whs_" + hex.EncodeToString(b)
}

// sealSecret / openSecret 落库前加密、读出后解密（DATA_SECRET_KEY，见 secret.go）。
func sealSecret(secret string) string {
	sealed, err := seal(secret)
	if err != nil {
		log.Fatalf("seal secret: %v", err)
	}
	return sealed
}

// upsertRecord 单条一个事务：record 覆盖写入 + 索引行按当前规则重建（幂等）。
func upsertRecord(ctx context.Context, d *Dataset, pk string, payload []byte, entries []indexEntry) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO record (dataset, pk, payload, updated_at) VALUES ($1,$2,$3, now())
		ON CONFLICT (dataset, pk) DO UPDATE SET payload=$3, updated_at=now()`,
		d.ID, pk, payload); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM record_index WHERE dataset=$1 AND pk=$2`, d.ID, pk); err != nil {
		return err
	}
	if err := insertIndexEntries(ctx, tx, d.ID, pk, entries); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func insertIndexEntries(ctx context.Context, tx pgx.Tx, dataset, pk string, entries []indexEntry) error {
	for _, e := range entries {
		if _, err := tx.Exec(ctx, `
			INSERT INTO record_index (dataset, pk, field, k, num, txt) VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (dataset, pk, field, k) DO UPDATE SET num=$5, txt=$6`,
			dataset, pk, e.Field, e.K, e.Num, e.Txt); err != nil {
			return err
		}
	}
	return nil
}

// reindexDataset 规则变更后全量重建索引，返回处理的记录数。
func reindexDataset(ctx context.Context, d *Dataset) (int, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM record_index WHERE dataset=$1`, d.ID); err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx, `SELECT pk, payload FROM record WHERE dataset=$1`, d.ID)
	if err != nil {
		return 0, err
	}
	type rec struct {
		pk      string
		payload []byte
	}
	recs := []rec{}
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.pk, &r.payload); err != nil {
			rows.Close()
			return 0, err
		}
		recs = append(recs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, r := range recs {
		if err := insertIndexEntries(ctx, tx, d.ID, r.pk, extractEntries(d.Rules, r.payload)); err != nil {
			return 0, err
		}
	}
	return len(recs), tx.Commit(ctx)
}

// findDelivery 命中已处理投递时返回其首次结果（去重）。
func findDelivery(ctx context.Context, dataset, delivery string) ([]byte, bool) {
	if delivery == "" {
		return nil, false
	}
	var result []byte
	err := db.QueryRow(ctx, `SELECT result FROM delivery WHERE dataset=$1 AND delivery=$2`,
		dataset, delivery).Scan(&result)
	return result, err == nil
}

func saveDelivery(ctx context.Context, dataset, delivery string, result []byte) {
	if delivery == "" {
		return
	}
	if _, err := db.Exec(ctx, `
		INSERT INTO delivery (dataset, delivery, result) VALUES ($1,$2,$3)
		ON CONFLICT (dataset, delivery) DO NOTHING`, dataset, delivery, result); err != nil {
		log.Printf("save delivery %s/%s: %v", dataset, delivery, err)
	}
}

// writeAudit 记录管理操作；失败只记日志，不阻断主操作。
func writeAudit(ctx context.Context, dataset, actor, action string, detail any) {
	if _, err := db.Exec(ctx, `
		INSERT INTO dataset_audit (dataset, actor, action, detail) VALUES ($1,$2,$3,$4)`,
		dataset, actor, action, mustJSON(detail)); err != nil {
		log.Printf("audit %s %s: %v", dataset, action, err)
	}
}

type auditEntry struct {
	Actor     string          `json:"actor"`
	Action    string          `json:"action"`
	Detail    json.RawMessage `json:"detail"`
	CreatedAt string          `json:"createdAt"`
}

func listAudit(ctx context.Context, dataset string, limit int) ([]auditEntry, error) {
	rows, err := db.Query(ctx, `
		SELECT actor, action, detail, created_at::text FROM dataset_audit
		WHERE dataset=$1 ORDER BY id DESC LIMIT $2`, dataset, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []auditEntry{}
	for rows.Next() {
		var e auditEntry
		if err := rows.Scan(&e.Actor, &e.Action, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// purgeExpired 按 retention_days 清理过期记录与投递去重行，返回删除的记录数。
func purgeExpired(ctx context.Context, d *Dataset) (int, error) {
	if d.RetentionDays <= 0 {
		return 0, nil
	}
	tag, err := db.Exec(ctx, `
		DELETE FROM record WHERE dataset=$1 AND updated_at < now() - make_interval(days => $2)`,
		d.ID, d.RetentionDays)
	if err != nil {
		return 0, err
	}
	if _, err := db.Exec(ctx, `
		DELETE FROM record_index i WHERE i.dataset=$1
		AND NOT EXISTS (SELECT 1 FROM record r WHERE r.dataset=i.dataset AND r.pk=i.pk)`, d.ID); err != nil {
		return 0, err
	}
	if _, err := db.Exec(ctx, `
		DELETE FROM delivery WHERE dataset=$1 AND created_at < now() - make_interval(days => $2)`,
		d.ID, d.RetentionDays); err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
