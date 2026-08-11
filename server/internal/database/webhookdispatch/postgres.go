package webhookdispatch

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const (
	GenerationFunctionName = "chronodesk_enforce_outbox_dispatch_generation"
	GenerationTriggerName  = "trg_outbox_dispatch_generation_update"
)

const PostgresGenerationFunctionBody = `
BEGIN
    IF OLD.destination_type = 'webhook'
       AND OLD.status = 'processing'
       AND NEW.status = 'processing' THEN
        IF NEW.destination_type <> 'webhook' THEN
            RAISE EXCEPTION USING
                ERRCODE = '23514',
                MESSAGE = 'outbox dispatch generation invariant';
        END IF;
        IF OLD.attempts IS DISTINCT FROM NEW.attempts
           OR OLD.locked_at IS DISTINCT FROM NEW.locked_at
           OR OLD.locked_by IS DISTINCT FROM NEW.locked_by
           OR OLD.lock_token IS DISTINCT FROM NEW.lock_token THEN
            IF NOT (
                OLD.dispatch_started_at IS NOT NULL
                AND OLD.locked_at IS NOT NULL
                AND OLD.dispatch_started_at = OLD.locked_at
                AND NEW.dispatch_started_at IS NOT NULL
                AND NEW.locked_at IS NOT NULL
                AND NEW.dispatch_started_at = NEW.locked_at
                AND NEW.dispatch_started_at IS DISTINCT FROM OLD.dispatch_started_at
                AND NEW.attempts = OLD.attempts + 1
                AND NEW.locked_at > OLD.locked_at
                AND TRIM(NEW.locked_by) <> ''
                AND NEW.lock_token IS NOT NULL
                AND NEW.lock_token IS DISTINCT FROM OLD.lock_token
                AND NEW.lock_token ~ '^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
            ) THEN
                RAISE EXCEPTION USING
                    ERRCODE = '23514',
                    MESSAGE = 'outbox dispatch generation invariant';
            END IF;
        ELSIF OLD.dispatch_started_at IS NULL THEN
            IF NEW.dispatch_started_at IS NOT NULL THEN
                RAISE EXCEPTION USING
                    ERRCODE = '23514',
                    MESSAGE = 'outbox dispatch generation invariant';
            END IF;
        ELSIF OLD.locked_at IS NOT NULL
              AND OLD.dispatch_started_at = OLD.locked_at THEN
            IF NEW.dispatch_started_at IS NULL
               OR NOT (
                   NEW.dispatch_started_at = OLD.dispatch_started_at
                   OR NEW.dispatch_started_at > NEW.locked_at
               ) THEN
                RAISE EXCEPTION USING
                    ERRCODE = '23514',
                    MESSAGE = 'outbox dispatch generation invariant';
            END IF;
        ELSIF NEW.dispatch_started_at IS DISTINCT FROM OLD.dispatch_started_at THEN
            RAISE EXCEPTION USING
                ERRCODE = '23514',
                MESSAGE = 'outbox dispatch generation invariant';
        END IF;
    END IF;
    RETURN NEW;
END;
`

