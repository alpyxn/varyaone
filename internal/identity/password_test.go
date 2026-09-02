package identity

import "testing"

func TestPasswordHashRoundTripAndRejectsWrongPassword(t *testing.T) {
	hash, err := HashPassword("doğru-ve-uzun-parola")
	if err != nil {
		t.Fatal(err)
	}
	valid, err := VerifyPassword(hash, "doğru-ve-uzun-parola")
	if err != nil || !valid {
		t.Fatalf("correct password rejected: valid=%v err=%v", valid, err)
	}
	valid, err = VerifyPassword(hash, "yanlış-ve-uzun-parola")
	if err != nil || valid {
		t.Fatalf("wrong password accepted: valid=%v err=%v", valid, err)
	}
}

func TestPasswordPolicy(t *testing.T) {
	if err := ValidatePassword("çok-kısa"); err == nil {
		t.Fatal("expected short password to be rejected")
	}
}
