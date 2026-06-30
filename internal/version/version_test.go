package version

import "testing"

func TestString(t *testing.T) {
	originalVersion := Version
	originalCommit := Commit
	originalDate := Date
	t.Cleanup(func() {
		Version = originalVersion
		Commit = originalCommit
		Date = originalDate
	})

	Version = "1.2.3"
	Commit = "abc123"
	Date = "2026-06-30"

	got := String()
	want := "ip-notify 1.2.3 (commit abc123, built 2026-06-30)"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
