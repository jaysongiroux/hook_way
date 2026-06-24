package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// --- key + token helpers -------------------------------------------------

func rsaKeyPEM(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa gen: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal pub: %v", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	return priv, string(pubPEM)
}

func ecKeyPEM(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ec gen: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatalf("marshal pub: %v", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	return priv, string(pubPEM)
}

func signToken(t *testing.T, method jwt.SigningMethod, key any, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(method, claims)
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

// --- happy paths ---------------------------------------------------------

func TestJWTVerifiersAcceptValidTokens(t *testing.T) {
	now := time.Unix(1710000000, 0)
	claims := jwt.MapClaims{"exp": now.Add(time.Hour).Unix(), "iat": now.Unix()}

	hs, err := newJWTHS256Verifier("shared-secret", 0, "", "", "")
	if err != nil {
		t.Fatalf("hs256 ctor: %v", err)
	}
	if err := hs.Verify("Bearer "+signToken(t, jwt.SigningMethodHS256, []byte("shared-secret"), claims), nil, now); err != nil {
		t.Fatalf("hs256 valid token rejected: %v", err)
	}

	rsaPriv, rsaPub := rsaKeyPEM(t)
	rs, err := newJWTRS256Verifier(rsaPub, 0, "", "", "")
	if err != nil {
		t.Fatalf("rs256 ctor: %v", err)
	}
	if err := rs.Verify(signToken(t, jwt.SigningMethodRS256, rsaPriv, claims), nil, now); err != nil {
		t.Fatalf("rs256 valid token rejected: %v", err)
	}

	ecPriv, ecPub := ecKeyPEM(t)
	es, err := newJWTES256Verifier(ecPub, 0, "", "", "")
	if err != nil {
		t.Fatalf("es256 ctor: %v", err)
	}
	if err := es.Verify(signToken(t, jwt.SigningMethodES256, ecPriv, claims), nil, now); err != nil {
		t.Fatalf("es256 valid token rejected: %v", err)
	}
}

// --- algorithm-confusion: the core security property ---------------------

func TestJWTRS256RejectsAlgorithmConfusion(t *testing.T) {
	now := time.Unix(1710000000, 0)
	claims := jwt.MapClaims{"exp": now.Add(time.Hour).Unix()}
	_, rsaPub := rsaKeyPEM(t)

	rs, err := newJWTRS256Verifier(rsaPub, 0, "", "", "")
	if err != nil {
		t.Fatalf("rs256 ctor: %v", err)
	}
	// Classic attack: sign HS256 using the PUBLIC key bytes as the HMAC secret,
	// then present it to the RS256 endpoint. WithValidMethods must reject it.
	forged := signToken(t, jwt.SigningMethodHS256, []byte(rsaPub), claims)
	if err := rs.Verify(forged, nil, now); err == nil {
		t.Fatal("RS256 verifier accepted an HS256-forged token (alg confusion)")
	}
}

func TestJWTRejectsNoneAlgorithm(t *testing.T) {
	now := time.Unix(1710000000, 0)
	claims := jwt.MapClaims{"exp": now.Add(time.Hour).Unix()}
	unsigned := signToken(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, claims)

	for name, ctor := range map[string]func() (Verifier, error){
		"hs256": func() (Verifier, error) { return newJWTHS256Verifier("s", 0, "", "", "") },
		"rs256": func() (Verifier, error) { _, p := rsaKeyPEM(t); return newJWTRS256Verifier(p, 0, "", "", "") },
		"es256": func() (Verifier, error) { _, p := ecKeyPEM(t); return newJWTES256Verifier(p, 0, "", "", "") },
	} {
		t.Run(name, func(t *testing.T) {
			v, err := ctor()
			if err != nil {
				t.Fatalf("ctor: %v", err)
			}
			if err := v.Verify(unsigned, nil, now); err == nil {
				t.Fatal("alg=none token must be rejected")
			}
		})
	}
}

func TestJWTHS256RejectsAsymmetricToken(t *testing.T) {
	now := time.Unix(1710000000, 0)
	claims := jwt.MapClaims{"exp": now.Add(time.Hour).Unix()}
	rsaPriv, _ := rsaKeyPEM(t)
	hs, _ := newJWTHS256Verifier("shared-secret", 0, "", "", "")
	if err := hs.Verify(signToken(t, jwt.SigningMethodRS256, rsaPriv, claims), nil, now); err == nil {
		t.Fatal("HS256 verifier accepted an RS256 token")
	}
}

// --- claim + structural validation --------------------------------------

func TestJWTVerifierRejectsBadTokens(t *testing.T) {
	now := time.Unix(1710000000, 0)
	hs, _ := newJWTHS256Verifier("shared-secret", 0, "", "", "")
	secret := []byte("shared-secret")

	cases := map[string]string{
		"empty header":  "",
		"not a jwt":     "Bearer not.a.valid.jwt",
		"expired":       "Bearer " + signToken(t, jwt.SigningMethodHS256, secret, jwt.MapClaims{"exp": now.Add(-time.Hour).Unix()}),
		"not yet valid": "Bearer " + signToken(t, jwt.SigningMethodHS256, secret, jwt.MapClaims{"exp": now.Add(time.Hour).Unix(), "nbf": now.Add(time.Hour).Unix()}),
		"missing exp":   "Bearer " + signToken(t, jwt.SigningMethodHS256, secret, jwt.MapClaims{"iat": now.Unix()}),
		"wrong secret":  "Bearer " + signToken(t, jwt.SigningMethodHS256, []byte("attacker"), jwt.MapClaims{"exp": now.Add(time.Hour).Unix()}),
		"tampered":      "Bearer " + tamperLastChar(signToken(t, jwt.SigningMethodHS256, secret, jwt.MapClaims{"exp": now.Add(time.Hour).Unix()})),
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			if err := hs.Verify(header, nil, now); err == nil {
				t.Fatal("expected verification error")
			}
		})
	}
}

