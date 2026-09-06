package tokendb

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAndList(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := Open(dbPath, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	tokens, err := db.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tokens) != 0 {
		t.Fatalf("expected 0 tokens, got %d", len(tokens))
	}
}

func TestAddAndList(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "test.db"), nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	db.Add("tok-aaa")
	db.Add("tok-bbb")
	tokens, _ := db.List()
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(tokens))
	}
	if tokens[0] != "tok-aaa" || tokens[1] != "tok-bbb" {
		t.Fatalf("unexpected tokens: %v", tokens)
	}
}

func TestAddDuplicate(t *testing.T) {
	dir := t.TempDir()
	db, _ := Open(filepath.Join(dir, "test.db"), nil)
	defer db.Close()
	db.Add("tok-dup")
	db.Add("tok-dup") // IGNORE
	n, _ := db.Count()
	if n != 1 {
		t.Fatalf("expected 1 token, got %d", n)
	}
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	db, _ := Open(filepath.Join(dir, "test.db"), nil)
	defer db.Close()
	db.Add("tok-1")
	db.Add("tok-2")
	n, err := db.Remove("tok-1")
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 row affected, got %d", n)
	}
	tokens, _ := db.List()
	if len(tokens) != 1 || tokens[0] != "tok-2" {
		t.Fatalf("unexpected tokens after remove: %v", tokens)
	}
}

func TestRemoveLast(t *testing.T) {
	dir := t.TempDir()
	db, _ := Open(filepath.Join(dir, "test.db"), nil)
	defer db.Close()
	db.Add("tok-a")
	db.Add("tok-b")
	removed, _ := db.RemoveLast()
	if removed != "tok-b" {
		t.Fatalf("expected tok-b, got %q", removed)
	}
}

func TestRemoveLastEmpty(t *testing.T) {
	dir := t.TempDir()
	db, _ := Open(filepath.Join(dir, "test.db"), nil)
	defer db.Close()
	removed, _ := db.RemoveLast()
	if removed != "" {
		t.Fatalf("expected empty, got %q", removed)
	}
}

func TestRemoveAll(t *testing.T) {
	dir := t.TempDir()
	db, _ := Open(filepath.Join(dir, "test.db"), nil)
	defer db.Close()
	db.Add("tok-1")
	db.Add("tok-2")
	db.RemoveAll()
	n, _ := db.Count()
	if n != 0 {
		t.Fatalf("expected 0 after RemoveAll, got %d", n)
	}
}

func TestMigrateFromEnv(t *testing.T) {
	dir := t.TempDir()
	db, _ := Open(filepath.Join(dir, "test.db"), nil)
	defer db.Close()
	added, err := db.MigrateFromEnv("tok-x,tok-y,tok-z")
	if err != nil {
		t.Fatalf("MigrateFromEnv: %v", err)
	}
	if added != 3 {
		t.Fatalf("expected 3 added, got %d", added)
	}
	// Second run deduplicates.
	added, _ = db.MigrateFromEnv("tok-x,tok-w")
	if added != 1 {
		t.Fatalf("expected 1 new, got %d", added)
	}
	n, _ := db.Count()
	if n != 4 {
		t.Fatalf("expected 4, got %d", n)
	}
}

func TestMigrateFromEnvEmpty(t *testing.T) {
	dir := t.TempDir()
	db, _ := Open(filepath.Join(dir, "test.db"), nil)
	defer db.Close()
	added, _ := db.MigrateFromEnv("")
	if added != 0 {
		t.Fatalf("expected 0, got %d", added)
	}
}

func TestOpenCreatesDir(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sub", "deep", "test.db")
	db, err := Open(dbPath, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("db file not created: %v", err)
	}
}

func TestAddEmptyToken(t *testing.T) {
	dir := t.TempDir()
	db, _ := Open(filepath.Join(dir, "test.db"), nil)
	defer db.Close()
	if _, err := db.Add(""); err == nil {
		t.Fatal("expected error for empty token")
	}
	if _, err := db.Add("   "); err == nil {
		t.Fatal("expected error for whitespace token")
	}
}

