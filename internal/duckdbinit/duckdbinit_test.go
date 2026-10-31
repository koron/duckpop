package duckdbinit_test

import (
	"database/sql"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/koron/duckpop/internal/assert"
	"github.com/koron/duckpop/internal/duckdbinit"
)

func testSetting[T any](t *testing.T, conn *sql.Conn, name string, want T) {
	t.Helper()
	var got T
	err := conn.QueryRowContext(t.Context(), "SELECT current_setting(?)", name).Scan(&got)
	if err != nil {
		t.Errorf("failed to retrieve threads setting: %s", err)
		return
	}
	assert.Equal(t, want, got)
}

func TestSettings(t *testing.T) {
	settings := duckdbinit.Settings{
		Threads:     3,
		MemoryLimit: "2GiB",
		LockConfig:  true,
	}
	db, conn, err := duckdbinit.Open(t.Context(), settings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conn.Close()
		db.Close()
	})
	testSetting(t, conn, "threads", 3)
	testSetting(t, conn, "memory_limit", "2.0 GiB")
	testSetting(t, conn, "lock_configuration", true)
}

func TestLockConfig(t *testing.T) {
	t.Run("true", func(t *testing.T) {
		ctx := t.Context()
		settings := duckdbinit.Settings{
			Threads:    1,
			LockConfig: true,
		}
		db, conn, err := duckdbinit.Open(ctx, settings, "SET threads = 4")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			conn.Close()
			db.Close()
		})

		// Verify that initQueries is overwriting Settings.
		testSetting(t, conn, "threads", 4)
		if t.Failed() {
			return
		}

		// Verify that changes cannot be made after initQueries.
		_, err = conn.ExecContext(ctx, "SET threads = 8")
		assert.Equal(t, `Invalid Input Error: Cannot change configuration option ""threads"" - the configuration has been locked`, err.Error())
	})

	t.Run("false", func(t *testing.T) {
		ctx := t.Context()
		settings := duckdbinit.Settings{
			Threads:    1,
			LockConfig: false,
		}
		db, conn, err := duckdbinit.Open(ctx, settings, "SET threads = 4")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			conn.Close()
			db.Close()
		})

		// Verify that initQueries is overwriting Settings.
		testSetting(t, conn, "threads", 4)
		if t.Failed() {
			return
		}

		// Verify that changes can be made after initQueries.
		_, err = conn.ExecContext(ctx, "SET threads = 8")
		if err != nil {
			t.Fatalf("failed to set threads: %s", err)
		}
		testSetting(t, conn, "threads", 8)
	})
}
