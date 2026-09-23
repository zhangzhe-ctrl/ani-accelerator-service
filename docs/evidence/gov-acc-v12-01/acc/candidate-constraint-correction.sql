ALTER TABLE accelerator.bindings DROP CONSTRAINT binding_identity_json;
ALTER TABLE accelerator.bindings ADD CONSTRAINT binding_identity_json CHECK ((payload->>'binding_id'=binding_id::text AND payload->'pod'->>'cluster_id'=cluster_id::text AND payload->'pod'->>'uid'=pod_uid AND payload->'pod'->>'container_name'=container_name AND payload->>'physical_device_id'=device_id::text) IS TRUE);
