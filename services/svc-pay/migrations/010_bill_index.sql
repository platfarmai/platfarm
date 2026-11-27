-- 商户查账（开放 API /v1/bills/*）按「商户 + 成功时间」取交易流水。
CREATE INDEX IF NOT EXISTS pay_order_mch_success_idx ON pay_order (mch_no, success_at) WHERE state = 'success';
CREATE INDEX IF NOT EXISTS pay_refund_mch_success_idx ON pay_refund (mch_no, success_at) WHERE state = 'success';
