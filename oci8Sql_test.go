package oci8

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log"
	"os"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"
)

// testGetDB connects to the test database and returns the database connection
func testGetDB(params string) *sql.DB {
	OCI8Driver.Logger = log.New(os.Stderr, "oci8 ", log.Ldate|log.Ltime|log.LUTC|log.Llongfile)

	os.Setenv("NLS_LANG", "American_America.AL32UTF8")

	var openString string
	// [username/[password]@]host[:port][/instance_name][?param1=value1&...&paramN=valueN]
	if len(TestUsername) > 0 {
		if len(TestPassword) > 0 {
			openString = TestUsername + "/" + TestPassword + "@"
		} else {
			openString = TestUsername + "@"
		}
	}
	openString += TestHostValid + params

	db, err := sql.Open("oci8", openString)
	if err != nil {
		fmt.Println("open error:", err)
		return nil
	}
	if db == nil {
		fmt.Println("db is nil")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), TestContextTimeout)
	err = db.PingContext(ctx)
	cancel()
	if err != nil {
		fmt.Println("ping error:", err)
		return nil
	}

	return db
}

func testDropTable(t *testing.T, tableName string) {
	err := testExec(t, "drop table "+tableName, nil)
	if err != nil {
		t.Errorf("drop table %v error: %v", tableName, err)
	}
}

func testExecQuery(t *testing.T, query string, args []interface{}) {
	err := testExec(t, query, args)
	if err != nil {
		t.Errorf("query %v error: %v", query, err)
	}
}

// testGetRows runs a statment and returns the rows as [][]interface{}
func testGetRows(t *testing.T, stmt *sql.Stmt, args []interface{}) ([][]interface{}, error) {
	// get rows
	ctx, cancel := context.WithTimeout(context.Background(), TestContextTimeout)
	defer cancel()
	var rows *sql.Rows
	rows, err := stmt.QueryContext(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("query error: %v", err)
	}

	// get column infomration
	var columns []string
	columns, err = rows.Columns()
	if err != nil {
		rows.Close()
		return nil, fmt.Errorf("columns error: %v", err)
	}

	// create values
	values := make([][]interface{}, 0, 1)

	// get values
	pRowInterface := make([]interface{}, len(columns))

	for rows.Next() {
		rowInterface := make([]interface{}, len(columns))
		for i := 0; i < len(rowInterface); i++ {
			pRowInterface[i] = &rowInterface[i]
		}

		err = rows.Err()
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("rows error: %v", err)
		}

		err = rows.Scan(pRowInterface...)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan error: %v", err)
		}

		values = append(values, rowInterface)
	}

	err = rows.Err()
	if err != nil {
		rows.Close()
		return nil, fmt.Errorf("rows error: %v", err)
	}

	err = rows.Close()
	if err != nil {
		return nil, fmt.Errorf("close error: %v", err)
	}

	// return values
	return values, nil
}

// testExec runs an exec query and returns error
func testExec(t *testing.T, query string, args []interface{}) error {
	ctx, cancel := context.WithTimeout(context.Background(), TestContextTimeout)
	stmt, err := TestDB.PrepareContext(ctx, query)
	cancel()
	if err != nil {
		return fmt.Errorf("prepare error: %v", err)
	}

	ctx, cancel = context.WithTimeout(context.Background(), TestContextTimeout)
	_, err = stmt.ExecContext(ctx, args...)
	cancel()
	if err != nil {
		stmt.Close()
		return fmt.Errorf("exec error: %v", err)
	}

	err = stmt.Close()
	if err != nil {
		return fmt.Errorf("stmt close error: %v", err)
	}

	return nil
}

// testExecRows runs exec query for each arg row and returns error
func testExecRows(t *testing.T, query string, args [][]interface{}) error {
	ctx, cancel := context.WithTimeout(context.Background(), TestContextTimeout)
	stmt, err := TestDB.PrepareContext(ctx, query)
	cancel()
	if err != nil {
		return fmt.Errorf("prepare error: %v", err)
	}

	for i := 0; i < len(args); i++ {
		ctx, cancel = context.WithTimeout(context.Background(), TestContextTimeout)
		_, err = stmt.ExecContext(ctx, args[i]...)
		cancel()
		if err != nil {
			stmt.Close()
			return fmt.Errorf("exec - row %v - error: %v", i, err)
		}
	}

	err = stmt.Close()
	if err != nil {
		return fmt.Errorf("stmt close error: %v", err)
	}

	return nil
}

