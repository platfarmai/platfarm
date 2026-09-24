// 孤儿元数据清理（specs/021）：upload-url 建行后浏览器可能从未 PUT——
// 每小时扫一批"建行超 1 小时且对象不存在"的元数据行并删除。对象无行的反向清理不做
// （storage_key 全部经本服务生成，正向清理已闭环；bucket 全量遍历成本高收益低）。
package main

import (
	"context"
	"log"
	"time"

	"github.com/minio/minio-go/v7"
)

const (
	sweepInterval = 1 * time.Hour
	sweepMinAge   = 1 * time.Hour
	sweepBatch    = 200
)

func orphanSweeper(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(sweepInterval):
		}
		if n, err := sweepOrphans(ctx); err != nil {
			log.Printf("sweeper: %v", err)
		} else if n > 0 {
			log.Printf("sweeper: removed %d orphan rows", n)
		}
	}
}

func sweepOrphans(ctx context.Context) (int, error) {
	rows, err := db.Query(ctx,
		`SELECT id, storage_key FROM files WHERE created_at < now()-$1::interval ORDER BY id LIMIT $2`,
		sweepMinAge.String(), sweepBatch)
	if err != nil {
		return 0, err
	}
	type cand struct {
		id  int64
		key string
	}
	var cands []cand
	for rows.Next() {
		var c cand
		if rows.Scan(&c.id, &c.key) == nil {
			cands = append(cands, c)
		}
	}
	rows.Close()

	removed := 0
	for _, c := range cands {
		if _, err := s3.StatObject(ctx, bucket, c.key, minio.StatObjectOptions{}); err == nil {
			continue // 对象存在，正常文件
		} else if resp := minio.ToErrorResponse(err); resp.Code != "NoSuchKey" && resp.StatusCode != 404 {
			continue // 存储不可用等非缺失错误：跳过，下轮再看
		}
		if _, err := db.Exec(ctx, `DELETE FROM files WHERE id=$1`, c.id); err == nil {
			removed++
		}
	}
	return removed, nil
}
