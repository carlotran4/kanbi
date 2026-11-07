package runtime

import (
	"reflect"
	"testing"
)

func TestCommandMessageIdentityPreservesNativeEnvironment(t *testing.T) {
	command := []string{"env", "KANBI_SESSION_ID=old", "KANBI_DB=old", "TOKEN=kept", "pi", "-e", "ref.ts"}
	got := commandWithMessageIdentity(command, 42, "/tmp/my board.db")
	want := []string{"env", "KANBI_SESSION_ID=42", "KANBI_DB=/tmp/my board.db", "TOKEN=kept", "pi", "-e", "ref.ts"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q != %q", got, want)
	}
	if command[1] != "KANBI_SESSION_ID=old" {
		t.Fatal("mutated input command")
	}
}

func TestCodexMessageIdentityIsThreadLocalShellPolicy(t *testing.T) {
	got := commandWithMessageIdentity([]string{"codex", "resume", "--no-alt-screen", "native-ref"}, 7, "/tmp/board \"quoted\".db")
	want := []string{"env", "KANBI_SESSION_ID=7", "KANBI_DB=/tmp/board \"quoted\".db", "codex", "-c", `shell_environment_policy.set.KANBI_SESSION_ID="7"`, "-c", `shell_environment_policy.set.KANBI_DB="/tmp/board \"quoted\".db"`, "resume", "--no-alt-screen", "native-ref"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%q != %q", got, want)
	}
}