// testRunExecResults runs testRunExecResult for each execResults
func testRunExecResults(t *testing.T, execResults testExecResults) {
	ctx, cancel := context.WithTimeout(context.Background(), TestContextTimeout)
	stmt, err := TestDB.PrepareContext(ctx, execResults.query)
	cancel()
	if err != nil {
		t.Errorf("prepare error: %v - query: %v", err, execResults.query)
		return
	}

	for _, execResult := range execResults.execResults {
		testRunExecResult(t, execResult, execResults.query, stmt)
	}

	err = stmt.Close()
	if err != nil {
		t.Errorf("close error: %v - query: %v", err, execResults.query)
	}
}

// testRunExecResult runs exec query for execResult and tests result
func testRunExecResult(t *testing.T, execResult testExecResult, query string, stmt *sql.Stmt) {
	var rv reflect.Value
	results := make(map[string]interface{}, len(execResult.args))

	// change args to namedArgs
	namedArgs := make([]interface{}, 0, len(execResult.args))
	for key, value := range execResult.args {
		// make pointer
		rv = reflect.ValueOf(value.Dest)
		out := reflect.New(rv.Type())
		if !out.Elem().CanSet() {
			t.Fatalf("unable to set pointer: %v - query: %v", key, query)
			return
		}
		out.Elem().Set(rv)
		results[key] = out.Interface()

		namedArgs = append(namedArgs, sql.Named(key, sql.Out{Dest: out.Interface(), In: value.In}))
	}

	// exec query with namedArgs
	ctx, cancel := context.WithTimeout(context.Background(), TestContextTimeout)
	_, err := stmt.ExecContext(ctx, namedArgs...)
	cancel()
	if err != nil {
		t.Errorf("exec error: %v - query: %v - args: %v", err, query, execResult.args)
		return
	}

	// check results
	for key, value := range execResult.results {
		// check if have result
		result, ok := results[key]
		if !ok {
			t.Errorf("result not found: %v - query: %v", key, query)
			continue
		}

		// get result from result pointer
		rv = reflect.ValueOf(result)
		rv = reflect.Indirect(rv)
		result = rv.Interface()

		// check if value matches result
		if !reflect.DeepEqual(result, value) {
			t.Errorf("arg: %v - received: %T, %v - expected: %T, %v - query: %v",
				key, result, result, value, value, query)
		}
	}
}

// testRunQueryResults runs testRunQueryResult for each queryResults
func testRunQueryResults(t *testing.T, queryResults testQueryResults) {
	ctx, cancel := context.WithTimeout(context.Background(), TestContextTimeout)
	stmt, err := TestDB.PrepareContext(ctx, queryResults.query)
	cancel()
	if err != nil {
		t.Errorf("prepare error: %v - query: %v", err, queryResults.query)
		return
	}

	for _, queryResult := range queryResults.queryResults {
		testRunQueryResult(t, queryResult, queryResults.query, stmt)
	}

	err = stmt.Close()
	if err != nil {
		t.Errorf("close error: %v - query: %v", err, queryResults.query)
	}
}

// testRunQueryResult runs a single testQueryResults test
func testRunQueryResult(t *testing.T, queryResult testQueryResult, query string, stmt *sql.Stmt) {

	result, err := testGetRows(t, stmt, queryResult.args)
	if err != nil {
		t.Errorf("get rows error: %v - query: %v", err, query)
		return
	}
	if result == nil && queryResult.results != nil {
		t.Errorf("result is nil - query: %v", query)
		return
	}
	if len(result) != len(queryResult.results) {
		t.Errorf("result rows len %v not equal to results len %v - query: %v",
			len(result), len(queryResult.results), query)
		return
	}

	for i := 0; i < len(result); i++ {
		if len(result[i]) != len(queryResult.results[i]) {
			t.Errorf("result columns len %v not equal to results len %v - query: %v",
				len(result[i]), len(queryResult.results[i]), query)
			continue
		}

		for j := 0; j < len(result[i]); j++ {
			bad := false
			type1 := reflect.TypeOf(result[i][j])
			type2 := reflect.TypeOf(queryResult.results[i][j])
			switch {
			case type1 == nil || type2 == nil:
				if type1 != type2 {
					bad = true
				}
			case type1 == TestTypeTime || type2 == TestTypeTime:
				if type1 != type2 {
					bad = true
					break
				}
				time1 := result[i][j].(time.Time)
				time2 := queryResult.results[i][j].(time.Time)
				if !time1.Equal(time2) {
					bad = true
				}
			case type1.Kind() == reflect.Slice || type2.Kind() == reflect.Slice:
				if !reflect.DeepEqual(result[i][j], queryResult.results[i][j]) {
					bad = true
				}
			default:
				if result[i][j] != queryResult.results[i][j] {
					bad = true
				}
			}
			if bad {
				t.Errorf("result - row %v, %v - received: %T, %v - expected: %T, %v - query: %v", i, j,
					result[i][j], result[i][j], queryResult.results[i][j], queryResult.results[i][j], query)
			}
		}

	}

}

