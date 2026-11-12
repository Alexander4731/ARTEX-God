package db

import (
	"context"
	"fmt"
)

// Preserve the environment proxy on upgrades; a fresh database starts direct.
// Both inserts run under the existing schema migration lock and transaction.
const notificationRouteBeforeSchema = `
DO $$ BEGIN
 IF to_regclass('public.settings') IS NOT NULL THEN
  INSERT INTO settings(key,value) VALUES ('notify_http_route','legacy_environment') ON CONFLICT DO NOTHING;
 END IF;
END $$;
`
const notificationRouteAfterSchema = `
INSERT INTO settings(key,value) VALUES ('notify_http_route','direct') ON CONFLICT DO NOTHING;
`

func (d *DB) NotificationHTTPRoute(ctx context.Context) (string, error) {
	var route string
	if err := d.QueryRowContext(ctx, "SELECT value FROM settings WHERE key='notify_http_route'").Scan(&route); err != nil {
		return "", fmt.Errorf("读取通知 HTTP 路由: %w", err)
	}
	if route != "direct" && route != "legacy_environment" {
		return "", fmt.Errorf("未知通知 HTTP 路由")
	}
	return route, nil
}
