package db

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"
)

const runtimeLockKey int64 = 7337741003

type RuntimeLock struct {
	conn   *sql.Conn
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

// AcquireRuntimeLock pins exclusive runtime ownership to a dedicated PostgreSQL
// session. Losing that session cancels execution rather than silently reacquiring.
func (d *DB) AcquireRuntimeLock(ctx context.Context, lost func(error)) (*RuntimeLock, error) {
	conn, err := d.Conn(ctx)
	if err != nil {
		return nil, err
	}
	var acquired bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, runtimeLockKey).Scan(&acquired); err != nil || !acquired {
		conn.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("已有实例正在运行此数据库的任务")
	}
	monitor, cancel := context.WithCancel(ctx)
	lock := &RuntimeLock{conn: conn, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(lock.done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-monitor.Done():
				return
			case <-ticker.C:
				ping, stop := context.WithTimeout(monitor, 2*time.Second)
				err := conn.PingContext(ping)
				stop()
				if err != nil {
					if monitor.Err() == nil && lost != nil {
						lost(fmt.Errorf("运行实例锁连接丢失: %w", err))
					}
					return
				}
			}
		}
	}()
	return lock, nil
}

func (l *RuntimeLock) Close() {
	l.once.Do(func() {
		l.cancel()
		<-l.done
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = l.conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, runtimeLockKey)
		_ = l.conn.Close()
	})
}