// TestConnect checks basic invalid connection
func TestConnect(t *testing.T) {
	if TestDisableDatabase {
		t.SkipNow()
	}

	// invalid
	db, err := sql.Open("oci8", TestHostInvalid)
	if err != nil {
		t.Fatal("open error:", err)
	}
	if db == nil {
		t.Fatal("db is nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	err = db.PingContext(ctx)
	cancel()
	if err == nil || err != driver.ErrBadConn {
		t.Fatalf("ping error - received: %v - expected: %v", err, driver.ErrBadConn)
	}

	err = db.Close()
	if err != nil {
		t.Fatal("close error:", err)
	}

	// wrong username
	db, err = sql.Open("oci8", "dFQXYoApiU2YbquMQnfPyqxR2kAoeuWngDvtTpl3@"+TestHostValid)
	if err != nil {
		t.Fatal("open error:", err)
	}
	if db == nil {
		t.Fatal("db is nil")
	}

	ctx, cancel = context.WithTimeout(context.Background(), TestContextTimeout)
	err = db.PingContext(ctx)
	cancel()
	if err == nil || err != driver.ErrBadConn {
		t.Fatalf("ping error - received: %v - expected: %v", err, driver.ErrBadConn)
	}

	err = db.Close()
	if err != nil {
		t.Fatal("close error:", err)
	}
}

// TestSelectParallel checks parallel select from dual
func TestSelectParallel(t *testing.T) {
	if TestDisableDatabase {
		t.SkipNow()
	}

	ctx, cancel := context.WithTimeout(context.Background(), TestContextTimeout)
	stmt, err := TestDB.PrepareContext(ctx, "select :1 from dual")
	cancel()
	if err != nil {
		t.Fatal("prepare error:", err)
	}

	var waitGroup sync.WaitGroup
	waitGroup.Add(100)

	for i := 0; i < 100; i++ {
		go func(num int) {
			defer waitGroup.Done()
			var result [][]interface{}
			result, err = testGetRows(t, stmt, []interface{}{num})
			if err != nil {
				t.Fatal("get rows error:", err)
			}
			if result == nil {
				t.Fatal("result is nil")
			}
			if len(result) != 1 {
				t.Fatal("len result not equal to 1")
			}
			if len(result[0]) != 1 {
				t.Fatal("len result[0] not equal to 1")
			}
			data, ok := result[0][0].(float64)
			if !ok {
				t.Fatal("result not float64")
			}
			if data != float64(num) {
				t.Fatal("result not equal to:", num)
			}
		}(i)
	}

	waitGroup.Wait()

	err = stmt.Close()
	if err != nil {
		t.Fatal("stmt close error:", err)
	}
}

// TestContextTimeoutBreak checks that ExecContext timeout works
func TestContextTimeoutBreak(t *testing.T) {
	if TestDisableDatabase {
		t.SkipNow()
	}

	// exec
	ctx, cancel := context.WithTimeout(context.Background(), TestContextTimeout)
	stmt, err := TestDB.PrepareContext(ctx, "begin SYS.DBMS_LOCK.SLEEP(1); end;")
	cancel()
	if err != nil {
		t.Fatal("prepare error:", err)
	}

	ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
	_, err = stmt.ExecContext(ctx)
	cancel()
	expected := "ORA-01013"
	if err == nil || len(err.Error()) < len(expected) || err.Error()[:len(expected)] != expected {
		t.Fatalf("stmt exec - expected: %v - received: %v", expected, err)
	}

	err = stmt.Close()
	if err != nil {
		t.Fatal("stmt close error:", err)
	}

	// query
	ctx, cancel = context.WithTimeout(context.Background(), TestContextTimeout)
	stmt, err = TestDB.PrepareContext(ctx, "select SLEEP_SECONDS(1) from dual")
	cancel()
	if err != nil {
		t.Fatal("prepare error:", err)
	}

	ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
	_, err = stmt.QueryContext(ctx)
	cancel()
	if err == nil || len(err.Error()) < len(expected) || err.Error()[:len(expected)] != expected {
		t.Fatalf("stmt query - expected: %v - received: %v", expected, err)
	}

	err = stmt.Close()
	if err != nil {
		t.Fatal("stmt close error:", err)
	}
}

// TestDestructiveTransaction tests a transaction
func TestDestructiveTransaction(t *testing.T) {
	if TestDisableDatabase || TestDisableDestructive {
		t.SkipNow()
	}

	err := testExec(t, "create table TRANSACTION_"+TestTimeString+
		" ( A INT, B INT, C INT )", nil)
	if err != nil {
		t.Fatal("create table error:", err)
	}

	defer testExecQuery(t, "drop table TRANSACTION_"+TestTimeString, nil)

	err = testExecRows(t, "insert into TRANSACTION_"+TestTimeString+" ( A, B, C ) values (:1, :2, :3)",
		[][]interface{}{
			[]interface{}{1, 2, 3},
			[]interface{}{4, 5, 6},
			[]interface{}{6, 7, 8},
		})
	if err != nil {
		t.Fatal("insert error:", err)
	}

	// TODO: How should context work? Probably should have more context create and cancel.

	var tx1 *sql.Tx
	var tx2 *sql.Tx
	ctx, cancel := context.WithTimeout(context.Background(), 2*TestContextTimeout)
	defer cancel()
	tx1, err = TestDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("begin tx error:", err)
	}
	tx2, err = TestDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("begin tx error:", err)
	}

	queryResults := testQueryResults{
		query: "select A, B, C from TRANSACTION_" + TestTimeString + " order by A",
		queryResults: []testQueryResult{
			testQueryResult{
				results: [][]interface{}{
					[]interface{}{int64(1), int64(2), int64(3)},
					[]interface{}{int64(4), int64(5), int64(6)},
					[]interface{}{int64(6), int64(7), int64(8)},
				},
			},
		},
	}
	testRunQueryResults(t, queryResults)

	var result sql.Result
	result, err = tx1.ExecContext(ctx, "update TRANSACTION_"+TestTimeString+" set B = :1 where A = :2", []interface{}{22, 1}...)
	if err != nil {
		t.Fatal("exec error:", err)
	}

	var count int64
	count, err = result.RowsAffected()
	if err != nil {
		t.Fatal("rows affected error:", err)
	}
	if count != 1 {
		t.Fatalf("rows affected %v not equal to 1", count)
	}

	result, err = tx2.ExecContext(ctx, "update TRANSACTION_"+TestTimeString+" set B = :1 where A = :2", []interface{}{55, 4}...)
	if err != nil {
		t.Fatal("exec error:", err)
	}

	count, err = result.RowsAffected()
	if err != nil {
		t.Fatal("rows affected error:", err)
	}
	if count != 1 {
		t.Fatalf("rows affected %v not equal to 1", count)
	}

	queryResults = testQueryResults{
		query: "select A, B, C from TRANSACTION_" + TestTimeString + " order by A",
		queryResults: []testQueryResult{
			testQueryResult{
				results: [][]interface{}{
					[]interface{}{int64(1), int64(2), int64(3)},
					[]interface{}{int64(4), int64(5), int64(6)},
					[]interface{}{int64(6), int64(7), int64(8)},
				},
			},
		},
	}
	testRunQueryResults(t, queryResults)

	// tx1 with rows A = 1
	var stmt *sql.Stmt
	stmt, err = tx1.PrepareContext(ctx, "select A, B, C from TRANSACTION_"+TestTimeString+" where A = :1")
	if err != nil {
		t.Fatal("prepare error:", err)
	}
	var rows [][]interface{}
	rows, err = testGetRows(t, stmt, []interface{}{1})
	if result == nil {
		t.Fatal("rows is nil")
	}
	if len(rows) != 1 {
		t.Fatal("len rows not equal to 1")
	}
	if len(rows[0]) != 3 {
		t.Fatal("len rows[0] not equal to 3")
	}
	data, ok := rows[0][0].(int64)
	if !ok {
		t.Fatal("rows not int64")
	}
	expected := int64(1)
	if data != expected {
		t.Fatal("rows not equal to:", expected)
	}
	data, ok = rows[0][1].(int64)
	if !ok {
		t.Fatal("rows not int64")
	}
	expected = int64(22)
	if data != expected {
		t.Fatal("rows not equal to:", expected)
	}
	data, ok = rows[0][2].(int64)
	if !ok {
		t.Fatal("rows not int64")
	}
	expected = int64(3)
	if data != expected {
		t.Fatal("rows not equal to:", expected)
	}

	// tx1 with rows A = 4
	rows, err = testGetRows(t, stmt, []interface{}{4})
	if rows == nil {
		t.Fatal("rows is nil")
	}
	if len(rows) != 1 {
		t.Fatal("len rows not equal to 1")
	}
	if len(rows[0]) != 3 {
		t.Fatal("len rows[0] not equal to 3")
	}
	data, ok = rows[0][0].(int64)
	if !ok {
		t.Fatal("rows not int64")
	}
	expected = int64(4)
	if data != expected {
		t.Fatal("rows not equal to:", expected)
	}
	data, ok = rows[0][1].(int64)
	if !ok {
		t.Fatal("rows not int64")
	}
	expected = int64(5)
	if data != expected {
		t.Fatal("rows not equal to:", expected)
	}
	data, ok = rows[0][2].(int64)
	if !ok {
		t.Fatal("rows not int64")
	}
	expected = int64(6)
	if data != expected {
		t.Fatal("rows not equal to:", expected)
	}

	// tx2 with rows A = 1
	stmt, err = tx2.PrepareContext(ctx, "select A, B, C from TRANSACTION_"+TestTimeString+" where A = :1")
	if err != nil {
		t.Fatal("prepare error:", err)
	}
	rows, err = testGetRows(t, stmt, []interface{}{1})
	if rows == nil {
		t.Fatal("rows is nil")
	}
	if len(rows) != 1 {
		t.Fatal("len rows not equal to 1")
	}
	if len(rows[0]) != 3 {
		t.Fatal("len rows[0] not equal to 3")
	}
	data, ok = rows[0][0].(int64)
	if !ok {
		t.Fatal("rows not int64")
	}
	expected = int64(1)
	if data != expected {
		t.Fatal("rows not equal to:", expected)
	}
	data, ok = rows[0][1].(int64)
	if !ok {
		t.Fatal("rows not int64")
	}
	expected = int64(2)
	if data != expected {
		t.Fatal("rows not equal to:", expected)
	}
	data, ok = rows[0][2].(int64)
	if !ok {
		t.Fatal("rows not int64")
	}
	expected = int64(3)
	if data != expected {
		t.Fatal("rows not equal to:", expected)
	}

	// tx2 with rows A = 4
	rows, err = testGetRows(t, stmt, []interface{}{4})
	if result == nil {
		t.Fatal("rows is nil")
	}
	if len(rows) != 1 {
		t.Fatal("len rows not equal to 1")
	}
	if len(rows[0]) != 3 {
		t.Fatal("len rows[0] not equal to 3")
	}
	data, ok = rows[0][0].(int64)
	if !ok {
		t.Fatal("rows not int64")
	}
	expected = int64(4)
	if data != expected {
		t.Fatal("rows not equal to:", expected)
	}
	data, ok = rows[0][1].(int64)
	if !ok {
		t.Fatal("rows not int64")
	}
	expected = int64(55)
	if data != expected {
		t.Fatal("rows not equal to:", expected)
	}
	data, ok = rows[0][2].(int64)
	if !ok {
		t.Fatal("rows not int64")
	}
	expected = int64(6)
	if data != expected {
		t.Fatal("rows not equal to:", expected)
	}

	err = tx1.Commit()
	if err != nil {
		t.Fatal("commit err:", err)
	}
	err = tx2.Commit()
	if err != nil {
		t.Fatal("commit err:", err)
	}

	queryResults = testQueryResults{
		query: "select A, B, C from TRANSACTION_" + TestTimeString + " order by A",
		queryResults: []testQueryResult{
			testQueryResult{
				results: [][]interface{}{
					[]interface{}{int64(1), int64(22), int64(3)},
					[]interface{}{int64(4), int64(55), int64(6)},
					[]interface{}{int64(6), int64(7), int64(8)},
				},
			},
		},
	}
	testRunQueryResults(t, queryResults)
}