func MigratePostgresGenerationFence(db *gorm.DB) error {
	if db == nil || db.Dialector.Name() != "postgres" {
		return errors.New(
			"PostgreSQL Webhook dispatch generation database is required",
		)
	}
	return db.Transaction(func(tx *gorm.DB) error {
		schema, err := currentSchema(tx)
		if err != nil {
			return err
		}
		table := qualified(schema, "outbox_deliveries")
		if err := tx.Exec(
			"LOCK TABLE " + table + " IN SHARE ROW EXCLUSIVE MODE",
		).Error; err != nil {
			return fmt.Errorf(
				"lock Webhook dispatch generation table: %w",
				err,
			)
		}
		valid, err := PostgresGenerationFenceIsValid(tx)
		if err != nil {
			return err
		}
		if valid {
			return nil
		}
		function := qualified(schema, GenerationFunctionName)
		if err := tx.Exec(
			"DROP TRIGGER IF EXISTS " + quote(GenerationTriggerName) +
				" ON " + table,
		).Error; err != nil {
			return fmt.Errorf(
				"drop Webhook dispatch generation trigger: %w",
				err,
			)
		}
		if err := tx.Exec(
			"DROP FUNCTION IF EXISTS " + function + "()",
		).Error; err != nil {
			return fmt.Errorf(
				"drop Webhook dispatch generation function: %w",
				err,
			)
		}
		if err := tx.Exec(
			"CREATE FUNCTION " + function +
				"() RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER AS $$" +
				PostgresGenerationFunctionBody + "$$",
		).Error; err != nil {
			return fmt.Errorf(
				"install Webhook dispatch generation function: %w",
				err,
			)
		}
		if err := tx.Exec(
			"REVOKE ALL ON FUNCTION " + function + "() FROM PUBLIC",
		).Error; err != nil {
			return fmt.Errorf(
				"restrict Webhook dispatch generation function: %w",
				err,
			)
		}
		if err := tx.Exec(
			"CREATE TRIGGER " + quote(GenerationTriggerName) +
				" AFTER UPDATE ON " + table + " FOR EACH ROW " +
				"EXECUTE FUNCTION " + function + "()",
		).Error; err != nil {
			return fmt.Errorf(
				"install Webhook dispatch generation trigger: %w",
				err,
			)
		}
		valid, err = PostgresGenerationFenceIsValid(tx)
		if err != nil {
			return err
		}
		if !valid {
			return errors.New(
				"installed PostgreSQL Webhook dispatch generation fence is incompatible",
			)
		}
		return nil
	})
}

