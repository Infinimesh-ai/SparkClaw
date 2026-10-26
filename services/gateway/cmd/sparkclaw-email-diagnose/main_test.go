package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestInvalidCLIInputsNeverEchoPrivateArguments(t *testing.T) {
	for _, args := range [][]string{{"--private-token=secret@example.test"}, {"--owner", "owner", "--draft", "one", "--job", "two", "--snapshot", "/private/source"}, {"--owner", "owner", "--operation", "op", "--snapshot", "/private/source"}, {"--postgres-env", "MISSING_PRIVATE_DATABASE_CONFIG"}} {
		var out, stderr bytes.Buffer
		if code := run(context.Background(), args, &out, &stderr); code != 2 || out.Len() != 0 {
			t.Fatalf("invalid query executed: %d %s", code, &out)
		}
		if strings.Contains(stderr.String(), "secret") || strings.Contains(stderr.String(), "/private/") {
			t.Fatalf("private arguments echoed: %s", &stderr)
		}
	}
}