// TestSelectDualNull checks nulls
func TestSelectDualNull(t *testing.T) {
	if TestDisableDatabase {
		t.SkipNow()
	}

	queryResults := testQueryResults{
		query: "select null from dual",
		queryResults: []testQueryResult{testQueryResult{
			results: [][]interface{}{[]interface{}{nil}}}}}
	testRunQueryResults(t, queryResults)
}

func BenchmarkSimpleInsert(b *testing.B) {
	if TestDisableDatabase || TestDisableDestructive {
		b.SkipNow()
	}

	// SIMPLE_INSERT
	tableName := "SIMPLE_INSERT_" + TestTimeString
	query := "create table " + tableName + " ( A INTEGER )"

	// create table
	ctx, cancel := context.WithTimeout(context.Background(), TestContextTimeout)
	stmt, err := TestDB.PrepareContext(ctx, query)
	cancel()
	if err != nil {
		b.Fatal("prepare error:", err)
	}

	ctx, cancel = context.WithTimeout(context.Background(), TestContextTimeout)
	_, err = stmt.ExecContext(ctx)
	cancel()
	if err != nil {
		stmt.Close()
		b.Fatal("exec error:", err)
	}

	err = stmt.Close()
	if err != nil {
		b.Fatal("stmt close error:", err)
	}

	// drop table
	defer func() {
		query := "drop table " + tableName
		ctx, cancel := context.WithTimeout(context.Background(), TestContextTimeout)
		stmt, err := TestDB.PrepareContext(ctx, query)
		cancel()
		if err != nil {
			b.Fatal("prepare error:", err)
		}

		ctx, cancel = context.WithTimeout(context.Background(), TestContextTimeout)
		_, err = stmt.ExecContext(ctx)
		cancel()
		if err != nil {
			stmt.Close()
			b.Fatal("exec error:", err)
		}

		err = stmt.Close()
		if err != nil {
			b.Fatal("stmt close error:", err)
		}
	}()

	// insert into table
	query = "insert into " + tableName + " ( A ) values (:1)"
	ctx, cancel = context.WithTimeout(context.Background(), TestContextTimeout)
	stmt, err = TestDB.PrepareContext(ctx, query)
	cancel()
	if err != nil {
		b.Fatal("prepare error:", err)
	}

	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		ctx, cancel = context.WithTimeout(context.Background(), TestContextTimeout)
		_, err = stmt.ExecContext(ctx, n)
		cancel()
		if err != nil {
			stmt.Close()
			b.Fatal("exec error:", err)
		}
	}

	err = stmt.Close()
	if err != nil {
		b.Fatal("stmt close error", err)
	}
}

