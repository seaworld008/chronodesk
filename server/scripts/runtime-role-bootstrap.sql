\set ON_ERROR_STOP on

DO $chronodesk$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_roles
        WHERE rolname = 'chronodesk_runtime'
    ) THEN
        CREATE ROLE chronodesk_runtime
            LOGIN
            NOINHERIT
            NOSUPERUSER
            NOCREATEDB
            NOCREATEROLE
            NOREPLICATION
            NOBYPASSRLS
            PASSWORD 'chronodesk_runtime_dev_only';
    ELSE
        ALTER ROLE chronodesk_runtime
            LOGIN
            NOINHERIT
            NOSUPERUSER
            NOCREATEDB
            NOCREATEROLE
            NOREPLICATION
            NOBYPASSRLS
            PASSWORD 'chronodesk_runtime_dev_only';
    END IF;
    IF pg_has_role(
        'chronodesk_runtime',
        'chronodesk',
        'MEMBER'
    ) THEN
        REVOKE chronodesk FROM chronodesk_runtime;
    END IF;
END
$chronodesk$;

GRANT CONNECT ON DATABASE chronodesk TO chronodesk_runtime;
GRANT USAGE ON SCHEMA public TO chronodesk_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE
    ON ALL TABLES IN SCHEMA public
    TO chronodesk_runtime;
GRANT USAGE, SELECT, UPDATE
    ON ALL SEQUENCES IN SCHEMA public
    TO chronodesk_runtime;

ALTER DEFAULT PRIVILEGES
    FOR ROLE chronodesk
    IN SCHEMA public
    GRANT SELECT, INSERT, UPDATE, DELETE
    ON TABLES
    TO chronodesk_runtime;
ALTER DEFAULT PRIVILEGES
    FOR ROLE chronodesk
    IN SCHEMA public
    GRANT USAGE, SELECT, UPDATE
    ON SEQUENCES
    TO chronodesk_runtime;
