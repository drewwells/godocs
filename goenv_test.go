package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSpecificityPrefersFullVersions(t *testing.T) {
	cases := []struct {
		path string
		want int
	}{
		{"/home/u/.local/share/mise/installs/go/1.26.4/bin/go", 2},
		{"/home/u/.local/share/mise/installs/go/1.26/bin/go", 1},
		{"/home/u/.local/share/mise/installs/go/1/bin/go", 0},
		{"/home/u/.local/share/mise/installs/go/latest/bin/go", 0},
		{"/home/u/.asdf/installs/golang/1.25.1/go/bin/go", 2},
	}
	for _, c := range cases {
		if got := specificity(c.path); got != c.want {
			t.Errorf("specificity(%q) = %d, want %d", c.path, got, c.want)
		}
	}
}

func TestVersionManagerInstallsOrdersMostSpecificFirst(t *testing.T) {
	home := t.TempDir()
	base := filepath.Join(home, ".local", "share", "mise", "installs", "go")
	for _, version := range []string{"1", "1.26", "1.26.4", "latest"} {
		dir := filepath.Join(base, version, "bin")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "go"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	got := versionManagerInstalls(home)
	if len(got) != 4 {
		t.Fatalf("found %d installs, want 4: %v", len(got), got)
	}
	// A cached path should name a version that only disappears when that
	// toolchain is actually removed, so the moving aliases must come last.
	if want := filepath.Join(base, "1.26.4", "bin", "go"); got[0] != want {
		t.Errorf("first candidate = %q, want %q", got[0], want)
	}
}

// goWorks must reject a toolchain that only resolves in some directories, which
// is what a version-manager shim does when no version is configured for the
// working directory. Getting this wrong means godocs works in a terminal and
// fails under Raycast.
func TestGoWorksProbesFromANeutralDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script fixture is POSIX-only")
	}

	dir := t.TempDir()
	marker := filepath.Join(dir, "cwd-sensitive")
	if err := os.WriteFile(marker, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	// Stands in for a shim: succeeds only when run from a directory containing
	// the marker file, the way a shim succeeds only where its config applies.
	fake := filepath.Join(dir, "go")
	script := "#!/bin/sh\nif [ -f ./cwd-sensitive ]; then echo go1.26.4; exit 0; fi\necho 'no version set' >&2\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	// It "works" from its own directory...
	restore, err := chdir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer restore()

	if out, err := runCandidate(fake); err != nil {
		t.Fatalf("fixture is broken: %v (%s)", err, out)
	}
	// ...but goWorks probes elsewhere, so it must still be rejected.
	if goWorks(fake) {
		t.Error("goWorks accepted a candidate that only resolves in one directory")
	}
}

func chdir(dir string) (func(), error) {
	previous, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if err := os.Chdir(dir); err != nil {
		return nil, err
	}
	return func() { _ = os.Chdir(previous) }, nil
}

func runCandidate(path string) (string, error) {
	out, err := exec.Command(path, "env", "GOVERSION").Output()
	return string(out), err
}
