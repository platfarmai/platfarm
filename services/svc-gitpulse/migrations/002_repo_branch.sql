-- 仓库可指定分析分支：留空 = 使用 GitHub 默认分支
ALTER TABLE repos ADD COLUMN branch TEXT NOT NULL DEFAULT '';
