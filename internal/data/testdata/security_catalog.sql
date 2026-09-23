SELECT NOT r.rolsuper AND NOT r.rolbypassrls AND NOT r.rolcreatedb AND NOT r.rolcreaterole
AND NOT has_database_privilege(current_user,current_database(),'CREATE')
AND NOT has_database_privilege(current_user,current_database(),'TEMPORARY')
AND NOT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='accelerator' AND (c.relrowsecurity OR c.relforcerowsecurity OR pg_has_role(current_user,c.relowner,'USAGE')))
AND NOT EXISTS(SELECT 1 FROM pg_policy p JOIN pg_class c ON c.oid=p.polrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='accelerator')
FROM pg_roles r WHERE r.rolname=current_user;
