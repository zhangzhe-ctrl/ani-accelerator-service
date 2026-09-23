UPDATE accelerator.usages SET payload = payload #- $4::text[]
WHERE tenant_id=$1 AND owner_service=$2 AND resource_id=$3;
