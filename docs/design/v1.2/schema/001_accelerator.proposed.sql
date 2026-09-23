-- v1.2 设计提案，不是已执行迁移。目标PostgreSQL，真实工程应从schema/Atlas生成并审查。
-- 所有UUID校验还需API完成；没有跨服务FK，没有库存余额或provider写入事务。
CREATE SCHEMA IF NOT EXISTS accelerator;
CREATE TABLE accelerator.clusters (
 cluster_id uuid PRIMARY KEY,
 display_name varchar(64) NOT NULL,
 connection_ref text NOT NULL,
 version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE accelerator.pools (
 pool_id uuid PRIMARY KEY,
 cluster_id uuid NOT NULL REFERENCES accelerator.clusters ON DELETE RESTRICT,
 display_name varchar(64) NOT NULL,
 UNIQUE(cluster_id,pool_id)
);
CREATE TABLE accelerator.supply_groups (
 group_id uuid PRIMARY KEY,
 cluster_id uuid NOT NULL,
 pool_id uuid NOT NULL,
 display_name varchar(64) NOT NULL,
 mode text NOT NULL CHECK (mode IN ('WHOLE_EXCLUSIVE','SHARED_FIXED')),
 model_key text NOT NULL,
 queue_name text NOT NULL,
 baseline_id uuid NOT NULL,
 baseline_digest char(64) NOT NULL,
 baseline_json jsonb NOT NULL CHECK(jsonb_typeof(baseline_json)='object'),
 memory_block_mib integer NOT NULL CHECK(memory_block_mib > 0),
 split_count integer NOT NULL CHECK(split_count > 0),
 admission text NOT NULL DEFAULT 'CLOSED' CHECK(admission IN ('CLOSED','OPEN')),
 version bigint NOT NULL DEFAULT 1 CHECK(version > 0),
 adopted_at timestamptz NOT NULL,
 FOREIGN KEY(cluster_id,pool_id) REFERENCES accelerator.pools(cluster_id,pool_id) ON DELETE RESTRICT,
 CHECK((mode='WHOLE_EXCLUSIVE' AND split_count=1) OR (mode='SHARED_FIXED' AND split_count>=2)),
 UNIQUE(cluster_id,group_id)
);
CREATE TABLE accelerator.group_nodes (
 cluster_id uuid NOT NULL,
 node_uid text NOT NULL,
 node_name text NOT NULL,
 group_id uuid NOT NULL,
 PRIMARY KEY(cluster_id,node_uid),
 FOREIGN KEY(cluster_id,group_id) REFERENCES accelerator.supply_groups(cluster_id,group_id) ON DELETE RESTRICT
);
CREATE TABLE accelerator.profiles (
 profile_id uuid NOT NULL,
 profile_version bigint NOT NULL CHECK(profile_version > 0),
 group_id uuid NOT NULL REFERENCES accelerator.supply_groups ON DELETE RESTRICT,
 display_name varchar(128) NOT NULL,
 mode text NOT NULL CHECK(mode IN ('WHOLE_EXCLUSIVE','SHARED_FIXED')),
 shared_memory_mib bigint NOT NULL CHECK(shared_memory_mib >= 0),
 memory_block_mib integer NOT NULL CHECK(memory_block_mib > 0),
 memory_blocks_per_device integer NOT NULL CHECK(memory_blocks_per_device >= 0),
 core_limit_percent integer NOT NULL,
 spec_json jsonb NOT NULL CHECK(jsonb_typeof(spec_json)='object'),
 spec_digest char(64) NOT NULL,
 baseline_digest char(64) NOT NULL,
 published boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(profile_id,profile_version),
 CHECK((mode='WHOLE_EXCLUSIVE' AND shared_memory_mib=0 AND memory_blocks_per_device=0 AND core_limit_percent=100)
    OR (mode='SHARED_FIXED' AND shared_memory_mib>0 AND core_limit_percent BETWEEN 1 AND 99
       AND shared_memory_mib % memory_block_mib = 0
       AND shared_memory_mib = memory_blocks_per_device::bigint * memory_block_mib))
);
CREATE UNIQUE INDEX profiles_one_published_per_group ON accelerator.profiles(group_id) WHERE published;
-- 同组model/mode/F/基线一致、每集群单一共享向量：应用在cluster->group行锁内校验。
-- 上线后首期冻结spec和成员；不得以UPDATE JSON绕过发布校验。

CREATE TABLE accelerator.gpu_usages (
 tenant_id uuid NOT NULL,
 owner_service text NOT NULL,
 resource_id uuid NOT NULL,
 create_operation_id uuid NOT NULL,
 revision smallint NOT NULL CHECK(revision IN (1,2)),
 state text NOT NULL CHECK(state IN ('DECLARED','ENDED')),
 immutable_plan_digest char(64) NOT NULL,
 plan_json jsonb NOT NULL CHECK(jsonb_typeof(plan_json)='object'),
 payload_digest char(64) NOT NULL,
 source_fact_ref text NOT NULL,
 source_operation_created_at timestamptz NOT NULL,
 end_reason text NULL,
 received_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(tenant_id,owner_service,resource_id),
 UNIQUE(tenant_id,owner_service,resource_id,create_operation_id),
 CHECK((revision=1 AND state='DECLARED' AND end_reason IS NULL)
    OR(revision=2 AND state='ENDED' AND end_reason IN ('GOVERNANCE_CANCELED_UNSENT','OWNER_RESOURCE_RELEASED')))
);
-- 使用行锁/受控upsert：revision不得回退；低版本先校验原ref和immutable_plan_digest，再no-op。
-- 同revision异payload拒绝；revision2可先建行。终态保留，严禁ENDED触发Binding级联删除。

CREATE TABLE accelerator.devices (
 device_id uuid PRIMARY KEY,
 cluster_id uuid NOT NULL REFERENCES accelerator.clusters ON DELETE RESTRICT,
 node_uid text NOT NULL,
 vendor text NOT NULL,
 vendor_device_id text NOT NULL,
 group_id uuid NULL,
 model_key text NOT NULL,
 physical_memory_bytes bigint NOT NULL CHECK(physical_memory_bytes >= 0),
 provider_memory_blocks bigint NULL CHECK(provider_memory_blocks >= 0),
 source_memory_block_mib integer NULL CHECK(source_memory_block_mib > 0),
 source_baseline_digest char(64) NULL,
 safe_memory_budget_mib bigint NULL CHECK(safe_memory_budget_mib >= 0),
 health text NOT NULL CHECK(health IN ('HEALTHY','UNHEALTHY','UNKNOWN')),
 observed_at timestamptz NULL,
 observation_complete boolean NOT NULL DEFAULT false,
 FOREIGN KEY(cluster_id,group_id) REFERENCES accelerator.supply_groups(cluster_id,group_id) ON DELETE RESTRICT,
 UNIQUE(cluster_id,vendor,vendor_device_id),
 UNIQUE(cluster_id,device_id)
);
CREATE TABLE accelerator.binding_facts (
 fact_id uuid PRIMARY KEY,
 cluster_id uuid NOT NULL,
 physical_device_id uuid NOT NULL,
 pod_uid text NOT NULL,
 namespace text NOT NULL,
 pod_name text NOT NULL,
 container_name text NOT NULL,
 source_kind text NOT NULL,
 source_event_ref text NOT NULL,
 source_unit text NOT NULL CHECK(source_unit IN ('BLOCKS','MIB','BYTES')),
 source_value bigint NOT NULL CHECK(source_value >= 0),
 source_memory_block_mib integer NULL CHECK(source_memory_block_mib > 0),
 source_baseline_digest char(64) NOT NULL,
 memory_mib bigint NOT NULL CHECK(memory_mib >= 0),
 core_limit_percent integer NOT NULL CHECK(core_limit_percent BETWEEN 0 AND 100),
 allocation_state text NOT NULL CHECK(allocation_state IN ('ACTIVE','RELEASE_CONFIRMED','UNKNOWN')),
 release_evidence_ref text NULL,
 verified_tenant_id uuid NULL,
 verified_owner_service text NULL,
 verified_resource_id uuid NULL,
 verified_create_operation_id uuid NULL,
 pending_association_json jsonb NULL,
 observed_at timestamptz NOT NULL,
 received_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(cluster_id,physical_device_id) REFERENCES accelerator.devices(cluster_id,device_id) ON DELETE RESTRICT,
 FOREIGN KEY(verified_tenant_id,verified_owner_service,verified_resource_id,verified_create_operation_id)
  REFERENCES accelerator.gpu_usages(tenant_id,owner_service,resource_id,create_operation_id) MATCH FULL ON DELETE RESTRICT,
 CHECK(allocation_state<>'RELEASE_CONFIRMED' OR release_evidence_ref IS NOT NULL),
 CHECK(source_unit<>'BLOCKS' OR source_memory_block_mib IS NOT NULL),
 UNIQUE(cluster_id,pod_uid,container_name,physical_device_id,source_kind,source_event_ref)
);
-- 绑定可在usage不存在时保存，verified_*全NULL；pending_association仅保存白名单未验证身份。
-- 没有父卡映射的样本不能填假device_id：保存在下列原始观测表，节点完整度为false。
CREATE TABLE accelerator.unresolved_observations (
 observation_id uuid PRIMARY KEY,
 cluster_id uuid NOT NULL REFERENCES accelerator.clusters ON DELETE RESTRICT,
 node_uid text NOT NULL,
 issue_code text NOT NULL,
 source_ref text NOT NULL,
 safe_payload jsonb NOT NULL,
 observed_at timestamptz NOT NULL,
 resolved_at timestamptz NULL
);
CREATE TABLE accelerator.release_observations (
 evidence_id uuid PRIMARY KEY,
 tenant_id uuid NOT NULL,
 owner_service text NOT NULL,
 resource_id uuid NOT NULL,
 create_operation_id uuid NOT NULL,
 delete_operation_id uuid NOT NULL,
 finding text NOT NULL CHECK(finding IN ('ALLOCATION_RELEASED','ALLOCATION_STILL_PRESENT','RELEASE_UNKNOWN')),
 scope_and_evidence_json jsonb NOT NULL,
 evidence_digest char(64) NOT NULL,
 observed_at timestamptz NOT NULL
);
-- 不对usage建FK，ObserveRelease不能等待投影。范围由owner校验，结果不是退款授权。
CREATE TABLE accelerator.admin_requests (
 actor_type text NOT NULL,
 actor_id text NOT NULL,
 action text NOT NULL,
 idempotency_key uuid NOT NULL,
 request_hash char(64) NOT NULL,
 result_json jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(actor_type,actor_id,action,idempotency_key)
);
-- 初始化业务修改与本域幂等/必要审计同事务；不在数据库内发RPC或provider写。
