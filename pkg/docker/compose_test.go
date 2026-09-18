package docker

import (
	"errors"
	"reflect"
	"testing"
)

func TestComposeConfigPathsPreferCanonicalFile(t *testing.T) {
	want := []string{
		"/data/appdata/compose/demo/compose.yaml",
		"/data/appdata/compose/demo/docker-compose.yml",
	}
	if got := composeConfigPaths(userComposeRoot, "demo"); !reflect.DeepEqual(got, want) {
		t.Fatalf("composeConfigPaths() = %v, want %v", got, want)
	}
}

func TestDiscoveredComposeConfigPathsFiltersAndDeduplicates(t *testing.T) {
	output := "" +
		"/data/appdata/compose/demo/compose.yaml\n" +
		"/data/appdata/compose/demo/compose.yaml\n" +
		"/data/appdata/compose/legacy/docker-compose.yml\n" +
		"/data/appdata/compose/demo/data.txt\n" +
		"/data/appdata/other/compose.yaml\n"
	want := []string{
		"/data/appdata/compose/demo/compose.yaml",
		"/data/appdata/compose/legacy/docker-compose.yml",
	}
	if got := discoveredComposeConfigPaths(output); !reflect.DeepEqual(got, want) {
		t.Fatalf("discoveredComposeConfigPaths() = %v, want %v", got, want)
	}
}

func TestComposeProjectDeleteTargetRejectsSystemFiles(t *testing.T) {
	if got, err := composeProjectDeleteTargets("/data/appdata/alist/compose.yaml"); !errors.Is(err, ErrSystemComposeProject) || got != nil {
		t.Fatalf("system delete targets = (%v, %v), want nil paths and ErrSystemComposeProject", got, err)
	}

	got, err := composeProjectDeleteTargets("/data/appdata/compose/demo/compose.yaml")
	want := []string{
		"/data/appdata/compose/demo/compose.yaml",
		"/data/appdata/compose/demo/docker-compose.yml",
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("user delete targets = (%v, %v), want %v", got, err, want)
	}
}

func TestComposeDownArgsPreserveVolumesByDefault(t *testing.T) {
	filePath := "/data/appdata/compose/demo/compose.yaml"
	if got, want := composeDownArgs(filePath, false), []string{"compose", "-f", filePath, "down"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("composeDownArgs(false) = %v, want %v", got, want)
	}
	if got, want := composeDownArgs(filePath, true), []string{"compose", "-f", filePath, "down", "-v"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("composeDownArgs(true) = %v, want %v", got, want)
	}
}
