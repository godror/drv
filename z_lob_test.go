// Copyright 2019, 2020 The Godror Authors
//
//
// SPDX-License-Identifier: UPL-1.0 OR Apache-2.0

package godror_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	godror "github.com/godror/godror"
)

func TestLOBAppend(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(testContext("LOBAppend"), 30*time.Second)
	defer cancel()

	// To have a valid LOB locator, we have to keep the Stmt around.
	qry := `DECLARE tmp BLOB;
BEGIN
  DBMS_LOB.createtemporary(tmp, TRUE, DBMS_LOB.SESSION);
  :1 := tmp;
END;`
	tx, err := testDb.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, qry)
	if err != nil {
		t.Fatal(fmt.Errorf("%s: %w", qry, err))
	}
	defer stmt.Close()
	var tmp godror.Lob
	if _, err := stmt.ExecContext(ctx, godror.LobAsReader(), sql.Out{Dest: &tmp}); err != nil {
		t.Fatalf("Failed to create temporary lob: %+v", err)
	}
	t.Logf("tmp: %#v", tmp)

	want := [...]byte{1, 2, 3, 4, 5}
	if _, err := tx.ExecContext(ctx,
		"BEGIN dbms_lob.append(:1, :2); END;",
		tmp, godror.Lob{Reader: bytes.NewReader(want[:])},
	); err != nil {
		t.Errorf("Failed to write buffer(%v) to lob(%v): %+v", want, tmp, err)
	}

	if true {
		// Either use DBMS_LOB.freetemporary
		if _, err := tx.ExecContext(ctx, "BEGIN dbms_lob.freetemporary(:1); END;", tmp); err != nil {
			t.Errorf("Failed to close temporary lob(%v): %+v", tmp, err)
		}
	} else {
		// Or Hijack and Close it.
		dl, err := tmp.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		defer dl.Close()
		length, err := dl.Size()
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("length: %d", length)
		if length != int64(len(want)) {
			t.Errorf("length mismatch: got %d, wanted %d", length, len(want))
		}
	}
}

func TestStatWithLobs(t *testing.T) {
	t.Parallel()
	//defer tl.enableLogging(t)()
	ctx, cancel := context.WithTimeout(testContext("StatWithLobs"), 30*time.Second)
	defer cancel()

	ms, err := newMetricSet(ctx, testDb)
	if err != nil {
		t.Fatal(err)
	}
	defer ms.Close()
	if _, err = ms.Fetch(ctx); err != nil {
		var c interface{ Code() int }
		if errors.As(err, &c) && c.Code() == 942 {
			t.Skip(err)
			return
		}
		t.Fatal(err)
	}

	for i := 0; i < 100; i++ {
		if err := ctx.Err(); err != nil {
			break
		}
		events, err := ms.Fetch(ctx)
		t.Log("events:", len(events))
		if err != nil {
			t.Fatal(err)
		}
	}
}

func newMetricSet(ctx context.Context, db *sql.DB) (*metricSet, error) {
	qry := "select /* metricset: sqlstats */ inst_id, sql_fulltext, last_active_time from gv$sqlstats WHERE ROWNUM < 11"
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	stmt, err := tx.PrepareContext(ctx, qry)
	if err != nil {
		return nil, err
	}

	return &metricSet{tx: tx, stmt: stmt}, nil
}

type metricSet struct {
	tx   *sql.Tx
	stmt *sql.Stmt
}

func (m *metricSet) Close() error {
	tx, st := m.tx, m.stmt
	m.tx, m.stmt = nil, nil
	if st == nil {
		return nil
	}
	st.Close()
	if tx == nil {
		return nil
	}
	return tx.Rollback()
}

// Fetch methods implements the data gathering and data conversion to the right format
// It returns the event which is then forward to the output. In case of an error, a
// descriptive error must be returned.
func (m *metricSet) Fetch(ctx context.Context) ([]event, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	rows, err := m.stmt.QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []event
	for rows.Next() {
		var e event
		if err := rows.Scan(&e.ID, &e.Text, &e.LastActive); err != nil {
			return events, err
		}
		events = append(events, e)
	}

	return events, nil
}

type event struct {
	ID         int64
	Text       string
	LastActive time.Time
}
