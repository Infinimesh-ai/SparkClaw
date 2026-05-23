package sandbox

import "testing"

func TestLocalDockerRunnerMapsContainerWorkspaceToHostMount(t *testing.T) {
	runner := LocalDockerRunner{
		HostWorkspaceRoot:      "/Users/owner/sparkclaw/data/workspaces",
		ContainerWorkspaceRoot: "/var/lib/sparkclaw/workspaces",
	}

	if got := runner.workspaceRootForDocker("/var/lib/sparkclaw/workspaces"); got != "/Users/owner/sparkclaw/data/workspaces" {
		t.Fatalf("root mapping = %q", got)
	}
	if got := runner.workspaceRootForDocker("/var/lib/sparkclaw/workspaces/project"); got != "/Users/owner/sparkclaw/data/workspaces/project" {
		t.Fatalf("nested mapping = %q", got)
	}
	if got := runner.workspaceRootForDocker("/tmp/other"); got != "/tmp/other" {
		t.Fatalf("unmatched mapping = %q", got)
	}
}