func PostgresGenerationFenceIsValid(db *gorm.DB) (bool, error) {
	if db == nil || db.Dialector.Name() != "postgres" {
		return false, errors.New(
			"PostgreSQL Webhook dispatch generation database is required",
		)
	}
	var functions []struct {
		OID             uint64 `gorm:"column:oid"`
		Body            string `gorm:"column:body"`
		Language        string `gorm:"column:language"`
		Kind            string `gorm:"column:kind"`
		Volatility      string `gorm:"column:volatility"`
		Parallel        string `gorm:"column:parallel"`
		SecurityDefiner bool   `gorm:"column:security_definer"`
		Leakproof       bool   `gorm:"column:leakproof"`
		ArgumentCount   int    `gorm:"column:argument_count"`
		DefaultCount    int    `gorm:"column:default_count"`
		VariadicType    uint32 `gorm:"column:variadic_type"`
		HasAllArgTypes  bool   `gorm:"column:has_all_arg_types"`
		HasConfig       bool   `gorm:"column:has_config"`
		OwnerMatches    bool   `gorm:"column:owner_matches"`
		OwnerOnlyACL    bool   `gorm:"column:owner_only_acl"`
		ResultType      string `gorm:"column:result_type"`
	}
	if err := db.Raw(`
		SELECT
			routine.oid::bigint AS oid,
			routine.prosrc AS body,
			language.lanname AS language,
			routine.prokind::text AS kind,
			routine.provolatile::text AS volatility,
			routine.proparallel::text AS parallel,
			routine.prosecdef AS security_definer,
			routine.proleakproof AS leakproof,
			routine.pronargs AS argument_count,
			routine.pronargdefaults AS default_count,
			routine.provariadic AS variadic_type,
			routine.proallargtypes IS NOT NULL AS has_all_arg_types,
			routine.proconfig IS NOT NULL AS has_config,
			routine.proowner = table_state.relowner AS owner_matches,
			NOT EXISTS (
				SELECT 1
				FROM aclexplode(
					COALESCE(
						routine.proacl,
						acldefault('f', routine.proowner)
					)
				) AS grant_state
				WHERE grant_state.grantee <> routine.proowner
			) AS owner_only_acl,
			pg_get_function_result(routine.oid) AS result_type
		FROM pg_proc AS routine
		JOIN pg_namespace AS namespace
		  ON namespace.oid = routine.pronamespace
		JOIN pg_language AS language
		  ON language.oid = routine.prolang
		JOIN pg_class AS table_state
		  ON table_state.relname = 'outbox_deliveries'
		JOIN pg_namespace AS table_namespace
		  ON table_namespace.oid = table_state.relnamespace
		 AND table_namespace.nspname = CURRENT_SCHEMA()
		WHERE namespace.nspname = CURRENT_SCHEMA()
		  AND routine.proname = ?
		  AND routine.pronargs = 0
		  AND routine.prorettype = 'trigger'::regtype
	`, GenerationFunctionName).Scan(&functions).Error; err != nil {
		return false, err
	}
	if len(functions) != 1 ||
		functions[0].Language != "plpgsql" ||
		functions[0].Kind != "f" ||
		functions[0].Volatility != "v" ||
		functions[0].Parallel != "u" ||
		functions[0].SecurityDefiner ||
		functions[0].Leakproof ||
		functions[0].ArgumentCount != 0 ||
		functions[0].DefaultCount != 0 ||
		functions[0].VariadicType != 0 ||
		functions[0].HasAllArgTypes ||
		functions[0].HasConfig ||
		!functions[0].OwnerMatches ||
		!functions[0].OwnerOnlyACL ||
		functions[0].ResultType != "trigger" ||
		canonicalBody(functions[0].Body) !=
			canonicalBody(PostgresGenerationFunctionBody) {
		return false, nil
	}

	var triggers []struct {
		Enabled        string `gorm:"column:enabled"`
		Internal       bool   `gorm:"column:internal"`
		RowLevel       bool   `gorm:"column:row_level"`
		BeforeEvent    bool   `gorm:"column:before_event"`
		InsteadEvent   bool   `gorm:"column:instead_event"`
		InsertEvent    bool   `gorm:"column:insert_event"`
		DeleteEvent    bool   `gorm:"column:delete_event"`
		UpdateEvent    bool   `gorm:"column:update_event"`
		TruncateEvent  bool   `gorm:"column:truncate_event"`
		ColumnSpecific bool   `gorm:"column:column_specific"`
		HasArguments   bool   `gorm:"column:has_arguments"`
		HasWhen        bool   `gorm:"column:has_when"`
		FunctionName   string `gorm:"column:function_name"`
		FunctionSchema string `gorm:"column:function_schema"`
		FunctionOID    uint64 `gorm:"column:function_oid"`
	}
	if err := db.Raw(`
		SELECT
			trigger.tgenabled::text AS enabled,
			trigger.tgisinternal AS internal,
			(trigger.tgtype & 1) <> 0 AS row_level,
			(trigger.tgtype & 2) <> 0 AS before_event,
			(trigger.tgtype & 64) <> 0 AS instead_event,
			(trigger.tgtype & 4) <> 0 AS insert_event,
			(trigger.tgtype & 8) <> 0 AS delete_event,
			(trigger.tgtype & 16) <> 0 AS update_event,
			(trigger.tgtype & 32) <> 0 AS truncate_event,
			trigger.tgattr::text <> '' AS column_specific,
			octet_length(trigger.tgargs) <> 0 AS has_arguments,
			trigger.tgqual IS NOT NULL AS has_when,
			routine.proname AS function_name,
			routine_namespace.nspname AS function_schema,
			routine.oid::bigint AS function_oid
		FROM pg_trigger AS trigger
		JOIN pg_class AS table_state
		  ON table_state.oid = trigger.tgrelid
		JOIN pg_namespace AS namespace
		  ON namespace.oid = table_state.relnamespace
		JOIN pg_proc AS routine
		  ON routine.oid = trigger.tgfoid
		JOIN pg_namespace AS routine_namespace
		  ON routine_namespace.oid = routine.pronamespace
		WHERE namespace.nspname = CURRENT_SCHEMA()
		  AND table_state.relname = 'outbox_deliveries'
		  AND trigger.tgname = ?
		  AND routine_namespace.nspname = CURRENT_SCHEMA()
	`, GenerationTriggerName).Scan(&triggers).Error; err != nil {
		return false, err
	}
	if len(triggers) != 1 {
		return false, nil
	}
	trigger := triggers[0]
	return trigger.Enabled == "O" &&
		!trigger.Internal &&
		trigger.RowLevel &&
		!trigger.BeforeEvent &&
		!trigger.InsteadEvent &&
		!trigger.InsertEvent &&
		!trigger.DeleteEvent &&
		trigger.UpdateEvent &&
		!trigger.TruncateEvent &&
		!trigger.ColumnSpecific &&
		!trigger.HasArguments &&
		!trigger.HasWhen &&
		trigger.FunctionName == GenerationFunctionName &&
		trigger.FunctionSchema != "" &&
		trigger.FunctionOID == functions[0].OID, nil
}

