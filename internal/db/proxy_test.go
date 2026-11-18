package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestProxyMigrationFromVersionSeven(t *testing.T) {
	path := filepath.Join(t.TempDir(), "omo.db")
	d, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations[:7] {
		if _, err := d.Exec(migration); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Exec(`PRAGMA user_version=7; INSERT INTO server_state(key,value) VALUES('keep','yes')`); err != nil {
		t.Fatal(err)
	}
	d.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if userVersion(t, d) != SchemaVersion {
		t.Fatalf("schema %d want %d", userVersion(t, d), SchemaVersion)
	}
	var value string
	if err := d.QueryRow(`SELECT value FROM server_state WHERE key='keep'`).Scan(&value); err != nil || value != "yes" {
		t.Fatalf("lost data: %q %v", value, err)
	}
	if _, err := d.Exec(`INSERT INTO proxy_forwards(id,host,port,scheme,name,start_path,strip_prefix,verify_tls) VALUES('abc','::1',5173,'http','dev','/',0,0)`); err != nil {
		t.Fatal(err)
	}
}
