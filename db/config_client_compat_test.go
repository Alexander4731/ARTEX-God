package db

import "testing"

func TestProfileClientCompatRoundTrip(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	p := &LLMProfile{Name: "test-client-compat", Format: "openai-responses", Model: "test", APIKey: "secret-key", ClientCompat: true}
	p.ID, err = d.SaveProfile(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM llm_profiles WHERE id=$1`, p.ID) })
	check := func(want bool, key string) {
		t.Helper()
		got, err := d.ProfileByID(p.ID)
		if err != nil || got == nil {
			t.Fatalf("read: %v", err)
		}
		if got.ClientCompat != want || got.APIKey != key {
			t.Fatal("compatibility or stored credential failed to round-trip")
		}
		rows, err := d.ListProfiles()
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.ID == p.ID {
				if row.ClientCompat != want {
					t.Fatal("list lost compatibility setting")
				}
				return
			}
		}
		t.Fatal("profile not listed")
	}
	check(true, "secret-key")
	p.APIKey, p.ClientCompat = "", false
	if _, err := d.SaveProfile(p); err != nil {
		t.Fatal(err)
	}
	check(false, "secret-key")
	p.APIKey, p.ClientCompat = "replacement-key", true
	if _, err := d.SaveProfile(p); err != nil {
		t.Fatal(err)
	}
	check(true, "replacement-key")
}