func tamperLastChar(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	if b[len(b)-1] == 'A' {
		b[len(b)-1] = 'B'
	} else {
		b[len(b)-1] = 'A'
	}
	return string(b)
}

func TestJWTLeewayAcceptsRecentlyExpired(t *testing.T) {
	now := time.Unix(1710000000, 0)
	hs, _ := newJWTHS256Verifier("shared-secret", 2*time.Minute, "", "", "")
	// Expired 1 minute ago, but within 2-minute leeway.
	header := "Bearer " + signToken(t, jwt.SigningMethodHS256, []byte("shared-secret"), jwt.MapClaims{"exp": now.Add(-time.Minute).Unix()})
	if err := hs.Verify(header, nil, now); err != nil {
		t.Fatalf("token within leeway should be accepted: %v", err)
	}
}

func TestJWTConstructorsRejectBadKeys(t *testing.T) {
	if _, err := newJWTHS256Verifier("", 0, "", "", ""); err == nil {
		t.Fatal("empty HS256 secret should error")
	}
	if _, err := newJWTRS256Verifier("not a pem", 0, "", "", ""); err == nil {
		t.Fatal("invalid RSA PEM should error")
	}
	if _, err := newJWTES256Verifier("not a pem", 0, "", "", ""); err == nil {
		t.Fatal("invalid EC PEM should error")
	}
	// An RSA PEM handed to the ES256 constructor must error.
	_, rsaPub := rsaKeyPEM(t)
	if _, err := newJWTES256Verifier(rsaPub, 0, "", "", ""); err == nil {
		t.Fatal("RSA key in ES256 constructor should error")
	}
}

// --- body_hash_claim enforcement -----------------------------------------

