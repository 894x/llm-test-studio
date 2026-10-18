package sqlite

import "testing"

func TestContentCurrentSchemaObjectSet(t *testing.T) {
	if len(currentSchemaObjects) != 90 || len(contentSchemaObjects) != 24 {
		t.Fatalf("current objects=%d content objects=%d", len(currentSchemaObjects), len(contentSchemaObjects))
	}
	t.Logf("current schema: version=%d name=%s sha256=%s objects=%d", CurrentSchemaVersion,
		currentSchemaName, currentSchemaChecksum(), len(currentSchemaObjects))
}
