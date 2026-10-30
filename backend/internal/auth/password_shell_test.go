package auth

import (
	"os/exec"
	"testing"
)

// install.sh refuses a weak first password before it writes .env, with a copy
// of the rule in shell. A difference between the two would have it accept a
// password the backend then refuses at startup, with .env already written.
func TestShellPasswordRuleMatchesTheBackend(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	for _, pw := range []string{
		"",
		"Aa1!",
		"Aa1!Aa1!Aa1",  // 11
		"Aa1!Aa1!Aa1!", // 12
		"alllowercaseletters",
		"ALLUPPERCASELETTERS",
		"123456789012345",
		"!!!!!!!!!!!!!!!",
		"lowerandUPPERonly",
		"lowerand12345678",
		"lower and spaces",
		"lower-and-1digit",
		"UPPER AND SPACES 1",
		"héééééééa",     // bytes reach 12 before characters do
		"ééééééééééééé", // only one class, however long
		"ééééééééa1",
		"back\\slash*[?]AA",
		"$(not a command)A",
	} {
		cmd := exec.Command(bash, "-c", `source ../../../scripts/password.sh; jd_password_problem "$1"`, "test", pw)
		// A shell that cares about the locale must still count bytes.
		cmd.Env = []string{"LC_ALL=C.UTF-8"}
		out, shellErr := cmd.CombinedOutput()
		if _, isExit := shellErr.(*exec.ExitError); shellErr != nil && !isExit {
			t.Fatal(shellErr)
		}
		if goErr := ValidatePasswordStrength(pw); (goErr == nil) != (shellErr == nil) {
			t.Errorf("%q: backend says %v, shell says %v (%s)", pw, goErr, shellErr, out)
		}
	}
}
