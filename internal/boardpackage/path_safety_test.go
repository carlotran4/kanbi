package boardpackage

import "testing"

func TestFlatAttachmentNameRejectsBackslash(t *testing.T) {
	if _, err := flatAttachmentName(`report\final.png`); err == nil {
		t.Fatal("backslash attachment basename accepted")
	}
}
