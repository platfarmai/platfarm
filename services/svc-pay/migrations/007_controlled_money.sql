-- svc-pay M4 资金模型修正（审计）。
-- 结算单：是否冻结供分账（直付通 royalty_freeze）、结算前已退款金额、记入可提现余额的金额
-- （收付通 = 本次释放的金额；直付通资金直接到二级商户结算账户，记 0）。
ALTER TABLE pay_settle ADD COLUMN hold_for_division BOOL NOT NULL DEFAULT false;
ALTER TABLE pay_settle ADD COLUMN refunded_amount BIGINT NOT NULL DEFAULT 0;
ALTER TABLE pay_settle ADD COLUMN credit_amount BIGINT NOT NULL DEFAULT 0;
UPDATE pay_settle SET credit_amount = amount;

-- 提现改为“发起即记借方（预占）、失败冲回”：此前只在成功时记借方，给在途的提现补记。
INSERT INTO pay_ledger (mch_no, isv_no, if_code, kind, ref_type, ref_id, amount, memo, tenant_id)
SELECT mch_no, isv_no, if_code, 'withdrawal', 'withdrawal', withdraw_no, -amount, '提现预占（迁移补记）', tenant_id
FROM pay_withdrawal WHERE state IN ('pending', 'accepted')
ON CONFLICT (kind, ref_type, ref_id, ref_sub) DO NOTHING;
