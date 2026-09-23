SELECT count(*) FROM accelerator.pools WHERE cluster_id=$1 AND payload->>'display_name'='must-rollback';
