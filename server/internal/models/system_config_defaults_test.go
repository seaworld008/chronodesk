package models

import "testing"

func TestDefaultSystemConfigsAreUniqueActiveAndSelfDescribing(t *testing.T) {
	configs := DefaultSystemConfigs("test-version")
	if len(configs) == 0 {
		t.Fatal("default system configuration catalog is empty")
	}
	seen := make(map[string]struct{}, len(configs))
	for _, config := range configs {
		if config.Key == "" ||
			config.ValueType == "" ||
			config.Category == "" ||
			config.Description == "" {
			t.Errorf("incomplete default system config: %+v", config)
		}
		if _, exists := seen[config.Key]; exists {
			t.Errorf("duplicate default system config key %q", config.Key)
		}
		seen[config.Key] = struct{}{}
		if !config.IsRequired || !config.IsActive || config.Version != 1 {
			t.Errorf("default config is not active and required: %+v", config)
		}
		if config.DefaultValue != config.Value {
			t.Errorf("default value differs from current seed for %q", config.Key)
		}
	}
	if _, exists := seen["security.password_min_length"]; !exists {
		t.Error("required password policy is absent")
	}
	if _, exists := seen["security.password_require_symbol"]; !exists {
		t.Error("password symbol policy is absent")
	}
}
