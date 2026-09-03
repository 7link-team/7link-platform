package main

import (
	"strings"
	"testing"
)

func TestStripSQLCommentsPreservesQuotedMarkers(t *testing.T) {
	contents := "-- source\nSELECT '--value', \"/*value*/\", `#name`; # tail\n/* block\ncomment */\nSELECT 1;"
	stripped, err := stripSQLComments(contents)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"'--value'", "\"/*value*/\"", "`#name`"} {
		if !strings.Contains(stripped, value) {
			t.Errorf("stripped SQL does not contain quoted marker %q", value)
		}
	}
	for _, comment := range []string{"source", "tail", "comment"} {
		if strings.Contains(stripped, comment) {
			t.Errorf("stripped SQL still contains comment %q", comment)
		}
	}
}

func TestVerifyLockAllowsOnlyHigherVersionAdditions(t *testing.T) {
	base := migrationFile{path: "core/sql/mysql/00001_base.sql", hash: strings.Repeat("a", 64)}
	lock := schemaLock{Version: lockVersion, Entries: []lockEntry{{Path: base.path, SHA256: base.hash}}}

	if err := verifyLock(lock, []migrationFile{
		base,
		{path: "core/sql/mysql/00002_append.sql", hash: strings.Repeat("b", 64)},
	}); err != nil {
		t.Fatalf("higher-version append rejected: %v", err)
	}
	assertErrorContains(t, verifyLock(lock, []migrationFile{
		base,
		{path: "core/sql/mysql/00000_inserted.sql", hash: strings.Repeat("b", 64)},
	}), "inserted at version")
	modified := base
	modified.hash = strings.Repeat("c", 64)
	assertErrorContains(t, verifyLock(lock, []migrationFile{modified}), "published migration was modified")
	assertErrorContains(t, verifyLock(schemaLock{Version: lockVersion}, []migrationFile{base}), "no published migrations")
	invalid := lock
	invalid.Entries = append([]lockEntry(nil), lock.Entries...)
	invalid.Entries[0].SHA256 = "not-a-digest"
	assertErrorContains(t, verifyLock(invalid, []migrationFile{base}), "invalid SHA-256")
}

func assertErrorContains(t *testing.T, err error, fragment string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), fragment) {
		t.Fatalf("error %v does not contain %q", err, fragment)
	}
}
