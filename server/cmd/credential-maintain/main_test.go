package main

import "testing"

func TestSelectMaintenanceModeRequiresExactlyOneMode(t *testing.T) {
	tests := []struct {
		name                         string
		validate, rotate, quarantine bool
		want                         maintenanceMode
		wantError                    bool
	}{
		{name: "validate", validate: true, want: modeValidate},
		{name: "rotate", rotate: true, want: modeRotate},
		{name: "quarantine", quarantine: true, want: modeQuarantine},
		{name: "none", wantError: true},
		{name: "multiple", validate: true, rotate: true, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := selectMaintenanceMode(test.validate, test.rotate, test.quarantine)
			if test.wantError {
				if err == nil {
					t.Fatalf("selectMaintenanceMode() = %q, nil; want error", got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("selectMaintenanceMode() = %q, %v; want %q, nil", got, err, test.want)
			}
		})
	}
}
