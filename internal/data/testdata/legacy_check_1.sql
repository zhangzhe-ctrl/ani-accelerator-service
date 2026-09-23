UPDATE accelerator.bindings SET tenant_id=$1::uuid,payload=jsonb_set(payload,'{verified_usage_ref,tenant_id}',to_jsonb($1::text)) WHERE device_id=$2;
