-- 版本化数据增量；原001实验脚本和旧gpu.count不改。本文件不会配置任何租户套餐额度。
BEGIN;
INSERT INTO sys_quota_definitions(code,display_name,unit,accounting_kind)
VALUES ('gpu.physical.count','整卡 GPU','gpu','CONCURRENT'),
       ('gpu.shared_memory_mib','共享 GPU 显存','MiB','CONCURRENT')
ON CONFLICT(code) DO NOTHING;
DO $$
BEGIN
 IF EXISTS (
  SELECT 1 FROM (VALUES ('gpu.physical.count','gpu','CONCURRENT'),
                       ('gpu.shared_memory_mib','MiB','CONCURRENT')) AS e(code,unit,kind)
  LEFT JOIN sys_quota_definitions d ON d.code=e.code
  WHERE d.code IS NULL OR d.unit<>e.unit OR d.accounting_kind<>e.kind
 ) THEN
  RAISE EXCEPTION 'GPU quota definition conflict; no silent overwrite';
 END IF;
END $$;
COMMIT;
-- ENFORCED/NOT_ENABLED是代码/Proto派生执行能力，不写目录表。另走既有套餐CRUD设置上限。
