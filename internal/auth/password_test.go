package auth

import (
	"strings"
	"testing"
)

func TestCheckPassword(t *testing.T) {
	t.Parallel()
	const pw = "correct horse battery"
	h, err := HashPassword(pw)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := CheckPassword(h, pw); !ok || err != nil {
		t.Errorf("right password: ok=%v err=%v", ok, err)
	}
	if ok, err := CheckPassword(h, "wrong password"); ok || err != nil {
		t.Errorf("wrong password: ok=%v err=%v", ok, err)
	}
	for _, bad := range []string{"", "plain", "$argon2i$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA", strings.Replace(h, "v=19", "v=16", 1)} {
		if _, err := CheckPassword(bad, pw); err == nil {
			t.Errorf("CheckPassword(%q) accepted a malformed hash", bad)
		}
	}
}

func TestRoles(t *testing.T) {
	t.Parallel()
	if Can(Viewer, EditTasks) || !Can(Member, EditTasks) || Can(Member, ManageMembers) || !Can(Admin, ManageMembers) {
		t.Error("workspace roles allow the wrong actions")
	}
	if CanPlatform("", ViewPlatform) || !CanPlatform(Support, ViewPlatform) || CanPlatform(Support, ManagePlatformRoles) {
		t.Error("platform roles allow the wrong actions")
	}
}