func ValidatePostgresRuntimePrivileges(db *gorm.DB) error {
	if db == nil || db.Config == nil || db.Statement == nil ||
		db.Dialector == nil || db.Dialector.Name() != "postgres" {
		return errors.New(
			"PostgreSQL Webhook dispatch runtime database is required",
		)
	}
	var state struct {
		MarkerUpdate    bool  `gorm:"column:marker_update"`
		SchemaCreate    bool  `gorm:"column:schema_create"`
		TableTrigger    bool  `gorm:"column:table_trigger"`
		FunctionCount   int64 `gorm:"column:function_count"`
		FunctionExecute bool  `gorm:"column:function_execute"`
	}
	if err := db.Raw(`
		WITH generation_function AS (
			SELECT routine.oid
			FROM pg_proc AS routine
			JOIN pg_namespace AS namespace
			  ON namespace.oid = routine.pronamespace
			WHERE namespace.nspname = CURRENT_SCHEMA()
			  AND routine.proname = ?
			  AND routine.pronargs = 0
			  AND routine.prorettype = 'trigger'::regtype
		)
		SELECT
			has_column_privilege(
				current_user,
				format('%I.%I', CURRENT_SCHEMA(), 'outbox_deliveries'),
				'dispatch_started_at',
				'UPDATE'
			) AS marker_update,
			has_schema_privilege(
				current_user,
				CURRENT_SCHEMA(),
				'CREATE'
			) AS schema_create,
			has_table_privilege(
				current_user,
				format('%I.%I', CURRENT_SCHEMA(), 'outbox_deliveries'),
				'TRIGGER'
			) AS table_trigger,
			(SELECT COUNT(*) FROM generation_function) AS function_count,
			COALESCE(
				(
					SELECT BOOL_OR(
						has_function_privilege(
							current_user,
							generation_function.oid,
							'EXECUTE'
						)
					)
					FROM generation_function
				),
				FALSE
			) AS function_execute
	`, GenerationFunctionName).Scan(&state).Error; err != nil {
		return fmt.Errorf(
			"inspect Webhook dispatch runtime privileges: %w",
			err,
		)
	}
	if !state.MarkerUpdate ||
		state.SchemaCreate ||
		state.TableTrigger ||
		state.FunctionCount != 1 ||
		state.FunctionExecute {
		return fmt.Errorf(
			"Webhook dispatch runtime privileges are unsafe: marker_update=%t schema_create=%t table_trigger=%t function_count=%d function_execute=%t",
			state.MarkerUpdate,
			state.SchemaCreate,
			state.TableTrigger,
			state.FunctionCount,
			state.FunctionExecute,
		)
	}
	return nil
}

func currentSchema(db *gorm.DB) (string, error) {
	var schema string
	if err := db.Raw("SELECT CURRENT_SCHEMA()").Scan(&schema).Error; err != nil {
		return "", fmt.Errorf(
			"resolve PostgreSQL Webhook dispatch schema: %w",
			err,
		)
	}
	if strings.TrimSpace(schema) == "" {
		return "", errors.New(
			"PostgreSQL Webhook dispatch current schema is required",
		)
	}
	return schema, nil
}

func quote(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func qualified(schema string, name string) string {
	return quote(schema) + "." + quote(name)
}

func canonicalBody(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
