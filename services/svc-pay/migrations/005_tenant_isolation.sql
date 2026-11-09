-- svc-pay：租户隔离补齐。对账批次按服务商出账单，归属服务商所在租户（平台服务商为租户 0）。
-- 渠道参数行的 tenant_id 此前没写，按归属（服务商 / 商户）回填。

ALTER TABLE pay_recon_batch ADD COLUMN tenant_id INT NOT NULL DEFAULT 0;
UPDATE pay_recon_batch b SET tenant_id = i.tenant_id FROM pay_isv i WHERE i.isv_no = b.isv_no;

UPDATE pay_channel_config c SET tenant_id = i.tenant_id FROM pay_isv i
	WHERE c.owner_type = 'isv' AND i.isv_no = c.owner_id;
UPDATE pay_channel_config c SET tenant_id = m.tenant_id FROM pay_merchant m
	WHERE c.owner_type = 'merchant' AND m.mch_no = c.owner_id;
