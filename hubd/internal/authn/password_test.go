package authn

import "testing"

func TestHashVerifyRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := VerifyPassword(h, "correct horse battery staple")
	if err != nil || !ok {
		t.Fatalf("verify good: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword(h, "wrong password")
	if err != nil || ok {
		t.Fatalf("verify bad must be (false,nil): ok=%v err=%v", ok, err)
	}
}

func TestHashIsSaltedAndPHCShaped(t *testing.T) {
	a, _ := HashPassword("pw")
	b, _ := HashPassword("pw")
	if a == b {
		t.Fatal("hashes must differ (random salt)")
	}
	if a[:10] != "$argon2id$" {
		t.Fatalf("not PHC argon2id: %q", a)
	}
}

func TestVerifyMalformedEncodedErrors(t *testing.T) {
	if _, err := VerifyPassword("not-a-phc-string", "pw"); err == nil {
		t.Fatal("malformed encoded must error")
	}
}
