package intercept

import (
	"testing"

	"github.com/Autumn-27/artex/db"
)

func TestToolConfigRoundTripPreservesDynamicDefaults(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("test PostgreSQL not configured")
	}
	d, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	const key = "intercept_enabled_tools"
	prior, exists, err := d.GetSetting(key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if exists {
			d.SetSetting(key, prior)
		} else {
			d.Exec(`DELETE FROM settings WHERE key=$1`, key)
		}
	})
	if _, err := d.Exec(`DELETE FROM settings WHERE key=$1`, key); err != nil {
		t.Fatal(err)
	}
	i := New(d)
	check := func(want bool) {
		t.Helper()
		for _, name := range []string{"custom_scan", "mcp__test__write"} {
			if enabled, err := i.CheckToolEnabled(name, true); err != nil || enabled != want {
				t.Fatalf("%s enabled=%v want=%v err=%v", name, enabled, want, err)
			}
		}
	}
	check(true)
	names, err := i.GetEnabledTools()
	if err != nil {
		t.Fatal(err)
	}
	if err := i.SetEnabledTools(names); err != nil {
		t.Fatal(err)
	}
	check(true)
	if err := i.SetEnabledTools([]string{"Write"}); err != nil {
		t.Fatal(err)
	}
	check(false)
}
