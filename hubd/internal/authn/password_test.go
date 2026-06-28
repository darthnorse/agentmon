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

func TestVerifyRejectsWrongArgon2Version(t *testing.T) {
	// PHC string with v=18 (current is 19) — must error before attempting argon2.
	encoded := "$argon2id$v=18$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNo"
	_, err := VerifyPassword(encoded, "pw")
	if err == nil {
		t.Fatal("v=18 must return an error")
	}
}

func TestVerifyRejectsHugeMemoryParam(t *testing.T) {
	// PHC string with m=9999999999 — far beyond the 2 GiB cap — must error.
	encoded := "$argon2id$v=19$m=9999999999,t=3,p=2$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNo"
	_, err := VerifyPassword(encoded, "pw")
	if err == nil {
		t.Fatal("m=9999999999 must return an error")
	}
}

func TestVerifyNormalHashPasswordOutputPasses(t *testing.T) {
	// Production params (m=65536, t=3, p=2) must pass the range gate.
	h, err := HashPassword("agentmon")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	ok, err := VerifyPassword(h, "agentmon")
	if err != nil || !ok {
		t.Fatalf("normal hash must verify: ok=%v err=%v", ok, err)
	}
}
