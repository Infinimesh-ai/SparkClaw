package agent

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestDocumentRoutePathsFindNonASCIINamesAndTrimLeadingWords(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "计算机网络.pdf"), []byte("%PDF-1.7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ text, want string }{
		{"计算机网络.pdf 里 TCP 的拥塞控制", "计算机网络.pdf"},
		{"根据 计算机网络.pdf，总结一下", "计算机网络.pdf"},
		{"根据计算机网络.pdf总结", "计算机网络.pdf"},
		{"总结 uploads/实验4报告.docx 的结论", "uploads/实验4报告.docx"},
		{"Summarize book.md", "book.md"},
	} {
		paths := documentRoutePaths(test.text)
		resolved := []string{}
		for _, path := range paths {
			resolved = append(resolved, workspaceDocumentPath(root, path))
		}
		if !slices.Contains(resolved, test.want) {
			t.Errorf("%q resolved to %q, want %q", test.text, resolved, test.want)
		}
	}
	if got := workspaceDocumentPath(root, "不存在的文件.pdf"); got != "不存在的文件.pdf" {
		t.Fatalf("a missing non-ASCII name was rewritten to %q", got)
	}
}
