package toolhub

import "testing"

func TestApplyPatchFile(t *testing.T) {
	files, err := parseUnifiedPatch(`--- a/example.txt
+++ b/example.txt
@@ -1,3 +1,3 @@
 alpha
-beta
+bravo
 gamma`)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	next, err := applyPatchFile("alpha\nbeta\ngamma", files[0])
	if err != nil {
		t.Fatal(err)
	}
	if next != "alpha\nbravo\ngamma" {
		t.Fatalf("unexpected patch result: %q", next)
	}
}

func TestApplyPatchFileRejectsContextMismatch(t *testing.T) {
	files, err := parseUnifiedPatch(`--- a/example.txt
+++ b/example.txt
@@ -1,3 +1,3 @@
 alpha
-beta
+bravo
 gamma`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := applyPatchFile("alpha\nwrong\ngamma", files[0]); err == nil {
		t.Fatal("expected context mismatch")
	}
}

func TestBuildRollbackPatchRestoresOriginal(t *testing.T) {
	files, err := parseUnifiedPatch(`--- a/example.txt
+++ b/example.txt
@@ -1,3 +1,3 @@
 alpha
-beta
+bravo
 gamma`)
	if err != nil {
		t.Fatal(err)
	}
	next, err := applyPatchFile("alpha\nbeta\ngamma", files[0])
	if err != nil {
		t.Fatal(err)
	}
	rollback, err := parseUnifiedPatch(buildRollbackPatch(files))
	if err != nil {
		t.Fatal(err)
	}
	restored, err := applyPatchFile(next, rollback[0])
	if err != nil {
		t.Fatal(err)
	}
	if restored != "alpha\nbeta\ngamma" {
		t.Fatalf("rollback did not restore original: %q", restored)
	}
}
