package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
)

var connectDriverNumber atomic.Uint64

type connectDriver struct {
	err   error
	opens atomic.Int32
}

func (d *connectDriver) Open(string) (driver.Conn, error) {
	if d.opens.Add(1) == 1 {
		return nil, d.err
	}
	return connectConn{}, nil
}

type connectConn struct{}

func (connectConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (connectConn) Close() error                        { return nil }
func (connectConn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }

type codedConnectError int

func (e codedConnectError) Error() string { return "arbitrary diagnostic wording" }
func (e codedConnectError) Code() int     { return int(e) }

func TestConnectUsesErrorCodeRatherThanDiagnosticWords(t *testing.T) {
	for i, tc := range []struct {
		err   error
		retry bool
	}{
		{errors.New("SQLITE_BUSY is mentioned but this is another failure"), false},
		{fmt.Errorf("wrapped: %w", codedConnectError(5)), true},
		{codedConnectError(5 | 1<<8), true},
		{codedConnectError(6), false},
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			driverName := fmt.Sprintf("omo-connect-display-test-%d", connectDriverNumber.Add(1))
			drv := &connectDriver{err: tc.err}
			sql.Register(driverName, drv)
			d, err := sql.Open(driverName, "")
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			conn, err := connect(context.Background(), d)
			if conn != nil {
				defer conn.Close()
			}
			if tc.retry {
				if err != nil || drv.opens.Load() != 2 {
					t.Fatalf("busy connection: %v, opens %d", err, drv.opens.Load())
				}
			} else if err != tc.err || drv.opens.Load() != 1 {
				t.Fatalf("unrelated connection error: %v, opens %d", err, drv.opens.Load())
			}
		})
	}
}
