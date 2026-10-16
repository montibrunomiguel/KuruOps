package testutil

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// subprocessEnv marks a re-executed test binary as the child of RunMainInSubprocess.
const subprocessEnv = "KURUOPS_TEST_RUN_MAIN"

// InSubprocess reports whether this test binary was re-executed by RunMainInSubprocess
// to run a command's main(). A test that uses RunMainInSubprocess starts with
//
//	if testutil.InSubprocess() { main(); return }
//
// so the very same Test function is both the parent (which spawns the child and checks
// what happened) and the child (which just runs main()).
func InSubprocess() bool { return os.Getenv(subprocessEnv) == "1" }

// RunMainInSubprocess re-runs the current test binary with only the test named testName
// selected and the given environment, so that test's `main()` call runs as a real
// process. It returns the process's exit code and its combined output.
//
// This is how a test checks what a command does with main()'s os.Exit paths -- "refuses
// to start with a default password" -- which an in-process test cannot do without
// killing itself. The child gets ONLY env (plus the marker), not the parent's
// environment, so what the command sees is exactly what the test states.
func RunMainInSubprocess(t *testing.T, testName string, env ...string) (exitCode int, output string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$", "-test.count=1")
	cmd.Env = append([]string{subprocessEnv + "=1"}, env...)
	// Windows needs these to start a process at all; they carry nothing a command reads.
	for _, k := range []string{"SystemRoot", "PATH", "TEMP", "TMP"} {
		if v, ok := os.LookupEnv(k); ok {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, string(out)
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), string(out)
	default:
		t.Fatalf("run subprocess: %v\n%s", err, strings.TrimSpace(string(out)))
		return -1, ""
	}
}
