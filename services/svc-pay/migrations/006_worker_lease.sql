-- svc-pay：后台扫描租约。多副本时同一类扫描只由持有租约的实例执行；持有者每轮续租，宕机后租约过期由别的实例接手。
-- 渠道请求都按单号幂等、状态推进都是 CAS，并发执行不会出错；租约只是避免重复打渠道。
CREATE TABLE pay_worker_lease (
    name   TEXT PRIMARY KEY,
    holder TEXT NOT NULL,
    until  TIMESTAMPTZ NOT NULL
);
