DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'quickstack_owner') THEN
        CREATE ROLE quickstack_owner LOGIN BYPASSRLS;
    END IF;
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'quickstack_app') THEN
        CREATE ROLE quickstack_app LOGIN;
    END IF;
END
$$;