func benchmarkSelectSetup(b *testing.B) {
	fmt.Println("benchmark select setup start")

	benchmarkSelectTableName = "BM_SELECT_" + TestTimeString

	// create table
	tableName := benchmarkSelectTableName
	query := "create table " + tableName + " ( A INTEGER )"
	ctx, cancel := context.WithTimeout(context.Background(), TestContextTimeout)
	stmt, err := TestDB.PrepareContext(ctx, query)
	cancel()
	if err != nil {
		b.Fatal("prepare error:", err)
	}

	ctx, cancel = context.WithTimeout(context.Background(), TestContextTimeout)
	_, err = stmt.ExecContext(ctx)
	cancel()
	if err != nil {
		stmt.Close()
		b.Fatal("exec error:", err)
	}

	// enable drop table in TestMain
	benchmarkSelectTableCreated = true

	err = stmt.Close()
	if err != nil {
		b.Fatal("stmt close error:", err)
	}

	// insert into table
	query = "insert into " + tableName + " ( A ) select :1 from dual union all select :2 from dual union all select :3 from dual union all select :4 from dual union all select :5 from dual union all select :6 from dual union all select :7 from dual union all select :8 from dual union all select :9 from dual union all select :10 from dual"
	ctx, cancel = context.WithTimeout(context.Background(), TestContextTimeout)
	stmt, err = TestDB.PrepareContext(ctx, query)
	cancel()
	if err != nil {
		b.Fatal("prepare error:", err)
	}

	for i := 0; i < 20000; i += 10 {
		ctx, cancel = context.WithTimeout(context.Background(), TestContextTimeout)
		_, err = stmt.ExecContext(ctx, i, i+1, i+2, i+3, i+4, i+5, i+6, i+7, i+8, i+9)
		cancel()
		if err != nil {
			stmt.Close()
			b.Fatal("exec error:", err)
		}
	}

	err = stmt.Close()
	if err != nil {
		b.Fatal("stmt close error", err)
	}

	// select from table to warm up database cache
	query = "select A from " + tableName
	ctx, cancel = context.WithTimeout(context.Background(), TestContextTimeout)
	stmt, err = TestDB.PrepareContext(ctx, query)
	cancel()
	if err != nil {
		b.Fatal("prepare error:", err)
	}

	defer func() {
		err = stmt.Close()
		if err != nil {
			b.Fatal("stmt close error", err)
		}
	}()

	var rows *sql.Rows
	ctx, cancel = context.WithTimeout(context.Background(), 20*TestContextTimeout)
	defer cancel()
	rows, err = stmt.QueryContext(ctx)
	if err != nil {
		b.Fatal("exec error:", err)
	}

	defer func() {
		err = rows.Close()
		if err != nil {
			b.Fatal("row close error:", err)
		}
	}()

	var data int64
	for rows.Next() {
		err = rows.Scan(&data)
		if err != nil {
			b.Fatal("scan error:", err)
		}
	}

	err = rows.Err()
	if err != nil {
		b.Fatal("err error:", err)
	}

	fmt.Println("benchmark select setup end")
}

