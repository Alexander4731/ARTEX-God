package db

import (
	"errors"
	"slices"
	"testing"
)

func TestConfigurationEnumNormalization(t *testing.T) {
	var noDB *DB
	for _, value := range []string{"", "unknown", "OpenAI"} {
		_, err := noDB.SaveProfile(&LLMProfile{Format: value})
		var input *InputError
		if !errors.As(err, &input) || input.Field != "format" {
			t.Fatalf("invalid profile: %v", err)
		}
		if _, err := noDB.SaveMCP(&MCPServer{Transport: value}); !errors.As(err, &input) || input.Field != "transport" {
			t.Fatalf("invalid MCP: %v", err)
		}
	}
	d, _, _ := testSetup(t)
	defer d.Close()
	p := &LLMProfile{Name: "foundation-enum", Format: " openai ", Model: "test"}
	id, err := d.SaveProfile(p)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec("DELETE FROM llm_profiles WHERE id=$1", id)
	saved, err := d.ProfileByID(id)
	if err != nil || saved.Format != "openai" {
		t.Fatalf("normalized profile: %+v %v", saved, err)
	}
	m := &MCPServer{Name: "foundation-enum", Transport: " stdio "}
	id, err = d.SaveMCP(m)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec("DELETE FROM mcp_servers WHERE id=$1", id)
	var transport string
	if err = d.QueryRow("SELECT transport FROM mcp_servers WHERE id=$1", id).Scan(&transport); err != nil || transport != "stdio" {
		t.Fatalf("normalized transport: %q %v", transport, err)
	}
}

func TestNotificationRouteMigration(t *testing.T) {
	d, _, _ := testSetup(t)
	defer d.Close()
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for _, scenario := range []struct{ sql, want string }{
		{"DELETE FROM settings WHERE key='notify_http_route';" + notificationRouteAfterSchema, "direct"},
		{notificationRouteBeforeSchema + notificationRouteAfterSchema, "direct"},
		{"DELETE FROM settings WHERE key='notify_http_route';" + notificationRouteBeforeSchema + notificationRouteAfterSchema, "legacy_environment"},
		{notificationRouteBeforeSchema + notificationRouteAfterSchema, "legacy_environment"},
	} {
		if _, err = tx.Exec(scenario.sql); err != nil {
			t.Fatal(err)
		}
		var got string
		if err = tx.QueryRow("SELECT value FROM settings WHERE key='notify_http_route'").Scan(&got); err != nil || got != scenario.want {
			t.Fatalf("route=%q want=%q err=%v", got, scenario.want, err)
		}
	}
}

func TestAssetDSLEmptyMatchesNullAndEmpty(t *testing.T) {
	d, assets, _ := testSetup(t)
	defer d.Close()
	var ids []int64
	for i, icp := range []string{"", "", "registered"} {
		domain := []string{"foundation-null.example", "foundation-empty.example", "foundation-full.example"}[i]
		id, err := assets.UpsertRootDomain(UpsertRootDomainReq{Domain: domain, ICP: icp, TaskID: 876001})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		defer deleteAsset(d, id)
	}
	if _, err := d.Exec("UPDATE assets SET icp='' WHERE id=$1", ids[1]); err != nil {
		t.Fatal(err)
	}
	rows, err := assets.QueryDSL(`icp==""`, "root_domain", 876001, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, a := range rows {
		got = append(got, a.ID)
	}
	slices.Sort(got)
	slices.Sort(ids[:2])
	if !slices.Equal(got, ids[:2]) {
		t.Fatalf("empty result=%v want=%v", got, ids[:2])
	}
	rows, err = assets.QueryDSL(`icp=="registered"`, "root_domain", 876001, 100, 0)
	if err != nil || len(rows) != 1 || rows[0].ID != ids[2] {
		t.Fatalf("nonempty result=%v err=%v", rows, err)
	}
}
