// Copyright 2019, 2020 The Godror Authors
//
//
// SPDX-License-Identifier: UPL-1.0 OR Apache-2.0

package godror_test

import (
	"context"
	"strings"
	"testing"
	"time"

	errors "golang.org/x/xerrors"

	godror "github.com/godror/godror"
)

func TestQueue(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(testContext("Queue"), 30*time.Second)
	defer cancel()
	conn, err := testDb.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var user string
	if err = conn.QueryRowContext(ctx, "SELECT USER FROM DUAL").Scan(&user); err != nil {
		t.Fatal(err)
	}

	const qName = "TEST_Q"
	const qTblName = qName + "_TBL"
	qry := `DECLARE
		tbl CONSTANT VARCHAR2(61) := '` + user + "." + qTblName + `';
		q CONSTANT VARCHAR2(61) := '` + user + "." + qName + `';
	BEGIN
		BEGIN SYS.DBMS_AQADM.stop_queue(q); EXCEPTION WHEN OTHERS THEN NULL; END;
		BEGIN SYS.DBMS_AQADM.drop_queue(q); EXCEPTION WHEN OTHERS THEN NULL; END;
		BEGIN SYS.DBMS_AQADM.drop_queue_table(tbl); EXCEPTION WHEN OTHERS THEN NULL; END;

		SYS.DBMS_AQADM.CREATE_QUEUE_TABLE(tbl, 'RAW');
		SYS.DBMS_AQADM.CREATE_QUEUE(q, tbl);
		SYS.DBMS_AQADM.grant_queue_privilege('ENQUEUE', q, '` + user + `');
		SYS.DBMS_AQADM.grant_queue_privilege('DEQUEUE', q, '` + user + `');
		SYS.DBMS_AQADM.start_queue(q);
	END;`
	if _, err = conn.ExecContext(ctx, qry); err != nil {
		if strings.Contains(err.Error(), "PLS-00201: identifier 'SYS.DBMS_AQADM' must be declared") {
			t.Skip(err.Error())
		}
		t.Log(errors.Errorf("%s: %w", qry, err))
	}
	defer func() {
		conn.ExecContext(
			testContext("Queue-drop"),
			`DECLARE
			tbl CONSTANT VARCHAR2(61) := USER||'.'||:1;
			q CONSTANT VARCHAR2(61) := USER||'.'||:2;
		BEGIN
			BEGIN SYS.DBMS_AQADM.stop_queue(q); EXCEPTION WHEN OTHERS THEN NULL; END;
			BEGIN SYS.DBMS_AQADM.drop_queue(q); EXCEPTION WHEN OTHERS THEN NULL; END;
			BEGIN SYS.DBMS_AQADM.drop_queue_table(tbl); EXCEPTION WHEN OTHERS THEN NULL;
		END;`,
			qTblName, qName,
		)
	}()

	q, err := godror.NewQueue(ctx, conn, qName, "")
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	t.Log("name:", q.Name())
	enqOpts, err := q.EnqOptions()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("enqOpts: %#v", enqOpts)
	deqOpts, err := q.DeqOptions()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("deqOpts: %#v", deqOpts)

	if err = q.Enqueue([]godror.Message{godror.Message{Raw: []byte("árvíztűrő tükörfúrógép")}}); err != nil {
		var ec interface{ Code() int }
		if errors.As(err, &ec) && ec.Code() == 24444 {
			t.Skip(err)
		}
		t.Fatal("enqueue:", err)
	}
	msgs := make([]godror.Message, 1)
	n, err := q.Dequeue(msgs)
	if err != nil {
		t.Error("dequeue:", err)
	}
	t.Logf("received %d messages", n)
	for _, m := range msgs[:n] {
		t.Logf("got: %#v (%q)", m, string(m.Raw))
	}
}
func TestQueueObject(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(testContext("QueueObject"), 30*time.Second)
	defer cancel()
	conn, err := testDb.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var user string
	if err = conn.QueryRowContext(ctx, "SELECT USER FROM DUAL").Scan(&user); err != nil {
		t.Fatal(err)
	}

	const qName = "TEST_QOBJ"
	const qTblName = qName + "_TBL"
	const qTypName = qName + "_TYP"
	const arrTypName = qName + "_ARR_TYP"
	conn.ExecContext(ctx, "DROP TYPE "+qTypName)
	conn.ExecContext(ctx, "DROP TYPE "+arrTypName)

	var plus strings.Builder
	for _, qry := range []string{
		"BEGIN SYS.DBMS_AQADM.stop_queue('" + user + "." + qName + "'); END;",
		"BEGIN SYS.DBMS_AQADM.drop_queue('" + user + "." + qName + "'); END;",
		"BEGIN SYS.DBMS_AQADM.drop_queue_table('" + user + "." + qTblName + "'); END;",
		"DROP TABLE " + user + "." + qTblName,
		"DROP TYPE " + user + "." + qTypName + " FORCE",
		"DROP TYPE " + user + "." + arrTypName + " FORCE",
		"CREATE OR REPLACE TYPE " + user + "." + arrTypName + " IS TABLE OF VARCHAR2(1000)",
		"CREATE OR REPLACE TYPE " + user + "." + qTypName + " IS OBJECT (f_vc20 VARCHAR2(20), f_num NUMBER, f_dt DATE/*, f_arr " + arrTypName + "*/)",
	} {
		if _, err = conn.ExecContext(ctx, qry); err != nil {
			if strings.HasPrefix(qry, "CREATE ") || !strings.Contains(err.Error(), "not exist") {
				t.Log(errors.Errorf("%s: %w", qry, err))
			}
			if strings.Contains(err.Error(), "PLS-00201: identifier 'SYS.DBMS_AQADM' must be declared") {
				t.Skip(err)
			}
			continue
		}
		plus.WriteString(qry)
		plus.WriteString(";\n")
	}
	defer func() {
		testDb.Exec("DROP TYPE " + user + "." + qTypName)
		testDb.Exec("DROP TYPE " + user + "." + arrTypName)
	}()
	{
		qry := `DECLARE
		tbl CONSTANT VARCHAR2(61) := '` + user + "." + qTblName + `';
		q CONSTANT VARCHAR2(61) := '` + user + "." + qName + `';
		typ CONSTANT VARCHAR2(61) := '` + user + "." + qTypName + `';
	BEGIN
		BEGIN SYS.DBMS_AQADM.stop_queue(q); EXCEPTION WHEN OTHERS THEN NULL; END;
		BEGIN SYS.DBMS_AQADM.drop_queue(q); EXCEPTION WHEN OTHERS THEN NULL; END;
		BEGIN SYS.DBMS_AQADM.drop_queue_table(tbl); EXCEPTION WHEN OTHERS THEN NULL; END;

		SYS.DBMS_AQADM.CREATE_QUEUE_TABLE(tbl, typ);
		SYS.DBMS_AQADM.CREATE_QUEUE(q, tbl);

		SYS.DBMS_AQADM.grant_queue_privilege('ENQUEUE', q, '` + user + `');
		SYS.DBMS_AQADM.grant_queue_privilege('DEQUEUE', q, '` + user + `');
		SYS.DBMS_AQADM.start_queue(q);
	END;`
		if _, err = conn.ExecContext(ctx, qry); err != nil {
			t.Logf("%v", errors.Errorf("%s: %w", qry, err))
		}
	}
	defer func() {
		conn.ExecContext(
			testContext("QueueObject-drop"),
			`DECLARE
			tbl CONSTANT VARCHAR2(61) := USER||'.'||:1;
			q CONSTANT VARCHAR2(61) := USER||'.'||:2;
		BEGIN
			BEGIN SYS.DBMS_AQADM.stop_queue(q); EXCEPTION WHEN OTHERS THEN NULL; END;
			BEGIN SYS.DBMS_AQADM.drop_queue(q); EXCEPTION WHEN OTHERS THEN NULL; END;
			BEGIN SYS.DBMS_AQADM.drop_queue_table(tbl); EXCEPTION WHEN OTHERS THEN NULL;
		END;`,
			qTblName, qName,
		)
	}()

	q, err := godror.NewQueue(ctx, conn, qName, qTypName)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	t.Log("name:", q.Name())
	enqOpts, err := q.EnqOptions()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("enqOpts: %#v", enqOpts)
	deqOpts, err := q.DeqOptions()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("deqOpts: %#v", deqOpts)

	oTyp, err := godror.GetObjectType(ctx, conn, qTypName)
	if err != nil {
		t.Fatal(err)
	}
	obj, err := oTyp.NewObject()
	if err != nil {
		t.Fatal(err)
	}
	defer obj.Close()
	if err = obj.Set("F_DT", time.Now()); err != nil {
		t.Error(err)
	}
	if err = obj.Set("F_VC20", "árvíztűrő"); err != nil {
		t.Error(err)
	}
	if err = obj.Set("F_NUM", 3.14); err != nil {
		t.Error(err)
	}
	t.Log("obj:", obj)
	if err = q.Enqueue([]godror.Message{godror.Message{Object: obj}}); err != nil {
		var ec interface{ Code() int }
		if errors.As(err, &ec) && ec.Code() == 24444 {
			t.Skip(err)
		}
		t.Fatal("enqueue:", err)
	}
	msgs := make([]godror.Message, 1)
	n, err := q.Dequeue(msgs)
	if err != nil {
		t.Error("dequeue:", err)
	}
	t.Logf("received %d messages", n)
	for _, m := range msgs[:n] {
		t.Logf("got: %#v (%q)", m, string(m.Raw))
	}
}