func TestJWTBodyHashClaim(t *testing.T) {
	now := time.Unix(1710000000, 0)
	body := []byte(`{"event":"test","id":42}`)
	sum := sha256.Sum256(body)
	correctHash := hex.EncodeToString(sum[:])

	v, err := newJWTHS256Verifier("secret", 0, "", "", "body_sha256")
	if err != nil {
		t.Fatalf("hs256 ctor: %v", err)
	}
	secret := []byte("secret")
	exp := now.Add(time.Hour).Unix()

	t.Run("correct lowercase hash accepted", func(t *testing.T) {
		tok := signToken(t, jwt.SigningMethodHS256, secret, jwt.MapClaims{
			"exp":         exp,
			"body_sha256": correctHash,
		})
		if err := v.Verify("Bearer "+tok, body, now); err != nil {
			t.Fatalf("expected acceptance, got: %v", err)
		}
	})

	t.Run("correct hash wrong body rejected", func(t *testing.T) {
		tok := signToken(t, jwt.SigningMethodHS256, secret, jwt.MapClaims{
			"exp":         exp,
			"body_sha256": correctHash,
		})
		differentBody := []byte(`{"event":"other","id":99}`)
		if err := v.Verify("Bearer "+tok, differentBody, now); err == nil {
			t.Fatal("expected rejection when body does not match hash")
		}
	})

	t.Run("missing claim rejected", func(t *testing.T) {
		tok := signToken(t, jwt.SigningMethodHS256, secret, jwt.MapClaims{
			"exp": exp,
		})
		if err := v.Verify("Bearer "+tok, body, now); err == nil {
			t.Fatal("expected rejection when body_sha256 claim absent")
		}
	})

	t.Run("empty claim rejected", func(t *testing.T) {
		tok := signToken(t, jwt.SigningMethodHS256, secret, jwt.MapClaims{
			"exp":         exp,
			"body_sha256": "",
		})
		if err := v.Verify("Bearer "+tok, body, now); err == nil {
			t.Fatal("expected rejection when body_sha256 claim is empty string")
		}
	})

	t.Run("non-string claim rejected", func(t *testing.T) {
		tok := signToken(t, jwt.SigningMethodHS256, secret, jwt.MapClaims{
			"exp":         exp,
			"body_sha256": 12345,
		})
		if err := v.Verify("Bearer "+tok, body, now); err == nil {
			t.Fatal("expected rejection when body_sha256 claim is a number")
		}
	})

	t.Run("uppercase hex accepted", func(t *testing.T) {
		upperHash := strings.ToUpper(correctHash)
		tok := signToken(t, jwt.SigningMethodHS256, secret, jwt.MapClaims{
			"exp":         exp,
			"body_sha256": upperHash,
		})
		if err := v.Verify("Bearer "+tok, body, now); err != nil {
			t.Fatalf("expected acceptance for uppercase hex hash, got: %v", err)
		}
	})
}

// --- issuer / audience enforcement ---------------------------------------

func TestJWTIssuerAudienceEnforcement(t *testing.T) {
	now := time.Unix(1710000000, 0)
	exp := now.Add(time.Hour).Unix()
	secret := []byte("secret")

	v, err := newJWTHS256Verifier("secret", 0, "https://issuer.example.com", "hookway", "")
	if err != nil {
		t.Fatalf("hs256 ctor: %v", err)
	}

	t.Run("matching iss and aud accepted", func(t *testing.T) {
		tok := signToken(t, jwt.SigningMethodHS256, secret, jwt.MapClaims{
			"exp": exp,
			"iss": "https://issuer.example.com",
			"aud": "hookway",
		})
		if err := v.Verify("Bearer "+tok, nil, now); err != nil {
			t.Fatalf("expected acceptance, got: %v", err)
		}
	})

	t.Run("wrong iss rejected", func(t *testing.T) {
		tok := signToken(t, jwt.SigningMethodHS256, secret, jwt.MapClaims{
			"exp": exp,
			"iss": "https://evil.example.com",
			"aud": "hookway",
		})
		if err := v.Verify("Bearer "+tok, nil, now); err == nil {
			t.Fatal("expected rejection for wrong iss")
		}
	})

	t.Run("missing iss rejected", func(t *testing.T) {
		tok := signToken(t, jwt.SigningMethodHS256, secret, jwt.MapClaims{
			"exp": exp,
			"aud": "hookway",
		})
		if err := v.Verify("Bearer "+tok, nil, now); err == nil {
			t.Fatal("expected rejection when iss is absent")
		}
	})

	t.Run("wrong aud rejected", func(t *testing.T) {
		tok := signToken(t, jwt.SigningMethodHS256, secret, jwt.MapClaims{
			"exp": exp,
			"iss": "https://issuer.example.com",
			"aud": "different-service",
		})
		if err := v.Verify("Bearer "+tok, nil, now); err == nil {
			t.Fatal("expected rejection for wrong aud")
		}
	})

	t.Run("missing aud rejected", func(t *testing.T) {
		tok := signToken(t, jwt.SigningMethodHS256, secret, jwt.MapClaims{
			"exp": exp,
			"iss": "https://issuer.example.com",
		})
		if err := v.Verify("Bearer "+tok, nil, now); err == nil {
			t.Fatal("expected rejection when aud is absent")
		}
	})
}

func TestJWTNoIssuerAudienceConfigured(t *testing.T) {
	now := time.Unix(1710000000, 0)
	exp := now.Add(time.Hour).Unix()
	secret := []byte("secret")

	v, err := newJWTHS256Verifier("secret", 0, "", "", "")
	if err != nil {
		t.Fatalf("hs256 ctor: %v", err)
	}

	tok := signToken(t, jwt.SigningMethodHS256, secret, jwt.MapClaims{
		"exp": exp,
	})
	if err := v.Verify("Bearer "+tok, nil, now); err != nil {
		t.Fatalf("verifier with no iss/aud config should accept token with no iss/aud claims: %v", err)
	}
}