func benchmarkPrefetchSelect(b *testing.B, prefetchRows int64, prefetchMemory int64) {
	b.StopTimer()

	benchmarkSelectTableOnce.Do(func() { benchmarkSelectSetup(b) })

	var err error

	db := testGetDB("?prefetch_rows=" + strconv.FormatInt(prefetchRows, 10) + "&prefetch_memory=" + strconv.FormatInt(prefetchMemory, 10))
	if db == nil {
		b.Fatal("db is null")
	}

	defer func() {
		err = db.Close()
		if err != nil {
			b.Fatal("db close error:", err)
		}
	}()

	var stmt *sql.Stmt
	tableName := benchmarkSelectTableName
	query := "select A from " + tableName
	ctx, cancel := context.WithTimeout(context.Background(), TestContextTimeout)
	stmt, err = db.PrepareContext(ctx, query)
	cancel()
	if err != nil {
		b.Fatal("prepare error:", err)
	}

	defer func() {
		err = stmt.Close()
		if err != nil {
			b.Fatal("stmt close error", err)
		}
	}()

	b.StartTimer()

	var rows *sql.Rows
	ctx, cancel = context.WithTimeout(context.Background(), 20*TestContextTimeout)
	defer cancel()
	rows, err = stmt.QueryContext(ctx)
	if err != nil {
		b.Fatal("exec error:", err)
	}

	defer func() {
		err = rows.Close()
		if err != nil {
			b.Fatal("row close error:", err)
		}
	}()

	var data int64
	for rows.Next() {
		err = rows.Scan(&data)
		if err != nil {
			b.Fatal("scan error:", err)
		}
	}

	b.StopTimer()

	err = rows.Err()
	if err != nil {
		b.Fatal("err error:", err)
	}
}

func BenchmarkPrefetchR1000M32768(b *testing.B) {
	if TestDisableDatabase || TestDisableDestructive {
		b.SkipNow()
	}

	benchmarkPrefetchSelect(b, 1000, 32768)
}

func BenchmarkPrefetchR1000M16384(b *testing.B) {
	if TestDisableDatabase || TestDisableDestructive {
		b.SkipNow()
	}

	benchmarkPrefetchSelect(b, 1000, 16384)
}

func BenchmarkPrefetchR1000M8192(b *testing.B) {
	if TestDisableDatabase || TestDisableDestructive {
		b.SkipNow()
	}

	benchmarkPrefetchSelect(b, 1000, 8192)
}

func BenchmarkPrefetchR1000M4096(b *testing.B) {
	if TestDisableDatabase || TestDisableDestructive {
		b.SkipNow()
	}

	benchmarkPrefetchSelect(b, 1000, 4096)
}

func BenchmarkPrefetchR1000M2048(b *testing.B) {
	if TestDisableDatabase || TestDisableDestructive {
		b.SkipNow()
	}

	benchmarkPrefetchSelect(b, 1000, 2048)
}
