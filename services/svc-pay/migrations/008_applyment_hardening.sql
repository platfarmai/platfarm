-- svc-pay 进件加固（审计）：
-- 图片（身份证正反面、营业执照等）落库加密；此前的行是明文，content_sealed=false 照旧读取。
-- 记录最后编辑资料的人：录入或改过资料的人不能审核通过（双人复核）。
ALTER TABLE pay_applyment_media ADD COLUMN content_sealed BOOL NOT NULL DEFAULT false;
ALTER TABLE pay_applyment ADD COLUMN edited_by TEXT NOT NULL DEFAULT '';
UPDATE pay_applyment SET edited_by = created_by;
