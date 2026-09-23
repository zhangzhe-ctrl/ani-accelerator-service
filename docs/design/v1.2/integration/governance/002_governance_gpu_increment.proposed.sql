-- 对用户上传Governance的增量提案。该库operation_id为varchar、tenant_id为bigint，不擅自改成uuid。
-- 用Ent schema + 固定工具 + schema导出器 + Atlas生成正式迁移。未在PostgreSQL执行。
CREATE TABLE sys_gpu_usage_sync (
 tenant_id bigint NOT NULL CHECK(tenant_id>0),
 create_operation_id varchar NOT NULL,
 desired_revision smallint NOT NULL CHECK(desired_revision IN (1,2)),
 acked_revision smallint NOT NULL DEFAULT 0 CHECK(acked_revision BETWEEN 0 AND 2),
 desired_payload_json jsonb NOT NULL,
 desired_payload_digest char(64) NOT NULL,
 retry_blocked boolean NOT NULL DEFAULT false,
 attempt_count integer NOT NULL DEFAULT 0 CHECK(attempt_count>=0),
 lease_generation bigint NOT NULL DEFAULT 0 CHECK(lease_generation>=0),
 lease_owner text NULL,
 lease_until timestamptz NULL,
 next_attempt_at timestamptz NULL,
 last_error_code text NULL,
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(tenant_id,create_operation_id),
 FOREIGN KEY(tenant_id,create_operation_id) REFERENCES sys_quota_operations(tenant_id,operation_id) ON DELETE RESTRICT,
 CHECK(acked_revision<=desired_revision)
);
CREATE INDEX sys_gpu_usage_sync_pending_idx ON sys_gpu_usage_sync(next_attempt_at)
 WHERE acked_revision<desired_revision AND retry_blocked=false;
-- 此表只有GPU关联同步进度；不得以它是否存在决定业务创建/删除/退额。
-- desired_payload必须由原始canonical+完整charge向量确定性生成；不能把扫描now加入内容。
-- 原业务dispatch_state/attempt_count只跟踪给Inference的命令投递，不被本worker更新。

CREATE TABLE sys_gpu_delete_acceptances (
 delete_request_id varchar PRIMARY KEY,
 tenant_id bigint NOT NULL CHECK(tenant_id>0),
 actor_type varchar NOT NULL,
 actor_id varchar NOT NULL,
 action varchar NOT NULL,
 idempotency_key varchar NOT NULL,
 request_hash char(64) NOT NULL,
 resource_id varchar NOT NULL,
 create_operation_id varchar NOT NULL,
 result varchar NOT NULL CHECK(result IN ('CANCELED_BEFORE_DISPATCH','QUEUED_FOR_OWNER')),
 dispatch_operation_id varchar NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(tenant_id,actor_type,actor_id,action,idempotency_key),
 FOREIGN KEY(tenant_id,create_operation_id) REFERENCES sys_quota_operations(tenant_id,operation_id) ON DELETE RESTRICT,
 FOREIGN KEY(tenant_id,dispatch_operation_id) REFERENCES sys_quota_operations(tenant_id,operation_id) ON DELETE RESTRICT,
 CHECK((result='CANCELED_BEFORE_DISPATCH' AND dispatch_operation_id IS NULL)
 OR(result='QUEUED_FOR_OWNER' AND dispatch_operation_id IS NOT NULL))
);
-- 这只是公开DELETE的不可变幂等受理结果，没有worker、没有额外执行状态和第二份删除投递任务。
-- 在tenant->原CREATE->已有幂等记录->必要account/charge锁序内，一次事务决定本地撤销或建既有DELETE operation。
-- 本地撤销沿用CancelUnsent算法；排队分支沿用CreateDeleteOperation的同事务helper。
-- 并发投递领取也锁原CREATE，因此不能出现已退额后首次发CREATE。
