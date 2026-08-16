package ws

import (
	"database/sql"
	"testing"

	"powercodedeck/services"

	_ "modernc.org/sqlite"
)

func TestNativeLaunchIdentityComesFromAgentRow(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE agents (
		id TEXT PRIMARY KEY,preset TEXT,name TEXT,tmux_session TEXT,working_dir TEXT,
		command TEXT,args TEXT,status TEXT,color_hue INTEGER,color_name TEXT,
		created_at TEXT,updated_at TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	_, err = db.Exec(`INSERT INTO agents VALUES('a1','codex-cli','n','pcd-a1',?,'codex','[]','stopped',220,'blue','','')`, cwd)
	if err != nil {
		t.Fatal(err)
	}
	h := &Hub{agentSvc: services.NewAgentService(db, &gateEngine{})}
	driver, gotCwd, err := h.nativeLaunchIdentity("a1")
	if err != nil {
		t.Fatal(err)
	}
	if driver != "codex" || gotCwd != cwd {
		t.Fatalf("identity did not come from durable row: driver=%q cwd=%q", driver, gotCwd)
	}
}