func TestRemoveNonexistent(t *testing.T) {
	dir := t.TempDir()
	db, _ := Open(filepath.Join(dir, "test.db"), nil)
	defer db.Close()
	n, _ := db.Remove("no-such")
	if n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}
}

func TestCount(t *testing.T) {
	dir := t.TempDir()
	db, _ := Open(filepath.Join(dir, "test.db"), nil)
	defer db.Close()
	db.Add("a")
	db.Add("b")
	db.Add("c")
	n, _ := db.Count()
	if n != 3 {
		t.Fatalf("expected 3, got %d", n)
	}
}

func TestTokensSorted(t *testing.T) {
	dir := t.TempDir()
	db, _ := Open(filepath.Join(dir, "test.db"), nil)
	defer db.Close()
	db.Add("tok-c")
	db.Add("tok-a")
	db.Add("tok-b")
	tokens := db.Tokens()
	if len(tokens) != 3 {
		t.Fatalf("expected 3, got %d", len(tokens))
	}
	if tokens[0] != "tok-a" || tokens[1] != "tok-b" || tokens[2] != "tok-c" {
		t.Fatalf("expected sorted, got %v", tokens)
	}
}
func TestSaveLoadTokenState(t *testing.T) {
	dir := t.TempDir()
	db, _ := Open(filepath.Join(dir, "test.db"), nil)
	defer db.Close()
	db.Add("tok-a")
	db.Add("tok-b")

	if err := db.SaveTokenState("tok-a", []byte(`{"Locked":true}`)); err != nil {
		t.Fatalf("SaveTokenState: %v", err)
	}
	if err := db.SaveTokenState("tok-b", []byte(`{"Quarantined":true,"QuarantineReason":"banned"}`)); err != nil {
		t.Fatalf("SaveTokenState: %v", err)
	}
	states, err := db.LoadTokenStates()
	if err != nil {
		t.Fatalf("LoadTokenStates: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("expected 2 states, got %d: %v", len(states), states)
	}
	if string(states["tok-a"]) != `{"Locked":true}` {
		t.Errorf("tok-a state = %q", states["tok-a"])
	}

	// Upsert replaces the previous blob for the same token.
	if err := db.SaveTokenState("tok-a", []byte(`{"Locked":false}`)); err != nil {
		t.Fatal(err)
	}
	states, _ = db.LoadTokenStates()
	if string(states["tok-a"]) != `{"Locked":false}` {
		t.Errorf("after upsert tok-a state = %q, want {Locked:false}", states["tok-a"])
	}
	if len(states) != 2 {
		t.Errorf("upsert must not add a row, got %d states", len(states))
	}
}

func TestLoadTokenStatesIgnoresOrphans(t *testing.T) {
	dir := t.TempDir()
	db, _ := Open(filepath.Join(dir, "test.db"), nil)
	defer db.Close()

	// State saved for a token that is NOT in auth_tokens must stay invisible.
	if err := db.SaveTokenState("ghost", []byte(`{"Locked":true}`)); err != nil {
		t.Fatal(err)
	}
	states, err := db.LoadTokenStates()
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 0 {
		t.Fatalf("orphan state surfaced: %v", states)
	}

	// Once the token exists, its state becomes visible (re-added account
	// keeps its terminal state — the anti-ban contract).
	db.Add("ghost")
	states, _ = db.LoadTokenStates()
	if len(states) != 1 || string(states["ghost"]) != `{"Locked":true}` {
		t.Errorf("after Add, states = %v, want the ghost state", states)
	}
}

func TestClearTokenState(t *testing.T) {
	dir := t.TempDir()
	db, _ := Open(filepath.Join(dir, "test.db"), nil)
	defer db.Close()
	db.Add("tok-a")
	if err := db.SaveTokenState("tok-a", []byte(`{"Locked":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := db.ClearTokenState("tok-a"); err != nil {
		t.Fatalf("ClearTokenState: %v", err)
	}
	states, _ := db.LoadTokenStates()
	if len(states) != 0 {
		t.Errorf("states after clear = %v, want empty", states)
	}
	// Clearing an absent token is a no-op.
	if err := db.ClearTokenState("no-such"); err != nil {
		t.Errorf("ClearTokenState(absent) = %v, want nil", err)
	}
}
